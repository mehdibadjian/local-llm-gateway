package observability_test

import (
	"testing"

	"github.com/caw/wrapper/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		observability.RequestsInFlight,
		observability.RedisLatency,
		observability.IngestDLQDepth,
		observability.RAGDegradedTotal,
		observability.CritiquePassTotal,
	)
	return reg
}

func gatherMetric(t *testing.T, reg *prometheus.Registry, name string) *dto.MetricFamily {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

func TestAllMetricsRegistered(t *testing.T) {
	reg := newTestRegistry(t)

	// Use Describe() so CounterVec metrics show up even before any observation.
	descCh := make(chan *prometheus.Desc, 32)
	reg.Describe(descCh)
	close(descCh)

	names := make(map[string]bool)
	for d := range descCh {
		names[d.String()] = true
	}
	// Build a set of fqNames from the descriptions string representation.
	fqNames := make(map[string]bool)
	for d := range func() chan *prometheus.Desc {
		ch := make(chan *prometheus.Desc, 32)
		go func() { reg.Describe(ch); close(ch) }()
		return ch
	}() {
		// prometheus.Desc.String() contains Desc{fqName: "...", ...}
		s := d.String()
		start := len(`Desc{fqName: "`)
		end := len(s)
		for i := start; i < end; i++ {
			if s[i] == '"' {
				fqNames[s[start:i]] = true
				break
			}
		}
	}

	expected := []string{
		"caw_requests_in_flight",
		"caw_redis_latency_seconds",
		"caw_ingest_dlq_depth",
		"caw_rag_degraded_total",
		"caw_critique_pass_total",
	}
	for _, name := range expected {
		assert.True(t, fqNames[name], "metric %q not registered", name)
	}
}

func TestRequestsInFlight_IncDec(t *testing.T) {
	reg := newTestRegistry(t)

	observability.RequestsInFlight.Inc()
	observability.RequestsInFlight.Inc()

	mf := gatherMetric(t, reg, "caw_requests_in_flight")
	require.NotNil(t, mf)
	require.Len(t, mf.GetMetric(), 1)
	assert.Equal(t, float64(2), mf.GetMetric()[0].GetGauge().GetValue())

	observability.RequestsInFlight.Dec()
	mf = gatherMetric(t, reg, "caw_requests_in_flight")
	assert.Equal(t, float64(1), mf.GetMetric()[0].GetGauge().GetValue())

	// reset
	observability.RequestsInFlight.Dec()
}

func TestRedisLatency_Observed(t *testing.T) {
	reg := newTestRegistry(t)

	observability.RedisLatency.Observe(0.003)
	observability.RedisLatency.Observe(0.007)

	mf := gatherMetric(t, reg, "caw_redis_latency_seconds")
	require.NotNil(t, mf)
	require.Len(t, mf.GetMetric(), 1)
	assert.Equal(t, uint64(2), mf.GetMetric()[0].GetHistogram().GetSampleCount())
}

func TestRAGDegradedTotal_Increments(t *testing.T) {
	reg := newTestRegistry(t)

	observability.RAGDegradedTotal.WithLabelValues("finance").Inc()
	observability.RAGDegradedTotal.WithLabelValues("finance").Inc()
	observability.RAGDegradedTotal.WithLabelValues("medical").Inc()

	mf := gatherMetric(t, reg, "caw_rag_degraded_total")
	require.NotNil(t, mf)
	assert.Len(t, mf.GetMetric(), 2)

	total := 0.0
	for _, m := range mf.GetMetric() {
		total += m.GetCounter().GetValue()
	}
	assert.Equal(t, float64(3), total)
}

func TestCritiquePassTotal_Increments(t *testing.T) {
	reg := newTestRegistry(t)

	observability.CritiquePassTotal.WithLabelValues("auto").Inc()
	observability.CritiquePassTotal.WithLabelValues("manual").Inc()

	mf := gatherMetric(t, reg, "caw_critique_pass_total")
	require.NotNil(t, mf)
	assert.Len(t, mf.GetMetric(), 2)
}

func TestIngestDLQDepth_Set(t *testing.T) {
	reg := newTestRegistry(t)

	observability.IngestDLQDepth.Set(42)

	mf := gatherMetric(t, reg, "caw_ingest_dlq_depth")
	require.NotNil(t, mf)
	require.Len(t, mf.GetMetric(), 1)
	assert.Equal(t, float64(42), mf.GetMetric()[0].GetGauge().GetValue())

	// reset
	observability.IngestDLQDepth.Set(0)
}
