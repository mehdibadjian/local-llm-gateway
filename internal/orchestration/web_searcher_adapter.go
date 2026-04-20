package orchestration

import (
	"context"

	"github.com/caw/wrapper/internal/tools"
)

// toolsWebSearchAdapter adapts tools.WebSearchExecutor to the orchestration.WebSearcher interface.
type toolsWebSearchAdapter struct {
	exec *tools.WebSearchExecutor
}

// NewToolsWebSearcher wraps a tools.WebSearchExecutor as a WebSearcher.
func NewToolsWebSearcher(exec *tools.WebSearchExecutor) WebSearcher {
	return &toolsWebSearchAdapter{exec: exec}
}

func (a *toolsWebSearchAdapter) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	raw, err := a.exec.Execute(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, len(raw))
	for i, r := range raw {
		results[i] = SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Snippet}
	}
	return results, nil
}
