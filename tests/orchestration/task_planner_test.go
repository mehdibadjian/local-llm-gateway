package orchestration_test

import (
	"testing"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/gateway"
	"github.com/caw/wrapper/internal/orchestration"
	"github.com/stretchr/testify/assert"
)

func TestClassifyIntent_SimplePlainChat(t *testing.T) {
	req := orchestration.OrchestrationRequest{
		Messages: []adapter.Message{adapter_message("hello")},
	}
	intent, fallback := orchestration.ClassifyIntent(req)
	assert.Equal(t, orchestration.IntentSimpleGenerate, intent)
	assert.False(t, fallback)
}

func TestClassifyIntent_JSONResponseFormat(t *testing.T) {
	req := orchestration.OrchestrationRequest{
		Messages:       []adapter.Message{adapter_message("give me json")},
		ResponseFormat: &gateway.ResponseFormat{Type: "json_object"},
	}
	intent, fallback := orchestration.ClassifyIntent(req)
	assert.Equal(t, orchestration.IntentStructuredOutput, intent)
	assert.False(t, fallback)
}

func TestClassifyIntent_AgentMode(t *testing.T) {
	req := orchestration.OrchestrationRequest{
		Messages:  []adapter.Message{adapter_message("do task")},
		AgentMode: true,
	}
	intent, fallback := orchestration.ClassifyIntent(req)
	assert.Equal(t, orchestration.IntentAgentLoop, intent)
	assert.False(t, fallback)
}

func TestClassifyIntent_RAGQueryWithDomain(t *testing.T) {
	req := orchestration.OrchestrationRequest{
		Messages:   []adapter.Message{adapter_message("search docs")},
		RAGEnabled: true,
		Domain:     "legal",
	}
	intent, fallback := orchestration.ClassifyIntent(req)
	assert.Equal(t, orchestration.IntentRAGQuery, intent)
	assert.False(t, fallback)
}

func TestClassifyIntent_Fallback(t *testing.T) {
	// No messages, no other signals.
	req := orchestration.OrchestrationRequest{}
	intent, fallback := orchestration.ClassifyIntent(req)
	assert.Equal(t, orchestration.IntentSimpleGenerate, intent)
	assert.True(t, fallback)
}
