package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// PluginRequest is the JSON payload written to a plugin binary's stdin.
type PluginRequest struct {
	ToolName string                 `json:"tool_name"`
	Params   map[string]interface{} `json:"params"`
}

// PluginResponse is the JSON payload read from a plugin binary's stdout.
type PluginResponse struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// PluginExecutor forks a community plugin binary as a subprocess, passing
// a JSON-encoded PluginRequest on stdin and reading a PluginResponse from stdout.
type PluginExecutor struct {
	BinaryPath string
	Timeout    time.Duration // defaults to 5s when zero
}

// Execute runs the plugin binary with a 5-second (or configured) timeout.
// It returns an error if the process exits non-zero, times out, or returns a
// non-empty error field in the response.
func (p *PluginExecutor) Execute(ctx context.Context, toolName string, params map[string]interface{}) (string, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if params == nil {
		params = make(map[string]interface{})
	}
	req := PluginRequest{ToolName: toolName, Params: params}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("plugin marshal input: %w", err)
	}

	cmd := exec.CommandContext(ctx, p.BinaryPath)
	cmd.Stdin = bytes.NewReader(reqBytes)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("plugin exec %q: %w", p.BinaryPath, err)
	}

	var resp PluginResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("plugin decode response from %q: %w", p.BinaryPath, err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("plugin %q returned error: %s", p.BinaryPath, resp.Error)
	}
	return resp.Result, nil
}
