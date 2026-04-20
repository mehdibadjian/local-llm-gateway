package orchestration

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/caw/wrapper/internal/adapter"
)

const (
	defaultMaxCritiqueRounds = 3
	critiquePassThreshold    = 7 // score ≥ 7/10 is considered good enough
)

// CritiqueTrigger describes why self-critique was activated.
type CritiqueTrigger string

const (
	TriggerOptIn       CritiqueTrigger = "opt-in"
	TriggerDomain      CritiqueTrigger = "domain"
	TriggerSideEffect  CritiqueTrigger = "side-effect"
	TriggerRAGDegraded CritiqueTrigger = "rag-degraded"
	TriggerComplex     CritiqueTrigger = "complex-query"
	TriggerCode        CritiqueTrigger = "code-generation"
)

// ShouldCritique returns (true, trigger) if the request requires a self-critique pass.
func ShouldCritique(req OrchestrationRequest) (bool, CritiqueTrigger) {
	if req.Critique {
		return true, TriggerOptIn
	}
	if req.RAGDegraded && (req.Domain == "legal" || req.Domain == "medical") {
		return true, TriggerRAGDegraded
	}
	if req.Domain == "legal" || req.Domain == "medical" {
		return true, TriggerDomain
	}
	if req.SideEffect {
		return true, TriggerSideEffect
	}
	return false, ""
}

// SelfCritiquer applies a scored critique pass to a generated response.
type SelfCritiquer struct {
	backend adapter.InferenceBackend
}

// NewSelfCritiquer returns a SelfCritiquer backed by the given inference backend.
func NewSelfCritiquer(backend adapter.InferenceBackend) *SelfCritiquer {
	return &SelfCritiquer{backend: backend}
}

// Critique applies a single weak critique pass (legacy — kept for backward compat).
// Prefer CritiqueLoop for new callers.
func (sc *SelfCritiquer) Critique(ctx context.Context, originalContent string, req OrchestrationRequest) (string, error) {
	improved, _, _, err := sc.CritiqueLoop(ctx, originalContent, req, 1)
	return improved, err
}

// CritiqueLoop runs up to maxRounds of scored self-critique.
// Each round:
//  1. Asks the model to score the response 1-10 and explain flaws.
//  2. If score ≥ critiquePassThreshold, returns immediately (good enough).
//  3. Otherwise asks the model to produce an improved version and loops.
//
// Returns (best content, final score, rounds used, error).
// On backend failure the best content seen so far is returned.
func (sc *SelfCritiquer) CritiqueLoop(ctx context.Context, content string, req OrchestrationRequest, maxRounds int) (string, int, int, error) {
	if maxRounds <= 0 {
		maxRounds = defaultMaxCritiqueRounds
	}

	best := content
	bestScore := 0

	for round := 1; round <= maxRounds; round++ {
		// Step A: score the current best.
		scorePrompt := fmt.Sprintf(
			"Rate the following response on a scale from 1 to 10 for accuracy, completeness, and correctness.\n"+
				"Reply ONLY in this format (no other text):\n"+
				"SCORE: <number>\n"+
				"FLAWS: <one sentence describing the main flaw, or 'none' if perfect>\n\n"+
				"Response to evaluate:\n%s",
			best,
		)
		scoreResp, err := sc.backend.Generate(ctx, &adapter.GenerateRequest{
			Model:    req.Model,
			Messages: []adapter.Message{{Role: "user", Content: scorePrompt}},
		})
		if err != nil {
			// Can't score — return best so far.
			return best, bestScore, round - 1, nil
		}

		score, flaws := parseScoreResponse(scoreResp.Choices[0].Message.Content)
		if score > bestScore {
			bestScore = score
		}

		if score >= critiquePassThreshold {
			// Good enough — stop early.
			return best, score, round, nil
		}

		// Step B: ask for an improved version.
		improvePrompt := fmt.Sprintf(
			"The following response scored %d/10. The main flaw is: %s\n\n"+
				"Please provide an improved, corrected version. Be concise and accurate.\n\n"+
				"Original response:\n%s",
			score, flaws, best,
		)
		improvedResp, err := sc.backend.Generate(ctx, &adapter.GenerateRequest{
			Model:    req.Model,
			Messages: []adapter.Message{{Role: "user", Content: improvePrompt}},
		})
		if err != nil {
			return best, bestScore, round, nil
		}
		best = improvedResp.Choices[0].Message.Content
	}

	return best, bestScore, maxRounds, nil
}

// parseScoreResponse extracts the numeric score and flaw description from the
// model's scoring response. Returns (0, "unknown") if parsing fails.
func parseScoreResponse(response string) (int, string) {
	scoreRe := regexp.MustCompile(`(?i)SCORE:\s*(\d+)`)
	flawsRe := regexp.MustCompile(`(?i)FLAWS:\s*(.+)`)

	score := 0
	flaws := "unknown"

	if m := scoreRe.FindStringSubmatch(response); len(m) == 2 {
		if n, err := strconv.Atoi(strings.TrimSpace(m[1])); err == nil {
			score = n
		}
	}
	if m := flawsRe.FindStringSubmatch(response); len(m) == 2 {
		flaws = strings.TrimSpace(m[1])
	}
	return score, flaws
}

