package embed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// EmbedClient is the interface for the embedding service.
type EmbedClient interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	HealthCheck(ctx context.Context) error
}

// HTTPEmbedClient calls the Python EmbedSvc via HTTP, with a circuit breaker
// and an LRU cache in front.
type HTTPEmbedClient struct {
	baseURL        string
	httpClient     *http.Client
	circuitBreaker *CircuitBreaker
	cache          *LRUCache
}

// NewHTTPEmbedClient returns a production-ready embed client.
// opts are passed through to the CircuitBreaker (e.g. WithOpenDuration for tests).
func NewHTTPEmbedClient(baseURL string, opts ...Option) *HTTPEmbedClient {
	return &HTTPEmbedClient{
		baseURL:        baseURL,
		httpClient:     &http.Client{Timeout: 25 * time.Second},
		circuitBreaker: NewCircuitBreaker(opts...),
		cache:          NewLRUCache(1000, 5*time.Minute),
	}
}

// cacheKey returns the SHA-256 hex digest of text.
func cacheKey(text string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
}

// Embed returns the embedding vector for text.
// Order of operations: cache hit → circuit check → HTTP call → cache store.
func (c *HTTPEmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
	key := cacheKey(text)

	// 1. Cache hit – skip service entirely
	if cached, ok := c.cache.Get(key); ok {
		return cached, nil
	}

	// 2. Circuit breaker gate
	if err := c.circuitBreaker.Allow(); err != nil {
		return nil, err
	}

	// 3. Call EmbedSvc
	payload, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(payload))
	if err != nil {
		c.circuitBreaker.RecordFailure()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.circuitBreaker.RecordFailure()
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.circuitBreaker.RecordFailure()
		return nil, fmt.Errorf("embed service returned status %d", resp.StatusCode)
	}

	var result struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.circuitBreaker.RecordFailure()
		return nil, err
	}

	// 4. Store in cache and reset circuit breaker
	c.cache.Set(key, result.Embedding)
	c.circuitBreaker.RecordSuccess()

	return result.Embedding, nil
}

// HealthCheck pings the /health endpoint and verifies status == "SERVING".
func (c *HTTPEmbedClient) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed with status %d", resp.StatusCode)
	}

	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if result.Status != "SERVING" {
		return fmt.Errorf("service not serving: %s", result.Status)
	}
	return nil
}
