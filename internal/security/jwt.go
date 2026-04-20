package security

import (
	"errors"
	"os"
	"strings"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/gofiber/fiber/v2"
)

// CAWClaims carries tenant identity and per-domain access list.
type CAWClaims struct {
	TenantID string   `json:"sub"`
	Domains  []string `json:"domains"`
	gojwt.RegisteredClaims
}

// ParseJWT validates a raw token string against the given HMAC-SHA256 secret
// and returns the parsed CAWClaims on success.
func ParseJWT(tokenStr, secret string) (*CAWClaims, error) {
	claims := &CAWClaims{}
	token, err := gojwt.ParseWithClaims(tokenStr, claims, func(t *gojwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	if len(claims.Domains) == 0 {
		return nil, errors.New("missing domains claim")
	}
	return claims, nil
}

// NewJWTMiddleware returns a Fiber middleware that validates JWT Bearer tokens.
// When CAW_JWT_SECRET is not set it falls back to NewAuthMiddleware (API key auth).
// On success it stores "tenant_id" and "domains" in Locals for downstream handlers.
func NewJWTMiddleware() fiber.Handler {
	secret := os.Getenv("CAW_JWT_SECRET")
	if secret == "" {
		return NewAuthMiddleware()
	}
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

		claims, err := ParseJWT(parts[1], secret)
		if err != nil {
			// Expired tokens and missing-domains both surface here.
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"message": err.Error(),
					"type":    "auth_error",
					"code":    "invalid_token",
				},
			})
		}

		// Enforce per-domain access when the caller supplies X-Domain.
		requestedDomain := c.Get("X-Domain")
		if requestedDomain != "" {
			allowed := false
			for _, d := range claims.Domains {
				if d == requestedDomain {
					allowed = true
					break
				}
			}
			if !allowed {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error": fiber.Map{
						"message": "domain not permitted for this token",
						"type":    "auth_error",
						"code":    "domain_forbidden",
					},
				})
			}
		}

		c.Locals("tenant_id", claims.TenantID)
		c.Locals("domains", claims.Domains)
		return c.Next()
	}
}
