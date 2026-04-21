package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/gofiber/fiber/v2"
)

// ChatHandler dispatches to the streaming or non-streaming path based on
// the stream field in the request body.
func (h *Handler) ChatHandler(c *fiber.Ctx) error {
	var req ChatCompletionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(errResp("invalid request body"))
	}
	if len(req.Messages) == 0 {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(errResp("messages field is required"))
	}
	if req.Model == "" {
		return c.Status(fiber.StatusBadRequest).JSON(errResp("model is required"))
	}

	if req.Stream {
		return h.chatStream(c, &req)
	}
	return h.chatComplete(c, &req)
}

// chatComplete handles non-streaming inference and returns a single JSON body.
func (h *Handler) chatComplete(c *fiber.Ctx, req *ChatCompletionRequest) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(errResp("server busy"))
	}
	defer h.pool.Release()

	// ── Semantic cache lookup ────────────────────────────────────────────────
	var queryEmb []float32
	if h.embedClient != nil && h.semCache != nil {
		if text := lastUserMessage(req.Messages); text != "" {
			if emb, err := h.embedClient.Embed(c.Context(), text); err == nil {
				queryEmb = emb
				if cached, ok := h.semCache.Lookup(emb, 0.95); ok {
					c.Set("X-CAW-Cache-Hit", "semantic")
					c.Set("Content-Type", "application/json")
					return c.SendString(cached)
				}
			}
		}
	}

	msgs := req.Messages

	if req.SessionID != "" && h.historyMgr != nil {
		history, err := h.historyMgr.LoadAndTrim(c.Context(), req.SessionID, h.backend)
		if err == nil && len(history) > 0 {
			adapted := make([]adapter.Message, len(history))
			for i, m := range history {
				adapted[i] = adapter.Message{Role: m.Role, Content: m.Content}
			}
			msgs = append(adapted, msgs...)
		}
	}

	if h.augmenter != nil {
		augmented, searched, _ := h.augmenter.Augment(c.Context(), msgs)
		if searched {
			msgs = augmented
			c.Set("X-CAW-Web-Searched", "true")
		}
	}

	genReq := &adapter.GenerateRequest{
		Model:       req.Model,
		Messages:    msgs,
		Stream:      false,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	resp, err := h.backend.Generate(c.Context(), genReq)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(errResp(err.Error()))
	}

	chatResp := toChatResponse(resp)

	// ── Semantic cache store ─────────────────────────────────────────────────
	if queryEmb != nil {
		if data, merr := json.Marshal(chatResp); merr == nil {
			h.semCache.Store(queryEmb, string(data))
		}
	}

	return c.JSON(chatResp)
}

// lastUserMessage returns the content of the last message with role "user",
// falling back to the last message in the slice if none has that role.
func lastUserMessage(msgs []adapter.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content
	}
	return ""
}

// chatStream handles streaming inference using SSE (Server-Sent Events).
// The worker pool slot is released inside the stream writer, after all tokens
// have been flushed or an error occurs.
func (h *Handler) chatStream(c *fiber.Ctx, req *ChatCompletionRequest) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(errResp("server busy"))
	}

	msgs := req.Messages
	if h.augmenter != nil {
		augmented, searched, _ := h.augmenter.Augment(c.Context(), msgs)
		if searched {
			msgs = augmented
			c.Set("X-CAW-Web-Searched", "true")
		}
	}

	genReq := &adapter.GenerateRequest{
		Model:       req.Model,
		Messages:    msgs,
		Stream:      true,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer h.pool.Release()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ch, err := h.backend.Stream(ctx, genReq)
		if err != nil {
			fmt.Fprintf(w, "data: {\"error\": \"%s\"}\n\n", err.Error())
			w.Flush() //nolint:errcheck
			return
		}

		for resp := range ch {
			data, _ := json.Marshal(toStreamChunk(resp))
			fmt.Fprintf(w, "data: %s\n\n", data)
			if err := w.Flush(); err != nil {
				// Client disconnected — context cancel stops the backend.
				cancel()
				return
			}
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		w.Flush() //nolint:errcheck
	})
	return nil
}

// toChatResponse converts an adapter response to the OpenAI chat completion schema.
func toChatResponse(resp *adapter.GenerateResponse) *ChatCompletionResponse {
	choices := make([]ChatCompletionChoice, len(resp.Choices))
	for i, ch := range resp.Choices {
		c := ch // avoid loop-variable capture
		choices[i] = ChatCompletionChoice{
			Index:        c.Index,
			Message:      &adapter.Message{Role: c.Message.Role, Content: c.Message.Content},
			FinishReason: &c.FinishReason,
		}
	}
	return &ChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
		Choices: choices,
	}
}

// toStreamChunk converts a streaming adapter response to a chat.completion.chunk.
func toStreamChunk(resp *adapter.GenerateResponse) *ChatCompletionResponse {
	choices := make([]ChatCompletionChoice, len(resp.Choices))
	for i, ch := range resp.Choices {
		c := ch
		var finishReason *string
		if c.FinishReason != "" {
			finishReason = &c.FinishReason
		}
		choices[i] = ChatCompletionChoice{
			Index:        c.Index,
			Delta:        c.Delta,
			FinishReason: finishReason,
		}
	}
	return &ChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   resp.Model,
		Choices: choices,
	}
}

// errResp builds an OpenAI-compatible error response map.
func errResp(msg string) fiber.Map {
	return fiber.Map{
		"error": fiber.Map{
			"message": msg,
			"type":    "invalid_request_error",
		},
	}
}
