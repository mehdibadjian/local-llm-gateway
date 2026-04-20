package observability

import "github.com/prometheus/client_golang/prometheus"

var (
	// RequestsInFlight — current worker pool occupancy (KEDA trigger metric).
	RequestsInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "caw_requests_in_flight",
		Help: "Current number of requests being processed by the worker pool.",
	})

	// RedisLatency — Redis command round-trip time.
	RedisLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "caw_redis_latency_seconds",
		Help:    "Redis command latency in seconds.",
		Buckets: []float64{0.001, 0.005, 0.010, 0.025, 0.050, 0.100},
	})

	// IngestDLQDepth — observable gauge polled from the DLQ.
	IngestDLQDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "caw_ingest_dlq_depth",
		Help: "Current depth of the ingest dead-letter queue.",
	})

	// RAGDegradedTotal — requests served in RAG-degraded mode, by domain.
	RAGDegradedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "caw_rag_degraded_total",
		Help: "Total requests served in RAG-degraded mode.",
	}, []string{"domain"})

	// CritiquePassTotal — self-critique passes applied, by trigger type.
	CritiquePassTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "caw_critique_pass_total",
		Help: "Total self-critique passes applied.",
	}, []string{"trigger"})
)

// RegisterMetrics registers the 5 metrics owned by this package with the
// default Prometheus registry.  caw_retrieval_leg_timeout_total is registered
// in internal/rag/retriever.go and must NOT be re-registered here.
func RegisterMetrics() {
	prometheus.MustRegister(
		RequestsInFlight,
		RedisLatency,
		IngestDLQDepth,
		RAGDegradedTotal,
		CritiquePassTotal,
	)
}
