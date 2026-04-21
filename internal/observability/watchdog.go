package observability

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type watchdogConfig struct {
	memStatsFn func(*runtime.MemStats)
	interval   time.Duration
}

// WatchdogOption configures StartRAMWatchdog behaviour.
type WatchdogOption func(*watchdogConfig)

// WithMemStatsFn replaces the default runtime.ReadMemStats call — used in
// tests to inject deterministic memory readings.
func WithMemStatsFn(fn func(*runtime.MemStats)) WatchdogOption {
	return func(c *watchdogConfig) { c.memStatsFn = fn }
}

// WithInterval overrides the default 5 s polling interval — useful in tests.
func WithInterval(d time.Duration) WatchdogOption {
	return func(c *watchdogConfig) { c.interval = d }
}

// StartRAMWatchdog starts a goroutine that polls HeapAlloc every interval
// (default 5 s).  When HeapAlloc crosses thresholdMB * 1024 * 1024 bytes,
// onExhausted is called exactly once (edge-trigger: resets only when the
// allocation drops back below the threshold).
// The returned stop func cancels the goroutine; it is safe to call once.
func StartRAMWatchdog(ctx context.Context, thresholdMB uint64, onExhausted func(), opts ...WatchdogOption) func() {
	cfg := &watchdogConfig{
		memStatsFn: runtime.ReadMemStats,
		interval:   5 * time.Second,
	}
	for _, o := range opts {
		o(cfg)
	}

	stopCh := make(chan struct{})
	var once sync.Once

	// fired is 1 while HeapAlloc is above threshold; 0 when below.
	// CompareAndSwap from 0→1 is the single-fire edge trigger.
	var fired atomic.Bool

	go func() {
		ticker := time.NewTicker(cfg.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				var ms runtime.MemStats
				cfg.memStatsFn(&ms)
				if ms.HeapAlloc > thresholdMB*1024*1024 {
					if fired.CompareAndSwap(false, true) {
						onExhausted()
					}
				} else {
					fired.Store(false) // reset — allow re-fire on next crossing
				}
			}
		}
	}()

	return func() { once.Do(func() { close(stopCh) }) }
}
