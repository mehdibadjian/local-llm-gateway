package security

import (
	"crypto/hmac"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// NewAuthMiddleware returns a Fiber middleware that enforces API key auth.
// Accepts both:
//   - Authorization: Bearer <key>   (OpenAI-compatible clients)
//   - x-api-key: <key>              (Anthropic SDK / Claude Code CLI)
//
// API key is read from CAW_API_KEY env var at startup.
// Uses hmac.Equal (constant-time) to prevent timing attacks.
func NewAuthMiddleware() fiber.Handler {
	apiKey := os.Getenv("CAW_API_KEY")
	return func(c *fiber.Ctx) error {
		token := extractToken(c)
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"message": "missing API key (provide Authorization: Bearer <key> or x-api-key: <key>)",
					"type":    "auth_error",
					"code":    "missing_auth",
				},
			})
		}

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

// extractToken pulls the API key from either the Authorization: Bearer header
// or the x-api-key header. Returns "" if neither is present.
func extractToken(c *fiber.Ctx) string {
	// Anthropic SDK sends x-api-key
	if key := c.Get("x-api-key"); key != "" {
		return key
	}
	// OpenAI-compatible clients send Authorization: Bearer
	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
		return parts[1]
	}
	return ""
}
