package orchestration

import (
	"context"
	"fmt"
	"strings"

	"github.com/caw/wrapper/internal/adapter"
)

// webSearchSignals triggers a web search before generation.
var webSearchSignals = []string{
	// temporal
	"latest", "current", "today", "recent", "news", "2024", "2025", "2026",
	"right now", "this week", "this month", "this year", "just released",
	// factual lookup
	"what is", "who is", "where is", "when did", "when was", "how many",
	"what happened", "tell me about", "explain what",
	// explicit search intent
	"search for", "look up", "find out", "can you find",
}

// WebSearcher is the interface the pipeline uses to run a web query.
type WebSearcher interface {
	Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error)
}

// SearchResult mirrors tools.SearchResult to avoid a circular import.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebAugmenter enriches a generation request with live web results
// when the user message contains web-search signals.
type WebAugmenter struct {
	searcher WebSearcher
}

// NewWebAugmenter creates a WebAugmenter.
func NewWebAugmenter(s WebSearcher) *WebAugmenter {
	return &WebAugmenter{searcher: s}
}

// NeedsSearch reports whether the last user message contains web-search signals.
func NeedsSearch(messages []adapter.Message) bool {
	last := strings.ToLower(lastUserMessage(messages))
	if last == "" {
		return false
	}
	for _, sig := range webSearchSignals {
		if strings.Contains(last, sig) {
			return true
		}
	}
	return false
}

// Augment performs a web search and prepends the results as a system context
// message so the model can cite them in its answer.
// Returns the augmented message slice (original is unchanged).
func (wa *WebAugmenter) Augment(ctx context.Context, messages []adapter.Message) ([]adapter.Message, bool, error) {
	if !NeedsSearch(messages) {
		return messages, false, nil
	}

	query := buildSearchQuery(lastUserMessage(messages))
	results, err := wa.searcher.Search(ctx, query, 3)
	if err != nil || len(results) == 0 {
		// Degrade gracefully — continue without web context.
		return messages, false, nil
	}

	context := formatResultsAsContext(query, results)
	augmented := injectWebContext(messages, context)
	return augmented, true, nil
}

// buildSearchQuery strips filler words to make a tighter search query.
func buildSearchQuery(userMsg string) string {
	fillers := []string{
		"can you", "please", "tell me about", "search for", "look up",
		"find out", "explain what", "what is the", "what are the",
	}
	q := strings.ToLower(userMsg)
	for _, f := range fillers {
		q = strings.ReplaceAll(q, f, "")
	}
	// Collapse whitespace and trim.
	q = strings.Join(strings.Fields(q), " ")
	if len(q) > 120 {
		q = q[:120]
	}
	return strings.TrimSpace(q)
}

// formatResultsAsContext formats search results as plain text for small models.
// Avoids JSON to prevent small models (gemma:2b) from hallucinating on structured data.
func formatResultsAsContext(_ string, results []SearchResult) string {
	var sb strings.Builder
	sb.WriteString("Use the following real-time information to answer the question:\n\n")
	for i, r := range results {
		if r.Snippet == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n%s\n\n", i+1, r.Title, r.Snippet))
	}
	sb.WriteString("Answer based on the information above.")
	return sb.String()
}

// injectWebContext prepends or appends a system message containing web context.
// If there is already a system message, the web context is appended to it.
func injectWebContext(messages []adapter.Message, webCtx string) []adapter.Message {
	out := make([]adapter.Message, 0, len(messages)+1)

	injected := false
	for _, m := range messages {
		if m.Role == "system" && !injected {
			out = append(out, adapter.Message{
				Role:    "system",
				Content: m.Content + "\n\n" + webCtx,
			})
			injected = true
		} else {
			out = append(out, m)
		}
	}
	if !injected {
		// No existing system message — prepend one.
		out = append([]adapter.Message{{Role: "system", Content: webCtx}}, out...)
	}
	return out
}
