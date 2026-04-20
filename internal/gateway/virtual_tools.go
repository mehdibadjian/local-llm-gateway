package gateway

// Virtual Tool Calling — makes gemma:2b (which has no native tool-use support)
// work with Claude Code CLI's tool-calling protocol.
//
// Flow:
//  1. Claude Code CLI sends POST /v1/messages with `tools` array.
//  2. CAW injects a plain-text tool-instruction system prompt.
//  3. gemma:2b responds with text that contains tool intent.
//  4. CAW parses the text for tool calls (XML tags, bash blocks, fish-style).
//  5. CAW returns a proper Anthropic `tool_use` content block + stop_reason="tool_use".
//  6. Claude Code CLI executes the tool locally and sends back `tool_result`.
//  7. Next turn: CAW strips the injection prompt, gemma:2b produces the final answer.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// AnthropicTool mirrors the Anthropic tool definition schema.
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicToolChoice controls which tool (if any) the model must call.
type AnthropicToolChoice struct {
	Type string `json:"type"` // "auto", "any", "tool"
	Name string `json:"name,omitempty"`
}

// toolCallResult holds a parsed tool call from the model's text output.
type toolCallResult struct {
	Name  string
	Input json.RawMessage
	ID    string
}

// ── System-prompt injection ───────────────────────────────────────────────────

// buildToolSystemPrompt converts Anthropic tool schemas into a plain-text
// instruction block that small models (gemma:2b, llama3.2) can follow.
// The model is instructed to use XML-style <tool_call> tags when it needs a tool.
func buildToolSystemPrompt(tools []AnthropicTool) string {
	// For small models (gemma:2b, llama3.2:3b), the simplest prompt works best.
	// gemma:2b naturally generates bash code blocks — we parse those as tool calls.
	hasBash := false
	for _, t := range tools {
		if t.Name == "bash" || t.Name == "shell" || t.Name == "sh" {
			hasBash = true
			break
		}
	}

	var sb strings.Builder
	if hasBash {
		sb.WriteString("When you need to run a shell command, write ONLY a bash code block with the command. Example:\n")
		sb.WriteString("```bash\necho hello\n```\n")
		sb.WriteString("Do not add explanation. Just the code block.")
		return sb.String()
	}

	// Generic tools — use a simple tagged format
	sb.WriteString("You have tools. To call a tool respond with:\n")
	sb.WriteString("TOOL: tool_name\nINPUT: {\"key\": \"value\"}\n\n")
	sb.WriteString("Tools:\n")
	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", t.Name, t.Description))
	}
	return strings.TrimSpace(sb.String())
}

// buildToolResultPrompt formats a tool result so gemma:2b can use it.
func buildToolResultPrompt(toolName, result string) string {
	return fmt.Sprintf("Tool '%s' returned:\n%s\n\nNow provide your final answer.", toolName, result)
}

// ── Parser ────────────────────────────────────────────────────────────────────

var (
	// <tool_call>...<name>bash</name>...<input>{"command":"ls"}</input>...</tool_call>
	// Permissive: allows extra text between tags (model imperfections)
	reXMLToolCall = regexp.MustCompile(`(?s)<tool_call>.*?<name>([^<]+)</name>.*?<input>(.*?)</input>.*?</tool_call>`)

	// <tool_call>\nbash\n{...}\n</tool_call> — bare format (no XML sub-tags)
	reXMLToolCallBare = regexp.MustCompile(`(?s)<tool_call>\s*(\w+)\s*\n([\s\S]*?)\s*</tool_call>`)

	// ```bash\ncommand\n``` — natural code blocks treated as bash calls
	reBashBlock = regexp.MustCompile("(?s)```(?:bash|sh|shell|zsh)\\s*\n(.*?)```")

	// ```python\n...\n``` — python code blocks
	rePythonBlock = regexp.MustCompile("(?s)```(?:python|py)\\s*\n(.*?)```")

	// TOOL_CALL: bash\nCOMMAND: ls (fish-style fallback)
	reFishStyle = regexp.MustCompile(`(?i)TOOL[_\s]CALL:\s*(\w+)\s*\n(?:COMMAND|INPUT|ARGS?):\s*(.+)`)

	// Direct write_file pattern: "Write to file X:\n```\ncontent\n```"
	reWriteFile = regexp.MustCompile(`(?i)(?:write|create|save).*?['"` + "`" + `]([^'` + "`" + `"]+)['"` + "`" + `].*?\n` + "```" + `[a-z]*\n(.*?)` + "```")
)

// parseToolCall attempts to extract a tool call from gemma:2b's text output.
// Returns nil if no tool call is found (plain text answer).
func parseToolCall(text string) *toolCallResult {
	text = strings.TrimSpace(text)

	// 1. XML tags — highest fidelity (model followed the prompt exactly)
	if m := reXMLToolCall.FindStringSubmatch(text); len(m) == 3 {
		name := strings.TrimSpace(m[1])
		inputStr := strings.TrimSpace(m[2])
		// If the model wrote "TOOL_NAME" literally, try to infer from bare name
		if name == "TOOL_NAME" || name == "TOOL" {
			// Try bare pattern: <tool_call>\nbash\n...
			if m2 := reXMLToolCallBare.FindStringSubmatch(text); len(m2) == 3 {
				name = strings.TrimSpace(m2[1])
			} else {
				name = "bash" // default to bash
			}
		}
		input, err := normalizeInput(name, inputStr)
		if err == nil {
			return &toolCallResult{Name: name, Input: input, ID: newToolID()}
		}
	}

	// 2. Bare name pattern: <tool_call>\nbash\n{json}\n</tool_call>
	if m := reXMLToolCallBare.FindStringSubmatch(text); len(m) == 3 {
		name := strings.TrimSpace(m[1])
		// Skip if it looks like it contains nested XML (handled by pattern 1)
		if !strings.Contains(m[2], "<name>") {
			inputStr := strings.TrimSpace(m[2])
			input, err := normalizeInput(name, inputStr)
			if err == nil {
				return &toolCallResult{Name: name, Input: input, ID: newToolID()}
			}
		}
	}

	// 3. Bash/shell code block → bash tool call
	if m := reBashBlock.FindStringSubmatch(text); len(m) == 2 {
		cmd := strings.TrimSpace(m[1])
		if cmd != "" {
			input, _ := json.Marshal(map[string]string{"command": cmd})
			return &toolCallResult{Name: "bash", Input: input, ID: newToolID()}
		}
	}

	// 4. Python code block → bash tool call (run with python3)
	if m := rePythonBlock.FindStringSubmatch(text); len(m) == 2 {
		code := strings.TrimSpace(m[1])
		if code != "" {
			cmd := "python3 -c " + shellQuote(code)
			input, _ := json.Marshal(map[string]string{"command": cmd})
			return &toolCallResult{Name: "bash", Input: input, ID: newToolID()}
		}
	}

	// 5. Fish-style: TOOL_CALL: bash / COMMAND: ls
	if m := reFishStyle.FindStringSubmatch(text); len(m) == 3 {
		name := strings.ToLower(strings.TrimSpace(m[1]))
		inputStr := strings.TrimSpace(m[2])
		input, err := normalizeInput(name, inputStr)
		if err == nil {
			return &toolCallResult{Name: name, Input: input, ID: newToolID()}
		}
	}

	// 6. Write-file pattern: natural language description + code block
	if m := reWriteFile.FindStringSubmatch(text); len(m) == 3 {
		filename := strings.TrimSpace(m[1])
		content := m[2]
		cmd := fmt.Sprintf("cat > %s << 'HEREDOC'\n%s\nHEREDOC", filename, content)
		input, _ := json.Marshal(map[string]string{"command": cmd})
		return &toolCallResult{Name: "bash", Input: input, ID: newToolID()}
	}

	return nil
}

// normalizeInput converts a raw input string into JSON bytes for a given tool.
// Handles three cases: already-JSON, plain command string, bare text.
func normalizeInput(toolName, raw string) (json.RawMessage, error) {
	raw = strings.TrimSpace(raw)

	// Already valid JSON object?
	if strings.HasPrefix(raw, "{") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			return json.RawMessage(raw), nil
		}
	}

	// Tool-specific default field
	field := toolInputField(toolName)
	input, err := json.Marshal(map[string]string{field: raw})
	if err != nil {
		return nil, err
	}
	return input, nil
}

// toolInputField returns the primary input field name for known tools.
func toolInputField(name string) string {
	switch name {
	case "bash", "shell", "sh":
		return "command"
	case "read_file":
		return "path"
	case "write_file", "create_file":
		return "path"
	case "str_replace_editor", "editor":
		return "command"
	default:
		return "input"
	}
}

// ── Content block extraction ──────────────────────────────────────────────────

// extractToolResults scans a message content array for tool_result blocks
// and returns them as plain text for injection into the model context.
func extractToolResults(raw json.RawMessage) string {
	var blocks []struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   any    `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		switch v := b.Content.(type) {
		case string:
			parts = append(parts, v)
		case []interface{}:
			for _, item := range v {
				if m, ok := item.(map[string]interface{}); ok {
					if t, ok := m["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

// ── Utilities ─────────────────────────────────────────────────────────────────

var (
	toolIDmu      sync.Mutex
	toolIDCounter int64
)

func newToolID() string {
	toolIDmu.Lock()
	toolIDCounter++
	id := toolIDCounter
	toolIDmu.Unlock()
	return fmt.Sprintf("toolu_%016x", id)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
