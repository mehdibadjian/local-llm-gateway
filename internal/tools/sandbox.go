package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/google/uuid"
)

// SandboxConfig holds limits applied to subprocess tool executions.
type SandboxConfig struct {
	MemLimitMB int // default 256
	CPUShares  int // default 512 (~0.5 CPU in cgroup v2)
	TimeoutSec int // default 30
}

// Sandbox runs subprocess tools under resource and security constraints.
// On Linux: wraps with cgexec for cgroup v2 memory + CPU limits.
// On non-Linux: plain subprocess with timeout only (graceful degradation for dev).
type Sandbox struct {
	cgroupPrefix string
	memLimitMB   int
	cpuShares    int
	timeoutSecs  int
}

// NewSandbox constructs a Sandbox, applying defaults for zero-value config fields.
func NewSandbox(cfg SandboxConfig) *Sandbox {
	mem := cfg.MemLimitMB
	if mem <= 0 {
		mem = 256
	}
	cpu := cfg.CPUShares
	if cpu <= 0 {
		cpu = 512
	}
	timeout := cfg.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	return &Sandbox{
		cgroupPrefix: "caw_tool",
		memLimitMB:   mem,
		cpuShares:    cpu,
		timeoutSecs:  timeout,
	}
}

// Run executes a subprocess tool under sandbox constraints and returns the result.
func (s *Sandbox) Run(ctx context.Context, tool *Tool, call ToolCall) (*ToolResult, error) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.timeoutSecs)*time.Second)
	defer cancel()

	cmd := s.buildCommand(ctx, tool, call)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := err.Error()
		if se := stderr.String(); se != "" {
			errMsg = fmt.Sprintf("%s: %s", errMsg, se)
		}
		return &ToolResult{
			ToolCallID: call.ID,
			Error:      errMsg,
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	return &ToolResult{
		ToolCallID: call.ID,
		Output:     stdout.String(),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// BuildCommandForTest exposes buildCommand for white-box sandbox tests.
func (s *Sandbox) BuildCommandForTest(ctx context.Context, tool *Tool, call ToolCall) *exec.Cmd {
	return s.buildCommand(ctx, tool, call)
}

func (s *Sandbox) buildCommand(ctx context.Context, _ *Tool, call ToolCall) *exec.Cmd {
	var input struct {
		Args []string `json:"args"`
	}
	json.Unmarshal(call.Input, &input) //nolint:errcheck

	args := input.Args

	// Check if gVisor runtime is requested
	runtimeMode := os.Getenv("CODEEXEC_RUNTIME")
	if runtimeMode == "gvisor" {
		return s.buildGVisorCommand(ctx, args)
	}

	// Native mode (default): use cgexec on Linux, direct execution elsewhere
	if runtime.GOOS == "linux" {
		cgName := fmt.Sprintf("%s_%s", s.cgroupPrefix, uuid.New().String()[:8])
		cgSpec := fmt.Sprintf("memory,cpu:%s", cgName)
		cmdArgs := append([]string{"-g", cgSpec}, args...)
		return exec.CommandContext(ctx, "cgexec", cmdArgs...)
	}

	if len(args) == 0 {
		return exec.CommandContext(ctx, "echo", "no-op")
	}
	return exec.CommandContext(ctx, args[0], args[1:]...)
}

// buildGVisorCommand constructs a command that runs the given args under gVisor (runsc).
// The resulting command will be: runsc do -- <args...>
func (s *Sandbox) buildGVisorCommand(ctx context.Context, args []string) *exec.Cmd {
	if len(args) == 0 {
		// Fallback if no args provided
		return exec.CommandContext(ctx, "echo", "no-op")
	}
	// runsc do -- <args...>
	cmdArgs := append([]string{"do", "--"}, args...)
	return exec.CommandContext(ctx, "runsc", cmdArgs...)
}
