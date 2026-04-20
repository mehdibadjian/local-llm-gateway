package observability

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
)

// InFlightMiddleware returns a Fiber middleware that increments/decrements the
// canonical caw_requests_in_flight gauge around every request.
func InFlightMiddleware() fiber.Handler {
	return InFlightMiddlewareWith(RequestsInFlight)
}

// InFlightMiddlewareWith is the injectable variant used by unit tests.
func InFlightMiddlewareWith(g prometheus.Gauge) fiber.Handler {
	return func(c *fiber.Ctx) error {
		g.Inc()
		defer g.Dec()
		return c.Next()
	}
}

// ObserveRedisLatency wraps a Redis operation and records its duration in the
// canonical caw_redis_latency_seconds histogram.
func ObserveRedisLatency(op func() error) error {
	return ObserveRedisLatencyWith(RedisLatency, op)
}

// ObserveRedisLatencyWith is the injectable variant used by unit tests.
func ObserveRedisLatencyWith(h prometheus.Histogram, op func() error) error {
	start := time.Now()
	err := op()
	h.Observe(time.Since(start).Seconds())
	return err
}
