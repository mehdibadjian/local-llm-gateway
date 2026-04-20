package tools

import (
	"context"
	"fmt"
	"sync"
)

// PGToolStore abstracts the PostgreSQL backend for tool persistence.
// The production implementation is memory.PGStore; tests use an in-memory mock.
type PGToolStore interface {
	ListTools(ctx context.Context) ([]Tool, error)
	GetTool(ctx context.Context, name string) (*Tool, error)
	CreateTool(ctx context.Context, t Tool) (*Tool, error)
}

// Registry is an in-memory + PG-backed store of registered tools.
type Registry struct {
	pg    PGToolStore
	cache map[string]*Tool
	mu    sync.RWMutex
}

// NewRegistry constructs a Registry backed by the given PGToolStore.
func NewRegistry(pg PGToolStore) *Registry {
	return &Registry{
		pg:    pg,
		cache: make(map[string]*Tool),
	}
}

// List returns only tools with enabled=true, sourced from the backing store.
func (r *Registry) List(ctx context.Context) ([]Tool, error) {
	all, err := r.pg.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	enabled := make([]Tool, 0, len(all))
	for _, t := range all {
		if t.Enabled {
			enabled = append(enabled, t)
		}
	}
	return enabled, nil
}

// Register persists a new tool and populates the in-memory cache.
func (r *Registry) Register(ctx context.Context, t Tool) (*Tool, error) {
	created, err := r.pg.CreateTool(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("register tool: %w", err)
	}
	r.mu.Lock()
	r.cache[created.Name] = created
	r.mu.Unlock()
	return created, nil
}

// Get retrieves a tool by name, checking the in-memory cache first.
func (r *Registry) Get(ctx context.Context, name string) (*Tool, error) {
	r.mu.RLock()
	t, ok := r.cache[name]
	r.mu.RUnlock()
	if ok {
		return t, nil
	}

	tool, err := r.pg.GetTool(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get tool %q: %w", name, err)
	}
	r.mu.Lock()
	r.cache[name] = tool
	r.mu.Unlock()
	return tool, nil
}
