package rag

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const cacheTTL = 60 * time.Second

type RetrievalCache struct {
	rdb *redis.Client
}

func NewRetrievalCache(rdb *redis.Client) *RetrievalCache {
	return &RetrievalCache{rdb: rdb}
}

func (rc *RetrievalCache) getVersion(ctx context.Context, domain string) (string, error) {
	key := fmt.Sprintf("caw:retrieval:%s:version", domain)
	val, err := rc.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return "0", nil
	}
	if err != nil {
		return "", fmt.Errorf("get version: %w", err)
	}
	return val, nil
}

func (rc *RetrievalCache) cacheKey(ctx context.Context, domain, query string) (string, error) {
	version, err := rc.getVersion(ctx, domain)
	if err != nil {
		return "", err
	}
	queryHash := fmt.Sprintf("%x", sha256.Sum256([]byte(query)))
	return fmt.Sprintf("caw:retrieval:%s:%s:%s", domain, queryHash, version), nil
}

func (rc *RetrievalCache) Get(ctx context.Context, domain, query string) ([]RetrievalResult, bool, error) {
	key, err := rc.cacheKey(ctx, domain, query)
	if err != nil {
		return nil, false, err
	}
	val, err := rc.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cache get: %w", err)
	}
	var results []RetrievalResult
	if err := json.Unmarshal([]byte(val), &results); err != nil {
		return nil, false, fmt.Errorf("cache unmarshal: %w", err)
	}
	return results, true, nil
}

func (rc *RetrievalCache) Set(ctx context.Context, domain, query string, results []RetrievalResult) error {
	key, err := rc.cacheKey(ctx, domain, query)
	if err != nil {
		return err
	}
	data, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("cache marshal: %w", err)
	}
	return rc.rdb.Set(ctx, key, string(data), cacheTTL).Err()
}
