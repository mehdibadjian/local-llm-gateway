package rag

import "sort"

type RerankerConfig struct {
	AgentMode bool
	IsAsync   bool
}

// Rerank applies cross-encoder scoring only when AgentMode=true OR IsAsync=true.
// Stub: sorts by Score descending (real model = Phase 2).
// For interactive turns (AgentMode=false, IsAsync=false): returns results unchanged.
func Rerank(results []RetrievalResult, cfg RerankerConfig) []RetrievalResult {
	if !cfg.AgentMode && !cfg.IsAsync {
		return results
	}
	sorted := make([]RetrievalResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})
	return sorted
}
