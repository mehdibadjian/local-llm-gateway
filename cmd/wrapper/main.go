package main

import (
	"log"
	"os"

	"github.com/caw/wrapper/internal/adapter"
	"github.com/caw/wrapper/internal/gateway"
	"github.com/caw/wrapper/internal/memory"
	"github.com/redis/go-redis/v9"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	session := memory.NewSessionStore(rdb)

	backend, err := adapter.NewBackend()
	if err != nil {
		log.Fatalf("backend init: %v", err)
	}

	srv := gateway.NewServer(backend, rdb, session)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Fatal(srv.Listen(":" + port))
}
