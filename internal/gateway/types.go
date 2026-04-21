package gateway

import "github.com/caw/wrapper/internal/adapter"

// ChatCompletionRequest mirrors the OpenAI Chat Completions request schema
// with CAW-specific extensions under x-caw-options.
type ChatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []adapter.Message `json:"messages"`
	Stream         bool            `json:"stream"`
	Temperature    float64         `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	// CAW extensions
	AgentMode  bool   `json:"agent_mode,omitempty"`
	RAGEnabled bool   `json:"rag_enabled,omitempty"`
	Domain     string `json:"domain,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
}

// ResponseFormat mirrors the OpenAI response_format field.
// Defined here (gateway-owned) and re-exported for use in orchestration.
type ResponseFormat struct {
	Type string `json:"type"` // "json_object" or "text"
}

type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`  // "chat.completion" or "chat.completion.chunk"
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *Usage                 `json:"usage,omitempty"`
}

type ChatCompletionChoice struct {
	Index        int              `json:"index"`
	Message      *adapter.Message `json:"message,omitempty"`
	Delta        *adapter.Message `json:"delta,omitempty"`
	FinishReason *string          `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type DocumentRequest struct {
	Domain  string `json:"domain"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content"`
}

type DocumentResponse struct {
	DocumentID string `json:"document_id"`
	Status     string `json:"status"`
}

// errorBody is the OpenAI-compatible error envelope.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}
