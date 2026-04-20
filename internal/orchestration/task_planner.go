package orchestration

import (
	"context"
	"fmt"
	"strings"

	"github.com/caw/wrapper/internal/adapter"
)

// complexitySignals are phrases that indicate a multi-step or reasoning-heavy query.
var complexitySignals = []string{
	"design", "implement", "how to", "how do", "explain", "compare",
	"analyze", "analyse", "walk through", "step by step", "why does",
	"what is the difference", "trade-off", "trade off", "pros and cons",
	"architecture", "algorithm", "optimize", "debug", "refactor",
}

// codeSignals are phrases that indicate a coding request.
var codeSignals = []string{
	"write a", "write the", "implement a", "implement the",
	"code for", "function that", "function to", "program that",
	"python", "golang", "go code", "javascript", "typescript",
	"sql query", "bash script", "dockerfile", "fix this code",
	"what's wrong with", "bug in",
}

// ClassifyIntent derives the processing intent from the request.
// Returns (intent, isFallback). isFallback is true only when no explicit
// signal was present and the default was applied.
func ClassifyIntent(req OrchestrationRequest) (Intent, bool) {
	if req.AgentMode {
		return IntentAgentLoop, false
	}
	if req.RAGEnabled && req.Domain != "" {
		return IntentRAGQuery, false
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		return IntentStructuredOutput, false
	}
	if req.CoTEnabled {
		return IntentComplexReasoning, false
	}
	if len(req.Messages) > 0 {
		last := lastUserMessage(req.Messages)
		lower := strings.ToLower(last)
		if isCodeQuery(lower) {
			return IntentCodeGeneration, false
		}
		if isComplexQuery(lower) {
			return IntentComplexReasoning, false
		}
		return IntentSimpleGenerate, false
	}
	return IntentSimpleGenerate, true
}

// IsComplexQuery reports whether text contains complexity signals.
func IsComplexQuery(text string) bool {
	return isComplexQuery(strings.ToLower(text))
}

// IsCodeQuery reports whether text is a coding request.
func IsCodeQuery(text string) bool {
	return isCodeQuery(strings.ToLower(text))
}

func isComplexQuery(lower string) bool {
	for _, sig := range complexitySignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	// Long multi-sentence question is likely complex.
	sentences := strings.Count(lower, "?") + strings.Count(lower, ".")
	return sentences >= 2 && len(lower) > 120
}

func isCodeQuery(lower string) bool {
	for _, sig := range codeSignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// ChainOfThoughtDecomposer wraps the inference backend with step-by-step reasoning.
type ChainOfThoughtDecomposer struct {
	backend adapter.InferenceBackend
}

// NewChainOfThoughtDecomposer returns a CoT decomposer.
func NewChainOfThoughtDecomposer(backend adapter.InferenceBackend) *ChainOfThoughtDecomposer {
	return &ChainOfThoughtDecomposer{backend: backend}
}

// Decompose runs a two-phase CoT pass:
//  1. Ask gemma:2b to break the question into explicit steps and reason through each.
//  2. Ask it to synthesise a final, clean answer from those steps.
//
// If either sub-call fails the original prompt is passed through unchanged.
func (c *ChainOfThoughtDecomposer) Decompose(ctx context.Context, req *adapter.GenerateRequest) (string, error) {
	// Phase 1: reasoning chain.
	reasoningMsgs := buildCoTMessages(req.Messages)
	reasonReq := &adapter.GenerateRequest{
		Model:    req.Model,
		Messages: reasoningMsgs,
	}
	reasonResp, err := c.backend.Generate(ctx, reasonReq)
	if err != nil {
		return "", fmt.Errorf("cot reasoning phase: %w", err)
	}
	reasoning := reasonResp.Choices[0].Message.Content

	// Phase 2: synthesise a clean final answer.
	synthMsgs := []adapter.Message{
		{
			Role: "user",
			Content: fmt.Sprintf(
				"Based on the following reasoning, provide a clear, concise, and complete final answer. Do not repeat the reasoning steps — just the answer.\n\nReasoning:\n%s",
				reasoning,
			),
		},
	}
	synthResp, err := c.backend.Generate(ctx, &adapter.GenerateRequest{
		Model:    req.Model,
		Messages: synthMsgs,
	})
	if err != nil {
		// Fall back to the raw reasoning if synthesis fails.
		return reasoning, nil
	}
	return synthResp.Choices[0].Message.Content, nil
}

// buildCoTMessages prepends a chain-of-thought system prompt.
func buildCoTMessages(messages []adapter.Message) []adapter.Message {
	system := adapter.Message{
		Role: "system",
		Content: "You are a careful, methodical reasoner. When given a question:\n" +
			"1. Break it into clear sub-problems.\n" +
			"2. Solve each sub-problem step by step, showing your work.\n" +
			"3. Check each step for errors before moving on.\n" +
			"Think out loud — your reasoning will be used to generate a final answer.",
	}
	// Prepend system message (or replace existing one).
	out := []adapter.Message{system}
	for _, m := range messages {
		if m.Role != "system" {
			out = append(out, m)
		}
	}
	return out
}

// lastUserMessage returns the content of the last user message, or "".
func lastUserMessage(messages []adapter.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

