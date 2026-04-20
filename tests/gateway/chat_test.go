package gateway_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCompletion_ValidRequest_Returns200(t *testing.T) {
	app, _ := newTestServer(t, &adapter.MockInferenceBackend{})

	body := `{"model":"gemma:2b","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	raw, _ := io.ReadAll(resp.Body)
	s := string(raw)
	assert.Contains(t, s, `"id"`)
	assert.Contains(t, s, `"choices"`)
	assert.Contains(t, s, `"chat.completion"`)
}

func TestChatCompletion_MissingMessages_Returns422(t *testing.T) {
	app, _ := newTestServer(t, &adapter.MockInferenceBackend{})

	body := `{"model":"gemma:2b"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestChatCompletion_Stream_SendsDoneEvent(t *testing.T) {
	app, _ := newTestServer(t, &adapter.MockInferenceBackend{})

	body := `{"model":"gemma:2b","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	raw, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(raw), "data: [DONE]")
}

func TestChatCompletion_Stream_ClientDisconnect_ReleasesPool(t *testing.T) {
	app, pool := newTestServer(t, &adapter.MockInferenceBackend{})

	body := `{"model":"gemma:2b","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// After the stream completes the pool slot must have been released.
	assert.Equal(t, 0, pool.InFlight())
}
