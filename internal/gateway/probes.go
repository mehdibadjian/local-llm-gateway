package gateway

import (
	"context"
	"time"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/gofiber/fiber/v2"
)

// HealthzHandler always returns 200 with no external dependency checks.
// Used by liveness probes to verify the process is alive.
func HealthzHandler(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// ReadyzHandler returns a readiness probe handler that calls backend.HealthCheck
// with a hard 200 ms deadline. Returns 503 if the backend is unreachable.
func ReadyzHandler(backend adapter.InferenceBackend) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		if err := backend.HealthCheck(ctx); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "not ready",
				"error":  err.Error(),
			})
		}
		return c.JSON(fiber.Map{"status": "ready"})
	}
}
