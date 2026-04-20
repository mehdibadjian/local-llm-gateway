package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/caw/wrapper/internal/mcp"
	"github.com/caw/wrapper/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fake ToolSource ───────────────────────────────────────────────────────────

type fakeToolSource struct {
	listed   []tools.Tool
	executed *tools.ToolResult
	execErr  error
}

func (f *fakeToolSource) ListTools(_ context.Context) ([]tools.Tool, error) {
	return f.listed, nil
}

func (f *fakeToolSource) ExecuteTool(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	result := *f.executed
	result.ToolCallID = call.ID
	return &result, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newServer(ts mcp.ToolSource) *mcp.Server { return mcp.NewServer(ts) }

func parseResponse(t *testing.T, body []byte) mcp.Response {
	t.Helper()
	var r mcp.Response
	require.NoError(t, json.Unmarshal(body, &r))
	return r
}

// ── initialize ────────────────────────────────────────────────────────────────

func TestMCP_Initialize(t *testing.T) {
	srv := newServer(&fakeToolSource{})
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test","version":"1"}}}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))

	assert.Nil(t, resp.Error)
	require.NotNil(t, resp.Result)

	raw, _ := json.Marshal(resp.Result)
	var result mcp.InitializeResult
	require.NoError(t, json.Unmarshal(raw, &result))

	assert.Equal(t, mcp.ProtocolVersion, result.ProtocolVersion)
	assert.Equal(t, "caw", result.ServerInfo.Name)
	assert.NotNil(t, result.Capabilities.Tools)
}

// ── tools/list ────────────────────────────────────────────────────────────────

func TestMCP_ToolsList_Empty(t *testing.T) {
	srv := newServer(&fakeToolSource{listed: nil})
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))

	assert.Nil(t, resp.Error)
	raw, _ := json.Marshal(resp.Result)
	var result mcp.ToolsListResult
	require.NoError(t, json.Unmarshal(raw, &result))

	// Always includes web_search + web_fetch builtins even when registry is empty.
	assert.GreaterOrEqual(t, len(result.Tools), 2)
	names := make(map[string]bool)
	for _, t2 := range result.Tools {
		names[t2.Name] = true
	}
	assert.True(t, names["web_search"], "web_search builtin must be present")
	assert.True(t, names["web_fetch"], "web_fetch builtin must be present")
}

func TestMCP_ToolsList_WithRegistered(t *testing.T) {
	src := &fakeToolSource{
		listed: []tools.Tool{
			{Name: "my_tool", Description: "does stuff", Enabled: true,
				InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	srv := newServer(src)
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))
	assert.Nil(t, resp.Error)

	raw, _ := json.Marshal(resp.Result)
	var result mcp.ToolsListResult
	require.NoError(t, json.Unmarshal(raw, &result))

	names := make(map[string]bool)
	for _, t2 := range result.Tools {
		names[t2.Name] = true
	}
	assert.True(t, names["my_tool"])
	assert.True(t, names["web_search"])
	assert.True(t, names["web_fetch"])
}

// ── tools/call ────────────────────────────────────────────────────────────────

func TestMCP_ToolsCall_Success(t *testing.T) {
	src := &fakeToolSource{
		executed: &tools.ToolResult{Output: "hello world"},
	}
	srv := newServer(src)
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"my_tool","arguments":{"x":1}}}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))

	assert.Nil(t, resp.Error)
	raw, _ := json.Marshal(resp.Result)
	var result mcp.ToolsCallResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "hello world", result.Content[0].Text)
}

func TestMCP_ToolsCall_ToolError(t *testing.T) {
	src := &fakeToolSource{
		executed: &tools.ToolResult{Error: "tool failed"},
	}
	srv := newServer(src)
	body := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"bad_tool","arguments":{}}}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))

	assert.Nil(t, resp.Error) // RPC itself succeeds; error is in result.isError
	raw, _ := json.Marshal(resp.Result)
	var result mcp.ToolsCallResult
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.True(t, result.IsError)
	assert.Equal(t, "tool failed", result.Content[0].Text)
}

func TestMCP_ToolsCall_MissingName(t *testing.T) {
	srv := newServer(&fakeToolSource{executed: &tools.ToolResult{Output: "x"}})
	body := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"arguments":{}}}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))
	assert.NotNil(t, resp.Error)
	assert.Equal(t, mcp.ErrInvalidParams, resp.Error.Code)
}

// ── error cases ───────────────────────────────────────────────────────────────

func TestMCP_MethodNotFound(t *testing.T) {
	srv := newServer(&fakeToolSource{})
	body := `{"jsonrpc":"2.0","id":7,"method":"unknown/method"}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))
	assert.NotNil(t, resp.Error)
	assert.Equal(t, mcp.ErrMethodNotFound, resp.Error.Code)
}

func TestMCP_InvalidJSON(t *testing.T) {
	srv := newServer(&fakeToolSource{})
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(`not json`)))
	assert.NotNil(t, resp.Error)
	assert.Equal(t, mcp.ErrParse, resp.Error.Code)
}

func TestMCP_WrongVersion(t *testing.T) {
	srv := newServer(&fakeToolSource{})
	body := `{"jsonrpc":"1.0","id":9,"method":"initialize"}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))
	assert.NotNil(t, resp.Error)
	assert.Equal(t, mcp.ErrInvalidRequest, resp.Error.Code)
}

func TestMCP_Ping(t *testing.T) {
	srv := newServer(&fakeToolSource{})
	body := `{"jsonrpc":"2.0","id":10,"method":"ping"}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))
	assert.Nil(t, resp.Error)
}

func TestMCP_Initialized_Notification(t *testing.T) {
	// "initialized" is a notification (no response ID in real use, but we handle it gracefully).
	srv := newServer(&fakeToolSource{})
	body := `{"jsonrpc":"2.0","id":null,"method":"initialized"}`
	resp := parseResponse(t, srv.Handle(context.Background(), []byte(body)))
	assert.Nil(t, resp.Error)
}
