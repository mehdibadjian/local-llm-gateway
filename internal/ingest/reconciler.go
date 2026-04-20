package ingest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/caw/wrapper/internal/memory"
)

// QdrantScroller can page through all points in a domain collection.
type QdrantScroller interface {
	ScrollPoints(ctx context.Context, domain string, limit int, offset string) ([]memory.QdrantPoint, string, error)
	DeletePoints(ctx context.Context, domain string, ids []string) error
}

// ChunkLookup checks whether a Qdrant point ID has a corresponding PG chunk row.
type ChunkLookup interface {
	HasQdrantPoint(ctx context.Context, qdrantPointID string) (bool, error)
}

// Reconcile scans every Qdrant point across the given domains and deletes any
// that no longer have a corresponding row in PostgreSQL.
//
// rdb is reserved for future use (e.g., distributed locking) and may be nil.
func Reconcile(
	ctx context.Context,
	_ *redis.Client,
	qdrant QdrantScroller,
	pg ChunkLookup,
	domains []string,
	log func(format string, args ...interface{}),
) error {
	for _, domain := range domains {
		orphansFound := 0
		orphansDeleted := 0
		offset := ""

		for {
			points, nextOffset, err := qdrant.ScrollPoints(ctx, domain, 100, offset)
			if err != nil {
				return fmt.Errorf("scroll points domain=%s: %w", domain, err)
			}

			var orphanIDs []string
			for _, p := range points {
				exists, err := pg.HasQdrantPoint(ctx, p.ID)
				if err != nil {
					return fmt.Errorf("has qdrant point %s: %w", p.ID, err)
				}
				if !exists {
					orphanIDs = append(orphanIDs, p.ID)
					orphansFound++
				}
			}

			if len(orphanIDs) > 0 {
				if err := qdrant.DeletePoints(ctx, domain, orphanIDs); err != nil {
					return fmt.Errorf("delete orphan points domain=%s: %w", domain, err)
				}
				orphansDeleted += len(orphanIDs)
			}

			if nextOffset == "" {
				break
			}
			offset = nextOffset
		}

		log("domain=%s orphans_found=%d orphans_deleted=%d", domain, orphansFound, orphansDeleted)
	}
	return nil
}

// parseJSON is a helper to unmarshal JSON strings — used by the Run loop.
func parseJSON(s string, v interface{}) error {
	return json.Unmarshal([]byte(s), v)
}
