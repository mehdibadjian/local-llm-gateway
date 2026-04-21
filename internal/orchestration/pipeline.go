package orchestration

import (
	"context"
	"fmt"

	"github.com/caw/wrapper/internal/adapter"
)

// Pipeline wires together ContextManager, TaskPlanner, CoT decomposer,
// OutputFormatter, code-feedback loop, SelfCritiquer, WebAugmenter, and the
// PEV state machine into a single orchestration pass that amplifies gemma:2b
// without changing its weights.
type Pipeline struct {
	contextMgr   *ContextManager
	formatter    *OutputFormatter
	critiquer    *SelfCritiquer
	cot          *ChainOfThoughtDecomposer
	codeFeedback *CodeFeedbackLoop
	pev          *PEVOrchestrator
	webAugmenter *WebAugmenter // nil = no web augmentation
}

// NewPipeline constructs a Pipeline from its component parts.
func NewPipeline(cm *ContextManager, formatter *OutputFormatter, critiquer *SelfCritiquer) *Pipeline {
	return &Pipeline{
		contextMgr:   cm,
		formatter:    formatter,
		critiquer:    critiquer,
		cot:          NewChainOfThoughtDecomposer(formatter.Backend()),
		codeFeedback: NewCodeFeedbackLoop(formatter.Backend()),
		pev:          NewPEVOrchestrator(formatter.Backend()),
	}
}

// WithWebAugmenter attaches a WebAugmenter so the pipeline can enrich queries
// with live web results before passing them to the model.
func (p *Pipeline) WithWebAugmenter(wa *WebAugmenter) *Pipeline {
	p.webAugmenter = wa
	return p
}

// Run executes the full amplified orchestration pipeline:
//
//  1. Context load + compression
//  2. Intent classification (simple / CoT / code / RAG / structured / agent)
//  3. Generation — with CoT decomposition for complex queries
//  4. Code execution feedback loop for coding intent
//  5. Scored self-critique retry loop (up to 3 rounds) when triggered
func (p *Pipeline) Run(ctx context.Context, req OrchestrationRequest) (*OrchestrationResult, error) {
	// 1. Load and manage context.
	messages, err := p.contextMgr.LoadAndManage(ctx, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("context manager: %w", err)
	}
	allMessages := append(messages, req.Messages...)

	// 1b. Web augmentation — inject live search results when signals detected.
	webSearched := false
	if p.webAugmenter != nil {
		augmented, searched, augErr := p.webAugmenter.Augment(ctx, allMessages)
		if augErr == nil && searched {
			allMessages = augmented
			webSearched = true
		}
	}

	// 2. Classify intent.
	intent, isFallback := ClassifyIntent(req)

	result := &OrchestrationResult{
		Intent:         intent,
		IntentFallback: isFallback,
		RAGDegraded:    req.RAGDegraded,
		WebSearched:    webSearched,
	}

	genReq := &adapter.GenerateRequest{
		Model:    req.Model,
		Messages: allMessages,
		Stream:   req.Stream,
	}

	// 3. Generate — PEV for code/complex-reasoning, CoT fallback, direct path otherwise.
	var content string
	var formatErr bool

	switch intent {
	case IntentComplexReasoning:
		var pevScore int
		content, pevScore, err = p.pev.Run(ctx, genReq)
		if err != nil {
			// PEV failed — fall back to CoT.
			content, err = p.cot.Decompose(ctx, genReq)
			if err != nil {
				content, formatErr, err = p.formatter.Format(ctx, genReq, IntentSimpleGenerate)
				if err != nil {
					return nil, fmt.Errorf("fallback generate: %w", err)
				}
			} else {
				result.CoTApplied = true
			}
		} else {
			result.PEVApplied = true
			result.PEVScore = pevScore
		}

	case IntentCodeGeneration:
		var pevScore int
		content, pevScore, err = p.pev.Run(ctx, genReq)
		if err != nil {
			// PEV failed — fall back to direct generation + code-feedback loop.
			content, formatErr, err = p.formatter.Format(ctx, genReq, intent)
			if err != nil {
				return nil, fmt.Errorf("code generate: %w", err)
			}
			// 4. Code execution feedback loop.
			improved, ran, feedbackErr := p.codeFeedback.Run(ctx, content, genReq)
			if feedbackErr == nil && ran {
				content = improved
				result.CodeFeedback = true
			}
		} else {
			result.PEVApplied = true
			result.PEVScore = pevScore
		}

	default:
		content, formatErr, err = p.formatter.Format(ctx, genReq, intent)
		if err != nil {
			return nil, fmt.Errorf("output formatter: %w", err)
		}
	}

	result.Content = content
	result.FormatError = formatErr

	// 5. Scored self-critique retry loop (conditional).
	maxRounds := req.MaxCritique
	if maxRounds <= 0 {
		maxRounds = defaultMaxCritiqueRounds
	}

	// Always critique complex reasoning and code; conditionally for others.
	shouldCritique := intent == IntentComplexReasoning || intent == IntentCodeGeneration
	if triggered, _ := ShouldCritique(req); triggered {
		shouldCritique = true
	}

	if shouldCritique {
		improved, score, rounds, critiqueErr := p.critiquer.CritiqueLoop(ctx, content, req, maxRounds)
		if critiqueErr == nil {
			result.Content = improved
		}
		result.CritiqueApplied = true
		result.CritiqueRounds = rounds
		result.CritiqueScore = score
	}

	return result, nil
}

