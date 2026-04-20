package security

import (
	"crypto/hmac"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// NewAuthMiddleware returns a Fiber middleware that enforces Bearer token auth.
// API key is read from CAW_API_KEY env var at startup (not per-request).
// Uses hmac.Equal (constant-time) to prevent timing attacks.
func NewAuthMiddleware() fiber.Handler {
	apiKey := os.Getenv("CAW_API_KEY")
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"message": "missing Authorization header",
					"type":    "auth_error",
					"code":    "missing_auth",
				},
			})
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"message": "invalid Authorization format",
					"type":    "auth_error",
				},
			})
		}

		token := parts[1]
		if !hmac.Equal([]byte(token), []byte(apiKey)) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": fiber.Map{
					"message": "invalid API key",
					"type":    "auth_error",
					"code":    "invalid_api_key",
				},
			})
		}

		c.Locals("api_key", token)
		return c.Next()
	}
}
