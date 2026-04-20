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
	Model     string                 `json:"model"`
	MaxTokens int                    `json:"max_tokens"`
	Messages  []AnthropicMessage     `json:"messages"`
	System    AnthropicMessageContent `json:"system"`
	Stream    bool                   `json:"stream,omitempty"`
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
	// Try array of content blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &blocks); err != nil {
		return err
	}
	var parts []string
	for _, blk := range blocks {
		if blk.Type == "text" && blk.Text != "" {
			parts = append(parts, blk.Text)
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
	Type string `json:"type"`
	Text string `json:"text"`
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
	msgs := make([]adapter.Message, 0, len(req.Messages)+1)
	if req.System.Text != "" {
		msgs = append(msgs, adapter.Message{Role: "system", Content: req.System.Text})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, adapter.Message{Role: m.Role, Content: m.Content.Text})
	}

	if req.Stream {
		return h.messagesStream(c, model, msgs, req.MaxTokens)
	}
	return h.messagesComplete(c, model, msgs, req.MaxTokens)
}

// messagesComplete handles non-streaming Anthropic requests.
func (h *Handler) messagesComplete(c *fiber.Ctx, model string, msgs []adapter.Message, maxTokens int) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(anthropicError("rate_limit_error", "server busy"))
	}
	defer h.pool.Release()

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
func (h *Handler) messagesStream(c *fiber.Ctx, model string, msgs []adapter.Message, maxTokens int) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(anthropicError("rate_limit_error", "server busy"))
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

		// message_delta
		writeSSE(w, "message_delta", anthropicMessageDelta{
			Type:  "message_delta",
			Delta: anthropicMessageDeltaVal{StopReason: "end_turn"},
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
