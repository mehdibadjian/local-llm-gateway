package orchestration_test

import (
	"context"
	"testing"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/orchestration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputFormatter_NonStructured_PassesThrough(t *testing.T) {
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "hello world"}}},
			}, nil
		},
	}
	f := orchestration.NewOutputFormatter(mock)
	genReq := &adapter.GenerateRequest{Model: "test", Messages: []adapter.Message{adapter_message("hi")}}

	content, formatErr, err := f.Format(context.Background(), genReq, orchestration.IntentSimpleGenerate)

	require.NoError(t, err)
	assert.False(t, formatErr)
	assert.Equal(t, "hello world", content)
}

func TestOutputFormatter_ValidJSONFirstAttempt(t *testing.T) {
	calls := 0
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			calls++
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: `{"key":"value"}`}}},
			}, nil
		},
	}
	f := orchestration.NewOutputFormatter(mock)
	genReq := &adapter.GenerateRequest{Model: "test", Messages: []adapter.Message{adapter_message("give json")}}

	content, formatErr, err := f.Format(context.Background(), genReq, orchestration.IntentStructuredOutput)

	require.NoError(t, err)
	assert.False(t, formatErr)
	assert.Equal(t, `{"key":"value"}`, content)
	assert.Equal(t, 1, calls, "should only call backend once on valid JSON")
}

func TestOutputFormatter_InvalidJSON_OneRetry(t *testing.T) {
	calls := 0
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			calls++
			if calls == 1 {
				return &adapter.GenerateResponse{
					Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "not json at all"}}},
				}, nil
			}
			// Retry returns valid JSON.
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: `{"fixed":true}`}}},
			}, nil
		},
	}
	f := orchestration.NewOutputFormatter(mock)
	genReq := &adapter.GenerateRequest{Model: "test", Messages: []adapter.Message{adapter_message("give json")}}

	content, formatErr, err := f.Format(context.Background(), genReq, orchestration.IntentStructuredOutput)

	require.NoError(t, err)
	assert.False(t, formatErr)
	assert.Equal(t, `{"fixed":true}`, content)
	assert.Equal(t, 2, calls, "should retry exactly once")
}

func TestOutputFormatter_DoubleFailure_FormatError(t *testing.T) {
	mock := &adapter.MockInferenceBackend{
		GenerateFn: func(_ context.Context, req *adapter.GenerateRequest) (*adapter.GenerateResponse, error) {
			return &adapter.GenerateResponse{
				Choices: []adapter.Choice{{Message: adapter.Message{Role: "assistant", Content: "still not json"}}},
			}, nil
		},
	}
	f := orchestration.NewOutputFormatter(mock)
	genReq := &adapter.GenerateRequest{Model: "test", Messages: []adapter.Message{adapter_message("give json")}}

	content, formatErr, err := f.Format(context.Background(), genReq, orchestration.IntentStructuredOutput)

	require.NoError(t, err)
	assert.True(t, formatErr, "format error flag must be set after double failure")
	assert.Equal(t, "still not json", content)
}
