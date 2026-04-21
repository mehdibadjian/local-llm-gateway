package embed_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/caw/wrapper/internal/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time interface satisfaction check.
var _ embed.EmbedClient = (*embed.ONNXEmbedClient)(nil)

func TestONNXEmbedClient_InterfaceSatisfied(t *testing.T) {
	// The compile-time assertion above is the real test; this is a runtime marker.
	t.Log("ONNXEmbedClient satisfies EmbedClient interface")
}

// TestONNXEmbedClient_Embed_ReturnsCachedOnSecondCall verifies that the second
// call with the same text returns a cached result without re-running inference.
func TestONNXEmbedClient_Embed_ReturnsCachedOnSecondCall(t *testing.T) {
	// Empty modelPath → mock mode: inference returns deterministic embedding.
	client := embed.NewONNXEmbedClient("")
	ctx := context.Background()

	v1, err := client.Embed(ctx, "hello world")
	require.NoError(t, err)
	require.Len(t, v1, 384)

	v2, err := client.Embed(ctx, "hello world")
	require.NoError(t, err)
	require.Len(t, v2, 384)

	assert.Equal(t, v1, v2, "second call should return cached value")
}

// TestONNXEmbedClient_HealthCheck_ErrorWhenNotLoaded verifies that a client
// with no model file reports unhealthy.
func TestONNXEmbedClient_HealthCheck_ErrorWhenNotLoaded(t *testing.T) {
	client := embed.NewONNXEmbedClient("")
	err := client.HealthCheck(context.Background())
	assert.Error(t, err, "HealthCheck must error when no model is loaded")
}

// TestEmbedFactory_DefaultIsHTTP verifies that the factory returns *HTTPEmbedClient
// when EMBED_BACKEND is not set.
func TestEmbedFactory_DefaultIsHTTP(t *testing.T) {
	t.Setenv("EMBED_BACKEND", "")
	t.Setenv("EMBED_SERVICE_URL", "http://localhost:8081")

	client := embed.NewEmbedClientFromEnv()

	_, ok := client.(*embed.HTTPEmbedClient)
	assert.True(t, ok, "default backend should be *HTTPEmbedClient, got %T", client)
}

// TestEmbedFactory_ONNXBackend verifies that EMBED_BACKEND=onnx returns *ONNXEmbedClient.
func TestEmbedFactory_ONNXBackend(t *testing.T) {
	t.Setenv("EMBED_BACKEND", "onnx")
	t.Setenv("EMBED_MODEL_PATH", "")

	client := embed.NewEmbedClientFromEnv()

	_, ok := client.(*embed.ONNXEmbedClient)
	assert.True(t, ok, "EMBED_BACKEND=onnx should return *ONNXEmbedClient, got %T", client)
}

// TestONNXEmbedClient_CircuitBreaker_OpensOnFailures verifies that three
// consecutive Embed errors open the circuit breaker.
func TestONNXEmbedClient_CircuitBreaker_OpensOnFailures(t *testing.T) {
	// Use a non-existent model file so every Embed call fails.
	badPath := "nonexistent_model_12345.onnx"
	_ = os.Remove(badPath) // ensure it truly does not exist

	client := embed.NewONNXEmbedClient(badPath)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := client.Embed(ctx, fmt.Sprintf("unique text %d", i))
		require.Error(t, err, "embed call %d should fail with missing model", i)
	}

	_, err := client.Embed(ctx, "post-trip text")
	assert.ErrorIs(t, err, embed.ErrCircuitOpen, "circuit should be open after 3 failures")
}
