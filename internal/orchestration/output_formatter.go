package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/caw/wrapper/internal/adapter"
)

// OutputFormatter applies grammar-constrained JSON formatting when the intent
// demands structured output, with a single correction-prompt retry on failure.
type OutputFormatter struct {
	backend adapter.InferenceBackend
}

// NewOutputFormatter returns an OutputFormatter backed by the given inference backend.
func NewOutputFormatter(backend adapter.InferenceBackend) *OutputFormatter {
	return &OutputFormatter{backend: backend}
}

// Format generates a response. For IntentStructuredOutput it enforces format:json
// and issues one correction-prompt retry on invalid JSON.
// Returns (content, formatError, error).
// formatError is true when both the primary attempt and the retry produced invalid JSON.
func (f *OutputFormatter) Format(ctx context.Context, req *adapter.GenerateRequest, intent Intent) (string, bool, error) {
	if intent != IntentStructuredOutput {
		resp, err := f.backend.Generate(ctx, req)
		if err != nil {
			return "", false, err
		}
		return resp.Choices[0].Message.Content, false, nil
	}

	// Grammar-constrained path.
	req.Format = "json"
	resp, err := f.backend.Generate(ctx, req)
	if err != nil {
		return "", false, err
	}

	content := resp.Choices[0].Message.Content
	if isValidJSON(content) {
		return content, false, nil
	}

	// One correction-prompt retry.
	correctionReq := buildCorrectionPrompt(req, content)
	resp2, err := f.backend.Generate(ctx, correctionReq)
	if err != nil {
		// Best-effort: return what we have with the format-error flag.
		return content, true, nil
	}

	content2 := resp2.Choices[0].Message.Content
	if isValidJSON(content2) {
		return content2, false, nil
	}
	return content2, true, nil
}

func isValidJSON(s string) bool {
	var v interface{}
	return json.Unmarshal([]byte(strings.TrimSpace(s)), &v) == nil
}

func buildCorrectionPrompt(original *adapter.GenerateRequest, badOutput string) *adapter.GenerateRequest {
	correctionMsg := adapter.Message{
		Role:    "user",
		Content: fmt.Sprintf("Your previous response was not valid JSON. Please respond with ONLY valid JSON, no explanation:\n%s", badOutput),
	}
	msgs := append(
		append([]adapter.Message{}, original.Messages...),
		adapter.Message{Role: "assistant", Content: badOutput},
		correctionMsg,
	)
	return &adapter.GenerateRequest{
		Model:    original.Model,
		Messages: msgs,
		Format:   "json",
	}
}
