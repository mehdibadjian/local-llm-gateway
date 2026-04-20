package rag

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/caw/wrapper/internal/embed"
	"github.com/caw/wrapper/internal/memory"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

var legTimeoutTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "caw_retrieval_leg_timeout_total",
		Help: "Total retrieval leg timeouts by leg type (ann|fts)",
	},
	[]string{"leg"},
)

func init() {
	prometheus.MustRegister(legTimeoutTotal)
}

const (
	defaultTopK      = 10
	defaultTimeoutMs = 300
)

type QdrantSearcher interface {
	Search(ctx context.Context, domain string, vector []float32, topK int) ([]memory.QdrantSearchResult, error)
}

type FTSSearcher interface {
	FTSSearch(ctx context.Context, domain, query string, limit int) ([]memory.Chunk, error)
}

type RetrievalResult struct {
	ChunkID string
	Content string
	Score   float64
	Source  string
	Domain  string
}

type HybridRetriever struct {
	qdrant      QdrantSearcher
	pg          FTSSearcher
	embedClient embed.EmbedClient
	rdb         *redis.Client
	timeoutMs   int
}

func NewHybridRetriever(qdrant QdrantSearcher, pg FTSSearcher, embedClient embed.EmbedClient, rdb *redis.Client) *HybridRetriever {
	return &HybridRetriever{
		qdrant:      qdrant,
		pg:          pg,
		embedClient: embedClient,
		rdb:         rdb,
		timeoutMs:   defaultTimeoutMs,
	}
}

func (r *HybridRetriever) Retrieve(ctx context.Context, query, domain string) ([]RetrievalResult, error) {
	var cache *RetrievalCache
	if r.rdb != nil {
		cache = &RetrievalCache{rdb: r.rdb}
		if cached, hit, err := cache.Get(ctx, domain, query); err == nil && hit {
			return cached, nil
		}
	}

	timeout := time.Duration(r.timeoutMs) * time.Millisecond
	annCtx, annCancel := context.WithTimeout(ctx, timeout)
	defer annCancel()
	ftsCtx, ftsCancel := context.WithTimeout(ctx, timeout)
	defer ftsCancel()

	var (
		annResults []RetrievalResult
		ftsResults []RetrievalResult
		mu         sync.Mutex
	)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		vec, err := r.embedClient.Embed(annCtx, query)
		if err != nil {
			if annCtx.Err() != nil {
				legTimeoutTotal.WithLabelValues("ann").Inc()
				log.Printf("rag: ANN embed timed out for domain=%s", domain)
			}
			return
		}
		results, err := r.qdrant.Search(annCtx, domain, vec, defaultTopK)
		if err != nil {
			if annCtx.Err() != nil {
				legTimeoutTotal.WithLabelValues("ann").Inc()
				log.Printf("rag: ANN search timed out for domain=%s", domain)
			}
			return
		}
		converted := make([]RetrievalResult, 0, len(results))
		for _, res := range results {
			content := ""
			if v, ok := res.Payload["content"]; ok {
				content, _ = v.(string)
			}
			converted = append(converted, RetrievalResult{
				ChunkID: res.ID,
				Content: content,
				Score:   float64(res.Score),
				Source:  "ann",
				Domain:  domain,
			})
		}
		mu.Lock()
		annResults = converted
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		results, err := r.pg.FTSSearch(ftsCtx, domain, query, defaultTopK)
		if err != nil {
			if ftsCtx.Err() != nil {
				legTimeoutTotal.WithLabelValues("fts").Inc()
				log.Printf("rag: FTS leg timed out for domain=%s", domain)
			}
			return
		}
		converted := make([]RetrievalResult, 0, len(results))
		for i, c := range results {
			converted = append(converted, RetrievalResult{
				ChunkID: c.ID,
				Content: c.Content,
				Score:   1.0 / float64(i+1),
				Source:  "fts",
				Domain:  domain,
			})
		}
		mu.Lock()
		ftsResults = converted
		mu.Unlock()
	}()

	wg.Wait()

	merged := RRFMerge(annResults, ftsResults, 5)

	if cache != nil && len(merged) > 0 {
		_ = cache.Set(ctx, domain, query, merged)
	}

	return merged, nil
}
