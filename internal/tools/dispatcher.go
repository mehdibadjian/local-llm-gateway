package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/grammar"
	"github.com/redis/go-redis/v9"
)

// Dispatcher routes ToolCall executions to the correct executor backend.
type Dispatcher struct {
	registry  *Registry
	sandbox   *Sandbox
	webSearch *WebSearchExecutor
	webFetch  *WebFetchExecutor
}

// webSearchInput is the JSON input schema for the web_search builtin.
type webSearchInput struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

// webFetchInput is the JSON input schema for the web_fetch builtin.
type webFetchInput struct {
	URL      string `json:"url"`
	MaxBytes int    `json:"max_bytes"`
}

// NewDispatcher constructs a Dispatcher.
func NewDispatcher(registry *Registry, sandbox *Sandbox) *Dispatcher {
	return &Dispatcher{registry: registry, sandbox: sandbox}
}

// NewDispatcherWithLearn constructs a Dispatcher with web search/fetch executors
// that auto-enqueue learned content into the RAG ingest pipeline.
func NewDispatcherWithLearn(registry *Registry, sandbox *Sandbox, rdb *redis.Client) *Dispatcher {
	return &Dispatcher{
		registry:  registry,
		sandbox:   sandbox,
		webSearch: NewWebSearchExecutor(rdb),
		webFetch:  NewWebFetchExecutor(rdb),
	}
}

// EnrichRequestWithGrammar populates req.Grammar with the JSON tool-call grammar
// so that the inference backend constrains its output to valid JSON tool calls.
func EnrichRequestWithGrammar(req *adapter.GenerateRequest) {
	if req.Grammar != "" {
		return
	}
	g, err := grammar.LoadGrammar("json")
	if err == nil {
		req.Grammar = g
	}
}

// Execute resolves the named tool and delegates to the appropriate executor.
// Built-in tools (web_search, web_fetch) are handled directly without a registry lookup.
func (d *Dispatcher) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	// Short-circuit for self-learning builtins — they are not stored in the DB.
	switch call.ToolName {
	case "web_search":
		return d.executeWebSearch(ctx, call, time.Now())
	case "web_fetch":
		return d.executeWebFetch(ctx, call, time.Now())
	}

	tool, err := d.registry.Get(ctx, call.ToolName)
	if err != nil {
		return nil, fmt.Errorf("tool not found: %w", err)
	}

	switch tool.ExecutorType {
	case "builtin":
		return d.executeBuiltin(ctx, tool, call)
	case "subprocess":
		return d.sandbox.Run(ctx, tool, call)
	case "http":
		return d.executeHTTP(ctx, tool, call)
	case "plugin":
		return d.executePlugin(ctx, tool, call)
	}
	return nil, fmt.Errorf("unknown executor type: %s", tool.ExecutorType)
}

// executeBuiltin dispatches to the appropriate builtin handler.
func (d *Dispatcher) executeBuiltin(ctx context.Context, tool *Tool, call ToolCall) (*ToolResult, error) {
	start := time.Now()

	switch tool.Name {
	case "web_search":
		return d.executeWebSearch(ctx, call, start)
	case "web_fetch":
		return d.executeWebFetch(ctx, call, start)
	default:
		// echo builtin: returns raw input
		return &ToolResult{
			ToolCallID: call.ID,
			Output:     string(call.Input),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}
}

func (d *Dispatcher) executeWebSearch(ctx context.Context, call ToolCall, start time.Time) (*ToolResult, error) {
	var in webSearchInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return &ToolResult{ToolCallID: call.ID, Error: "invalid input: " + err.Error(), DurationMs: time.Since(start).Milliseconds()}, nil
	}
	if in.Query == "" {
		return &ToolResult{ToolCallID: call.ID, Error: "query is required", DurationMs: time.Since(start).Milliseconds()}, nil
	}

	exec := d.webSearch
	if exec == nil {
		exec = NewWebSearchExecutor(nil)
	}

	results, err := exec.Execute(ctx, in.Query, in.MaxResults)
	if err != nil {
		return &ToolResult{ToolCallID: call.ID, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}, nil
	}

	out, _ := json.Marshal(results)
	return &ToolResult{ToolCallID: call.ID, Output: string(out), DurationMs: time.Since(start).Milliseconds()}, nil
}

func (d *Dispatcher) executeWebFetch(ctx context.Context, call ToolCall, start time.Time) (*ToolResult, error) {
	var in webFetchInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return &ToolResult{ToolCallID: call.ID, Error: "invalid input: " + err.Error(), DurationMs: time.Since(start).Milliseconds()}, nil
	}
	if in.URL == "" {
		return &ToolResult{ToolCallID: call.ID, Error: "url is required", DurationMs: time.Since(start).Milliseconds()}, nil
	}

	exec := d.webFetch
	if exec == nil {
		exec = NewWebFetchExecutor(nil)
	}

	text, err := exec.Execute(ctx, in.URL, in.MaxBytes)
	if err != nil {
		return &ToolResult{ToolCallID: call.ID, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}, nil
	}

	return &ToolResult{ToolCallID: call.ID, Output: text, DurationMs: time.Since(start).Milliseconds()}, nil
}

// executeHTTP POSTs the call input JSON to the tool's EndpointURL.
func (d *Dispatcher) executeHTTP(ctx context.Context, tool *Tool, call ToolCall) (*ToolResult, error) {
	start := time.Now()

	payload, err := json.Marshal(call.Input)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tool.EndpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &ToolResult{
			ToolCallID: call.ID,
			Error:      err.Error(),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &ToolResult{
			ToolCallID: call.ID,
			Error:      err.Error(),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	return &ToolResult{
		ToolCallID: call.ID,
		Output:     string(body),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// executePlugin forks the plugin binary, passing call input as a PluginRequest and
// returning the PluginResponse result. Non-zero exit or timeout returns an error
// in the ToolResult without crashing the main process.
func (d *Dispatcher) executePlugin(ctx context.Context, tool *Tool, call ToolCall) (*ToolResult, error) {
	start := time.Now()

	var params map[string]interface{}
	if err := json.Unmarshal(call.Input, &params); err != nil {
		params = make(map[string]interface{})
	}

	pe := &PluginExecutor{BinaryPath: tool.EndpointURL, Timeout: 5 * time.Second}
	output, err := pe.Execute(ctx, tool.Name, params)
	if err != nil {
		return &ToolResult{
			ToolCallID: call.ID,
			Error:      err.Error(),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}
	return &ToolResult{
		ToolCallID: call.ID,
		Output:     output,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}
