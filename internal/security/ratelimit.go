package security

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

const (
	RateLimitPerMin = 60
	WindowSeconds   = 60
)

// rateLimitScript atomically increments the request counter and sets TTL on
// first creation. Returns 1 if the request is allowed, 0 if the limit is exceeded.
var rateLimitScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local current = redis.call('INCR', key)
if current == 1 then
    redis.call('EXPIRE', key, window)
end
if current > limit then
    return 0
end
return 1
`)

// RateLimiter is a distributed rate limiter backed by Redis.
// Key pattern: caw:rate:{api_key}:{window_ts} where window_ts = Unix / 60.
type RateLimiter struct {
	rdb *redis.Client
}

// NewRateLimiter creates a RateLimiter using the provided Redis client.
func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb}
}

// Middleware returns a Fiber middleware that enforces per-key rate limiting.
// Must run after NewAuthMiddleware so that "api_key" is present in Locals.
func (rl *RateLimiter) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		apiKey, ok := c.Locals("api_key").(string)
		if !ok || apiKey == "" {
			return c.Next()
		}

		windowTs := time.Now().Unix() / WindowSeconds
		key := fmt.Sprintf("caw:rate:%s:%d", apiKey, windowTs)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		result, err := rateLimitScript.Run(ctx, rl.rdb, []string{key}, RateLimitPerMin, WindowSeconds).Int()
		if err != nil || result == 0 {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": fiber.Map{
					"message": "rate limit exceeded",
					"type":    "rate_limit_error",
				},
			})
		}
		return c.Next()
	}
}
