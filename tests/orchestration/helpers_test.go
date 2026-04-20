package orchestration_test

import "github.com/caw/wrapper/internal/adapter"

// adapter_message is a test helper that builds a single-element message slice.
func adapter_message(content string) adapter.Message {
	return adapter.Message{Role: "user", Content: content}
}
