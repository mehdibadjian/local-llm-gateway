---
name: go-redis-session
description: "Best practices for go-redis v9 in CAW's session store, rate limiter, ingest queue, retrieval cache, and compression lock. Covers RPUSH+LTRIM list cap, Lua scripts (rate limit, SET NX compression lock), Redis Streams consumer groups, pipeline usage, and connection pool sizing. Use when implementing any Redis interaction in the CAW service."
sources:
  - library: redis/go-redis
    context7_id: /redis/go-redis
    snippets: 167
    score: 78.57
  - library: redis/docs
    context7_id: /redis/docs
    snippets: 11896
    score: 82.43
---

# Go Redis Session Skill — CAW

## Role

You are implementing or reviewing any Redis interaction in the Capability Amplification Wrapper.
Every pattern here is grounded in the CAW architecture spec (`docs/reference/architecture.md`)
and verified against current go-redis v9 docs via Context7.

---

## 1 — Client Configuration (CAW Defaults)

```go
// internal/store/redis.go

func NewRedisClient(addr, password string) *redis.Client {
    return redis.NewClient(&redis.Options{
        Addr:         addr,
        Password:     password,
        DB:           0,
        DialTimeout:  5 * time.Second,
        ReadTimeout:  10 * time.Millisecond, // architecture CR-F: ≤ 10ms command budget
        WriteTimeout: 10 * time.Millisecond,
        PoolSize:     20,
        MinIdleConns: 5,
        MaxRetries:   3,
        DialerRetryBackoff: redis.DialRetryBackoffExponential(
            50*time.Millisecond, 500*time.Millisecond,
        ),
    })
}
```

**Rules (architecture CR-F):**
- `ReadTimeout` and `WriteTimeout` MUST be ≤ 10 ms.
- Track `caw_redis_latency_seconds` Prometheus histogram per operation; alert on p99 > 5 ms.
- Client is safe for concurrent use — create ONE instance, pass it everywhere.

---

## 2 — Session Message List: RPUSH + LTRIM (Architecture CR-P)

**NEVER** use bare `RPUSH` — lists are unbounded without `LTRIM`. Every write pipelines both
commands atomically. The hard cap is 200 entries.

```go
// internal/store/session.go

const MessageListCap = 200

func (s *SessionStore) AppendMessage(ctx context.Context, sessionID string, msg Message) error {
    key := "session:" + sessionID + ":messages"
    encoded, _ := json.Marshal(msg)

    // Atomic pipeline: RPUSH + LTRIM — single round-trip
    _, err := s.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
        pipe.RPush(ctx, key, encoded)
        pipe.LTrim(ctx, key, -MessageListCap, -1) // keep last 200
        return nil
    })
    if err != nil {
        return fmt.Errorf("append message: %w", err)
    }
    // Sliding TTL reset — 24h window
    s.rdb.Expire(ctx, "session:"+sessionID, 24*time.Hour)
    s.rdb.Expire(ctx, key, 24*time.Hour)
    return nil
}
```

**Unit test requirement (Phase 0):** After 250 writes, `LLEN` MUST return ≤ 200.

---

## 3 — Distributed Rate Limiter (Architecture CR-O)

Replaces per-pod token bucket. Lua script ensures `INCR` + `EXPIRE` are atomic: the TTL is set
only on the **first** increment in a window, preventing the race where two goroutines both call
INCR but only one sets the TTL.

```go
var rateLimitScript = redis.NewScript(`
    local key     = KEYS[1]
    local limit   = tonumber(ARGV[1])
    local window  = tonumber(ARGV[2])
    local current = redis.call("INCR", key)
    if current == 1 then
        redis.call("EXPIRE", key, window)
    end
    if current > limit then return 0 end
    return 1
`)

func (rl *RateLimiter) Allow(ctx context.Context, apiKey string) (bool, error) {
    window := int64(60) // 60-second window
    ts     := time.Now().Unix() / window
    key    := fmt.Sprintf("caw:rate:%s:%d", apiKey, ts)

    allowed, err := rateLimitScript.Run(ctx, rl.rdb, []string{key}, 60, window).Int()
    return allowed == 1, err
}
```

---

## 4 — Context Compression Lock (Architecture CR-B)

Prevents TOCTOU race when concurrent requests arrive on the same session. The winning goroutine
acquires the `SET NX` lock; losers wait ≤ 500 ms then hard-truncate to 2.5K tokens.

```go
var compressionLockScript = redis.NewScript(`
    return redis.call("SET", KEYS[1], "1", "NX", "PX", ARGV[1])
`)

func (cm *ContextManager) AcquireCompressionLock(ctx context.Context, sessionID string) (bool, error) {
    key := "session:" + sessionID + ":compressing"
    result, err := compressionLockScript.Run(ctx, cm.rdb, []string{key}, "2000").Result()
    if err == redis.Nil {
        return false, nil // lock held by another goroutine
    }
    return result == "OK", err
}

func (cm *ContextManager) MaybeCompress(ctx context.Context, sessionID string, tokenCount int) error {
    if tokenCount < 3000 {
        return nil
    }
    acquired, err := cm.AcquireCompressionLock(ctx, sessionID)
    if err != nil {
        return err
    }
    if !acquired {
        // Loser: wait up to 500ms for winner to finish
        deadline := time.Now().Add(500 * time.Millisecond)
        for time.Now().Before(deadline) {
            time.Sleep(50 * time.Millisecond)
            exists, _ := cm.rdb.Exists(ctx, "session:"+sessionID+":compressing").Result()
            if exists == 0 { return nil }
        }
        // Timeout: hard-truncate to 2.5K tokens — NEVER proceed uncompressed
        return cm.HardTruncate(ctx, sessionID, 2500)
    }
    defer cm.rdb.Del(ctx, "session:"+sessionID+":compressing")
    return cm.RecursiveCompress(ctx, sessionID)
}
```

---

## 5 — Retrieval Cache: Version-Counter Invalidation (Architecture EG-L)

Cache keys embed a domain version counter so that after ingest, old entries expire naturally
on their 60 s TTL. **SCAN+DEL is prohibited** (O(N) over full keyspace, blocks Redis).

```go
func (rc *RetrievalCache) Key(ctx context.Context, domain, queryHash string) (string, error) {
    // O(1) version read
    ver, err := rc.rdb.Get(ctx, "caw:retrieval:"+domain+":version").Result()
    if err == redis.Nil { ver = "0" } else if err != nil { return "", err }
    return fmt.Sprintf("caw:retrieval:%s:%s:%s", domain, ver, queryHash), nil
}

func (rc *RetrievalCache) Get(ctx context.Context, domain, queryHash string) ([]RankedChunk, bool, error) {
    key, err := rc.Key(ctx, domain, queryHash)
    if err != nil { return nil, false, err }

    data, err := rc.rdb.Get(ctx, key).Bytes()
    if err == redis.Nil { return nil, false, nil }
    if err != nil { return nil, false, err }

    var chunks []RankedChunk
    return chunks, true, json.Unmarshal(data, &chunks)
}

// Called by IngestWorker on completion (architecture ingest step B)
func (rc *RetrievalCache) InvalidateDomain(ctx context.Context, domain string) error {
    return rc.rdb.Incr(ctx, "caw:retrieval:"+domain+":version").Err()
}
```

---

## 6 — Redis Streams: Ingest Job Queue

```go
// Enqueue (Gateway handler)
func (q *IngestQueue) Enqueue(ctx context.Context, job IngestJob) error {
    data, _ := json.Marshal(job)
    return q.rdb.XAdd(ctx, &redis.XAddArgs{
        Stream: "caw:ingest:jobs",
        Values: map[string]any{"payload": string(data)},
    }).Err()
}

// Consume (IngestWorker pod)
func (w *IngestWorker) ConsumeLoop(ctx context.Context) {
    for {
        msgs, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
            Group:    "ingest-workers",
            Consumer: w.id,
            Streams:  []string{"caw:ingest:jobs", ">"},
            Count:    1,
            Block:    5 * time.Second,
        }).Result()
        if err != nil || len(msgs) == 0 { continue }

        for _, msg := range msgs[0].Messages {
            if err := w.process(ctx, msg); err != nil {
                w.sendToDLQ(ctx, msg, err)
            } else {
                w.rdb.XAck(ctx, "caw:ingest:jobs", "ingest-workers", msg.ID)
            }
        }
    }
}
```

**DLQ rules (architecture CR-J):**
- `max_retry_count = 3` — on exhaustion, set document `status = 'failed'` in PostgreSQL.
- Monitor `caw_ingest_dlq_depth` Prometheus gauge; alert on depth > 10.

---

## 7 — Redis Configuration (Architecture CR-N)

```
maxmemory-policy: noeviction    # NEVER use allkeys-lru — silently evicts active sessions
maxmemory: <80% of available RAM>
```

Prometheus alert: `redis_memory_used_bytes / redis_memory_max_bytes > 0.80`

---

## 8 — Key Naming Conventions

| Pattern | Purpose |
|---------|---------|
| `session:{id}` | Hash: user_id, domain, token_count |
| `session:{id}:messages` | List (capped at 200 via RPUSH+LTRIM) |
| `session:{id}:steps` | List: plan steps |
| `session:{id}:compressing` | Ephemeral lock (SET NX, TTL 2s) |
| `caw:rate:{api_key}:{ts}` | Rate limit counter (TTL = window) |
| `caw:retrieval:{domain}:{ver}:{hash}` | Retrieval result cache (TTL 60s) |
| `caw:retrieval:{domain}:version` | Domain ingest version counter |
| `caw:ingest:jobs` | Redis Streams ingest queue |

---

## Sources

| Library | Stars | Context7 ID | URL |
|---------|-------|-------------|-----|
| redis/go-redis | 20k+ | /redis/go-redis | https://github.com/redis/go-redis |
| redis/docs | — | /redis/docs | https://redis.io/docs |
