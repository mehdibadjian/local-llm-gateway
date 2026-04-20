package orchestration

import (
	"context"
	"fmt"

	"github.com/caw/wrapper/internal/adapter"
)

// Pipeline wires together ContextManager, TaskPlanner, OutputFormatter, and
// SelfCritiquer into a single orchestration pass.
type Pipeline struct {
	contextMgr *ContextManager
	formatter  *OutputFormatter
	critiquer  *SelfCritiquer
}

// NewPipeline constructs a Pipeline from its component parts.
func NewPipeline(cm *ContextManager, formatter *OutputFormatter, critiquer *SelfCritiquer) *Pipeline {
	return &Pipeline{
		contextMgr: cm,
		formatter:  formatter,
		critiquer:  critiquer,
	}
}

// Run executes the full orchestration pipeline for a single request.
func (p *Pipeline) Run(ctx context.Context, req OrchestrationRequest) (*OrchestrationResult, error) {
	// 1. Load and manage context.
	messages, err := p.contextMgr.LoadAndManage(ctx, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("context manager: %w", err)
	}
	// Merge managed history with request messages.
	allMessages := append(messages, req.Messages...)

	// 2. Classify intent.
	intent, isFallback := ClassifyIntent(req)

	// 3. Generate with output formatting.
	genReq := &adapter.GenerateRequest{
		Model:    req.Model,
		Messages: allMessages,
		Stream:   req.Stream,
	}
	content, formatErr, err := p.formatter.Format(ctx, genReq, intent)
	if err != nil {
		return nil, fmt.Errorf("output formatter: %w", err)
	}

	result := &OrchestrationResult{
		Content:        content,
		Intent:         intent,
		IntentFallback: isFallback,
		FormatError:    formatErr,
		RAGDegraded:    req.RAGDegraded,
	}

	// 4. Self-critique pass (conditional).
	if should, _ := ShouldCritique(req); should {
		critiqued, err := p.critiquer.Critique(ctx, content, req)
		if err == nil {
			result.Content = critiqued
		}
		result.CritiqueApplied = true
	}

	return result, nil
}
