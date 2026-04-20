package tools

import (
	"encoding/json"
	"time"
)

// Tool represents a registered capability in the Tool Registry.
type Tool struct {
	ID           string          `json:"id" db:"id"`
	Name         string          `json:"name" db:"name"`
	Description  string          `json:"description" db:"description"`
	InputSchema  json.RawMessage `json:"input_schema" db:"input_schema"`
	ExecutorType string          `json:"executor_type" db:"executor_type"` // builtin|subprocess|http
	EndpointURL  string          `json:"endpoint_url,omitempty" db:"endpoint_url"`
	Enabled      bool            `json:"enabled" db:"enabled"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
}

// ToolCall is a request to invoke a registered tool.
type ToolCall struct {
	ID       string          `json:"id"`
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input"`
}

// ToolResult is the outcome of a tool invocation.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// ValidExecutorTypes enumerates accepted executor_type values.
var ValidExecutorTypes = map[string]bool{
	"builtin":    true,
	"subprocess": true,
	"http":       true,
}
