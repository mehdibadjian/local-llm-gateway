package adapter

import (
	"context"

	"golang.org/x/sync/semaphore"
)

const maxWeight int64 = 2

// GlobalResourceSemaphore prevents the LLM and embedder from running at full
// CPU concurrently, guarding against thermal throttling on unified-memory
// hardware (e.g., 8 GB Apple Silicon or equivalent).
//
// Weight allocation:
//
//	LLM Generate call  → 2  (consumes entire budget)
//	Embed call         → 1  (two embeds may overlap, LLM+embed cannot)
type GlobalResourceSemaphore struct {
	sem *semaphore.Weighted
}

// NewGlobalResourceSemaphore returns a semaphore with maxWeight = 2.
func NewGlobalResourceSemaphore() *GlobalResourceSemaphore {
	return &GlobalResourceSemaphore{sem: semaphore.NewWeighted(maxWeight)}
}

// AcquireLLM blocks until weight=2 is available or ctx is cancelled.
func (g *GlobalResourceSemaphore) AcquireLLM(ctx context.Context) error {
	return g.sem.Acquire(ctx, 2)
}

// ReleaseLLM releases weight=2 back to the semaphore.
func (g *GlobalResourceSemaphore) ReleaseLLM() {
	g.sem.Release(2)
}

// AcquireEmbed blocks until weight=1 is available or ctx is cancelled.
func (g *GlobalResourceSemaphore) AcquireEmbed(ctx context.Context) error {
	return g.sem.Acquire(ctx, 1)
}

// ReleaseEmbed releases weight=1 back to the semaphore.
func (g *GlobalResourceSemaphore) ReleaseEmbed() {
	g.sem.Release(1)
}

// TryAcquireLLM attempts a non-blocking acquire of weight=2.
// Returns true if acquired (caller must call ReleaseLLM), false otherwise.
func (g *GlobalResourceSemaphore) TryAcquireLLM() bool {
	return g.sem.TryAcquire(2)
}
