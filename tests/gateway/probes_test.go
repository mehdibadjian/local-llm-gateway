package gateway_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthz_AlwaysOK(t *testing.T) {
	app, _ := newTestServer(t, &adapter.MockInferenceBackend{})

	req := httptest.NewRequest("GET", "/healthz", nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestReadyz_BackendHealthy_Returns200(t *testing.T) {
	mock := &adapter.MockInferenceBackend{
		HealthCheckFn: func(_ context.Context) error { return nil },
	}
	app, _ := newTestServer(t, mock)

	req := httptest.NewRequest("GET", "/readyz", nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestReadyz_BackendDown_Returns503(t *testing.T) {
	mock := &adapter.MockInferenceBackend{
		HealthCheckFn: func(_ context.Context) error {
			return errors.New("connection refused")
		},
	}
	app, _ := newTestServer(t, mock)

	req := httptest.NewRequest("GET", "/readyz", nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 503, resp.StatusCode)
}

func TestReadyz_CompletesUnder200ms(t *testing.T) {
	app, _ := newTestServer(t, &adapter.MockInferenceBackend{})

	req := httptest.NewRequest("GET", "/readyz", nil)

	start := time.Now()
	resp, err := app.Test(req, 5000)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Less(t, elapsed, 200*time.Millisecond)
}
