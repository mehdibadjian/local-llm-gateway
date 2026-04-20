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
	pythonBlockRe  = regexp.MustCompile("(?s)```python\n?(.*?)```")
	goBlockRe      = regexp.MustCompile("(?s)```go\n?(.*?)```")
	genericBlockRe = regexp.MustCompile("(?s)```(?:python|go|sh|bash)?\n?(.*?)```")
	// matches "def funcname(" at the start of a line
	pythonFuncRe = regexp.MustCompile(`(?m)^def ([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// CodeFeedbackLoop detects code blocks in a model response, executes them in a
// lightweight subprocess, and — if execution fails — feeds the error back to
// the model for self-correction. Up to maxCodeFeedbackRounds attempts are made.
//
// Phase 1: syntax/runtime check — catches crashes and SyntaxErrors.
// Phase 2: correctness check — asks the model to generate adversarial edge-case
// assertions, then runs code+assertions together to catch logic bugs
// (e.g. off-by-one, dropped tail, wrong branch).
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
		// Phase 1: syntax / runtime check.
		execErr := runCode(lang, code)
		if execErr != nil {
			best, code = c.requestFix(ctx, req, lang, code, execErr.Error())
			if code == "" {
				return best, true, nil
			}
			continue
		}

		// Phase 2: correctness check via adversarial edge-case assertions.
		if lang == "python" {
			assertions, genErr := c.generateEdgeCaseAssertions(ctx, req, code)
			if genErr == nil && assertions != "" {
				assertErr := runCodeWithAssertions(code, assertions)
				if assertErr != nil {
					best, code = c.requestFix(ctx, req, lang, code, assertErr.Error())
					if code == "" {
						return best, true, nil
					}
					continue
				}
			}
		}

		// Both phases passed — return the best response.
		return best, true, nil
	}
	return best, true, nil
}

// requestFix sends the error back to the model and returns the new (content, code).
// Returns ("", "") if the model response contains no code block.
func (c *CodeFeedbackLoop) requestFix(ctx context.Context, req *adapter.GenerateRequest, lang, code, errMsg string) (string, string) {
	fixPrompt := fmt.Sprintf(
		"The following %s code has a problem:\n\n"+
			"```%s\n%s\n```\n\n"+
			"Error / failing assertion:\n%s\n\n"+
			"Please provide a corrected version of the complete function(s) that fixes this issue. "+
			"Return ONLY the fixed code inside a single ```%s ... ``` block.",
		lang, lang, code, errMsg, lang,
	)
	fixResp, err := c.backend.Generate(ctx, &adapter.GenerateRequest{
		Model:    req.Model,
		Messages: []adapter.Message{{Role: "user", Content: fixPrompt}},
	})
	if err != nil {
		return "", ""
	}
	newContent := fixResp.Choices[0].Message.Content
	_, newCode := extractFirstCodeBlock(newContent)
	return newContent, newCode
}

// generateEdgeCaseAssertions asks the model to write adversarial assert statements
// for the functions defined in code. Returns a multi-line string of assert statements.
func (c *CodeFeedbackLoop) generateEdgeCaseAssertions(ctx context.Context, req *adapter.GenerateRequest, code string) (string, error) {
	funcs := extractFunctionNames(code)
	if len(funcs) == 0 {
		return "", nil
	}

	prompt := fmt.Sprintf(
		"Given this Python code:\n\n```python\n%s\n```\n\n"+
			"Write 6 to 8 assert statements that test EDGE CASES likely to expose bugs. "+
			"Think adversarially — include:\n"+
			"- Empty inputs (empty lists, empty strings, zero)\n"+
			"- Single-element inputs\n"+
			"- Inputs where one side is longer than the other\n"+
			"- Inputs with duplicate values\n"+
			"- Boundary values (None check if relevant, negative numbers)\n\n"+
			"Output ONLY raw assert statements (no markdown, no explanations, no imports). "+
			"Each assert on its own line. Example format:\n"+
			"assert func([]) == []\n"+
			"assert func([1], [1,2,3]) == [1,1,2,3]\n",
		code,
	)
	_ = funcs // already embedded in the code shown to the model

	resp, err := c.backend.Generate(ctx, &adapter.GenerateRequest{
		Model:    req.Model,
		Messages: []adapter.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	raw := resp.Choices[0].Message.Content

	// Strip any markdown fences the model sneaks in.
	raw = stripCodeFences(raw)

	// Keep only lines that start with "assert".
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "assert ") {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n"), nil
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

// extractFunctionNames returns the names of all top-level Python functions in code.
func extractFunctionNames(code string) []string {
	matches := pythonFuncRe.FindAllStringSubmatch(code, -1)
	seen := make(map[string]bool)
	var names []string
	for _, m := range matches {
		if len(m) == 2 && !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names
}

// ExtractFunctionNames is the exported alias used in tests.
var ExtractFunctionNames = extractFunctionNames

// stripCodeFences removes ``` fences from a string.
func stripCodeFences(s string) string {
	s = regexp.MustCompile("(?s)```[a-z]*\n?").ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "```", "")
	return s
}

// StripCodeFences is the exported alias used in tests.
var StripCodeFences = stripCodeFences

// runCode executes a snippet and returns any execution error.
// Python: python3 -c <code>; Go: skipped (compilation too slow for inline).
func runCode(lang, code string) error {
	if lang != "python" {
		return nil
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return nil // Graceful degradation.
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

// runCodeWithAssertions appends assert statements to the code and executes them.
// Assertion failures surface as AssertionError lines in the output.
func runCodeWithAssertions(code, assertions string) error {
	if assertions == "" {
		return nil
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return nil
	}

	combined := code + "\n\n# --- edge case assertions ---\n" + assertions

	ctx, cancel := context.WithTimeout(context.Background(), codeExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", "-c", combined)
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

// RunCodeWithAssertions is the exported alias used in tests.
var RunCodeWithAssertions = runCodeWithAssertions
