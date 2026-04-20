package memory_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/memory"
)

func TestQdrantEnsureCollections(t *testing.T) {
	createdCollections := []string{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/collections/") {
			name := strings.TrimPrefix(r.URL.Path, "/collections/")
			createdCollections = append(createdCollections, name)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result":true,"status":"ok","time":0.001}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := memory.NewQdrantClient(ts.URL)
	err := client.EnsureCollections(t.Context())
	require.NoError(t, err)

	expected := []string{"caw_general", "caw_legal", "caw_medical", "caw_code"}
	assert.ElementsMatch(t, expected, createdCollections,
		"must PUT all 4 domain collections")
}

func TestQdrantSearch_DomainFilterEnforced(t *testing.T) {
	var capturedBody map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/points/search") {
			err := json.NewDecoder(r.Body).Decode(&capturedBody)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result":[],"status":"ok","time":0.001}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := memory.NewQdrantClient(ts.URL)
	vector := make([]float32, 384)
	_, err := client.Search(t.Context(), "legal", vector, 5)
	require.NoError(t, err)

	// Verify the filter is present in the request body
	filter, ok := capturedBody["filter"]
	require.True(t, ok, "filter must be present in search request")

	filterMap, ok := filter.(map[string]interface{})
	require.True(t, ok)

	must, ok := filterMap["must"]
	require.True(t, ok, "filter must contain 'must' clause")

	mustSlice, ok := must.([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, mustSlice, "must clause must not be empty")

	// Verify the domain filter condition
	found := false
	for _, cond := range mustSlice {
		condMap, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}
		if key, ok := condMap["key"].(string); ok && key == "domain" {
			if match, ok := condMap["match"].(map[string]interface{}); ok {
				if val, ok := match["value"].(string); ok && val == "legal" {
					found = true
				}
			}
		}
	}
	assert.True(t, found, "domain filter must match 'legal'")
}

func TestQdrantSearch_MissingDomainReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":[],"status":"ok","time":0.001}`))
	}))
	defer ts.Close()

	client := memory.NewQdrantClient(ts.URL)
	vector := make([]float32, 384)

	_, err := client.Search(t.Context(), "", vector, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domain filter required")
}

func TestQdrantUpsert_IncludesDomainPayload(t *testing.T) {
	var capturedBody map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/points") {
			json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"result":{"operation_id":0,"status":"acknowledged"},"status":"ok","time":0.001}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := memory.NewQdrantClient(ts.URL)
	points := []memory.QdrantPoint{
		{
			ID:      "550e8400-e29b-41d4-a716-446655440000",
			Vector:  make([]float32, 384),
			Payload: map[string]interface{}{"text": "test chunk"},
		},
	}

	err := client.Upsert(t.Context(), "code", points)
	require.NoError(t, err)

	// Verify domain is included in the payload
	pointsArr, ok := capturedBody["points"].([]interface{})
	require.True(t, ok, "points must be present")
	require.NotEmpty(t, pointsArr)

	firstPoint, ok := pointsArr[0].(map[string]interface{})
	require.True(t, ok)
	payload, ok := firstPoint["payload"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "code", payload["domain"], "domain must be injected into payload")
}
