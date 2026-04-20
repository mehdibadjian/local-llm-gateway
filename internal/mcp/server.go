// Package mcp implements a Model Context Protocol (MCP) server over HTTP/JSON-RPC 2.0.
// It exposes CAW's tool registry to MCP clients (e.g., Claude Code CLI) so they can
// discover and invoke CAW tools without going through the OpenAI-compatible API.
//
// Endpoint: POST /mcp
// Protocol: JSON-RPC 2.0, MCP spec version 2024-11-05
// Supported methods: initialize, tools/list, tools/call
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/caw/wrapper/internal/tools"
)

// ToolSource provides the tool registry and execution capability needed by the MCP server.
type ToolSource interface {
	ListTools(ctx context.Context) ([]tools.Tool, error)
	ExecuteTool(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error)
}

// Server handles MCP JSON-RPC 2.0 requests.
type Server struct {
	ts ToolSource
}

// NewServer creates a new MCP server backed by the given ToolSource.
func NewServer(ts ToolSource) *Server {
	return &Server{ts: ts}
}

// Handle processes a single JSON-RPC request body and returns the response bytes.
func (s *Server) Handle(ctx context.Context, body []byte) []byte {
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return s.errResponse(nil, ErrParse, "parse error: "+err.Error())
	}
	if req.JSONRPC != "2.0" {
		return s.errResponse(req.ID, ErrInvalidRequest, "jsonrpc must be \"2.0\"")
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized":
		// Notification — no response needed; return empty success.
		return s.okResponse(req.ID, nil)
	case "tools/list":
		return s.handleToolsList(ctx, req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	case "ping":
		return s.okResponse(req.ID, map[string]string{})
	default:
		return s.errResponse(req.ID, ErrMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) handleInitialize(req Request) []byte {
	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      ServerInfo{Name: "caw", Version: "1.0.0"},
		Capabilities: Capabilities{
			Tools: &ToolsCapability{ListChanged: false},
		},
	}
	return s.okResponse(req.ID, result)
}

func (s *Server) handleToolsList(ctx context.Context, req Request) []byte {
	all, err := s.ts.ListTools(ctx)
	if err != nil {
		return s.errResponse(req.ID, -32000, "list tools: "+err.Error())
	}

	mcpTools := make([]MCPTool, 0, len(all))
	for _, t := range all {
		var schema interface{}
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &schema)
		}
		if schema == nil {
			schema = map[string]string{"type": "object"}
		}
		mcpTools = append(mcpTools, MCPTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	// Always expose the two self-learning builtins.
	mcpTools = append(mcpTools,
		builtinWebSearch(),
		builtinWebFetch(),
	)

	return s.okResponse(req.ID, ToolsListResult{Tools: mcpTools})
}

func (s *Server) handleToolsCall(ctx context.Context, req Request) []byte {
	var p ToolsCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.errResponse(req.ID, ErrInvalidParams, "invalid params: "+err.Error())
	}
	if p.Name == "" {
		return s.errResponse(req.ID, ErrInvalidParams, "name is required")
	}

	args := p.Arguments
	if args == nil {
		args = json.RawMessage("{}")
	}

	result, err := s.ts.ExecuteTool(ctx, tools.ToolCall{
		ID:       fmt.Sprintf("mcp-%s", p.Name),
		ToolName: p.Name,
		Input:    args,
	})
	if err != nil {
		return s.okResponse(req.ID, ToolsCallResult{
			Content: []ContentBlock{{Type: "text", Text: "error: " + err.Error()}},
			IsError: true,
		})
	}

	text := result.Output
	isError := false
	if result.Error != "" {
		text = result.Error
		isError = true
	}

	return s.okResponse(req.ID, ToolsCallResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
		IsError: isError,
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *Server) okResponse(id json.RawMessage, result interface{}) []byte {
	b, _ := json.Marshal(Response{JSONRPC: "2.0", ID: id, Result: result})
	return b
}

func (s *Server) errResponse(id json.RawMessage, code int, msg string) []byte {
	b, _ := json.Marshal(Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}})
	return b
}

// builtinWebSearch returns the MCP tool descriptor for the web_search builtin.
func builtinWebSearch() MCPTool {
	return MCPTool{
		Name:        "web_search",
		Description: "Search the web for current information. Results are automatically ingested into the RAG knowledge base so future queries benefit from what was learned.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":       map[string]string{"type": "string", "description": "Search query"},
				"max_results": map[string]interface{}{"type": "integer", "description": "Maximum number of results (default 5)", "default": 5},
			},
			"required": []string{"query"},
		},
	}
}

// builtinWebFetch returns the MCP tool descriptor for the web_fetch builtin.
func builtinWebFetch() MCPTool {
	return MCPTool{
		Name:        "web_fetch",
		Description: "Fetch and read the content of a URL as plain text. Content is automatically ingested into the RAG knowledge base for future retrieval.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url":       map[string]string{"type": "string", "description": "URL to fetch"},
				"max_bytes": map[string]interface{}{"type": "integer", "description": "Maximum bytes to read (default 32768)"},
			},
			"required": []string{"url"},
		},
	}
}
