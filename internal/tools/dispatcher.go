package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Dispatcher routes ToolCall executions to the correct executor backend.
type Dispatcher struct {
	registry *Registry
	sandbox  *Sandbox
}

// NewDispatcher constructs a Dispatcher.
func NewDispatcher(registry *Registry, sandbox *Sandbox) *Dispatcher {
	return &Dispatcher{registry: registry, sandbox: sandbox}
}

// Execute resolves the named tool and delegates to the appropriate executor.
func (d *Dispatcher) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
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

// executeBuiltin is the echo built-in: returns the raw input as output.
func (d *Dispatcher) executeBuiltin(_ context.Context, _ *Tool, call ToolCall) (*ToolResult, error) {
	start := time.Now()
	return &ToolResult{
		ToolCallID: call.ID,
		Output:     string(call.Input),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
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
