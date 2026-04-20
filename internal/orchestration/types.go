package orchestration

import (
	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/gateway"
)

// Intent classifies what the orchestration pipeline should do.
type Intent string

const (
	IntentSimpleGenerate   Intent = "simple-generate"
	IntentStructuredOutput Intent = "structured-output"
	IntentAgentLoop        Intent = "agent-loop"
	IntentRAGQuery         Intent = "rag-query"
)

// OrchestrationRequest carries all inputs the pipeline needs.
type OrchestrationRequest struct {
	SessionID      string
	Messages       []adapter.Message
	Model          string
	Stream         bool
	ResponseFormat *gateway.ResponseFormat
	AgentMode      bool
	RAGEnabled     bool
	Domain         string
	Critique       bool // x-caw-options.critique
	RAGDegraded    bool // x-caw-rag-degraded
	SideEffect     bool // tool wrote external state
}

// OrchestrationResult carries all outputs the pipeline produces.
type OrchestrationResult struct {
	Content         string
	Intent          Intent
	IntentFallback  bool // x-caw-intent-fallback
	FormatError     bool // x-caw-format-error
	RAGDegraded     bool // x-caw-rag-degraded
	CritiqueApplied bool
}

// CompressionResult holds the outcome of a context-management pass.
type CompressionResult struct {
	Messages   []adapter.Message
	TokenCount int
	Compressed bool
}
