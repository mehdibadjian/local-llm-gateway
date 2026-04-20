package main

import (
	"context"
	"encoding/json"

	"github.com/caw/wrapper/internal/memory"
	"github.com/caw/wrapper/internal/tools"
)

// pgToolStoreAdapter adapts memory.PGStore to the tools.PGToolStore interface.
type pgToolStoreAdapter struct {
	pg *memory.PGStore
}

func (a *pgToolStoreAdapter) ListTools(ctx context.Context) ([]tools.Tool, error) {
	records, err := a.pg.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tools.Tool, len(records))
	for i, r := range records {
		out[i] = recordToTool(r)
	}
	return out, nil
}

func (a *pgToolStoreAdapter) GetTool(ctx context.Context, name string) (*tools.Tool, error) {
	r, err := a.pg.GetTool(ctx, name)
	if err != nil {
		return nil, err
	}
	t := recordToTool(*r)
	return &t, nil
}

func (a *pgToolStoreAdapter) CreateTool(ctx context.Context, t tools.Tool) (*tools.Tool, error) {
	schema := t.InputSchema
	if schema == nil {
		schema = json.RawMessage(`{}`)
	}
	r, err := a.pg.CreateTool(ctx, memory.ToolRecord{
		Name:         t.Name,
		Description:  t.Description,
		InputSchema:  schema,
		ExecutorType: t.ExecutorType,
		EndpointURL:  t.EndpointURL,
		Enabled:      t.Enabled,
	})
	if err != nil {
		return nil, err
	}
	created := recordToTool(*r)
	return &created, nil
}

func recordToTool(r memory.ToolRecord) tools.Tool {
	return tools.Tool{
		ID:           r.ID,
		Name:         r.Name,
		Description:  r.Description,
		InputSchema:  r.InputSchema,
		ExecutorType: r.ExecutorType,
		EndpointURL:  r.EndpointURL,
		Enabled:      r.Enabled,
		CreatedAt:    r.CreatedAt,
	}
}
