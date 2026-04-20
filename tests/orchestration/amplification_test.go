package orchestration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/orchestration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSequentialMock returns a MockInferenceBackend that returns successive
// canned responses on each Generate call.
func newSequentialMock(responses ...string) *adapter.MockInferenceBackend {
	idx := 0
	return &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			content := ""
			if idx < len(responses) {
				content = responses[idx]
			} else if len(responses) > 0 {
				content = responses[len(responses)-1]
			}
			idx++
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{
					Message: adapter.Message{Role: "assistant", Content: content},
				}},
			}, nil
		},
	}
}

// ── ClassifyIntent amplification tests ──────────────────────────────────────

func TestClassifyIntent_DetectsComplexReasoning(t *testing.T) {
	cases := []string{
		"Design a distributed rate limiter across 100 nodes",
		"Explain how Raft consensus works step by step",
		"Compare the trade-offs between SQL and NoSQL databases",
		"Analyze the architecture of a microservices system",
	}
	for _, q := range cases {
		req := orchestration.OrchestrationRequest{
			Messages: []adapter.Message{{Role: "user", Content: q}},
		}
		intent, _ := orchestration.ClassifyIntent(req)
		assert.Equal(t, orchestration.IntentComplexReasoning, intent, "expected complex for: %q", q)
	}
}

func TestClassifyIntent_DetectsCodeGeneration(t *testing.T) {
	cases := []string{
		"Write a Python function to reverse a linked list",
		"Implement a binary search in Go",
		"Write the SQL query to find duplicate rows",
		"Fix this code: def foo(): return 1/0",
		"How do you implement a lock-free queue in Go?", // "implement" → code intent
	}
	for _, q := range cases {
		req := orchestration.OrchestrationRequest{
			Messages: []adapter.Message{{Role: "user", Content: q}},
		}
		intent, _ := orchestration.ClassifyIntent(req)
		assert.Equal(t, orchestration.IntentCodeGeneration, intent, "expected code for: %q", q)
	}
}

func TestClassifyIntent_SimpleQueryStaysSimple(t *testing.T) {
	cases := []string{"hello", "what time is it", "hi there"}
	for _, q := range cases {
		req := orchestration.OrchestrationRequest{
			Messages: []adapter.Message{{Role: "user", Content: q}},
		}
		intent, _ := orchestration.ClassifyIntent(req)
		assert.Equal(t, orchestration.IntentSimpleGenerate, intent, "expected simple for: %q", q)
	}
}

func TestClassifyIntent_CoTFlagOverrides(t *testing.T) {
	req := orchestration.OrchestrationRequest{
		Messages:   []adapter.Message{{Role: "user", Content: "hello"}},
		CoTEnabled: true,
	}
	intent, _ := orchestration.ClassifyIntent(req)
	assert.Equal(t, orchestration.IntentComplexReasoning, intent)
}

// ── IsComplexQuery / IsCodeQuery helpers ─────────────────────────────────────

func TestIsComplexQuery(t *testing.T) {
	assert.True(t, orchestration.IsComplexQuery("Design a system for high availability"))
	assert.True(t, orchestration.IsComplexQuery("Analyze the trade-offs between approaches"))
	assert.False(t, orchestration.IsComplexQuery("what is 2+2"))
}

func TestIsCodeQuery(t *testing.T) {
	assert.True(t, orchestration.IsCodeQuery("Write a Python function that sums a list"))
	assert.True(t, orchestration.IsCodeQuery("Implement a binary search tree in Go"))
	assert.False(t, orchestration.IsCodeQuery("what is the capital of France"))
}

// ── ChainOfThoughtDecomposer ─────────────────────────────────────────────────

func TestCoTDecomposer_ReturnsSynthesizedAnswer(t *testing.T) {
	calls := 0
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, _ *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			calls++
			content := "Step 1: reason. Step 2: solve."
			if calls == 2 {
				content = "The answer is 42, derived through careful analysis."
			}
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: content}}},
			}, nil
		},
	}
	cot := orchestration.NewChainOfThoughtDecomposer(backend)
	result, err := cot.Decompose(context.Background(), &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "What is the meaning of life?"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "should call backend twice (reason + synthesize)")
	assert.Contains(t, result, "42")
}

func TestCoTDecomposer_PrependSystemPrompt(t *testing.T) {
	var capturedMessages []adapter.Message
	captureCall := 0
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			if captureCall == 0 {
				capturedMessages = req.Messages
			}
			captureCall++
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "reasoning"}}},
			}, nil
		},
	}
	cot := orchestration.NewChainOfThoughtDecomposer(backend)
	_, _ = cot.Decompose(context.Background(), &adapter.GenerateRequest{
		Model:    "gemma:2b",
		Messages: []adapter.Message{{Role: "user", Content: "explain transformers"}},
	})
	require.NotEmpty(t, capturedMessages)
	assert.Equal(t, "system", capturedMessages[0].Role)
	assert.Contains(t, capturedMessages[0].Content, "step by step")
}

// ── ScoredCritique ───────────────────────────────────────────────────────────

func TestScoredCritiqueLoop_PassesOnHighScore(t *testing.T) {
	backend := newSequentialMock("SCORE: 9\nFLAWS: none")
	sc := orchestration.NewSelfCritiquer(backend)
	content, score, rounds, err := sc.CritiqueLoop(
		context.Background(), "great answer",
		orchestration.OrchestrationRequest{Model: "gemma:2b"}, 3,
	)
	require.NoError(t, err)
	assert.Equal(t, "great answer", content, "high score should keep original")
	assert.Equal(t, 9, score)
	assert.Equal(t, 1, rounds, "should stop after first round if score ≥ 7")
}

func TestScoredCritiqueLoop_ImprovesOnLowScore(t *testing.T) {
	backend := newSequentialMock(
		"SCORE: 3\nFLAWS: missing key details",
		"Here is the improved, complete answer.",
		"SCORE: 8\nFLAWS: none",
	)
	sc := orchestration.NewSelfCritiquer(backend)
	content, score, rounds, err := sc.CritiqueLoop(
		context.Background(), "weak answer",
		orchestration.OrchestrationRequest{Model: "gemma:2b"}, 3,
	)
	require.NoError(t, err)
	assert.Equal(t, "Here is the improved, complete answer.", content)
	assert.Equal(t, 8, score)
	assert.Equal(t, 2, rounds)
}

func TestScoredCritiqueLoop_RespectsMaxRounds(t *testing.T) {
	backend := newSequentialMock(
		"SCORE: 2\nFLAWS: always bad", "improved v1",
		"SCORE: 2\nFLAWS: still bad", "improved v2",
	)
	sc := orchestration.NewSelfCritiquer(backend)
	_, _, rounds, err := sc.CritiqueLoop(
		context.Background(), "bad answer",
		orchestration.OrchestrationRequest{Model: "gemma:2b"}, 2,
	)
	require.NoError(t, err)
	assert.Equal(t, 2, rounds)
}

// ── CodeFeedbackLoop ─────────────────────────────────────────────────────────

func TestCodeFeedbackLoop_NoCodePassesThrough(t *testing.T) {
	backend := newSequentialMock("unchanged")
	loop := orchestration.NewCodeFeedbackLoop(backend)
	result, ran, err := loop.Run(
		context.Background(), "no code here just text",
		&adapter.GenerateRequest{Model: "gemma:2b"},
	)
	require.NoError(t, err)
	assert.False(t, ran)
	assert.Equal(t, "no code here just text", result)
}

func TestCodeFeedbackLoop_ValidPythonReturnsImmediately(t *testing.T) {
	calls := 0
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, _ *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			calls++
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Content: "fixed"}}},
			}, nil
		},
	}
	loop := orchestration.NewCodeFeedbackLoop(backend)
	content := "```python\nprint('hello')\n```"
	result, ran, err := loop.Run(
		context.Background(), content,
		&adapter.GenerateRequest{Model: "gemma:2b"},
	)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Equal(t, content, result)
	assert.Equal(t, 0, calls, "valid code should not call backend")
}

func TestCodeFeedbackLoop_ErrorCodeTriggersImprovement(t *testing.T) {
	backend := newSequentialMock("```python\nprint('fixed!')\n```")
	loop := orchestration.NewCodeFeedbackLoop(backend)
	content := "```python\ndef broken(\n```" // SyntaxError
	result, ran, err := loop.Run(
		context.Background(), content,
		&adapter.GenerateRequest{Model: "gemma:2b"},
	)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.NotEqual(t, content, result)
	assert.Contains(t, result, "fixed!")
}

func TestCodeFeedbackLoop_DetectsPythonBlock(t *testing.T) {
	content := "Here is the answer:\n```python\ndef foo():\n    return 42\n```"
	assert.True(t, strings.Contains(content, "```python"))
}

