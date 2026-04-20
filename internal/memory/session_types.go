package memory

import "time"

const (
	SessionHashKey = "caw:session:%s:meta"
	SessionListKey = "caw:session:%s:messages"
	SessionTTL     = 24 * time.Hour
	SessionMaxMsgs = 200
)

// Message is a single conversation turn.
type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"ts,omitempty"`
}

// Session holds session-level metadata.
type Session struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
}
