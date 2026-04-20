package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadPlugins scans dir for executable files and registers each as a plugin tool.
// If dir is empty the function returns nil immediately (silent skip per AC-5).
// Each discovered binary is registered with ExecutorType "plugin" and Source "plugin";
// the binary path is stored in EndpointURL so the dispatcher can locate it.
func LoadPlugins(registry *Registry, dir string) error {
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("plugin dir %q: %w", dir, err)
	}

	ctx := context.Background()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Skip non-executable files.
		if info.Mode()&0111 == 0 {
			continue
		}

		name := entry.Name()
		binaryPath := filepath.Join(dir, name)

		_, err = registry.Register(ctx, Tool{
			Name:         name,
			Description:  fmt.Sprintf("Community plugin: %s", name),
			InputSchema:  json.RawMessage(`{}`),
			ExecutorType: "plugin",
			EndpointURL:  binaryPath,
			Source:       "plugin",
			Enabled:      true,
		})
		if err != nil {
			return fmt.Errorf("register plugin %q: %w", name, err)
		}
	}
	return nil
}
