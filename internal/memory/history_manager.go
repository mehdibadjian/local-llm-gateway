package memory

import (
	"context"
	"time"

	"github.com/caw/wrapper/internal/adapter"
)

const tokenBudget = 2000

// EstimateTokens estimates the number of tokens in text using len(text)/4.
func EstimateTokens(text string) int {
	return len(text) / 4
}

// HistoryManager wraps SessionStore and provides budget-aware history loading
// with automatic summarisation when the token budget is exceeded.
type HistoryManager struct {
	store *SessionStore
}

// NewHistoryManager creates a HistoryManager backed by the given SessionStore.
func NewHistoryManager(store *SessionStore) *HistoryManager {
	return &HistoryManager{store: store}
}

// LoadAndTrim loads conversation history for the session.
// If the total estimated token count exceeds 2000 tokens, it calls backend.Generate
// to produce a compact summary, replaces the session with that summary, and returns
// a slice containing only the synthetic system summary message.
// If the budget is not exceeded, history is returned as-is.
func (hm *HistoryManager) LoadAndTrim(ctx context.Context, sessionID string, backend adapter.InferenceBackend) ([]Message, error) {
	history, err := hm.store.LoadHistory(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	total := 0
	for _, m := range history {
		total += EstimateTokens(m.Role + m.Content)
	}

	if total <= tokenBudget {
		return history, nil
	}

	resp, err := backend.Generate(ctx, &adapter.GenerateRequest{
		Messages: []adapter.Message{
			{
				Role:    "user",
				Content: "Summarize the conversation state into a compact JSON object preserving key facts, decisions, and unresolved questions.",
			},
		},
	})
	if err != nil {
		return nil, err
	}

	var summaryContent string
	if len(resp.Choices) > 0 {
		summaryContent = resp.Choices[0].Message.Content
	}

	summaryMsg := Message{
		Role:      "system",
		Content:   summaryContent,
		Timestamp: time.Now().Unix(),
	}

	if err := hm.store.DeleteSession(ctx, sessionID); err != nil {
		return nil, err
	}
	if err := hm.store.SaveMessage(ctx, sessionID, summaryMsg); err != nil {
		return nil, err
	}

	return []Message{summaryMsg}, nil
}
