//go:build !cgo

package embed

import (
	"context"
	"errors"
)

// ONNXEmbedClient is a no-op stub compiled when CGO_ENABLED=0.
// All methods return an error explaining that CGO is required.
type ONNXEmbedClient struct{}

// NewONNXEmbedClient returns a stub client that always errors.
func NewONNXEmbedClient(_ string, _ ...Option) *ONNXEmbedClient {
	return &ONNXEmbedClient{}
}

// Embed always returns an error in the no-CGO build.
func (c *ONNXEmbedClient) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("onnx: ONNX embedding requires CGO_ENABLED=1")
}

// HealthCheck always returns an error in the no-CGO build.
func (c *ONNXEmbedClient) HealthCheck(_ context.Context) error {
	return errors.New("onnx: ONNX embedding requires CGO_ENABLED=1")
}
