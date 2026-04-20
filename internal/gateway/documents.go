package gateway

import (
	"time"

	"github.com/caw/wrapper/internal/ingest"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// EnqueueDocument validates and enqueues a document ingest job.
// Returns HTTP 202 with the assigned document_id on success.
func (h *Handler) EnqueueDocument(c *fiber.Ctx) error {
	var req DocumentRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(errResp("invalid request body"))
	}
	if req.Domain == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(errResp("domain is required"))
	}
	if req.Content == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(errResp("content is required"))
	}

	docID := uuid.NewString()
	job := ingest.IngestJob{
		DocumentID: docID,
		Domain:     req.Domain,
		Content:    req.Content,
		Title:      req.Title,
		EnqueuedAt: time.Now(),
	}

	if err := ingest.Enqueue(c.Context(), h.rdb, job); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(errResp(err.Error()))
	}

	return c.Status(fiber.StatusAccepted).JSON(DocumentResponse{
		DocumentID: docID,
		Status:     "pending",
	})
}

// DocumentStatus returns the current ingest status for the given document ID.
func (h *Handler) DocumentStatus(c *fiber.Ctx) error {
	id := c.Params("id")
	status, err := ingest.GetStatus(c.Context(), h.rdb, id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(errResp(err.Error()))
	}
	return c.JSON(status)
}
