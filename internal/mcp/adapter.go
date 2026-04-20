package mcp

import (
	"context"

	"github.com/caw/wrapper/internal/tools"
)

// DispatcherSource adapts a tools.Registry + tools.Dispatcher to the ToolSource interface.
type DispatcherSource struct {
	registry   *tools.Registry
	dispatcher *tools.Dispatcher
}

// NewDispatcherSource creates a DispatcherSource.
func NewDispatcherSource(reg *tools.Registry, d *tools.Dispatcher) *DispatcherSource {
	return &DispatcherSource{registry: reg, dispatcher: d}
}

// ListTools implements ToolSource.
func (ds *DispatcherSource) ListTools(ctx context.Context) ([]tools.Tool, error) {
	return ds.registry.List(ctx)
}

// ExecuteTool implements ToolSource.
func (ds *DispatcherSource) ExecuteTool(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return ds.dispatcher.Execute(ctx, call)
}
