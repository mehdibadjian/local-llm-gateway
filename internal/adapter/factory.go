package adapter

import "os"

// NewBackend constructs the InferenceBackend selected by the INFERENCE_BACKEND
// environment variable. Defaults to OllamaAdapter when the variable is unset or "ollama".
func NewBackend() (InferenceBackend, error) {
	switch os.Getenv("INFERENCE_BACKEND") {
	case "llamacpp":
		return NewLlamaCppAdapter(), nil
	case "vllm":
		return NewVLLMAdapter(), nil
	default: // "ollama" or empty
		return NewOllamaAdapter(), nil
	}
}
