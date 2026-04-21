package orchestration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/memory"
	"github.com/caw/wrapper/internal/orchestration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeResp is a test helper that wraps content in a GenerateResponse.
func makeResp(content string) *adapter.GenerateResponse {
	return &adapter.GenerateResponse{
		Choices: []adapter.Choice{{
			Message: adapter.Message{Role: "assistant", Content: content},
		}},
	}
}

// isPlanMsg returns true if the last message in req looks like a plan-phase prompt.
func isPlanMsg(req *adapter.GenerateRequest) bool {
	if len(req.Messages) == 0 {
		return false
	}
	last := req.Messages[len(req.Messages)-1].Content
	return strings.Contains(last, "JSON plan") || strings.Contains(last, "No code")
}

// isVerifyMsg returns true if the request looks like a PEV verify prompt.
func isVerifyMsg(req *adapter.GenerateRequest) bool {
	if len(req.Messages) == 0 {
		return false
	}
	last := req.Messages[len(req.Messages)-1].Content
	return strings.Contains(last, "1-10")
}

// ── TestPEV_PlanPhase_RejectsRawCode ─────────────────────────────────────────

func TestPEV_PlanPhase_RejectsRawCode(t *testing.T) {
	planCalls := 0
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			switch {
			case isPlanMsg(req):
				planCalls++
				if planCalls == 1 {
					// First plan call: return raw code to trigger retry.
					return makeResp("```go\nfunc sort(a []int) {}```"), nil
				}
				// Retry: return valid JSON plan.
				return makeResp(`{"steps":["implement the sort"]}`), nil
			case isVerifyMsg(req):
				return makeResp(`{"score":7,"verdict":"ok"}`), nil
			default:
				// Execute phase.
				return makeResp("executed step result"), nil
			}
		},
	}

	pev := orchestration.NewPEVOrchestrator(mock)
	content, _, err := pev.Run(context.Background(), &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "write a go sort function"}},
	})

	require.NoError(t, err)
	assert.NotEmpty(t, content)
	assert.Greater(t, planCalls, 1, "plan phase should retry when raw code is detected")
}

// ── TestPEV_PlanPhase_AcceptsJSON ─────────────────────────────────────────────

func TestPEV_PlanPhase_AcceptsJSON(t *testing.T) {
	planCalls := 0
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			switch {
			case isPlanMsg(req):
				planCalls++
				return makeResp(`{"steps":["step one","step two"]}`), nil
			case isVerifyMsg(req):
				return makeResp(`{"score":9,"verdict":"excellent"}`), nil
			default:
				return makeResp("step output"), nil
			}
		},
	}

	pev := orchestration.NewPEVOrchestrator(mock)
	_, _, err := pev.Run(context.Background(), &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "explain something"}},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, planCalls, "valid JSON plan should be accepted on first call, no retry")
}

// ── TestPEV_ExecutePhase_RunsSteps ────────────────────────────────────────────

func TestPEV_ExecutePhase_RunsSteps(t *testing.T) {
	executeCalls := 0
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			switch {
			case isPlanMsg(req):
				return makeResp(`{"steps":["step A","step B"]}`), nil
			case isVerifyMsg(req):
				return makeResp(`{"score":8,"verdict":"good"}`), nil
			default:
				// Execute phase — count each step call.
				executeCalls++
				return makeResp("step result"), nil
			}
		},
	}

	pev := orchestration.NewPEVOrchestrator(mock)
	_, _, err := pev.Run(context.Background(), &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "do two things"}},
	})

	require.NoError(t, err)
	assert.Equal(t, 2, executeCalls, "execute phase should call backend once per step")
}

// ── TestPEV_VerifyPhase_ParsesScore ──────────────────────────────────────────

func TestPEV_VerifyPhase_ParsesScore(t *testing.T) {
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			switch {
			case isPlanMsg(req):
				return makeResp(`{"steps":["the step"]}`), nil
			case isVerifyMsg(req):
				return makeResp(`{"score":8,"verdict":"good"}`), nil
			default:
				return makeResp("step output"), nil
			}
		},
	}

	pev := orchestration.NewPEVOrchestrator(mock)
	_, score, err := pev.Run(context.Background(), &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "test request"}},
	})

	require.NoError(t, err)
	assert.Equal(t, 8, score, "PEVScore should be parsed from verify phase JSON response")
}

// ── TestPEV_FallsBackOnMaxRetries ─────────────────────────────────────────────

func TestPEV_FallsBackOnMaxRetries(t *testing.T) {
	totalCalls := 0
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			totalCalls++
			if isPlanMsg(req) {
				// Always return raw code in plan phase → forces max retries.
				return makeResp("```python\ndef foo(): pass```"), nil
			}
			// Fallback direct generation call.
			return makeResp("fallback direct result"), nil
		},
	}

	pev := orchestration.NewPEVOrchestrator(mock)
	content, score, err := pev.Run(context.Background(), &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "write something"}},
	})

	require.NoError(t, err, "PEV should fall back gracefully, not return an error")
	assert.Equal(t, "fallback direct result", content, "fallback should return direct generation output")
	assert.Equal(t, 0, score, "score should be 0 on fallback path")
	// 3 plan attempts (initial + 2 retries) + 1 fallback = 4 total
	assert.Equal(t, 4, totalCalls, "expected 3 plan attempts + 1 fallback call")
}

// ── TestPipeline_UsesPEVForCodeGeneration ────────────────────────────────────

func TestPipeline_UsesPEVForCodeGeneration(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	_ = mr

	// Sequential mock: Plan → Execute → PEV Verify → Critique score (stops at ≥7)
	backend := newSequentialMock(
		`{"steps":["write the sort function"]}`, // 1: PEV plan
		"def sort_list(lst): return sorted(lst)",// 2: PEV execute step 1
		`{"score":8,"verdict":"looks good"}`,    // 3: PEV verify
		"SCORE: 8\nFLAWS: none",                 // 4: critique score (≥7 → stops)
	)

	store := memory.NewSessionStore(rdb)
	cm := orchestration.NewContextManager(store, rdb, backend)
	formatter := orchestration.NewOutputFormatter(backend)
	critiquer := orchestration.NewSelfCritiquer(backend)
	pipeline := orchestration.NewPipeline(cm, formatter, critiquer)

	req := orchestration.OrchestrationRequest{
		SessionID: "pev-pipeline-test",
		Model:     "gemma:2b",
		Messages:  []adapter.Message{{Role: "user", Content: "Write a python function to sort a list"}},
	}

	result, err := pipeline.Run(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.PEVApplied, "PEVApplied must be true for IntentCodeGeneration")
	assert.Greater(t, result.PEVScore, 0, "PEVScore must be populated from verify phase")
}
