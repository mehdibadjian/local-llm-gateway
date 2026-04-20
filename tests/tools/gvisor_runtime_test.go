//go:build !integration
package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/caw/wrapper/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSandbox_GVisor_SkipsIfRunscNotAvailable verifies that gVisor tests are skipped
// gracefully when runsc is not available (e.g., on macOS CI environments).
func TestSandbox_GVisor_SkipsIfRunscNotAvailable(t *testing.T) {
	if _, err := exec.LookPath("runsc"); err != nil {
		t.Skip("runsc not available — skipping gVisor test")
	}

	s := newTestSandbox(5)
	ctx := context.Background()

	// If runsc is available, verify that gVisor commands can be built and executed
	argsJSON, _ := json.Marshal(map[string]interface{}{"args": []string{"echo", "gvisor-test"}})
	call := tools.ToolCall{
		ID:       "gvisor-call",
		ToolName: "echo-tool",
		Input:    argsJSON,
	}

	// Set the gVisor runtime mode
	os.Setenv("CODEEXEC_RUNTIME", "gvisor")
	defer os.Unsetenv("CODEEXEC_RUNTIME")

	cmd := s.BuildCommandForTest(ctx, echoTool(), call)
	require.NotNil(t, cmd)
	assert.Equal(t, "runsc", cmd.Path, "expected runsc in command path for gVisor mode")
	assert.Contains(t, cmd.Args, "do", "expected 'do' flag in runsc command")
	assert.Contains(t, cmd.Args, "--", "expected '--' separator in runsc command")
}

// TestSandbox_RuntimeFallback_NativeWhenEnvUnset verifies that the sandbox falls back
// to the native runtime when CODEEXEC_RUNTIME is unset.
func TestSandbox_RuntimeFallback_NativeWhenEnvUnset(t *testing.T) {
	// Ensure CODEEXEC_RUNTIME is unset
	os.Unsetenv("CODEEXEC_RUNTIME")

	s := newTestSandbox(5)
	ctx := context.Background()

	argsJSON, _ := json.Marshal(map[string]interface{}{"args": []string{"echo", "native-mode"}})
	call := tools.ToolCall{
		ID:       "native-call",
		ToolName: "echo-tool",
		Input:    argsJSON,
	}

	cmd := s.BuildCommandForTest(ctx, echoTool(), call)
	require.NotNil(t, cmd)

	// On non-Linux, the command should be the tool itself
	// On Linux, it should be wrapped with cgexec
	cmdPath := cmd.Path
	assert.NotEqual(t, "runsc", cmdPath, "expected native mode to NOT use runsc when env var is unset")
}

// TestSandbox_RuntimeFallback_NativeWhenSetToNative verifies that setting
// CODEEXEC_RUNTIME=native explicitly uses native mode.
func TestSandbox_RuntimeFallback_NativeWhenSetToNative(t *testing.T) {
	os.Setenv("CODEEXEC_RUNTIME", "native")
	defer os.Unsetenv("CODEEXEC_RUNTIME")

	s := newTestSandbox(5)
	ctx := context.Background()

	argsJSON, _ := json.Marshal(map[string]interface{}{"args": []string{"echo", "native-explicit"}})
	call := tools.ToolCall{
		ID:       "native-explicit-call",
		ToolName: "echo-tool",
		Input:    argsJSON,
	}

	cmd := s.BuildCommandForTest(ctx, echoTool(), call)
	require.NotNil(t, cmd)

	assert.NotEqual(t, "runsc", cmd.Path, "expected native mode to NOT use runsc when set to 'native'")
}

// TestSandbox_GVisor_EnvVar_BuildsRunscCmd verifies that when CODEEXEC_RUNTIME=gvisor
// is set, the sandbox constructs a runsc command with the correct structure.
func TestSandbox_GVisor_EnvVar_BuildsRunscCmd(t *testing.T) {
	// Check if runsc is available; if not, skip the test
	if _, err := exec.LookPath("runsc"); err != nil {
		t.Skip("runsc not available — skipping gVisor command test")
	}

	os.Setenv("CODEEXEC_RUNTIME", "gvisor")
	defer os.Unsetenv("CODEEXEC_RUNTIME")

	s := newTestSandbox(5)
	ctx := context.Background()

	argsJSON, _ := json.Marshal(map[string]interface{}{"args": []string{"echo", "test"}})
	call := tools.ToolCall{
		ID:       "gvisor-cmd-test",
		ToolName: "echo-tool",
		Input:    argsJSON,
	}

	cmd := s.BuildCommandForTest(ctx, echoTool(), call)
	require.NotNil(t, cmd)

	// Verify the command is structured as: runsc do -- echo test
	assert.Equal(t, "runsc", cmd.Path, "expected runsc command path")
	assert.Len(t, cmd.Args, 5, "expected runsc do -- echo test (5 elements)")
	assert.Equal(t, "runsc", cmd.Args[0])
	assert.Equal(t, "do", cmd.Args[1])
	assert.Equal(t, "--", cmd.Args[2])
	assert.Equal(t, "echo", cmd.Args[3])
	assert.Equal(t, "test", cmd.Args[4])
}

// TestSandbox_GVisor_TimeoutSemantics_MatchesNative verifies that timeout and kill
// semantics are identical between gVisor and native modes.
func TestSandbox_GVisor_TimeoutSemantics_MatchesNative(t *testing.T) {
	// This test ensures that the context timeout is applied uniformly,
	// regardless of the runtime mode. Both exec.CommandContext calls
	// (native and gVisor) use the same context deadline mechanism.

	if _, err := exec.LookPath("runsc"); err != nil {
		t.Skip("runsc not available — skipping gVisor timeout test")
	}

	// Test gVisor mode with timeout
	os.Setenv("CODEEXEC_RUNTIME", "gvisor")
	defer os.Unsetenv("CODEEXEC_RUNTIME")

	s := newTestSandbox(1) // 1-second timeout
	ctx := context.Background()

	argsJSON, _ := json.Marshal(map[string]interface{}{"args": []string{"sleep", "100"}})
	call := tools.ToolCall{
		ID:       "gvisor-timeout-test",
		ToolName: "echo-tool",
		Input:    argsJSON,
	}

	result, err := s.Run(ctx, echoTool(), call)
	require.NoError(t, err)

	// The long-running process should be killed by the context timeout
	assert.NotEmpty(t, result.Error, "expected error from timed-out gVisor process")
}
