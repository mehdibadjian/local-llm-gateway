package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/caw/wrapper/internal/ingest"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	allTagsRe     = regexp.MustCompile(`<[^>]+>`)
	multiSpaceRe  = regexp.MustCompile(`[ \t]{2,}`)
	multiNewline  = regexp.MustCompile(`\n{3,}`)
)

// WebFetchExecutor fetches a URL, strips HTML, and auto-learns by enqueuing into RAG.
type WebFetchExecutor struct {
	rdb    *redis.Client
	client *http.Client
}

// NewWebFetchExecutor creates a WebFetchExecutor.
// Pass a non-nil rdb to enable auto-learning.
func NewWebFetchExecutor(rdb *redis.Client) *WebFetchExecutor {
	return &WebFetchExecutor{
		rdb:    rdb,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// Execute fetches rawURL and returns clean, readable text (max maxBytes bytes of body).
// Content is automatically enqueued into the RAG ingest pipeline for future retrieval.
func (e *WebFetchExecutor) Execute(ctx context.Context, rawURL string, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 // 32 KB default
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch: build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CAW-bot/1.0)")
	req.Header.Set("Accept", "text/html,text/plain")

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return "", fmt.Errorf("web_fetch: read body: %w", err)
	}

	text := extractText(string(raw))

	// Self-learning: enqueue the fetched page into the RAG ingest pipeline.
	if e.rdb != nil && text != "" {
		go e.learn(context.Background(), rawURL, text)
	}

	return text, nil
}

// learn enqueues fetched page content into the ingest pipeline.
func (e *WebFetchExecutor) learn(ctx context.Context, rawURL, text string) {
	_ = ingest.Enqueue(ctx, e.rdb, ingest.IngestJob{
		DocumentID: uuid.New().String(),
		Domain:     "web-fetch",
		Content:    text,
		Title:      "Fetched: " + rawURL,
		EnqueuedAt: time.Now(),
	})
}

// ExtractText converts HTML to clean plain text. Exported for testing.
func ExtractText(html string) string {
	return extractText(html)
}

// extractText is the internal implementation.
func extractText(html string) string {
	// Remove <script> and <style> blocks first.
	text := scriptStyleRe.ReplaceAllString(html, "")
	// Replace block-level tags with newlines for readability.
	for _, tag := range []string{"p", "div", "br", "h1", "h2", "h3", "h4", "li", "tr"} {
		text = regexp.MustCompile(`(?i)</?`+tag+`[^>]*>`).ReplaceAllString(text, "\n")
	}
	// Strip remaining tags.
	text = allTagsRe.ReplaceAllString(text, "")
	// Decode common HTML entities.
	text = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
	).Replace(text)
	// Collapse whitespace.
	text = multiSpaceRe.ReplaceAllString(text, " ")
	text = multiNewline.ReplaceAllString(text, "\n\n")
	// Trim each line and remove blank-only lines.
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimFunc(line, unicode.IsSpace)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n")
}
