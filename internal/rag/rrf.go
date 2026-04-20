package rag

import "sort"

// RRFMerge combines two ranked lists using Reciprocal Rank Fusion.
// score(d) = sum over lists: 1/(k + rank(d)) where k=60, rank is 1-based.
// Returns top-K results sorted by merged RRF score descending.
func RRFMerge(annResults, ftsResults []RetrievalResult, topK int) []RetrievalResult {
	const k = 60.0
	scores := make(map[string]float64)
	byID := make(map[string]RetrievalResult)

	for i, r := range annResults {
		rank := float64(i + 1)
		scores[r.ChunkID] += 1.0 / (k + rank)
		if _, exists := byID[r.ChunkID]; !exists {
			byID[r.ChunkID] = r
		}
	}
	for i, r := range ftsResults {
		rank := float64(i + 1)
		scores[r.ChunkID] += 1.0 / (k + rank)
		if _, exists := byID[r.ChunkID]; !exists {
			byID[r.ChunkID] = r
		}
	}

	merged := make([]RetrievalResult, 0, len(scores))
	for id, score := range scores {
		r := byID[id]
		r.Score = score
		merged = append(merged, r)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})
	if topK > 0 && len(merged) > topK {
		merged = merged[:topK]
	}
	return merged
}
