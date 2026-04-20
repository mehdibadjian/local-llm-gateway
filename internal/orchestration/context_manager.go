package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/memory"
	"github.com/redis/go-redis/v9"
)

const (
	CompressionThreshold = 3000 // tokens
	CompressionTarget    = 2500 // tokens after compression
	LockTTL              = 2 * time.Second
	LockWaitTimeout      = 500 * time.Millisecond
)

// ContextManager loads conversation history from Redis and ensures the token
// count stays within bounds, using an atomic SET NX lock to serialise compression.
type ContextManager struct {
	session *memory.SessionStore
	rdb     redis.UniversalClient
	backend adapter.InferenceBackend
}

// NewContextManager returns a ContextManager.
func NewContextManager(session *memory.SessionStore, rdb redis.UniversalClient, backend adapter.InferenceBackend) *ContextManager {
	return &ContextManager{
		session: session,
		rdb:     rdb,
		backend: backend,
	}
}

// LoadAndManage loads session history and compresses or truncates it when the
// token count exceeds CompressionThreshold.
func (cm *ContextManager) LoadAndManage(ctx context.Context, sessionID string) ([]adapter.Message, error) {
	memMsgs, err := cm.session.LoadHistory(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}

	messages := memoryToAdapter(memMsgs)
	tokenCount := countTokens(messages)

	if tokenCount < CompressionThreshold {
		return messages, nil
	}

	lockKey := fmt.Sprintf("caw:compression:%s", sessionID)
	acquired, err := cm.rdb.SetNX(ctx, lockKey, "1", LockTTL).Result()
	if err != nil {
		// Redis error — fall back to hard truncation.
		return HardTruncate(messages, CompressionTarget), nil
	}

	if acquired {
		// Reload after winning the lock: another goroutine may have compressed
		// and DEL'd before we called SetNX (TOCTOU guard).
		reloaded, reloadErr := cm.session.LoadHistory(ctx, sessionID)
		if reloadErr == nil {
			reloadedMsgs := memoryToAdapter(reloaded)
			if countTokens(reloadedMsgs) < CompressionThreshold {
				cm.rdb.Del(ctx, lockKey) //nolint:errcheck
				return reloadedMsgs, nil
			}
			messages = reloadedMsgs
		}

		// Compress, write back, then release lock (held until write-back).
		compressed, compErr := cm.CompressHistory(ctx, messages)
		if compErr != nil {
			cm.rdb.Del(ctx, lockKey) //nolint:errcheck
			return HardTruncate(messages, CompressionTarget), nil
		}
		writeErr := cm.writeBackHistory(ctx, sessionID, compressed)
		cm.rdb.Del(ctx, lockKey) //nolint:errcheck — release after write-back
		if writeErr != nil {
			return compressed, nil
		}
		return compressed, nil
	}

	// Loser: poll for lock release, then hard-truncate.
	deadline := time.Now().Add(LockWaitTimeout)
	for time.Now().Before(deadline) {
		exists, _ := cm.rdb.Exists(ctx, lockKey).Result()
		if exists == 0 {
			// Lock released — reload history.
			reloaded, err := cm.session.LoadHistory(ctx, sessionID)
			if err == nil {
				msgs := memoryToAdapter(reloaded)
				if countTokens(msgs) < CompressionThreshold {
					return msgs, nil
				}
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return HardTruncate(messages, CompressionTarget), nil
}

// CompressHistory sends a summarise prompt to the backend and returns a
// condensed message slice.
func (cm *ContextManager) CompressHistory(ctx context.Context, messages []adapter.Message) ([]adapter.Message, error) {
	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}

	req := &adapter.GenerateRequest{
		Model: "compression",
		Messages: []adapter.Message{
			{
				Role:    "user",
				Content: fmt.Sprintf("Summarize this conversation concisely, preserving key facts and context:\n\n%s", sb.String()),
			},
		},
	}

	resp, err := cm.backend.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}

	summary := resp.Choices[0].Message.Content
	return []adapter.Message{
		{Role: "system", Content: summary},
	}, nil
}

// HardTruncate keeps the most recent messages whose cumulative token count
// does not exceed targetTokens, always preserving message ordering.
func HardTruncate(messages []adapter.Message, targetTokens int) []adapter.Message {
	total := 0
	start := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		t := len(strings.Fields(messages[i].Content))
		if total+t > targetTokens {
			break
		}
		total += t
		start = i
	}
	return messages[start:]
}

// writeBackHistory replaces the session history with the compressed slice.
func (cm *ContextManager) writeBackHistory(ctx context.Context, sessionID string, messages []adapter.Message) error {
	listKey := fmt.Sprintf("caw:session:%s:messages", sessionID)
	pipe := cm.rdb.Pipeline()
	pipe.Del(ctx, listKey)
	for _, m := range messages {
		data, err := json.Marshal(memory.Message{Role: m.Role, Content: m.Content})
		if err != nil {
			return err
		}
		pipe.RPush(ctx, listKey, data)
	}
	pipe.Expire(ctx, listKey, memory.SessionTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// countTokens returns a simple word-count-based token estimate.
func countTokens(messages []adapter.Message) int {
	total := 0
	for _, m := range messages {
		total += len(strings.Fields(m.Content))
	}
	return total
}

// memoryToAdapter converts memory.Message slice to adapter.Message slice.
func memoryToAdapter(msgs []memory.Message) []adapter.Message {
	out := make([]adapter.Message, len(msgs))
	for i, m := range msgs {
		out[i] = adapter.Message{Role: m.Role, Content: m.Content}
	}
	return out
}
