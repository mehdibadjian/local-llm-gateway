package ingest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/ingest"
	"github.com/caw/wrapper/internal/memory"
)

// mockScrollQdrant implements QdrantScroller for testing.
type mockScrollQdrant struct {
	points  []memory.QdrantPoint
	deleted []string
}

func (m *mockScrollQdrant) ScrollPoints(ctx context.Context, domain string, limit int, offset string) ([]memory.QdrantPoint, string, error) {
	return m.points, "", nil
}

func (m *mockScrollQdrant) DeletePoints(ctx context.Context, domain string, ids []string) error {
	m.deleted = append(m.deleted, ids...)
	return nil
}

// mockChunkLookup: only knows about "known-point-id"
type mockChunkLookup struct {
	known map[string]bool
}

func (m *mockChunkLookup) HasQdrantPoint(ctx context.Context, qdrantPointID string) (bool, error) {
	return m.known[qdrantPointID], nil
}

func TestReconciler_DeletesOrphanedPoints(t *testing.T) {
	ctx := context.Background()

	q := &mockScrollQdrant{
		points: []memory.QdrantPoint{
			{ID: "point-orphan-1", Vector: []float32{0.1}, Payload: map[string]interface{}{"domain": "general"}},
			{ID: "point-orphan-2", Vector: []float32{0.2}, Payload: map[string]interface{}{"domain": "general"}},
			{ID: "point-known", Vector: []float32{0.3}, Payload: map[string]interface{}{"domain": "general"}},
		},
	}
	pg := &mockChunkLookup{
		known: map[string]bool{"point-known": true},
	}

	var logs []string
	err := ingest.Reconcile(ctx, nil, q, pg, []string{"general"}, func(format string, args ...interface{}) {
		logs = append(logs, format)
	})
	require.NoError(t, err)

	assert.Len(t, q.deleted, 2)
	assert.Contains(t, q.deleted, "point-orphan-1")
	assert.Contains(t, q.deleted, "point-orphan-2")
	assert.NotContains(t, q.deleted, "point-known")
}

func TestReconciler_Idempotent(t *testing.T) {
	ctx := context.Background()

	q := &mockScrollQdrant{
		points: []memory.QdrantPoint{
			{ID: "point-1", Vector: []float32{0.1}, Payload: map[string]interface{}{}},
			{ID: "point-2", Vector: []float32{0.2}, Payload: map[string]interface{}{}},
		},
	}
	pg := &mockChunkLookup{
		known: map[string]bool{
			"point-1": true,
			"point-2": true,
		},
	}

	err := ingest.Reconcile(ctx, nil, q, pg, []string{"general"}, func(format string, args ...interface{}) {})
	require.NoError(t, err)

	assert.Empty(t, q.deleted)
}

func TestReconciler_LogsCount(t *testing.T) {
	ctx := context.Background()

	q := &mockScrollQdrant{
		points: []memory.QdrantPoint{
			{ID: "orphan-1", Vector: []float32{0.1}, Payload: map[string]interface{}{}},
		},
	}
	pg := &mockChunkLookup{known: map[string]bool{}}

	var logMessages []string
	err := ingest.Reconcile(ctx, nil, q, pg, []string{"general"}, func(format string, args ...interface{}) {
		logMessages = append(logMessages, format)
	})
	require.NoError(t, err)

	assert.NotEmpty(t, logMessages, "reconciler should log results")
}
