package orchestration

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/caw/wrapper/internal/adapter"
)

const (
	maxCodeFeedbackRounds = 3
	codeExecTimeout       = 10 * time.Second
)

var (
	pythonBlockRe = regexp.MustCompile("(?s)```python\n?(.*?)```")
	goBlockRe     = regexp.MustCompile("(?s)```go\n?(.*?)```")
	genericBlockRe = regexp.MustCompile("(?s)```(?:python|go|sh|bash)?\n?(.*?)```")
)

// CodeFeedbackLoop detects code blocks in a model response, executes them in a
// lightweight subprocess, and — if execution fails — feeds the error back to the
// model for self-correction. Up to maxCodeFeedbackRounds attempts are made.
//
// Returns (improved content, whether feedback loop ran, error).
// If no code is detected the original content is returned unchanged.
type CodeFeedbackLoop struct {
	backend adapter.InferenceBackend
}

// NewCodeFeedbackLoop constructs a CodeFeedbackLoop.
func NewCodeFeedbackLoop(backend adapter.InferenceBackend) *CodeFeedbackLoop {
	return &CodeFeedbackLoop{backend: backend}
}

// Run attempts to execute any Python code found in content and loops on errors.
func (c *CodeFeedbackLoop) Run(ctx context.Context, content string, req *adapter.GenerateRequest) (string, bool, error) {
	lang, code := extractFirstCodeBlock(content)
	if code == "" {
		return content, false, nil
	}

	best := content
	for round := 1; round <= maxCodeFeedbackRounds; round++ {
		execErr := runCode(lang, code)
		if execErr == nil {
			// Code runs cleanly — done.
			return best, true, nil
		}

		// Feed the execution error back to the model.
		fixPrompt := fmt.Sprintf(
			"The following %s code produced this error when executed:\n\n"+
				"```\n%s\n```\n\n"+
				"Error:\n%s\n\n"+
				"Please provide a corrected version of the complete code that fixes this error.",
			lang, code, execErr.Error(),
		)
		fixResp, err := c.backend.Generate(ctx, &adapter.GenerateRequest{
			Model:    req.Model,
			Messages: []adapter.Message{{Role: "user", Content: fixPrompt}},
		})
		if err != nil {
			return best, true, nil
		}
		best = fixResp.Choices[0].Message.Content

		// Extract the new code block for the next round.
		_, newCode := extractFirstCodeBlock(best)
		if newCode == "" {
			return best, true, nil
		}
		code = newCode
	}
	return best, true, nil
}

// extractFirstCodeBlock returns (language, code) for the first fenced code block.
// Returns ("", "") if no block is found.
func extractFirstCodeBlock(s string) (string, string) {
	if m := pythonBlockRe.FindStringSubmatch(s); len(m) == 2 {
		return "python", strings.TrimSpace(m[1])
	}
	if m := goBlockRe.FindStringSubmatch(s); len(m) == 2 {
		return "go", strings.TrimSpace(m[1])
	}
	if m := genericBlockRe.FindStringSubmatch(s); len(m) == 2 {
		return "python", strings.TrimSpace(m[1]) // default to python for generic blocks
	}
	return "", ""
}

// runCode executes a small snippet and returns any execution error.
// Python: python3 -c <code>
// Go: skipped (compilation is too slow for inline feedback).
func runCode(lang, code string) error {
	if lang != "python" {
		return nil // Only Python gets live execution feedback for now.
	}
	// Check python3 is available.
	if _, err := exec.LookPath("python3"); err != nil {
		return nil // Graceful degradation — no feedback without python3.
	}

	ctx, cancel := context.WithTimeout(context.Background(), codeExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", "-c", code)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
