package observability_test

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWatchdog_DoesNotFireBelowThreshold verifies that onExhausted is never
// called when HeapAlloc stays well below the threshold.
func TestWatchdog_DoesNotFireBelowThreshold(t *testing.T) {
	var count int64
	stop := observability.StartRAMWatchdog(
		context.Background(),
		1000, // 1 000 MB threshold
		func() { atomic.AddInt64(&count, 1) },
		observability.WithInterval(10*time.Millisecond),
		observability.WithMemStatsFn(func(ms *runtime.MemStats) {
			ms.HeapAlloc = 100 * 1024 * 1024 // 100 MB — well below
		}),
	)
	defer stop()
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int64(0), atomic.LoadInt64(&count))
}

// TestWatchdog_FiresWhenThresholdExceeded verifies onExhausted is called
// within 200 ms when HeapAlloc exceeds the threshold.
func TestWatchdog_FiresWhenThresholdExceeded(t *testing.T) {
	var count int64
	stop := observability.StartRAMWatchdog(
		context.Background(),
		500, // 500 MB threshold
		func() { atomic.AddInt64(&count, 1) },
		observability.WithInterval(10*time.Millisecond),
		observability.WithMemStatsFn(func(ms *runtime.MemStats) {
			ms.HeapAlloc = 600 * 1024 * 1024 // 600 MB — above threshold
		}),
	)
	defer stop()

	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&count) > 0
	}, 200*time.Millisecond, 5*time.Millisecond, "onExhausted should be called within 200 ms")
}

// TestWatchdog_StopCancelsGoroutine verifies that calling stop() halts polling
// and no further onExhausted calls are made afterward.
func TestWatchdog_StopCancelsGoroutine(t *testing.T) {
	var count int64
	stop := observability.StartRAMWatchdog(
		context.Background(),
		500,
		func() { atomic.AddInt64(&count, 1) },
		observability.WithInterval(10*time.Millisecond),
		observability.WithMemStatsFn(func(ms *runtime.MemStats) {
			ms.HeapAlloc = 600 * 1024 * 1024
		}),
	)

	// Let the watchdog run and fire (edge trigger fires once).
	time.Sleep(60 * time.Millisecond)
	stop()
	snapshot := atomic.LoadInt64(&count)

	// After stop, no additional calls should occur.
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, snapshot, atomic.LoadInt64(&count), "count must not change after stop()")
}

// TestWatchdog_EdgeTrigger_FiresOnce verifies that onExhausted is called
// exactly once even when HeapAlloc exceeds the threshold on every poll.
func TestWatchdog_EdgeTrigger_FiresOnce(t *testing.T) {
	var count int64
	stop := observability.StartRAMWatchdog(
		context.Background(),
		500,
		func() { atomic.AddInt64(&count, 1) },
		observability.WithInterval(10*time.Millisecond),
		observability.WithMemStatsFn(func(ms *runtime.MemStats) {
			ms.HeapAlloc = 600 * 1024 * 1024 // always above — many polls
		}),
	)
	defer stop()

	time.Sleep(150 * time.Millisecond) // ~15 polls
	assert.Equal(t, int64(1), atomic.LoadInt64(&count), "edge-trigger: must fire exactly once")
}

// TestHeapAllocMetric_Registered verifies that caw_heap_alloc_mb can be
// registered and is gathered successfully.
func TestHeapAllocMetric_Registered(t *testing.T) {
	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(observability.HeapAllocMB))

	mfs, err := reg.Gather()
	require.NoError(t, err)

	found := false
	for _, mf := range mfs {
		if mf.GetName() == "caw_heap_alloc_mb" {
			found = true
			require.Len(t, mf.GetMetric(), 1)
			assert.Greater(t, mf.GetMetric()[0].GetGauge().GetValue(), float64(0))
			break
		}
	}
	assert.True(t, found, "caw_heap_alloc_mb metric not found after registration")
}
