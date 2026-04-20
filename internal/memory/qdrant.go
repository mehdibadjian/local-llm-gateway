package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

var validDomains = []string{"general", "legal", "medical", "code"}

// QdrantPoint represents a single vector point for upsert.
type QdrantPoint struct {
	ID      string                 `json:"id"`
	Vector  []float32              `json:"vector"`
	Payload map[string]interface{} `json:"payload"`
}

// QdrantSearchResult is returned from a vector search.
type QdrantSearchResult struct {
	ID      string                 `json:"id"`
	Score   float32                `json:"score"`
	Payload map[string]interface{} `json:"payload"`
}

// QdrantClient wraps the Qdrant REST API.
type QdrantClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewQdrantClient returns a QdrantClient pointing at baseURL.
// If baseURL is empty, QDRANT_BASE_URL env is used (default: http://localhost:6333).
func NewQdrantClient(baseURL string) *QdrantClient {
	if baseURL == "" {
		baseURL = os.Getenv("QDRANT_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "http://localhost:6333"
	}
	return &QdrantClient{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

// EnsureCollections creates all 4 domain collections if they do not exist.
// Vector size: 384, distance: Cosine.
func (q *QdrantClient) EnsureCollections(ctx context.Context) error {
	for _, domain := range validDomains {
		if err := q.ensureCollection(ctx, collectionName(domain)); err != nil {
			return fmt.Errorf("ensure collection %s: %w", domain, err)
		}
	}
	return nil
}

func (q *QdrantClient) ensureCollection(ctx context.Context, name string) error {
	body := map[string]interface{}{
		"vectors": map[string]interface{}{
			"size":     384,
			"distance": "Cosine",
		},
	}
	return q.do(ctx, http.MethodPut, "/collections/"+name, body, nil)
}

// Upsert adds or replaces points in the domain collection.
// The domain key is always injected into each point's payload.
func (q *QdrantClient) Upsert(ctx context.Context, domain string, points []QdrantPoint) error {
	if err := validateDomain(domain); err != nil {
		return err
	}

	// Inject domain into every point's payload
	enriched := make([]map[string]interface{}, 0, len(points))
	for _, p := range points {
		payload := make(map[string]interface{}, len(p.Payload)+1)
		for k, v := range p.Payload {
			payload[k] = v
		}
		payload["domain"] = domain

		enriched = append(enriched, map[string]interface{}{
			"id":      p.ID,
			"vector":  p.Vector,
			"payload": payload,
		})
	}

	body := map[string]interface{}{"points": enriched}
	return q.do(ctx, http.MethodPut,
		"/collections/"+collectionName(domain)+"/points", body, nil)
}

// Search performs an ANN search with a mandatory domain payload filter.
// Returns error if domain is empty or invalid.
func (q *QdrantClient) Search(ctx context.Context, domain string, vector []float32, topK int) ([]QdrantSearchResult, error) {
	if domain == "" {
		return nil, fmt.Errorf("domain filter required")
	}
	if err := validateDomain(domain); err != nil {
		return nil, err
	}

	body := map[string]interface{}{
		"vector": vector,
		"limit":  topK,
		"filter": map[string]interface{}{
			"must": []map[string]interface{}{
				{
					"key":   "domain",
					"match": map[string]interface{}{"value": domain},
				},
			},
		},
		"with_payload": true,
	}

	var resp struct {
		Result []struct {
			ID      string                 `json:"id"`
			Score   float32                `json:"score"`
			Payload map[string]interface{} `json:"payload"`
		} `json:"result"`
	}

	if err := q.do(ctx, http.MethodPost,
		"/collections/"+collectionName(domain)+"/points/search", body, &resp); err != nil {
		return nil, err
	}

	results := make([]QdrantSearchResult, 0, len(resp.Result))
	for _, r := range resp.Result {
		results = append(results, QdrantSearchResult{
			ID:      r.ID,
			Score:   r.Score,
			Payload: r.Payload,
		})
	}
	return results, nil
}

// do executes an HTTP request against the Qdrant REST API.
func (q *QdrantClient) do(ctx context.Context, method, path string, body, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, q.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := q.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant %s %s: status %d: %s", method, path, resp.StatusCode, string(b))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func collectionName(domain string) string {
	return "caw_" + domain
}

func validateDomain(domain string) error {
	for _, d := range validDomains {
		if domain == d {
			return nil
		}
	}
	return fmt.Errorf("invalid domain %q: must be one of %v", domain, validDomains)
}
