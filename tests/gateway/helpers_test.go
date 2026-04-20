// Package gateway_test provides shared test helpers for the gateway package tests.
package gateway_test

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/gateway"
	"github.com/caw/wrapper/internal/memory"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// newTestServer creates a gateway Server wired with the provided mock backend
// and an in-process Redis (miniredis). Returns the Fiber app and the pool for
// inspection. CAW_API_KEY is set to "test-key" for the duration of the test.
func newTestServer(t *testing.T, backend adapter.InferenceBackend) (*fiber.App, *gateway.WorkerPool) {
	t.Helper()
	t.Setenv("CAW_API_KEY", "test-key")

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	session := memory.NewSessionStore(rdb)

	srv := gateway.NewServer(backend, rdb, session)
	return srv.App(), srv.Pool()
}

// newTestServerWithRedis is like newTestServer but also exposes the raw redis
// client so callers can seed/inspect Redis keys directly.
func newTestServerWithRedis(t *testing.T, backend adapter.InferenceBackend) (*fiber.App, *gateway.WorkerPool, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	t.Setenv("CAW_API_KEY", "test-key")

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	session := memory.NewSessionStore(rdb)

	srv := gateway.NewServer(backend, rdb, session)
	return srv.App(), srv.Pool(), rdb, mr
}

// authHeader returns the Authorization header value for the test API key.
func authHeader() string { return "Bearer test-key" }
