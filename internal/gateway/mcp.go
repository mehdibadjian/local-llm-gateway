package gateway

import (
	"github.com/caw/wrapper/internal/mcp"
	"github.com/gofiber/fiber/v2"
)

// MCPHandler returns a Fiber handler for the MCP JSON-RPC 2.0 endpoint at POST /mcp.
// It delegates to the given mcp.Server for request processing.
func MCPHandler(srv *mcp.Server) fiber.Handler {
	return func(c *fiber.Ctx) error {
		result := srv.Handle(c.Context(), c.Body())
		c.Set("Content-Type", "application/json")
		return c.Send(result)
	}
}
