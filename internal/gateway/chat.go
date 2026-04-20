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

	genReq := &adapter.GenerateRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Stream:      false,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	resp, err := h.backend.Generate(c.Context(), genReq)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(errResp(err.Error()))
	}
	return c.JSON(toChatResponse(resp))
}

// chatStream handles streaming inference using SSE (Server-Sent Events).
// The worker pool slot is released inside the stream writer, after all tokens
// have been flushed or an error occurs.
func (h *Handler) chatStream(c *fiber.Ctx, req *ChatCompletionRequest) error {
	if !h.pool.Acquire() {
		return c.Status(fiber.StatusTooManyRequests).JSON(errResp("server busy"))
	}

	genReq := &adapter.GenerateRequest{
		Model:       req.Model,
		Messages:    req.Messages,
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
