package main

import (
	"context"
	"log"
	"os"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/gateway"
	"github.com/caw/wrapper/internal/mcp"
	"github.com/caw/wrapper/internal/memory"
	"github.com/caw/wrapper/internal/orchestration"
	"github.com/caw/wrapper/internal/tools"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx := context.Background()

	// ── Redis ─────────────────────────────────────────────────────────────────
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://caw:caw@localhost:5432/caw?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("pgxpool connect: %v", err)
	}
	pgStore := memory.NewPGStore(pool)
	if err := pgStore.Bootstrap(ctx); err != nil {
		log.Fatalf("pg bootstrap: %v", err)
	}

	// ── Session store ─────────────────────────────────────────────────────────
	session := memory.NewSessionStore(rdb)

	// ── Inference backend ─────────────────────────────────────────────────────
	backend, err := adapter.NewBackend()
	if err != nil {
		log.Fatalf("backend init: %v", err)
	}

	// ── Tool registry + dispatcher (with self-learning) ───────────────────────
	toolStore := &pgToolStoreAdapter{pg: pgStore}
	registry := tools.NewRegistry(toolStore)
	sandbox := tools.NewSandbox(tools.SandboxConfig{MemLimitMB: 256, CPUShares: 512, TimeoutSec: 30})
	dispatcher := tools.NewDispatcherWithLearn(registry, sandbox, rdb)

	// ── Web augmenter — injects live search into every inference request ───────
	webExec := tools.NewWebSearchExecutor(rdb)
	webSearcher := orchestration.NewToolsWebSearcher(webExec)
	webAugmenter := orchestration.NewWebAugmenter(webSearcher)

	// ── MCP server ────────────────────────────────────────────────────────────
	mcpSrc := mcp.NewDispatcherSource(registry, dispatcher)
	mcpSrv := mcp.NewServer(mcpSrc)

	// ── HTTP gateway ──────────────────────────────────────────────────────────
	srv := gateway.NewServer(backend, rdb, session)
	srv.RegisterWebAugmenter(webAugmenter)
	srv.RegisterToolRoutes(tools.NewToolHandler(registry))
	srv.RegisterMCPRoute(gateway.MCPHandler(mcpSrv))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("CAW listening on :%s (MCP at POST /mcp, tools at /v1/tools)", port)
	log.Fatal(srv.Listen(":" + port))
}
