package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueDocument_Returns202(t *testing.T) {
	app, _ := newTestServer(t, &adapter.MockInferenceBackend{})

	body := `{"domain":"general","content":"Hello world document"}`
	req := httptest.NewRequest("POST", "/v1/documents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 202, resp.StatusCode)

	raw, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.NotEmpty(t, result["document_id"])
	assert.Equal(t, "pending", result["status"])
}

func TestEnqueueDocument_MissingDomain_Returns422(t *testing.T) {
	app, _ := newTestServer(t, &adapter.MockInferenceBackend{})

	body := `{"content":"Hello world document"}`
	req := httptest.NewRequest("POST", "/v1/documents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestDocumentStatus_ReturnsCurrentState(t *testing.T) {
	app, _, _, _ := newTestServerWithRedis(t, &adapter.MockInferenceBackend{})

	// Enqueue a document first to get an ID.
	body := `{"domain":"general","content":"Hello world"}`
	enqReq := httptest.NewRequest("POST", "/v1/documents", strings.NewReader(body))
	enqReq.Header.Set("Content-Type", "application/json")
	enqReq.Header.Set("Authorization", authHeader())

	enqResp, err := app.Test(enqReq, 5000)
	require.NoError(t, err)
	require.Equal(t, 202, enqResp.StatusCode)

	raw, _ := io.ReadAll(enqResp.Body)
	var enqResult map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &enqResult))
	docID := enqResult["document_id"].(string)

	// Fetch the status.
	statusReq := httptest.NewRequest("GET", fmt.Sprintf("/v1/documents/%s/status", docID), nil)
	statusReq.Header.Set("Authorization", authHeader())

	statusResp, err := app.Test(statusReq, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, statusResp.StatusCode)

	statusRaw, _ := io.ReadAll(statusResp.Body)
	var statusResult map[string]interface{}
	require.NoError(t, json.Unmarshal(statusRaw, &statusResult))
	assert.Equal(t, docID, statusResult["document_id"])
	assert.Equal(t, "pending", statusResult["status"])
}

func TestDeleteSession_Returns204(t *testing.T) {
	app, _ := newTestServer(t, &adapter.MockInferenceBackend{})

	req := httptest.NewRequest("DELETE", "/v1/sessions/sess-abc", nil)
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 204, resp.StatusCode)
}

func TestDeleteSession_ClearsRedisKeys(t *testing.T) {
	app, _, rdb, _ := newTestServerWithRedis(t, &adapter.MockInferenceBackend{})

	// Seed session keys into Redis.
	ctx := context.Background()
	sessionID := "sess-xyz"
	store := memory.NewSessionStore(rdb)
	err := store.SaveMessage(ctx, sessionID, memory.Message{
		Role:      "user",
		Content:   "hello",
		Timestamp: time.Now().Unix(),
	})
	require.NoError(t, err)

	// Verify keys exist.
	listKey := fmt.Sprintf("caw:session:%s:messages", sessionID)
	count, _ := rdb.LLen(ctx, listKey).Result()
	assert.Equal(t, int64(1), count)

	// Delete session via API.
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/v1/sessions/%s", sessionID), nil)
	req.Header.Set("Authorization", authHeader())

	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 204, resp.StatusCode)

	// Keys must be gone.
	exists, _ := rdb.Exists(ctx, listKey).Result()
	assert.Equal(t, int64(0), exists)
}
