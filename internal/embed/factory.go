package embed

import "os"

// NewEmbedClientFromEnv constructs an EmbedClient based on environment variables.
//
// Environment variables:
//   - EMBED_BACKEND      — "onnx" selects ONNXEmbedClient; anything else (or unset)
//     selects HTTPEmbedClient (default, backwards-compatible).
//   - EMBED_SERVICE_URL  — base URL for HTTPEmbedClient (default: "http://localhost:8080").
//   - EMBED_MODEL_PATH   — path to the BGE-Small-v1.5 ONNX model file used by ONNXEmbedClient.
//
// opts are forwarded to the CircuitBreaker of whichever backend is selected.
func NewEmbedClientFromEnv(opts ...Option) EmbedClient {
	backend := os.Getenv("EMBED_BACKEND")
	if backend == "onnx" {
		modelPath := os.Getenv("EMBED_MODEL_PATH")
		return NewONNXEmbedClient(modelPath, opts...)
	}

	serviceURL := os.Getenv("EMBED_SERVICE_URL")
	if serviceURL == "" {
		serviceURL = "http://localhost:8080"
	}
	return NewHTTPEmbedClient(serviceURL, opts...)
}
