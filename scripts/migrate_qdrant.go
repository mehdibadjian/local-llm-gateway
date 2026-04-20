// +build ignore

package main

// migrate_qdrant.go is a standalone CLI utility for migrating Qdrant collections
// from single-node mode to distributed mode. It accepts source and target URLs,
// reads collections from the source, creates them in the target with
// replication_factor: 2, and migrates point data.
//
// Usage:
//   go run scripts/migrate_qdrant.go \
//     --source-url=http://localhost:6333 \
//     --target-url=http://qdrant-distributed:6333 \
//     --collection=legal

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

var (
	sourceURL  = flag.String("source-url", "http://localhost:6333", "Source Qdrant URL (single-node)")
	targetURL  = flag.String("target-url", "http://qdrant-distributed:6333", "Target Qdrant URL (distributed)")
	collection = flag.String("collection", "", "Collection name to migrate (required)")
)

func main() {
	flag.Parse()

	if *collection == "" {
		log.Fatal("--collection flag is required")
	}

	log.Printf("Starting Qdrant migration: %s → %s (collection: %s)",
		*sourceURL, *targetURL, *collection)

	// Step 1: Fetch source collection metadata
	sourceMeta, err := getCollectionMetadata(*sourceURL, *collection)
	if err != nil {
		log.Fatalf("Failed to fetch source collection metadata: %v", err)
	}
	log.Printf("Source collection: %d points, vector_size: %d",
		sourceMeta.PointsCount, sourceMeta.VectorSize)

	// Step 2: Create target collection with distributed params
	if err := createDistributedCollection(*targetURL, *collection, sourceMeta); err != nil {
		log.Fatalf("Failed to create target collection: %v", err)
	}
	log.Printf("Created distributed collection in target")

	// Step 3: Migrate point data from source to target
	if err := migratePoints(*sourceURL, *targetURL, *collection); err != nil {
		log.Fatalf("Failed to migrate points: %v", err)
	}
	log.Printf("Successfully migrated collection %q", *collection)
}

// CollectionMetadata holds Qdrant collection info.
type CollectionMetadata struct {
	PointsCount int `json:"points_count"`
	VectorSize  int `json:"config"`
}

func getCollectionMetadata(baseURL, collectionName string) (CollectionMetadata, error) {
	url := baseURL + "/collections/" + collectionName
	resp, err := http.Get(url)
	if err != nil {
		return CollectionMetadata{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return CollectionMetadata{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var body struct {
		Result struct {
			PointsCount int `json:"points_count"`
			Config      struct {
				Params struct {
					VectorSize int `json:"size"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return CollectionMetadata{}, fmt.Errorf("decode failed: %w", err)
	}

	return CollectionMetadata{
		PointsCount: body.Result.PointsCount,
		VectorSize:  body.Result.Config.Params.VectorSize,
	}, nil
}

// createDistributedCollection creates a new collection with replication_factor: 2
// and copies configuration from the source collection.
func createDistributedCollection(baseURL, collectionName string, sourceMeta CollectionMetadata) error {
	url := baseURL + "/collections/" + collectionName
	createReq := map[string]interface{}{
		"vectors": map[string]interface{}{
			"size":      sourceMeta.VectorSize,
			"distance":  "Cosine",
		},
		"replication_factor": 2,
		"write_consistency_factor": 2,
	}

	payload, err := json.Marshal(createReq)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	// Wait for collection to be ready
	time.Sleep(2 * time.Second)
	return nil
}

// ScrollRequest is used to scroll through all points in a collection.
type ScrollRequest struct {
	Limit     int       `json:"limit"`
	WithVector bool     `json:"with_vector"`
	WithPayload bool    `json:"with_payload"`
}

// UpsertRequest holds points for upsert operation.
type UpsertRequest struct {
	Points []interface{} `json:"points"`
}

// migratePoints reads all points from source and upserts them to target.
func migratePoints(sourceURL, targetURL, collectionName string) error {
	limit := 100
	offset := ""
	batchCount := 0

	for {
		// Scroll through source points
		scrollReq := map[string]interface{}{
			"limit":          limit,
			"with_vector":    true,
			"with_payload":   true,
		}
		if offset != "" {
			scrollReq["offset"] = offset
		}

		payload, err := json.Marshal(scrollReq)
		if err != nil {
			return fmt.Errorf("marshal scroll request: %w", err)
		}

		url := sourceURL + "/collections/" + collectionName + "/points/scroll"
		resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("scroll request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
		}

		var scrollResp struct {
			Result struct {
				Points []interface{} `json:"points"`
				NextOffset *string    `json:"next_page_offset"`
			} `json:"result"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&scrollResp); err != nil {
			resp.Body.Close()
			return fmt.Errorf("decode scroll response: %w", err)
		}
		resp.Body.Close()

		if len(scrollResp.Result.Points) == 0 {
			break
		}

		// Upsert points to target
		upsertReq := map[string]interface{}{
			"points": scrollResp.Result.Points,
		}

		upsertPayload, err := json.Marshal(upsertReq)
		if err != nil {
			return fmt.Errorf("marshal upsert request: %w", err)
		}

		upsertURL := targetURL + "/collections/" + collectionName + "/points?wait=true"
		resp, err = http.Post(upsertURL, "application/json", bytes.NewReader(upsertPayload))
		if err != nil {
			return fmt.Errorf("upsert request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("upsert status %d: %s", resp.StatusCode, string(body))
		}
		resp.Body.Close()

		batchCount++
		log.Printf("Migrated batch %d (%d points)", batchCount, len(scrollResp.Result.Points))

		// Check if there are more points
		if scrollResp.Result.NextOffset == nil {
			break
		}
		offset = *scrollResp.Result.NextOffset
	}

	return nil
}
