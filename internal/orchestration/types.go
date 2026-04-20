package orchestration

import (
	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/gateway"
)

// Intent classifies what the orchestration pipeline should do.
type Intent string

const (
	IntentSimpleGenerate    Intent = "simple-generate"
	IntentStructuredOutput  Intent = "structured-output"
	IntentAgentLoop         Intent = "agent-loop"
	IntentRAGQuery          Intent = "rag-query"
	IntentComplexReasoning  Intent = "complex-reasoning" // multi-step CoT
	IntentCodeGeneration    Intent = "code-generation"   // code + execution feedback
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
	CoTEnabled     bool // force chain-of-thought even for simple queries
	MaxCritique    int  // max scored-critique rounds (0 = use default 3)
}

// OrchestrationResult carries all outputs the pipeline produces.
type OrchestrationResult struct {
	Content         string
	Intent          Intent
	IntentFallback  bool // x-caw-intent-fallback
	FormatError     bool // x-caw-format-error
	RAGDegraded     bool // x-caw-rag-degraded
	CritiqueApplied bool
	CritiqueRounds  int    // how many critique passes were used
	CoTApplied      bool   // whether chain-of-thought decomposition was used
	CodeFeedback    bool   // whether code-execution feedback loop ran
	CritiqueScore   int    // final critique score (0 if not critiqued)
}

// CompressionResult holds the outcome of a context-management pass.
type CompressionResult struct {
	Messages   []adapter.Message
	TokenCount int
	Compressed bool
}
