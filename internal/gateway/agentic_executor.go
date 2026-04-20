package gateway

// Server-side agentic executor — runs a full multi-turn tool loop inside CAW.
//
// When Claude Code CLI sends a request with tools, instead of doing one turn
// and returning a tool_use block (which gemma:2b often botches), CAW now:
//  1. Asks gemma:2b for the first action (as a bash/write block)
//  2. Executes the tool server-side (bash, write_file, read_file)
//  3. Feeds the output back to gemma:2b
//  4. Loops until the model produces a plain-text answer (no tool call)
//  5. Returns the final answer to Claude Code CLI as a normal text message
//
// This closes the loop entirely on the server — gemma:2b drives the plan,
// CAW executes every step, Claude Code CLI sees the finished result.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/caw/wrapper/internal/adapter"
)

const (
	maxAgentSteps   = 20           // prevent infinite loops
	cmdTimeout      = 30 * time.Second
	maxOutputBytes  = 8 * 1024    // cap per-command stdout (8 KB)
)

// AgentStep records one iteration of the agentic loop for logging.
type AgentStep struct {
	Step    int
	Tool    string
	Input   string
	Output  string
	Error   string
}

// RunAgentLoop executes a full server-side agentic loop.
// It returns the final plain-text answer from the model and a log of steps taken.
func RunAgentLoop(
	ctx context.Context,
	backend adapter.InferenceBackend,
	model string,
	msgs []adapter.Message,
	maxTokens int,
	workdir string,
) (string, []AgentStep, error) {
	if workdir == "" {
		workdir = os.TempDir()
	}

	var steps []AgentStep

	// Seed the conversation with a clear step-by-step instruction.
	loopMsgs := make([]adapter.Message, len(msgs))
	copy(loopMsgs, msgs)
	loopMsgs = append(loopMsgs, adapter.Message{
		Role: "user",
		Content: "Complete this task step by step using non-interactive bash commands.\n" +
			"Rules:\n" +
			"- Write files with: echo 'content' > file.txt  (NOT nano, vim, or editors)\n" +
			"- Run Python with: python3 script.py\n" +
			"- For each step, write ONLY a single ```bash code block. No explanation.\n" +
			"- When fully done, write ONLY: DONE: <one sentence summary>",
	})

	nudges := 0 // consecutive turns with no tool call
	for i := 0; i < maxAgentSteps; i++ {
		req := &adapter.GenerateRequest{
			Model:     model,
			Messages:  loopMsgs,
			Stream:    false,
			MaxTokens: maxTokens,
		}
		resp, err := backend.Generate(ctx, req)
		if err != nil {
			return buildFinalAnswer("", steps), steps, fmt.Errorf("step %d: %w", i+1, err)
		}
		text := strings.TrimSpace(resp.Choices[0].Message.Content)

		// Check for DONE signal in non-code text ONLY (before any tool call attempt).
		// If DONE is inside a code block, let cleanToolCall strip it and execute first.
		textOutsideCode := stripCodeBlocks(text)
		if doneMsg, isDone := extractDone(textOutsideCode); isDone && len(steps) > 0 {
			return buildFinalAnswer(doneMsg, steps), steps, nil
		}

		// Try to extract a tool call.
		tc := parseToolCall(text)
		if tc == nil {
			nudges++
			// After 2 nudges without a tool call and steps done → treat as final answer.
			if nudges >= 2 {
				return buildFinalAnswer(text, steps), steps, nil
			}
			loopMsgs = append(loopMsgs,
				adapter.Message{Role: "assistant", Content: text},
				adapter.Message{Role: "user", Content: "Write the next bash command as a ```bash``` block. Or write DONE: <summary> if finished."},
			)
			continue
		}
		nudges = 0

		// Strip DONE lines from command; remember if DONE was present.
		tc, hasDone, doneMsg := cleanToolCallWithDone(tc)

		// Execute.
		step := AgentStep{Step: i + 1, Tool: tc.Name, Input: string(tc.Input)}
		output, execErr := executeTool(ctx, tc, workdir)
		if execErr != nil {
			step.Error = execErr.Error()
			output = fmt.Sprintf("error: %s", execErr.Error())
		} else {
			step.Output = output
		}
		steps = append(steps, step)

		// If the model signalled DONE inside the bash block, we're done.
		if hasDone {
			return buildFinalAnswer(doneMsg, steps), steps, nil
		}

		loopMsgs = append(loopMsgs,
			adapter.Message{Role: "assistant", Content: text},
			adapter.Message{Role: "user", Content: fmt.Sprintf(
				"Output:\n```\n%s\n```\nNext ```bash``` command, or DONE: <summary>.",
				output,
			)},
		)
	}

	return buildFinalAnswer("", steps), steps, nil
}

// extractDone checks whether the model's response signals task completion.
// Returns (summary, true) on match.
func extractDone(text string) (string, bool) {
	upper := strings.ToUpper(text)
	for _, prefix := range []string{"DONE:", "DONE ", "COMPLETE:", "FINISHED:", "ALL DONE"} {
		if idx := strings.Index(upper, prefix); idx >= 0 {
			// Make sure it's not buried deep inside a long code block
			// (allow up to 40 chars before it on the same logical line)
			lineStart := strings.LastIndex(text[:idx], "\n")
			if lineStart < 0 {
				lineStart = 0
			}
			if idx-lineStart <= 40 {
				summary := strings.TrimSpace(text[idx+len(prefix):])
				// Strip trailing code/markdown
				if nl := strings.Index(summary, "\n"); nl >= 0 {
					summary = summary[:nl]
				}
				return strings.TrimSpace(summary), true
			}
		}
	}
	return "", false
}

// stripCodeBlocks removes all fenced code blocks, returning only surrounding prose.
func stripCodeBlocks(text string) string {
	re := regexp.MustCompile("(?s)```[a-z]*\\s*\n.*?```")
	return strings.TrimSpace(re.ReplaceAllString(text, ""))
}

// cleanToolCallWithDone strips DONE lines from a command and reports if DONE was present.
func cleanToolCallWithDone(tc *toolCallResult) (*toolCallResult, bool, string) {
	var args map[string]interface{}
	if err := json.Unmarshal(tc.Input, &args); err != nil {
		return tc, false, ""
	}
	cmd, ok := args["command"].(string)
	if !ok {
		return tc, false, ""
	}
	var clean []string
	hasDone := false
	doneMsg := ""
	for _, line := range strings.Split(cmd, "\n") {
		upper := strings.ToUpper(strings.TrimSpace(line))
		if strings.HasPrefix(upper, "DONE") ||
			strings.HasPrefix(upper, "COMPLETE") ||
			strings.HasPrefix(upper, "FINISHED") {
			hasDone = true
			if idx := strings.Index(line, ":"); idx >= 0 {
				doneMsg = strings.TrimSpace(line[idx+1:])
			}
			continue
		}
		clean = append(clean, line)
	}
	newCmd := strings.TrimSpace(strings.Join(clean, "\n"))
	if newCmd == "" {
		return tc, hasDone, doneMsg
	}
	args["command"] = newCmd
	newInput, _ := json.Marshal(args)
	return &toolCallResult{Name: tc.Name, Input: newInput, ID: tc.ID}, hasDone, doneMsg
}

// cleanToolCall strips DONE lines from a command (compatibility wrapper).
func cleanToolCall(tc *toolCallResult) *toolCallResult {
	tc, _, _ = cleanToolCallWithDone(tc)
	return tc
}

// buildFinalAnswer composes the agent's final response with a step summary.
func buildFinalAnswer(summary string, steps []AgentStep) string {
	if len(steps) == 0 {
		return summary
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Completed in %d step(s)**\n\n", len(steps)))
	for _, s := range steps {
		sb.WriteString(fmt.Sprintf("**Step %d — `%s`**\n", s.Step, s.Tool))
		// Show the command
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(s.Input), &args); err == nil {
			if cmd, ok := args["command"]; ok {
				sb.WriteString(fmt.Sprintf("```bash\n%v\n```\n", cmd))
			}
		}
		if s.Output != "" {
			lines := strings.Split(strings.TrimSpace(s.Output), "\n")
			if len(lines) > 8 {
				lines = append(lines[:8], "…")
			}
			sb.WriteString(fmt.Sprintf("Output: `%s`\n", strings.Join(lines, " | ")))
		}
		if s.Error != "" {
			sb.WriteString(fmt.Sprintf("⚠️  Error: %s\n", s.Error))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("---\n")
	if summary != "" {
		sb.WriteString(summary)
	}
	return sb.String()
}

// executeTool runs a single tool call and returns its output.
func executeTool(ctx context.Context, tc *toolCallResult, workdir string) (string, error) {
	switch strings.ToLower(tc.Name) {
	case "bash", "shell", "sh", "computer":
		return executeBash(ctx, tc.Input, workdir)
	case "write_file", "create_file", "str_replace_editor", "editor":
		return executeWriteFile(tc.Input, workdir)
	case "read_file":
		return executeReadFile(tc.Input, workdir)
	default:
		// Unknown tool — try bash as fallback
		return executeBash(ctx, tc.Input, workdir)
	}
}

// executeBash runs a shell command and returns combined stdout+stderr (capped).
func executeBash(ctx context.Context, input json.RawMessage, workdir string) (string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(input, &args); err != nil {
		// Raw string fallback
		args = map[string]interface{}{"command": strings.Trim(string(input), `"`)}
	}

	command := ""
	for _, key := range []string{"command", "cmd", "input", "code"} {
		if v, ok := args[key]; ok {
			command = fmt.Sprintf("%v", v)
			break
		}
	}
	if command == "" {
		return "", fmt.Errorf("no command found in input: %s", input)
	}

	tctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(tctx, "bash", "-c", command)
	cmd.Dir = workdir

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	_ = cmd.Run() // capture output even on non-zero exit

	out := buf.String()
	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes] + "\n[output truncated]"
	}
	if out == "" {
		out = "(no output)"
	}
	return out, nil
}

// executeWriteFile writes content to a file, creating parent dirs as needed.
func executeWriteFile(input json.RawMessage, workdir string) (string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid write_file input: %w", err)
	}

	// Support multiple field name conventions
	path := stringField(args, "path", "file", "filename", "name")
	content := stringField(args, "content", "text", "body", "code")

	if path == "" {
		return "", fmt.Errorf("write_file: no path specified in %s", input)
	}

	// Resolve relative paths against workdir
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Written %d bytes to %s", len(content), path), nil
}

// executeReadFile reads a file and returns its content (capped).
func executeReadFile(input json.RawMessage, workdir string) (string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid read_file input: %w", err)
	}

	path := stringField(args, "path", "file", "filename")
	if path == "" {
		return "", fmt.Errorf("read_file: no path specified")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	out := string(data)
	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes] + "\n[truncated]"
	}
	return out, nil
}

// stringField returns the first non-empty string value from a map for any of the given keys.
func stringField(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s := fmt.Sprintf("%v", v); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

// agentWorkdir returns a per-request temp directory for isolated execution.
func agentWorkdir() string {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("caw_agent_%d", time.Now().UnixNano()))
	_ = os.MkdirAll(dir, 0755)
	return dir
}
