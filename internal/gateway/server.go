package gateway

import (
	"os"
	"strconv"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/memory"
	"github.com/caw/wrapper/internal/security"
	"github.com/caw/wrapper/internal/tools"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// Server is the top-level CAW API gateway.
type Server struct {
	app     *fiber.App
	api     fiber.Router // /v1 group (stored so callers can add routes)
	pool    *WorkerPool
	backend adapter.InferenceBackend
	session *memory.SessionStore
	rdb     *redis.Client
}

// Handler holds the dependencies shared by all route handlers.
type Handler struct {
	pool    *WorkerPool
	backend adapter.InferenceBackend
	session *memory.SessionStore
	rdb     *redis.Client
}

// NewServer wires the Fiber application, middleware chain, and all route handlers.
//
// Middleware order (per route group):
//  1. Bearer token auth  (skipped for /healthz, /readyz)
//  2. Distributed rate limiter
//
// Worker pool size defaults to 10 and can be overridden via WORKER_POOL_SIZE.
func NewServer(backend adapter.InferenceBackend, rdb *redis.Client, session *memory.SessionStore) *Server {
	poolSize, _ := strconv.Atoi(os.Getenv("WORKER_POOL_SIZE"))
	if poolSize <= 0 {
		poolSize = 10
	}

	app := fiber.New(fiber.Config{AppName: "CAW"})
	pool := NewWorkerPool(poolSize)
	rateLimiter := security.NewRateLimiter(rdb)

	// Unauthenticated probes (no external deps for /healthz).
	app.Get("/healthz", HealthzHandler)
	app.Get("/readyz", ReadyzHandler(backend))

	h := &Handler{pool: pool, backend: backend, session: session, rdb: rdb}

	// All /v1 routes require auth + rate limiting.
	api := app.Group("/v1", security.NewAuthMiddleware(), rateLimiter.Middleware())
	api.Post("/chat/completions", h.ChatHandler)
	api.Post("/messages", h.MessagesHandler) // Anthropic Messages API — used by Claude Code CLI
	api.Post("/documents", h.EnqueueDocument)
	api.Get("/documents/:id/status", h.DocumentStatus)
	api.Delete("/sessions/:id", h.DeleteSession)

	return &Server{
		app:     app,
		api:     api,
		pool:    pool,
		backend: backend,
		session: session,
		rdb:     rdb,
	}
}

// RegisterToolRoutes mounts GET /v1/tools and POST /v1/tools using the provided handler.
// Call this after NewServer, before Listen.
func (s *Server) RegisterToolRoutes(th *tools.ToolHandler) {
	s.api.Get("/tools", th.ListTools)
	s.api.Post("/tools", th.RegisterTool)
}

// Listen starts the HTTP server on the given address (e.g. ":8080").
func (s *Server) Listen(addr string) error {
	return s.app.Listen(addr)
}

// App returns the underlying Fiber application, primarily for use in tests.
func (s *Server) App() *fiber.App {
	return s.app
}

// Pool returns the bounded worker pool, primarily for use in tests.
func (s *Server) Pool() *WorkerPool {
	return s.pool
}
