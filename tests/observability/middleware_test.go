package observability_test

import (
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/observability"
	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gatherOne(t *testing.T, reg *prometheus.Registry, name string) *dto.MetricFamily {
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

func TestInFlightMiddleware_IncrementsOnRequest(t *testing.T) {
	// Use a fresh gauge so the test is isolated.
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "caw_requests_in_flight_mw_test"})
	reg := prometheus.NewRegistry()
	reg.MustRegister(gauge)

	// Build a tiny Fiber app that records the gauge value mid-flight.
	var inFlightDuringHandler float64
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(observability.InFlightMiddlewareWith(gauge))
	app.Get("/", func(c *fiber.Ctx) error {
		mfs, _ := reg.Gather()
		for _, mf := range mfs {
			if mf.GetName() == "caw_requests_in_flight_mw_test" {
				inFlightDuringHandler = mf.GetMetric()[0].GetGauge().GetValue()
			}
		}
		return c.SendStatus(200)
	})

	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	assert.Equal(t, float64(1), inFlightDuringHandler, "gauge should be 1 while handler runs")

	// After handler returns gauge should be back to 0.
	mf := gatherOne(t, reg, "caw_requests_in_flight_mw_test")
	require.NotNil(t, mf)
	assert.Equal(t, float64(0), mf.GetMetric()[0].GetGauge().GetValue())
}

func TestObserveRedisLatency_RecordsTime(t *testing.T) {
	hist := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "caw_redis_latency_seconds_test",
		Buckets: []float64{0.001, 0.010, 0.100},
	})
	reg := prometheus.NewRegistry()
	reg.MustRegister(hist)

	err := observability.ObserveRedisLatencyWith(hist, func() error {
		time.Sleep(5 * time.Millisecond)
		return nil
	})
	require.NoError(t, err)

	mf := gatherOne(t, reg, "caw_redis_latency_seconds_test")
	require.NotNil(t, mf)
	assert.Equal(t, uint64(1), mf.GetMetric()[0].GetHistogram().GetSampleCount())
}

func TestObserveRedisLatency_PropagatesError(t *testing.T) {
	hist := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "caw_redis_latency_seconds_err_test",
		Buckets: []float64{0.001, 0.010},
	})
	sentinel := errors.New("redis down")
	err := observability.ObserveRedisLatencyWith(hist, func() error { return sentinel })
	assert.ErrorIs(t, err, sentinel)
}
