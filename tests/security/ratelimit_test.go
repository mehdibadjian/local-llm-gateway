package security_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/security"
)

func newRateLimitApp(t *testing.T, rdb *redis.Client) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	// Inject api_key into Locals so rate limiter middleware can read it
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("api_key", "test-key")
		return c.Next()
	})
	app.Use(security.NewRateLimiter(rdb).Middleware())
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func newTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return rdb, mr
}

func TestRateLimiter_AllowsUpTo60(t *testing.T) {
	rdb, _ := newTestRedis(t)
	app := newRateLimitApp(t, rdb)

	for i := 1; i <= 60; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		resp, err := app.Test(req)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode, "request %d should be allowed", i)
	}
}

func TestRateLimiter_Blocks61st(t *testing.T) {
	rdb, _ := newTestRedis(t)
	app := newRateLimitApp(t, rdb)

	for i := 1; i <= 60; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		resp, err := app.Test(req)
		require.NoError(t, err)
		require.Equal(t, fiber.StatusOK, resp.StatusCode, "request %d should be allowed", i)
	}

	// 61st must be rate-limited
	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusTooManyRequests, resp.StatusCode, "61st request should be rate-limited")
}

func TestRateLimiter_KeyPattern(t *testing.T) {
	rdb, mr := newTestRedis(t)
	app := newRateLimitApp(t, rdb)

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	windowTs := time.Now().Unix() / 60
	expectedKey := fmt.Sprintf("caw:rate:test-key:%d", windowTs)

	keys := mr.Keys()
	assert.Contains(t, keys, expectedKey, "Redis key should match pattern caw:rate:{api_key}:{window_ts}")
}

func TestRateLimiter_TTLSet(t *testing.T) {
	rdb, _ := newTestRedis(t)
	app := newRateLimitApp(t, rdb)

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	windowTs := time.Now().Unix() / 60
	key := fmt.Sprintf("caw:rate:test-key:%d", windowTs)

	ttl, err := rdb.TTL(context.Background(), key).Result()
	require.NoError(t, err)
	assert.True(t, ttl > 0 && ttl <= 60*time.Second, "TTL should be set and <= 60s, got %v", ttl)
}

func TestRateLimiter_NoApiKey_Passthrough(t *testing.T) {
	rdb, _ := newTestRedis(t)

	// App without api_key in Locals — rate limiter should skip
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(security.NewRateLimiter(rdb).Middleware())
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}
