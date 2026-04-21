package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/caw/wrapper/internal/adapter"
)

// pevMaxPlanRetries is the maximum number of re-prompts when raw code is detected.
const pevMaxPlanRetries = 2

// PEVOrchestrator implements a three-pass Plan-Execute-Verify state machine
// that structures generation for code and complex-reasoning intents.
type PEVOrchestrator struct {
	backend adapter.InferenceBackend
}

// NewPEVOrchestrator returns a PEVOrchestrator backed by the given inference backend.
func NewPEVOrchestrator(backend adapter.InferenceBackend) *PEVOrchestrator {
	return &PEVOrchestrator{backend: backend}
}

type pevPlan struct {
	Steps []string `json:"steps"`
}

type pevVerifyResult struct {
	Score   int    `json:"score"`
	Verdict string `json:"verdict"`
}

// containsRawCode reports whether s looks like raw code output.
// Heuristic: contains fenced code blocks or common function-definition keywords.
func containsRawCode(s string) bool {
	return strings.Contains(s, "```") ||
		strings.Contains(s, "func ") ||
		strings.Contains(s, "def ") ||
		strings.Contains(s, "class ")
}

// extractJSONObject extracts the first {...} JSON object substring from s.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

// Run executes the three-pass PEV state machine:
//
//   - Pass 1 (Plan): prompt the model for a JSON step plan; retry up to
//     pevMaxPlanRetries times if raw code is detected in the response.
//   - Pass 2 (Execute): call backend.Generate for each step in the plan.
//   - Pass 3 (Verify): score the combined output against the original request.
//
// Returns (content, PEVScore, error). If the plan phase exhausts all retries,
// Run falls back to a direct generation call (score=0, error=nil).
func (p *PEVOrchestrator) Run(ctx context.Context, req *adapter.GenerateRequest) (string, int, error) {
	plan, err := p.runPlanPhase(ctx, req)
	if err != nil {
		// Plan phase failed — fall back to direct generation.
		resp, genErr := p.backend.Generate(ctx, req)
		if genErr != nil {
			return "", 0, fmt.Errorf("pev fallback generate: %w", genErr)
		}
		return resp.Choices[0].Message.Content, 0, nil
	}

	content, err := p.runExecutePhase(ctx, req, plan)
	if err != nil {
		return "", 0, fmt.Errorf("pev execute: %w", err)
	}

	score, _ := p.runVerifyPhase(ctx, req, content)
	return content, score, nil
}

func (p *PEVOrchestrator) runPlanPhase(ctx context.Context, req *adapter.GenerateRequest) (*pevPlan, error) {
	const planInstruction = "Output ONLY a JSON plan object with key 'steps' (array of strings). Do not include code."

	// Build initial plan messages from the original request messages.
	msgs := append(append([]adapter.Message{}, req.Messages...), adapter.Message{
		Role:    "user",
		Content: planInstruction,
	})
	planReq := &adapter.GenerateRequest{Model: req.Model, Messages: msgs}

	for attempt := 0; attempt <= pevMaxPlanRetries; attempt++ {
		resp, err := p.backend.Generate(ctx, planReq)
		if err != nil {
			return nil, fmt.Errorf("pev plan generate: %w", err)
		}

		raw := resp.Choices[0].Message.Content

		if containsRawCode(raw) {
			if attempt < pevMaxPlanRetries {
				// Re-prompt: append the bad response and a corrective user turn.
				planReq.Messages = append(planReq.Messages,
					adapter.Message{Role: "assistant", Content: raw},
					adapter.Message{
						Role:    "user",
						Content: "That contained code. Output ONLY a JSON plan object with key 'steps' (array of strings). No code.",
					},
				)
				continue
			}
			return nil, fmt.Errorf("pev plan: raw code detected after %d retries", pevMaxPlanRetries)
		}

		var plan pevPlan
		if err := json.Unmarshal([]byte(extractJSONObject(raw)), &plan); err != nil {
			return nil, fmt.Errorf("pev plan parse: %w", err)
		}
		if len(plan.Steps) == 0 {
			return nil, fmt.Errorf("pev plan: no steps returned")
		}
		return &plan, nil
	}

	return nil, fmt.Errorf("pev plan: exhausted retries")
}

func (p *PEVOrchestrator) runExecutePhase(ctx context.Context, req *adapter.GenerateRequest, plan *pevPlan) (string, error) {
	var parts []string
	for _, step := range plan.Steps {
		stepMsgs := append(append([]adapter.Message{}, req.Messages...), adapter.Message{
			Role:    "user",
			Content: step,
		})
		resp, err := p.backend.Generate(ctx, &adapter.GenerateRequest{
			Model:    req.Model,
			Messages: stepMsgs,
		})
		if err != nil {
			return "", fmt.Errorf("pev execute step %q: %w", step, err)
		}
		parts = append(parts, resp.Choices[0].Message.Content)
	}
	return strings.Join(parts, "\n\n"), nil
}

func (p *PEVOrchestrator) runVerifyPhase(ctx context.Context, req *adapter.GenerateRequest, content string) (int, error) {
	original := lastUserMessage(req.Messages)
	verifyPrompt := fmt.Sprintf(
		"On a scale 1-10, does the following output correctly address the original request?\n"+
			"Output JSON: {\"score\": N, \"verdict\": \"...\"}\n\n"+
			"Original request: %s\n\nOutput:\n%s",
		original, content,
	)
	resp, err := p.backend.Generate(ctx, &adapter.GenerateRequest{
		Model:    req.Model,
		Messages: []adapter.Message{{Role: "user", Content: verifyPrompt}},
	})
	if err != nil {
		return 0, fmt.Errorf("pev verify: %w", err)
	}

	raw := resp.Choices[0].Message.Content
	var vr pevVerifyResult
	if err := json.Unmarshal([]byte(extractJSONObject(raw)), &vr); err != nil {
		return 0, fmt.Errorf("pev verify parse: %w", err)
	}
	return vr.Score, nil
}
