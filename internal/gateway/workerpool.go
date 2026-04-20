package gateway

// WorkerPool is a bounded concurrency limiter backed by a buffered channel.
// Acquire returns false immediately when the pool is full, allowing the caller
// to return HTTP 429 without blocking.
type WorkerPool struct {
	sem     chan struct{}
	maxSize int
}

// NewWorkerPool creates a WorkerPool that allows at most size concurrent workers.
func NewWorkerPool(size int) *WorkerPool {
	return &WorkerPool{
		sem:     make(chan struct{}, size),
		maxSize: size,
	}
}

// Acquire claims a worker slot. Returns true on success, false when the pool
// is at capacity (caller should return HTTP 429).
func (p *WorkerPool) Acquire() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees a previously acquired worker slot.
func (p *WorkerPool) Release() {
	<-p.sem
}

// InFlight returns the number of currently occupied worker slots.
func (p *WorkerPool) InFlight() int {
	return len(p.sem)
}

// MaxSize returns the configured maximum pool capacity.
func (p *WorkerPool) MaxSize() int {
	return p.maxSize
}
