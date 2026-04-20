package tools_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caw/wrapper/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDispatcher(t *testing.T) (*tools.Dispatcher, *mockPGStore) {
	t.Helper()
	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	sandbox := tools.NewSandbox(tools.SandboxConfig{MemLimitMB: 256, CPUShares: 512, TimeoutSec: 5})
	return tools.NewDispatcher(reg, sandbox), store
}

func TestDispatcher_BuiltinEcho_ReturnsInput(t *testing.T) {
	d, store := newTestDispatcher(t)
	ctx := context.Background()

	// Insert directly into store; registry.Get falls back to pg.GetTool on cache miss.
	store.CreateTool(ctx, tools.Tool{ //nolint:errcheck
		Name:         "echo",
		ExecutorType: "builtin",
		InputSchema:  json.RawMessage(`{}`),
		Enabled:      true,
	})

	input := json.RawMessage(`{"message":"hello"}`)
	result, err := d.Execute(ctx, tools.ToolCall{
		ID:       "call-1",
		ToolName: "echo",
		Input:    input,
	})
	require.NoError(t, err)
	assert.Equal(t, string(input), result.Output)
	assert.Empty(t, result.Error)
}

func TestDispatcher_HTTPExecutor_PostsToEndpoint(t *testing.T) {
	// Start a test HTTP server that echoes the request body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(200)
		w.Write(body) //nolint:errcheck
	}))
	defer srv.Close()

	d, store := newTestDispatcher(t)
	ctx := context.Background()

	store.CreateTool(ctx, tools.Tool{ //nolint:errcheck
		Name:         "http-tool",
		ExecutorType: "http",
		EndpointURL:  srv.URL,
		InputSchema:  json.RawMessage(`{}`),
		Enabled:      true,
	})

	input := json.RawMessage(`{"key":"value"}`)
	result, err := d.Execute(ctx, tools.ToolCall{
		ID:       "call-2",
		ToolName: "http-tool",
		Input:    input,
	})
	require.NoError(t, err)
	assert.Empty(t, result.Error)
	assert.NotEmpty(t, result.Output)
}

func TestDispatcher_UnknownTool_ReturnsError(t *testing.T) {
	d, _ := newTestDispatcher(t)
	ctx := context.Background()

	_, err := d.Execute(ctx, tools.ToolCall{
		ID:       "call-3",
		ToolName: "nonexistent-tool",
		Input:    json.RawMessage(`{}`),
	})
	assert.Error(t, err)
}
