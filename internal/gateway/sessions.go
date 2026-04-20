package gateway

import "github.com/gofiber/fiber/v2"

// DeleteSession removes all Redis keys for a session and returns HTTP 204.
func (h *Handler) DeleteSession(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.session.DeleteSession(c.Context(), id); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(errResp(err.Error()))
	}
	return c.SendStatus(fiber.StatusNoContent)
}
