package adapter

import "context"

// MockInferenceBackend is a test double for InferenceBackend.
// Each method delegates to the corresponding Fn field when non-nil,
// otherwise returns a canned successful response.
type MockInferenceBackend struct {
	GenerateFn    func(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error)
	StreamFn      func(ctx context.Context, req *GenerateRequest) (<-chan *GenerateResponse, error)
	HealthCheckFn func(ctx context.Context) error
}

func (m *MockInferenceBackend) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	if m.GenerateFn != nil {
		return m.GenerateFn(ctx, req)
	}
	return &GenerateResponse{
		ID:     "mock-id",
		Object: "chat.completion",
		Model:  req.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: "mock response"},
				FinishReason: "stop",
			},
		},
	}, nil
}

func (m *MockInferenceBackend) Stream(ctx context.Context, req *GenerateRequest) (<-chan *GenerateResponse, error) {
	if m.StreamFn != nil {
		return m.StreamFn(ctx, req)
	}
	ch := make(chan *GenerateResponse, 1)
	ch <- &GenerateResponse{
		ID:     "mock-id",
		Object: "chat.completion.chunk",
		Model:  req.Model,
		Choices: []Choice{
			{
				Index: 0,
				Delta: &Message{Role: "assistant", Content: "mock token"},
			},
		},
	}
	close(ch)
	return ch, nil
}

func (m *MockInferenceBackend) HealthCheck(ctx context.Context) error {
	if m.HealthCheckFn != nil {
		return m.HealthCheckFn(ctx)
	}
	return nil
}
