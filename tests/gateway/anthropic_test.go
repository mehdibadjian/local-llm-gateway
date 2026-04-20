package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockBackendForMessages(content string) *adapter.MockInferenceBackend {
	return &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, _ *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			return &adapter.GenerateResponse{
				ID:    "msg_test",
				Model: "gemma:2b",
				Choices: []adapter.Choice{{
					Message:      adapter.Message{Role: "assistant", Content: content},
					FinishReason: "stop",
				}},
			}, nil
		},
	}
}

func TestMessagesHandler_NonStreaming(t *testing.T) {
	app, _ := newTestServer(t, mockBackendForMessages("Hello from CAW!"))

	// content as plain string, system as plain string
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	assert.Equal(t, "message", result["type"])
	assert.Equal(t, "assistant", result["role"])
	assert.Equal(t, "end_turn", result["stop_reason"])

	content := result["content"].([]any)
	require.Len(t, content, 1)
	block := content[0].(map[string]any)
	assert.Equal(t, "text", block["type"])
	assert.Equal(t, "Hello from CAW!", block["text"])
}

func TestMessagesHandler_ContentAsArray(t *testing.T) {
	var captured string
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			if len(req.Messages) > 0 {
				captured = req.Messages[len(req.Messages)-1].Content
			}
			return &adapter.GenerateResponse{
				ID: "msg_test", Model: "gemma:2b",
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "ok"}}},
			}, nil
		},
	}
	app, _ := newTestServer(t, backend)

	// content as array of blocks — what Claude Code CLI actually sends
	body := `{"model":"claude-sonnet-4-5","max_tokens":512,"messages":[{"role":"user","content":[{"type":"text","text":"hello from claude code"}]}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key") // Anthropic SDK header

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "hello from claude code", captured)
}

func TestMessagesHandler_SystemPromptIncluded(t *testing.T) {
	var capturedMsgs []adapter.Message
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			capturedMsgs = req.Messages
			return &adapter.GenerateResponse{
				ID:    "msg_test",
				Model: "gemma:2b",
				Choices: []adapter.Choice{{
					Message: adapter.Message{Role: "assistant", Content: "ok"},
				}},
			}, nil
		},
	}
	app, _ := newTestServer(t, backend)

	// system as plain string
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":512,"system":"You are a pirate.","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	require.Len(t, capturedMsgs, 2)
	assert.Equal(t, "system", capturedMsgs[0].Role)
	assert.Equal(t, "You are a pirate.", capturedMsgs[0].Content)
	assert.Equal(t, "user", capturedMsgs[1].Role)
}

func TestMessagesHandler_SystemAsArray(t *testing.T) {
	var capturedSystem string
	backend := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
				capturedSystem = req.Messages[0].Content
			}
			return &adapter.GenerateResponse{
				ID: "msg_test", Model: "gemma:2b",
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "ok"}}},
			}, nil
		},
	}
	app, _ := newTestServer(t, backend)

	// system as array of content blocks — what Claude Code CLI actually sends
	body := `{"model":"claude-sonnet-4-6","max_tokens":512,"system":[{"type":"text","text":"You are a helpful assistant."}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key")

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "You are a helpful assistant.", capturedSystem)
}

func TestMessagesHandler_EmptyMessagesReturns422(t *testing.T) {
	app, _ := newTestServer(t, mockBackendForMessages(""))

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":512,"messages":[]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestMessagesHandler_MissingAuthReturns401(t *testing.T) {
	app, _ := newTestServer(t, mockBackendForMessages(""))

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":512,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestMessagesHandler_Streaming(t *testing.T) {
	backend := &adapter.MockInferenceBackend{
		StreamFn: func(_ context.Context, _ *adapter.GenerateRequest) (<-chan *adapter.GenerateResponse, error) {
			ch := make(chan *adapter.GenerateResponse, 3)
			ch <- &adapter.GenerateResponse{Choices: []adapter.Choice{{Delta: &adapter.Message{Role: "assistant", Content: "Hello"}}}}
			ch <- &adapter.GenerateResponse{Choices: []adapter.Choice{{Delta: &adapter.Message{Role: "assistant", Content: " world"}}}}
			close(ch)
			return ch, nil
		},
	}
	app, _ := newTestServer(t, backend)

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":512,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body2 := string(raw)

	assert.Contains(t, body2, "message_start")
	assert.Contains(t, body2, "content_block_start")
	assert.Contains(t, body2, "content_block_delta")
	assert.Contains(t, body2, "Hello")
	assert.Contains(t, body2, "message_stop")
}
