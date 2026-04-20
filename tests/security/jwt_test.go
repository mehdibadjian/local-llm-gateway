package security_test

import (
	"net/http/httptest"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/security"
)

const testJWTSecret = "test-jwt-secret-for-caw"

// makeToken signs a CAWClaims token with the given secret and expiry offset.
func makeToken(t *testing.T, tenantID string, domains []string, expiresIn time.Duration, secret string) string {
	t.Helper()
	claims := &security.CAWClaims{
		TenantID: tenantID,
		Domains:  domains,
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(expiresIn)),
		},
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return signed
}

// makeTokenNoDomains creates a token with no domains claim.
func makeTokenNoDomains(t *testing.T, tenantID string, secret string) string {
	t.Helper()
	claims := &security.CAWClaims{
		TenantID: tenantID,
		Domains:  nil, // intentionally empty
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return signed
}

func newJWTApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(security.NewJWTMiddleware())
	app.Get("/v1/chat/completions", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

// AC1 + AC2: valid JWT with matching domain → 200
func TestJWT_ValidToken_AllowedDomain(t *testing.T) {
	t.Setenv("CAW_JWT_SECRET", testJWTSecret)

	token := makeToken(t, "tenant-1", []string{"finance"}, time.Hour, testJWTSecret)
	app := newJWTApp(t)

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Domain", "finance")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

// AC3: valid JWT but wrong domain → 403
func TestJWT_ValidToken_WrongDomain_Returns403(t *testing.T) {
	t.Setenv("CAW_JWT_SECRET", testJWTSecret)

	token := makeToken(t, "tenant-1", []string{"finance"}, time.Hour, testJWTSecret)
	app := newJWTApp(t)

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Domain", "legal")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

// AC4: expired JWT → 401
func TestJWT_ExpiredToken_Returns401(t *testing.T) {
	t.Setenv("CAW_JWT_SECRET", testJWTSecret)

	token := makeToken(t, "tenant-1", []string{"finance"}, -time.Hour, testJWTSecret)
	app := newJWTApp(t)

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Domain", "finance")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}

// AC5: CAW_JWT_SECRET not set → falls back to API key auth
func TestJWT_FallbackToAPIKey_WhenSecretNotSet(t *testing.T) {
	t.Setenv("CAW_JWT_SECRET", "") // ensure JWT secret is absent
	t.Setenv("CAW_API_KEY", "my-api-key")

	app := newJWTApp(t)

	// Valid API key → 200
	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer my-api-key")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	// Wrong API key → 403
	req2 := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req2.Header.Set("Authorization", "Bearer wrong-key")
	resp2, err := app.Test(req2)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp2.StatusCode)
}

// AC6: JWT with missing domains claim → 401
func TestJWT_MissingDomainsClaim_Returns401(t *testing.T) {
	t.Setenv("CAW_JWT_SECRET", testJWTSecret)

	token := makeTokenNoDomains(t, "tenant-1", testJWTSecret)
	app := newJWTApp(t)

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Domain", "finance")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}

// AC7 (structural): token with multi-domain list allows any matching domain
func TestJWT_MultiDomain_AllowsMatchingDomain(t *testing.T) {
	t.Setenv("CAW_JWT_SECRET", testJWTSecret)

	token := makeToken(t, "tenant-2", []string{"finance", "legal", "hr"}, time.Hour, testJWTSecret)
	app := newJWTApp(t)

	for _, domain := range []string{"finance", "legal", "hr"} {
		req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Domain", domain)
		resp, err := app.Test(req)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode, "domain: %s", domain)
	}

	// Domain not in list → 403
	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Domain", "medical")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

// Missing Authorization header → 401
func TestJWT_NoHeader_Returns401(t *testing.T) {
	t.Setenv("CAW_JWT_SECRET", testJWTSecret)
	app := newJWTApp(t)

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}
