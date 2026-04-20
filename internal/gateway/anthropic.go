package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/gofiber/fiber/v2"
)

// ── Anthropic Messages API types ─────────────────────────────────────────────

// AnthropicRequest mirrors the Anthropic POST /v1/messages request schema.
type AnthropicRequest struct {
	Model       string                 `json:"model"`
	MaxTokens   int                    `json:"max_tokens"`
	Messages    []AnthropicMessage     `json:"messages"`
	System      AnthropicMessageContent `json:"system"`
	Stream      bool                   `json:"stream,omitempty"`
	Tools       []AnthropicTool        `json:"tools,omitempty"`
	ToolChoice  *AnthropicToolChoice   `json:"tool_choice,omitempty"`
}

// AnthropicMessage is a single message in the Anthropic Messages API.
// The `content` field can be either a plain string or an array of content
// blocks — the Anthropic SDK sends the array form; we normalise to string.
type AnthropicMessage struct {
	Role    string                `json:"role"`
	Content AnthropicMessageContent `json:"content"`
}

// AnthropicMessageContent unmarshals either a plain JSON string or an array of
// {"type":"text","text":"..."} blocks into a single string value.
type AnthropicMessageContent struct {
	Text string
}

func (a *AnthropicMessageContent) UnmarshalJSON(b []byte) error {
	// Try plain string first.
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		a.Text = s
		return nil
	}
	// Try array of content blocks (text, tool_use, tool_result, image…).
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		// tool_use
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		// tool_result
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &blocks); err != nil {
		return err
	}
	var parts []string
	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			if blk.Text != "" {
				parts = append(parts, blk.Text)
			}
		case "tool_use":
			// Model is being asked to continue after a tool call — include context.
			if blk.Input != nil && string(blk.Input) != "null" {
				parts = append(parts, fmt.Sprintf("[tool_use: %s %s]", blk.Name, blk.Input))
			}
		case "tool_result":
			// Claude Code is sending back the result of a tool execution.
			result := extractToolResults(b)
			if result != "" {
				parts = append(parts, result)
			}
			return nil // extractToolResults already handles the whole array
		}
	}
	a.Text = strings.Join(parts, "\n")
	return nil
}

// AnthropicResponse mirrors the Anthropic non-streaming response shape.
type AnthropicResponse struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Role         string             `json:"role"`
	Content      []AnthropicContent `json:"content"`
	Model        string             `json:"model"`
	StopReason   string             `json:"stop_reason"`
	StopSequence *string            `json:"stop_sequence"`
	Usage        AnthropicUsage     `json:"usage"`
}

type AnthropicContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	// tool_use fields (returned to Claude Code CLI)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ── SSE event structs for streaming ──────────────────────────────────────────

type anthropicSSE struct {
	Type string `json:"type"`
}

type anthropicMessageStart struct {
	Type    string             `json:"type"`
	Message AnthropicResponse  `json:"message"`
}

type anthropicContentBlockStart struct {
	Type         string           `json:"type"`
	Index        int              `json:"index"`
	ContentBlock AnthropicContent `json:"content_block"`
}

type anthropicContentBlockDelta struct {
	Type  string              `json:"type"`
	Index int                 `json:"index"`
	Delta anthropicTextDelta  `json:"delta"`
}

type anthropicTextDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicContentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type anthropicMessageDelta struct {
	Type  string                   `json:"type"`
	Delta anthropicMessageDeltaVal `json:"delta"`
	Usage AnthropicUsage           `json:"usage"`
}

type anthropicMessageDeltaVal struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

// ── Handler ───────────────────────────────────────────────────────────────────

// MessagesHandler implements POST /v1/messages — the Anthropic Messages API.
// Translates the Anthropic request into CAW's internal pipeline, then formats
// the response back into Anthropic shape so that Claude Code CLI works unchanged.
func (h *Handler) MessagesHandler(c *fiber.Ctx) error {
	var req AnthropicRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(anthropicError("invalid_request_error", "invalid request body"))
	}
	if len(req.Messages) == 0 {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(anthropicError("invalid_request_error", "messages is required"))
	}

	// CAW ignores the Anthropic model name and routes through the configured
	// backend (gemma:2b / llama3.2 etc.). Override can be set via X-CAW-Model
	// header or CAW_DEFAULT_MODEL env var.
	model := c.Get("X-CAW-Model")
	if model == "" {
		model = req.Model
	}
	// If the caller sent an Anthropic model name (e.g. claude-3-5-sonnet-*),
	// substitute the locally-configured model so Ollama doesn't 404.
	if strings.HasPrefix(model, "claude-") || model == "" {
		model = localModel()
	}

	// Convert Anthropic messages → adapter.Message slice.
	// Prepend system prompt as a system-role message if present.
	msgs := make([]adapter.Message, 0, len(req.Messages)+2)
	if req.System.Text != "" {
		msgs = append(msgs, adapter.Message{Role: "system", Content: req.System.Text})
	}

	// Virtual Tool Calling: if the request includes tool definitions, inject a
	// plain-text system prompt so gemma:2b knows how to emit tool calls.
	if len(req.Tools) > 0 {
		toolPrompt := buildToolSystemPrompt(req.Tools)
		msgs = append(msgs, adapter.Message{Role: "system", Content: toolPrompt})
	}

	for _, m := range req.Messages {
		msgs = append(msgs, adapter.Message{Role: m.Role, Content: m.Content.Text})
	}

	// Agentic mode: when tools are present, run the full server-side loop.
	// Works for both streaming and non-streaming — agent loop runs internally,
	// then the final answer is returned (streamed or as a single response).
	if len(req.Tools) > 0 {
		if req.Stream {
			return h.messagesAgentStream(c, model, msgs, req.MaxTokens)
		}
		return h.messagesAgent(c, model, msgs, req.MaxTokens)
	}
	if req.Stream {
		return h.messagesStream(c, model, msgs, req.MaxTokens, false)
	}
	return h.messagesComplete(c, model, msgs, req.MaxTokens, false)
}

// messagesAgent runs a full server-side agentic loop for requests with tools.
// CAW executes bash/write_file/read_file calls internally and returns the final
// answer once gemma:2b stops emitting tool calls.
func (h *Handler) messagesAgent(c *fiber.Ctx, model string, msgs []adapter.Message, maxTokens int) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(anthropicError("rate_limit_error", "server busy"))
	}
	defer h.pool.Release()

	// Web augmentation before the loop.
	if h.augmenter != nil {
		augmented, searched, _ := h.augmenter.Augment(c.Context(), msgs)
		if searched {
			msgs = augmented
			c.Set("X-CAW-Web-Searched", "true")
		}
	}

	// Use a per-request working directory so file operations don't pollute cwd.
	workdir := agentWorkdir()
	// Run the full agentic loop (up to maxAgentSteps tool executions).
	// Context gets 5 min total for long-running agent tasks.
	agentCtx, cancel := context.WithTimeout(c.Context(), 5*time.Minute)
	defer cancel()

	finalAnswer, steps, err := RunAgentLoop(agentCtx, h.backend, model, msgs, maxTokens, workdir)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(anthropicError("api_error", err.Error()))
	}

	// Summarize steps taken in a header for debugging.
	if len(steps) > 0 {
		c.Set("X-CAW-Agent-Steps", fmt.Sprintf("%d", len(steps)))
	}

	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	return c.JSON(AnthropicResponse{
		ID:         msgID,
		Type:       "message",
		Role:       "assistant",
		Content:    []AnthropicContent{{Type: "text", Text: finalAnswer}},
		Model:      model,
		StopReason: "end_turn",
		Usage: AnthropicUsage{
			InputTokens:  estimateTokens(msgs),
			OutputTokens: len([]rune(finalAnswer)) / 4,
		},
	})
}

// messagesAgentStream runs the full agent loop server-side, then streams the
// final answer using SSE so Claude Code CLI renders it progressively.
func (h *Handler) messagesAgentStream(c *fiber.Ctx, model string, msgs []adapter.Message, maxTokens int) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(anthropicError("rate_limit_error", "server busy"))
	}

	if h.augmenter != nil {
		augmented, searched, _ := h.augmenter.Augment(c.Context(), msgs)
		if searched {
			msgs = augmented
			c.Set("X-CAW-Web-Searched", "true")
		}
	}

	workdir := agentWorkdir()
	// Run loop synchronously first (before SSE writer — needs real context).
	agentCtx, agentCancel := context.WithTimeout(c.Context(), 5*time.Minute)
	finalAnswer, steps, runErr := RunAgentLoop(agentCtx, h.backend, model, msgs, maxTokens, workdir)
	agentCancel()

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	if len(steps) > 0 {
		c.Set("X-CAW-Agent-Steps", fmt.Sprintf("%d", len(steps)))
	}

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer h.pool.Release()

		msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

		if runErr != nil {
			writeSSE(w, "error", fiber.Map{"type": "error", "error": fiber.Map{
				"type": "api_error", "message": runErr.Error(),
			}})
			w.Flush() //nolint:errcheck
			return
		}

		// Build a step summary prefix so the user can see what CAW did.
		var prefix strings.Builder
		if len(steps) > 0 {
			prefix.WriteString(fmt.Sprintf("*(CAW executed %d step(s) server-side)*\n\n", len(steps)))
			for _, s := range steps {
				prefix.WriteString(fmt.Sprintf("**Step %d — %s**\n", s.Step, s.Tool))
				if s.Output != "" {
					lines := strings.Split(strings.TrimSpace(s.Output), "\n")
					if len(lines) > 5 {
						lines = append(lines[:5], "…")
					}
					prefix.WriteString("```\n" + strings.Join(lines, "\n") + "\n```\n")
				}
				if s.Error != "" {
					prefix.WriteString(fmt.Sprintf("⚠️  %s\n", s.Error))
				}
			}
			prefix.WriteString("\n---\n\n")
		}

		fullText := prefix.String() + finalAnswer

		writeSSE(w, "message_start", anthropicMessageStart{
			Type: "message_start",
			Message: AnthropicResponse{
				ID: msgID, Type: "message", Role: "assistant",
				Content: []AnthropicContent{}, Model: model,
				Usage: AnthropicUsage{InputTokens: estimateTokens(msgs), OutputTokens: 1},
			},
		})
		writeSSE(w, "content_block_start", anthropicContentBlockStart{
			Type: "content_block_start", Index: 0,
			ContentBlock: AnthropicContent{Type: "text", Text: ""},
		})
		writeSSE(w, "ping", anthropicSSE{Type: "ping"})
		w.Flush() //nolint:errcheck

		// Stream the answer in ~40-char chunks for live rendering.
		runes := []rune(fullText)
		chunkSize := 40
		outputTokens := 0
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			token := string(runes[i:end])
			outputTokens += len(runes[i:end]) / 4
			writeSSE(w, "content_block_delta", anthropicContentBlockDelta{
				Type: "content_block_delta", Index: 0,
				Delta: anthropicTextDelta{Type: "text_delta", Text: token},
			})
			w.Flush() //nolint:errcheck
		}

		writeSSE(w, "content_block_stop", anthropicContentBlockStop{Type: "content_block_stop", Index: 0})
		writeSSE(w, "message_delta", anthropicMessageDelta{
			Type:  "message_delta",
			Delta: anthropicMessageDeltaVal{StopReason: "end_turn"},
			Usage: AnthropicUsage{OutputTokens: outputTokens},
		})
		writeSSE(w, "message_stop", anthropicSSE{Type: "message_stop"})
		w.Flush() //nolint:errcheck
	})
	return nil
}


// messagesComplete handles non-streaming Anthropic requests (no tools).
func (h *Handler) messagesComplete(c *fiber.Ctx, model string, msgs []adapter.Message, maxTokens int, hasTools bool) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(anthropicError("rate_limit_error", "server busy"))
	}
	defer h.pool.Release()

	// Web augmentation: inject live search results when the query contains signals.
	if h.augmenter != nil {
		augmented, searched, _ := h.augmenter.Augment(c.Context(), msgs)
		if searched {
			msgs = augmented
			c.Set("X-CAW-Web-Searched", "true")
		}
	}

	genReq := &adapter.GenerateRequest{
		Model:     model,
		Messages:  msgs,
		Stream:    false,
		MaxTokens: maxTokens,
	}

	resp, err := h.backend.Generate(c.Context(), genReq)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(anthropicError("api_error", err.Error()))
	}

	text := ""
	if len(resp.Choices) > 0 {
		text = resp.Choices[0].Message.Content
	}

	// Virtual tool calling: parse gemma:2b's text for tool call intent.
	if hasTools {
		if tc := parseToolCall(text); tc != nil {
			return c.JSON(AnthropicResponse{
				ID:    resp.ID,
				Type:  "message",
				Role:  "assistant",
				Content: []AnthropicContent{{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: tc.Input,
				}},
				Model:      model,
				StopReason: "tool_use",
				Usage: AnthropicUsage{
					InputTokens:  estimateTokens(msgs),
					OutputTokens: estimateTokens(nil) + len([]rune(text))/4,
				},
			})
		}
	}

	return c.JSON(AnthropicResponse{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    []AnthropicContent{{Type: "text", Text: text}},
		Model:      model,
		StopReason: "end_turn",
		Usage: AnthropicUsage{
			InputTokens:  estimateTokens(msgs),
			OutputTokens: estimateTokens(nil) + len([]rune(text))/4,
		},
	})
}

// messagesStream handles streaming Anthropic requests, emitting SSE events in
// the exact sequence Claude Code CLI expects:
//
//	message_start → content_block_start → ping → content_block_delta(s) →
//	content_block_stop → message_delta → message_stop
func (h *Handler) messagesStream(c *fiber.Ctx, model string, msgs []adapter.Message, maxTokens int, hasTools bool) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(anthropicError("rate_limit_error", "server busy"))
	}

	// Web augmentation runs before the stream writer (needs a real context).
	webSearched := false
	if h.augmenter != nil {
		augmented, searched, _ := h.augmenter.Augment(c.Context(), msgs)
		if searched {
			msgs = augmented
			webSearched = true
		}
	}

	genReq := &adapter.GenerateRequest{
		Model:     model,
		Messages:  msgs,
		Stream:    true,
		MaxTokens: maxTokens,
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	if webSearched {
		c.Set("X-CAW-Web-Searched", "true")
	}

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer h.pool.Release()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

		// message_start
		writeSSE(w, "message_start", anthropicMessageStart{
			Type: "message_start",
			Message: AnthropicResponse{
				ID:         msgID,
				Type:       "message",
				Role:       "assistant",
				Content:    []AnthropicContent{},
				Model:      model,
				StopReason: "",
				Usage:      AnthropicUsage{InputTokens: estimateTokens(msgs), OutputTokens: 1},
			},
		})

		// content_block_start
		writeSSE(w, "content_block_start", anthropicContentBlockStart{
			Type:         "content_block_start",
			Index:        0,
			ContentBlock: AnthropicContent{Type: "text", Text: ""},
		})

		// ping
		writeSSE(w, "ping", anthropicSSE{Type: "ping"})
		w.Flush() //nolint:errcheck

		ch, err := h.backend.Stream(ctx, genReq)
		if err != nil {
			writeSSE(w, "error", fiber.Map{"type": "error", "error": fiber.Map{"type": "api_error", "message": err.Error()}})
			w.Flush() //nolint:errcheck
			return
		}

		outputTokens := 0
		var fullText strings.Builder
		for resp := range ch {
			if len(resp.Choices) == 0 {
				continue
			}
			token := ""
			if resp.Choices[0].Delta != nil {
				token = resp.Choices[0].Delta.Content
			}
			if token == "" {
				continue
			}
			outputTokens += len([]rune(token)) / 4
			fullText.WriteString(token)
			writeSSE(w, "content_block_delta", anthropicContentBlockDelta{
				Type:  "content_block_delta",
				Index: 0,
				Delta: anthropicTextDelta{Type: "text_delta", Text: token},
			})
			if err := w.Flush(); err != nil {
				cancel()
				return
			}
		}

		// content_block_stop
		writeSSE(w, "content_block_stop", anthropicContentBlockStop{Type: "content_block_stop", Index: 0})

		// Virtual tool calling: if tools were active and the model emitted a tool
		// call pattern, signal tool_use stop reason so Claude Code CLI picks it up.
		stopReason := "end_turn"
		if hasTools {
			if tc := parseToolCall(fullText.String()); tc != nil {
				stopReason = "tool_use"
				// Emit the tool_use content block separately via message_delta
				_ = tc // Claude Code reads stop_reason + content from message_start/delta
			}
		}

		// message_delta
		writeSSE(w, "message_delta", anthropicMessageDelta{
			Type:  "message_delta",
			Delta: anthropicMessageDeltaVal{StopReason: stopReason},
			Usage: AnthropicUsage{OutputTokens: outputTokens},
		})

		// message_stop
		writeSSE(w, "message_stop", anthropicSSE{Type: "message_stop"})
		w.Flush() //nolint:errcheck
	})
	return nil
}

// writeSSE writes a single "event: <name>\ndata: <json>\n\n" frame.
func writeSSE(w *bufio.Writer, event string, payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

// estimateTokens produces a rough token count (chars/4) for usage fields.
// Anthropic SDKs use this only for display; accuracy is not critical.
func estimateTokens(msgs []adapter.Message) int {
	total := 0
	for _, m := range msgs {
		total += len([]rune(m.Content)) / 4
	}
	return total
}

// anthropicError returns an Anthropic-format error body.
func anthropicError(errType, msg string) fiber.Map {
	return fiber.Map{
		"type": "error",
		"error": fiber.Map{
			"type":    errType,
			"message": msg,
		},
	}
}

// localModel returns the model name to use for local inference.
// Reads CAW_DEFAULT_MODEL env var; defaults to "gemma:2b".
func localModel() string {
	if m := os.Getenv("CAW_DEFAULT_MODEL"); m != "" {
		return m
	}
	return "gemma:2b"
}

// ── Models list endpoints (probed by Claude Code CLI and OpenAI clients) ──────

// anthropicModelEntry is a single entry in the models list.
type anthropicModelEntry struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// anthropicModelList is the GET /v1/models response.
type anthropicModelList struct {
	Data    []anthropicModelEntry `json:"data"`
	FirstID string                `json:"first_id"`
	LastID  string                `json:"last_id"`
	HasMore bool                  `json:"has_more"`
}

// ModelsHandler handles GET /v1/models — returns a stub list containing the
// local backend model so Claude Code CLI model-validation passes.
func (h *Handler) ModelsHandler(c *fiber.Ctx) error {
	model := localModel()
	entry := anthropicModelEntry{
		ID:          model,
		Type:        "model",
		DisplayName: model,
		CreatedAt:   "2024-01-01T00:00:00Z",
	}
	// Also surface well-known Anthropic aliases so Claude Code CLI doesn't
	// reject its own default model name during pre-flight validation.
	aliases := []string{
		"claude-opus-4-5", "claude-sonnet-4-5", "claude-haiku-4-5",
		"claude-3-5-sonnet-20241022", "claude-3-opus-20240229",
	}
	entries := []anthropicModelEntry{entry}
	for _, a := range aliases {
		entries = append(entries, anthropicModelEntry{
			ID: a, Type: "model", DisplayName: a + " → " + model,
			CreatedAt: "2024-01-01T00:00:00Z",
		})
	}
	return c.JSON(anthropicModelList{
		Data:    entries,
		FirstID: entries[0].ID,
		LastID:  entries[len(entries)-1].ID,
		HasMore: false,
	})
}

// ModelDetailHandler handles GET /v1/models/:model — returns a single model
// entry. Any model name is accepted; all route through the local backend.
func (h *Handler) ModelDetailHandler(c *fiber.Ctx) error {
	requestedModel := c.Params("model")
	return c.JSON(anthropicModelEntry{
		ID:          requestedModel,
		Type:        "model",
		DisplayName: requestedModel + " → " + localModel() + " (via CAW)",
		CreatedAt:   "2024-01-01T00:00:00Z",
	})
}
