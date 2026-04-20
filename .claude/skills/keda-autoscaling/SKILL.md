---
name: keda-autoscaling
description: "Best practices for KEDA autoscaling in CAW. Covers the wrapper ScaledObject (Prometheus trigger, scale-to-zero, fallback), the ingest worker ScaledObject (Redis Streams trigger), and the inference backend keep-warm config. Names and thresholds are canonical — changing them breaks autoscaling. Use when implementing or reviewing any KEDA ScaledObject, HPA config, or scaling policy."
sources:
  - library: kedacore/keda
    context7_id: /kedacore/keda
    snippets: 3356
    score: 79.2
---

# KEDA Autoscaling Skill — CAW

## Role

You are implementing or reviewing Kubernetes autoscaling for the Capability Amplification Wrapper.
Every `metricName`, `query`, and `stream` value here is **canonical** — changing them breaks
KEDA's ability to read the correct metrics. The architecture spec is the source of truth:
`docs/reference/architecture.md § Deliverable 4 — IaC Strategy`.

---

## 1 — Wrapper ScaledObject (Prometheus Trigger)

Scales wrapper pods based on `caw_requests_in_flight`. Each replica handles 5 in-flight requests.
Scale-to-zero when idle; hold 3 replicas if Prometheus is unavailable (fallback block).

```yaml
# helm/caw-wrapper/templates/scaledobject.yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: caw-wrapper-scaler
  namespace: {{ .Release.Namespace }}
spec:
  scaleTargetRef:
    name: caw-wrapper
    kind: Deployment
    apiVersion: apps/v1

  pollingInterval: 15          # check every 15s for responsive scaling
  cooldownPeriod:  60          # wait 60s before scaling down
  minReplicaCount: 0           # scale-to-zero when idle
  maxReplicaCount: 20

  # Hold capacity if Prometheus is unavailable (architecture EG-K)
  fallback:
    failureThreshold: 3
    replicas: 3

  advanced:
    horizontalPodAutoscalerConfig:
      behavior:
        scaleDown:
          stabilizationWindowSeconds: 60   # prevent flapping
          policies:
          - type: Pods
            value: 1
            periodSeconds: 60
        scaleUp:
          stabilizationWindowSeconds: 0
          policies:
          - type: Pods
            value: 4
            periodSeconds: 15

  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus.monitoring.svc:9090
      metricName: caw_requests_in_flight        # CANONICAL — do not rename
      threshold: "5"                            # 5 in-flight per replica
      query: sum(caw_requests_in_flight)        # CANONICAL query
```

**Rules:**
- `metricName: caw_requests_in_flight` and `query: sum(caw_requests_in_flight)` are frozen.
- `fallback.replicas: 3` prevents scale-to-zero on Prometheus outage.
- Ops runbook: on KEDA failure, `kubectl scale deployment/caw-wrapper --replicas=3`.

---

## 2 — Ingest Worker ScaledObject (Redis Streams Trigger)

Scales ingest workers based on pending messages in the `caw:ingest:jobs` consumer group.
Each worker handles 50 pending messages. Scale-to-zero when queue is empty.

```yaml
# helm/ingest-worker/templates/scaledobject.yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: caw-ingest-worker-scaler
  namespace: {{ .Release.Namespace }}
spec:
  scaleTargetRef:
    name: caw-ingest-worker
    kind: Deployment

  pollingInterval: 10
  cooldownPeriod:  30
  minReplicaCount: 0           # scale-to-zero when queue is empty
  maxReplicaCount: 10

  triggers:
  - type: redis-streams
    metadata:
      address: redis.{{ .Release.Namespace }}.svc:6379
      stream: caw:ingest:jobs          # CANONICAL stream name
      consumerGroup: ingest-workers    # CANONICAL consumer group
      pendingEntriesCount: "50"        # 50 pending msgs per replica (architecture EG-H)
    authenticationRef:
      name: redis-auth
```

**Rules (architecture EG-H):**
- `pendingEntriesCount: "50"` — raised from 5 to 50 to avoid over-provisioning on bursts.
- Each worker caps EmbedSvc calls at `EMBED_CONCURRENCY=4` (semaphore-gated, env var).
- `consumerGroup: ingest-workers` MUST match the group name in the IngestWorker code.

---

## 3 — Inference Backend (Keep-Warm)

The inference backend MUST never scale to zero. Cold-start for `gemma:2b` is ~10s.

```yaml
# helm/inference-backend/templates/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: caw-inference-backend
spec:
  replicas: 1              # keep-warm: never 0
  # No ScaledObject — HPA disabled for inference backend in Phase 0/1
  # Add GPU-aware ScaledObject in Phase 3 if throughput demands it
```

For Phase 2+, if throughput requires scaling inference:

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: caw-inference-scaler
spec:
  scaleTargetRef:
    name: caw-inference-backend
  minReplicaCount: 1       # NEVER 0 — keep-warm
  maxReplicaCount: 4
  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus.monitoring.svc:9090
      metricName: caw_requests_in_flight
      threshold: "10"
      query: sum(caw_requests_in_flight)
```

---

## 4 — TriggerAuthentication for Redis

```yaml
# helm/redis/templates/trigger-auth.yaml
apiVersion: v1
kind: Secret
metadata:
  name: redis-secrets
  namespace: {{ .Release.Namespace }}
type: Opaque
stringData:
  password: {{ .Values.redis.password | quote }}
---
apiVersion: keda.sh/v1alpha1
kind: TriggerAuthentication
metadata:
  name: redis-auth
  namespace: {{ .Release.Namespace }}
spec:
  secretTargetRef:
  - parameter: password
    name: redis-secrets
    key: password
```

---

## 5 — Scaling Behaviour Summary

| Component | Trigger | Min | Max | Per-Replica Threshold |
|-----------|---------|-----|-----|-----------------------|
| `caw-wrapper` | `sum(caw_requests_in_flight)` | 0 (→3 fallback) | 20 | 5 in-flight |
| `caw-ingest-worker` | Redis Streams pending | 0 | 10 | 50 pending msgs |
| `caw-inference-backend` | — (manual) | 1 (keep-warm) | 1 | n/a |

---

## 6 — Stateless Routing Note (Architecture EG-F)

Wrapper pods are **fully stateless**. All session state is in Redis. Sticky session annotations
are **removed** — they are redundant with shared Redis state and misleading.

```yaml
# Do NOT add this to caw-wrapper Service or Ingress:
# sessionAffinity: ClientIP   ← WRONG — removed per EG-F
```

---

## Sources

| Library | Stars | Context7 ID | URL |
|---------|-------|-------------|-----|
| kedacore/keda | 8k+ | /kedacore/keda | https://github.com/kedacore/keda |
| keda.sh docs | — | — | https://keda.sh/docs |
