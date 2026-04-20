package tools

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
)

// ToolHandler handles HTTP requests for the Tool Registry API.
type ToolHandler struct {
	registry *Registry
}

// NewToolHandler constructs a ToolHandler.
func NewToolHandler(registry *Registry) *ToolHandler {
	return &ToolHandler{registry: registry}
}

// ListTools handles GET /v1/tools — returns all enabled tools.
func (h *ToolHandler) ListTools(c *fiber.Ctx) error {
	tools, err := h.registry.List(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(errorResponse(err.Error()))
	}
	return c.JSON(fiber.Map{"tools": tools, "count": len(tools)})
}

// RegisterTool handles POST /v1/tools — validates and persists a new tool.
func (h *ToolHandler) RegisterTool(c *fiber.Ctx) error {
	var tool Tool
	if err := c.BodyParser(&tool); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(errorResponse("invalid request body"))
	}
	if tool.Name == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(errorResponse("name is required"))
	}
	if !ValidExecutorTypes[tool.ExecutorType] {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(
			errorResponse("invalid executor_type: must be builtin, subprocess, or http"),
		)
	}
	if tool.InputSchema == nil {
		tool.InputSchema = json.RawMessage(`{}`)
	}

	created, err := h.registry.Register(c.Context(), tool)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(errorResponse(err.Error()))
	}
	return c.Status(fiber.StatusCreated).JSON(created)
}

func errorResponse(msg string) fiber.Map {
	return fiber.Map{"error": fiber.Map{"message": msg, "type": "tool_error"}}
}
