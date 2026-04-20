package tools_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMockPlugin creates an executable shell script in dir with the given content.
func writeMockPlugin(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(script), 0755))
	return path
}

// TestLoadPlugins_DiscoverAndRegister verifies that LoadPlugins finds an executable
// in the plugin dir and registers it as a tool with executor_type="plugin".
// AC-1: plugin binary discovered and registered on startup.
func TestLoadPlugins_DiscoverAndRegister(t *testing.T) {
	dir := t.TempDir()
	writeMockPlugin(t, dir, "mock-tool", "#!/bin/sh\necho '{\"result\":\"mock_output\"}'")

	store := newMockPGStore()
	reg := tools.NewRegistry(store)

	require.NoError(t, tools.LoadPlugins(reg, dir))

	tool, err := reg.Get(context.Background(), "mock-tool")
	require.NoError(t, err)
	assert.Equal(t, "mock-tool", tool.Name)
	assert.Equal(t, "plugin", tool.ExecutorType)
	assert.Equal(t, "plugin", tool.Source)
	assert.True(t, tool.Enabled)
}

// TestPluginExecutor_Execute_BasicIO verifies that PluginExecutor writes a JSON
// PluginRequest to stdin and correctly reads the PluginResponse from stdout.
// AC-2: subprocess invoked with JSON stdin/stdout contract.
func TestPluginExecutor_Execute_BasicIO(t *testing.T) {
	dir := t.TempDir()
	// Discard stdin, write a fixed JSON response.
	binPath := writeMockPlugin(t, dir, "echo-plugin",
		"#!/bin/sh\ncat > /dev/null\nprintf '{\"result\":\"mock_output\"}'")

	pe := &tools.PluginExecutor{BinaryPath: binPath, Timeout: 5 * time.Second}
	result, err := pe.Execute(context.Background(), "echo-plugin", map[string]interface{}{"key": "val"})
	require.NoError(t, err)
	assert.Equal(t, "mock_output", result)
}

// TestPluginExecutor_Execute_Timeout verifies that a plugin that hangs longer than
// the configured timeout causes Execute to return an error without crashing.
// AC-3: timeout enforced, main process unaffected.
func TestPluginExecutor_Execute_Timeout(t *testing.T) {
	dir := t.TempDir()
	binPath := writeMockPlugin(t, dir, "slow-plugin", "#!/bin/sh\nsleep 30")

	pe := &tools.PluginExecutor{BinaryPath: binPath, Timeout: 200 * time.Millisecond}
	_, err := pe.Execute(context.Background(), "slow-plugin", nil)
	require.Error(t, err)
}

// TestLoadPlugins_EmptyDir_Skipped verifies that passing an empty string for the
// plugin directory is a silent no-op — no error, no crash.
// AC-5: CAW_PLUGIN_DIR unset → silently skipped.
func TestLoadPlugins_EmptyDir_Skipped(t *testing.T) {
	store := newMockPGStore()
	reg := tools.NewRegistry(store)

	err := tools.LoadPlugins(reg, "")
	require.NoError(t, err)

	list, err := reg.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, list)
}

// TestLoadPlugins_SourceIsPlugin verifies that every plugin registered via
// LoadPlugins carries Source="plugin" in the registry list.
// AC-6: source field set to "plugin".
func TestLoadPlugins_SourceIsPlugin(t *testing.T) {
	dir := t.TempDir()
	writeMockPlugin(t, dir, "community-tool", "#!/bin/sh\nprintf '{\"result\":\"ok\"}'")

	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	require.NoError(t, tools.LoadPlugins(reg, dir))

	list, err := reg.List(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "plugin", list[0].Source)
}

// TestPluginExecutor_Execute_NonZeroExit verifies that a plugin exiting non-zero
// causes Execute to return an error without crashing the main process.
// AC-3: non-zero exit → error returned, no crash.
func TestPluginExecutor_Execute_NonZeroExit(t *testing.T) {
	dir := t.TempDir()
	binPath := writeMockPlugin(t, dir, "fail-plugin", "#!/bin/sh\nexit 1")

	pe := &tools.PluginExecutor{BinaryPath: binPath, Timeout: 5 * time.Second}
	_, err := pe.Execute(context.Background(), "fail-plugin", nil)
	require.Error(t, err)
}

// TestHandler_ListTools_ShowsPluginSource verifies that plugins loaded via
// LoadPlugins appear in the GET /v1/tools response with source="plugin".
// AC-6: plugin visible via HTTP with correct source field.
func TestHandler_ListTools_ShowsPluginSource(t *testing.T) {
	dir := t.TempDir()
	writeMockPlugin(t, dir, "listed-plugin", "#!/bin/sh\nprintf '{\"result\":\"ok\"}'")

	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	require.NoError(t, tools.LoadPlugins(reg, dir))

	app := newTestToolApp(t, reg)
	req := httptest.NewRequest("GET", "/v1/tools", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var body struct {
		Tools []tools.Tool `json:"tools"`
		Count int          `json:"count"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, 1, body.Count)
	assert.Equal(t, "plugin", body.Tools[0].Source)
	assert.Equal(t, "listed-plugin", body.Tools[0].Name)
}
