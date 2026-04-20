package orchestration_test

import (
	"context"
	"testing"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/orchestration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldCritique_OptIn(t *testing.T) {
	req := orchestration.OrchestrationRequest{Critique: true}
	ok, trigger := orchestration.ShouldCritique(req)
	assert.True(t, ok)
	assert.Equal(t, orchestration.TriggerOptIn, trigger)
}

func TestShouldCritique_LegalDomain(t *testing.T) {
	req := orchestration.OrchestrationRequest{Domain: "legal"}
	ok, trigger := orchestration.ShouldCritique(req)
	assert.True(t, ok)
	assert.Equal(t, orchestration.TriggerDomain, trigger)
}

func TestShouldCritique_MedicalDomain(t *testing.T) {
	req := orchestration.OrchestrationRequest{Domain: "medical"}
	ok, trigger := orchestration.ShouldCritique(req)
	assert.True(t, ok)
	assert.Equal(t, orchestration.TriggerDomain, trigger)
}

func TestShouldCritique_SideEffect(t *testing.T) {
	req := orchestration.OrchestrationRequest{SideEffect: true}
	ok, trigger := orchestration.ShouldCritique(req)
	assert.True(t, ok)
	assert.Equal(t, orchestration.TriggerSideEffect, trigger)
}

func TestShouldCritique_RAGDegradedLegal(t *testing.T) {
	req := orchestration.OrchestrationRequest{RAGDegraded: true, Domain: "legal"}
	ok, trigger := orchestration.ShouldCritique(req)
	assert.True(t, ok)
	assert.Equal(t, orchestration.TriggerRAGDegraded, trigger)
}

func TestShouldCritique_NotTriggered(t *testing.T) {
	req := orchestration.OrchestrationRequest{
		Messages: []adapter.Message{adapter_message("hello")},
	}
	ok, trigger := orchestration.ShouldCritique(req)
	assert.False(t, ok)
	assert.Empty(t, trigger)
}

func TestSelfCritiquer_AppliesCritiquePrompt(t *testing.T) {
	var firstCallContent string
	callCount := 0
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			if callCount == 0 {
				firstCallContent = req.Messages[0].Content
			}
			callCount++
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "verified response"}}},
			}, nil
		},
	}
	sc := orchestration.NewSelfCritiquer(mock)
	req := orchestration.OrchestrationRequest{Model: "test-model"}

	result, err := sc.Critique(context.Background(), "original response", req)

	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, firstCallContent, "original response", "scoring prompt must include the original content")
	assert.Contains(t, firstCallContent, "1 to 10", "scoring prompt must ask for a numeric score")
}
