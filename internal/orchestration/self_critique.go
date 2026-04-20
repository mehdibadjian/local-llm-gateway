package orchestration

import (
	"context"
	"fmt"

	"github.com/caw/wrapper/internal/adapter"
)

// CritiqueTrigger describes why self-critique was activated.
type CritiqueTrigger string

const (
	TriggerOptIn       CritiqueTrigger = "opt-in"
	TriggerDomain      CritiqueTrigger = "domain"
	TriggerSideEffect  CritiqueTrigger = "side-effect"
	TriggerRAGDegraded CritiqueTrigger = "rag-degraded"
)

// ShouldCritique returns (true, trigger) if the request requires a self-critique pass.
// Precedence: opt-in > rag-degraded+domain > plain domain > side-effect.
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

// SelfCritiquer applies a single critique pass to a generated response.
type SelfCritiquer struct {
	backend adapter.InferenceBackend
}

// NewSelfCritiquer returns a SelfCritiquer backed by the given inference backend.
func NewSelfCritiquer(backend adapter.InferenceBackend) *SelfCritiquer {
	return &SelfCritiquer{backend: backend}
}

// Critique asks the backend to verify and, if necessary, correct the original
// content. On backend failure the original content is returned unchanged.
func (sc *SelfCritiquer) Critique(ctx context.Context, originalContent string, req OrchestrationRequest) (string, error) {
	critiquePrompt := fmt.Sprintf(
		"Review this response for accuracy and safety. If correct, respond with the original. If not, provide a corrected version:\n\n%s",
		originalContent,
	)
	resp, err := sc.backend.Generate(ctx, &adapter.GenerateRequest{
		Model:    req.Model,
		Messages: []adapter.Message{{Role: "user", Content: critiquePrompt}},
	})
	if err != nil {
		return originalContent, err
	}
	return resp.Choices[0].Message.Content, nil
}
