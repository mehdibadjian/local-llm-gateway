package memory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// SessionStore manages conversation history in Redis using a sliding TTL window.
type SessionStore struct {
	rdb redis.UniversalClient
}

// NewSessionStore returns a SessionStore backed by the given Redis client.
func NewSessionStore(rdb redis.UniversalClient) *SessionStore {
	return &SessionStore{rdb: rdb}
}

// SaveMessage appends a message to the session list and enforces a 200-entry cap.
// All four commands run in a single pipeline for atomicity.
func (s *SessionStore) SaveMessage(ctx context.Context, sessionID string, msg Message) error {
	listKey := fmt.Sprintf(SessionListKey, sessionID)
	metaKey := fmt.Sprintf(SessionHashKey, sessionID)

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	_, err = s.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.RPush(ctx, listKey, data)
		pipe.LTrim(ctx, listKey, -(SessionMaxMsgs), -1)
		pipe.Expire(ctx, listKey, SessionTTL)
		pipe.HSet(ctx, metaKey, "session_id", sessionID)
		pipe.Expire(ctx, metaKey, SessionTTL)
		return nil
	})
	return err
}

// LoadHistory retrieves all messages for the session in insertion order.
func (s *SessionStore) LoadHistory(ctx context.Context, sessionID string) ([]Message, error) {
	listKey := fmt.Sprintf(SessionListKey, sessionID)

	raw, err := s.rdb.LRange(ctx, listKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("lrange session history: %w", err)
	}

	msgs := make([]Message, 0, len(raw))
	for _, r := range raw {
		var m Message
		if err := json.Unmarshal([]byte(r), &m); err != nil {
			return nil, fmt.Errorf("unmarshal message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// DeleteSession removes all keys associated with a session.
func (s *SessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	listKey := fmt.Sprintf(SessionListKey, sessionID)
	metaKey := fmt.Sprintf(SessionHashKey, sessionID)
	return s.rdb.Del(ctx, listKey, metaKey).Err()
}

// TouchSession resets the sliding TTL window on both session keys.
func (s *SessionStore) TouchSession(ctx context.Context, sessionID string) error {
	listKey := fmt.Sprintf(SessionListKey, sessionID)
	metaKey := fmt.Sprintf(SessionHashKey, sessionID)

	_, err := s.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Expire(ctx, listKey, SessionTTL)
		pipe.Expire(ctx, metaKey, SessionTTL)
		return nil
	})
	return err
}
