package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/caw/wrapper/internal/ingest"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// SearchResult is a single web search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebSearchExecutor performs web searches and auto-learns by enqueuing results into RAG.
type WebSearchExecutor struct {
	rdb    *redis.Client // nil disables auto-ingest
	client *http.Client
}

// NewWebSearchExecutor creates a WebSearchExecutor.
// Pass a non-nil rdb to enable auto-learning (results are enqueued to the RAG pipeline).
func NewWebSearchExecutor(rdb *redis.Client) *WebSearchExecutor {
	return &WebSearchExecutor{
		rdb:    rdb,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Execute runs the web search and returns up to maxResults results.
// The SEARCH_PROVIDER env var selects the backend: "brave", "searxng", or "ddg" (default).
func (e *WebSearchExecutor) Execute(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	var results []SearchResult
	var err error

	switch os.Getenv("SEARCH_PROVIDER") {
	case "brave":
		results, err = e.searchBrave(ctx, query, maxResults)
	case "searxng":
		results, err = e.searchSearXNG(ctx, query, maxResults)
	default: // ddg
		results, err = e.searchDDG(ctx, query, maxResults)
	}
	if err != nil {
		return nil, err
	}

	// Self-learning: enqueue results into the RAG ingest pipeline asynchronously.
	if e.rdb != nil && len(results) > 0 {
		go e.learn(context.Background(), query, results)
	}

	return results, nil
}

// learn enqueues search result snippets as a single document into the ingest pipeline.
func (e *WebSearchExecutor) learn(ctx context.Context, query string, results []SearchResult) {
	var sb strings.Builder
	sb.WriteString("Web search query: " + query + "\n\n")
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("## %s\nSource: %s\n%s\n\n", r.Title, r.URL, r.Snippet))
	}

	_ = ingest.Enqueue(ctx, e.rdb, ingest.IngestJob{
		DocumentID: uuid.New().String(),
		Domain:     "web-search",
		Content:    sb.String(),
		Title:      "Web search: " + query,
		EnqueuedAt: time.Now(),
	})
}

// ── DuckDuckGo Instant Answer API ─────────────────────────────────────────────
// Uses the free JSON API (no bot detection, no API key needed).
// Best for factual / named-entity queries; returns abstract + related topics.

type ddgIAResponse struct {
	AbstractText   string `json:"AbstractText"`
	AbstractURL    string `json:"AbstractURL"`
	AbstractSource string `json:"AbstractSource"`
	Results        []struct {
		Text      string `json:"Text"`
		FirstURL  string `json:"FirstURL"`
	} `json:"Results"`
	RelatedTopics []ddgRelatedTopic `json:"RelatedTopics"`
}

type ddgRelatedTopic struct {
	Text     string `json:"Text"`
	FirstURL string `json:"FirstURL"`
	Topics   []ddgRelatedTopic `json:"Topics"` // nested groups
}

func (e *WebSearchExecutor) searchDDG(ctx context.Context, query string, max int) ([]SearchResult, error) {
	u := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) +
		"&format=json&no_html=1&skip_disambig=1&t=caw-bot"

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "CAW-bot/1.0")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ddg request: %w", err)
	}
	defer resp.Body.Close()

	var r ddgIAResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("ddg decode: %w", err)
	}

	var results []SearchResult

	// Include the main abstract if present.
	if r.AbstractText != "" && len(results) < max {
		results = append(results, SearchResult{
			Title:   r.AbstractSource,
			URL:     r.AbstractURL,
			Snippet: r.AbstractText,
		})
	}

	// Flatten related topics (skip category headers that have no URL).
	var flatTopics []ddgRelatedTopic
	for _, t := range r.RelatedTopics {
		if t.FirstURL != "" {
			flatTopics = append(flatTopics, t)
		}
		for _, sub := range t.Topics {
			if sub.FirstURL != "" {
				flatTopics = append(flatTopics, sub)
			}
		}
	}

	for _, t := range flatTopics {
		if len(results) >= max {
			break
		}
		title := t.Text
		if idx := strings.Index(title, " — "); idx > 0 {
			title = title[:idx]
		}
		results = append(results, SearchResult{Title: title, URL: t.FirstURL, Snippet: t.Text})
	}

	return results, nil
}

// ── SearXNG ───────────────────────────────────────────────────────────────────

type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

func (e *WebSearchExecutor) searchSearXNG(ctx context.Context, query string, max int) ([]SearchResult, error) {
	base := os.Getenv("SEARXNG_URL")
	if base == "" {
		base = "http://localhost:8888"
	}
	u := base + "/search?q=" + url.QueryEscape(query) + "&format=json"

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng request: %w", err)
	}
	defer resp.Body.Close()

	var sr searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("searxng decode: %w", err)
	}

	var results []SearchResult
	for i, r := range sr.Results {
		if i >= max {
			break
		}
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return results, nil
}

// ── Brave Search ──────────────────────────────────────────────────────────────

type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

func (e *WebSearchExecutor) searchBrave(ctx context.Context, query string, max int) ([]SearchResult, error) {
	apiKey := os.Getenv("BRAVE_SEARCH_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("BRAVE_SEARCH_API_KEY not set")
	}

	u := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), max)

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave request: %w", err)
	}
	defer resp.Body.Close()

	var br braveResponse
	if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
		return nil, fmt.Errorf("brave decode: %w", err)
	}

	var results []SearchResult
	for i, r := range br.Web.Results {
		if i >= max {
			break
		}
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description})
	}
	return results, nil
}
