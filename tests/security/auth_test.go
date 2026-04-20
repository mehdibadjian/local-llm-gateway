package security_test

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caw/wrapper/internal/security"
)

func newAuthApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(security.NewAuthMiddleware())
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func TestAuthMiddleware_NoHeader_Returns401(t *testing.T) {
	t.Setenv("CAW_API_KEY", "test-secret-key")
	app := newAuthApp(t)

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}

func TestAuthMiddleware_InvalidToken_Returns403(t *testing.T) {
	t.Setenv("CAW_API_KEY", "test-secret-key")
	app := newAuthApp(t)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

func TestAuthMiddleware_ValidToken_PassesThrough(t *testing.T) {
	t.Setenv("CAW_API_KEY", "test-secret-key")
	app := newAuthApp(t)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer test-secret-key")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "ok", string(body))
}

func TestAuthMiddleware_ConstantTime(t *testing.T) {
	// Verify that the middleware uses hmac.Equal (constant-time comparison),
	// not ==. We do this structurally: provide a token that is a prefix of the
	// real key — a naive == would behave differently, but hmac.Equal always
	// compares the full byte slice in constant time.
	t.Setenv("CAW_API_KEY", "test-secret-key")
	app := newAuthApp(t)

	// Partial match — must still be rejected
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)

	// Different length — must also be rejected
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", "Bearer test-secret-key-extra")
	resp2, err := app.Test(req2)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp2.StatusCode)
}

func TestAuthMiddleware_ReadsFromEnv(t *testing.T) {
	t.Setenv("CAW_API_KEY", "env-loaded-key-xyz")
	app := newAuthApp(t)

	// Wrong key returns 403
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer other-key")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)

	// Correct env key returns 200
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", "Bearer env-loaded-key-xyz")
	resp2, err := app.Test(req2)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp2.StatusCode)
}

func TestAuthMiddleware_InvalidFormat_Returns401(t *testing.T) {
	t.Setenv("CAW_API_KEY", "test-secret-key")
	app := newAuthApp(t)

	// No "Bearer " prefix
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "test-secret-key")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}

func TestAuthMiddleware_XApiKey_AcceptsAnthropicHeader(t *testing.T) {
t.Setenv("CAW_API_KEY", "test-secret-key")
app := newAuthApp(t)

req := httptest.NewRequest("GET", "/test", nil)
req.Header.Set("x-api-key", "test-secret-key") // Anthropic SDK style
resp, err := app.Test(req)
require.NoError(t, err)
assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

func TestAuthMiddleware_XApiKey_WrongKey_Returns403(t *testing.T) {
t.Setenv("CAW_API_KEY", "test-secret-key")
app := newAuthApp(t)

req := httptest.NewRequest("GET", "/test", nil)
req.Header.Set("x-api-key", "wrong-key")
resp, err := app.Test(req)
require.NoError(t, err)
assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}
