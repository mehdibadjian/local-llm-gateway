package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caw/wrapper/internal/tools"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPGStore is an in-memory mock implementing tools.PGToolStore.
type mockPGStore struct {
	data map[string]*tools.Tool
}

func newMockPGStore() *mockPGStore {
	return &mockPGStore{data: make(map[string]*tools.Tool)}
}

func (m *mockPGStore) ListTools(_ context.Context) ([]tools.Tool, error) {
	out := make([]tools.Tool, 0, len(m.data))
	for _, t := range m.data {
		out = append(out, *t)
	}
	return out, nil
}

func (m *mockPGStore) GetTool(_ context.Context, name string) (*tools.Tool, error) {
	t, ok := m.data[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return t, nil
}

func (m *mockPGStore) CreateTool(_ context.Context, t tools.Tool) (*tools.Tool, error) {
	if t.ID == "" {
		t.ID = "mock-id-" + t.Name
	}
	m.data[t.Name] = &t
	return &t, nil
}

func newTestToolApp(t *testing.T, reg *tools.Registry) *fiber.App {
	t.Helper()
	app := fiber.New()
	h := tools.NewToolHandler(reg)
	app.Get("/v1/tools", h.ListTools)
	app.Post("/v1/tools", h.RegisterTool)
	return app
}

func TestRegistry_ListReturnsOnlyEnabled(t *testing.T) {
	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	ctx := context.Background()
	schema := json.RawMessage(`{}`)

	_, err := reg.Register(ctx, tools.Tool{
		Name: "enabled-tool", Description: "enabled", InputSchema: schema,
		ExecutorType: "builtin", Enabled: true,
	})
	require.NoError(t, err)

	_, err = reg.Register(ctx, tools.Tool{
		Name: "disabled-tool", Description: "disabled", InputSchema: schema,
		ExecutorType: "builtin", Enabled: false,
	})
	require.NoError(t, err)

	list, err := reg.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "enabled-tool", list[0].Name)
}

func TestRegistry_RegisterPersistsAndAvailable(t *testing.T) {
	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	ctx := context.Background()

	schema := json.RawMessage(`{"type":"object"}`)
	created, err := reg.Register(ctx, tools.Tool{
		Name: "my-tool", Description: "test", InputSchema: schema,
		ExecutorType: "builtin", Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "my-tool", created.Name)

	got, err := reg.Get(ctx, "my-tool")
	require.NoError(t, err)
	assert.Equal(t, "my-tool", got.Name)
}

func TestRegistry_GetByName(t *testing.T) {
	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	ctx := context.Background()

	schema := json.RawMessage(`{}`)
	_, err := reg.Register(ctx, tools.Tool{
		Name: "lookup-tool", InputSchema: schema,
		ExecutorType: "http", Enabled: true,
	})
	require.NoError(t, err)

	tool, err := reg.Get(ctx, "lookup-tool")
	require.NoError(t, err)
	assert.Equal(t, "lookup-tool", tool.Name)

	_, err = reg.Get(ctx, "nonexistent")
	assert.Error(t, err)
}

func TestRegistry_InvalidExecutorType_Rejected(t *testing.T) {
	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	app := newTestToolApp(t, reg)

	body := `{"name":"bad-tool","executor_type":"ftp","input_schema":{}}`
	req := httptest.NewRequest("POST", "/v1/tools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandler_RegisterTool_MissingName_Returns422(t *testing.T) {
	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	app := newTestToolApp(t, reg)

	body := `{"executor_type":"builtin","input_schema":{}}`
	req := httptest.NewRequest("POST", "/v1/tools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandler_ListTools_ReturnsJSON(t *testing.T) {
	store := newMockPGStore()
	reg := tools.NewRegistry(store)
	ctx := context.Background()
	schema := json.RawMessage(`{}`)

	_, err := reg.Register(ctx, tools.Tool{
		Name: "visible", InputSchema: schema,
		ExecutorType: "builtin", Enabled: true,
	})
	require.NoError(t, err)

	app := newTestToolApp(t, reg)
	req := httptest.NewRequest("GET", "/v1/tools", nil)

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, float64(1), body["count"])
}
