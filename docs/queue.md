# RabbitMQ Work Queue

ffxiv-census runs its durable async work queue on **RabbitMQ**. The broker replaces the former PostgreSQL-backed `queue_jobs` table, eliminating 30-goroutine polling and moving to push-based consumption. There is no deduplication at the queue level — database upserts in handlers are idempotent.

## Topology

The adapter declares the full topology idempotently on connection. All exchanges and queues are durable; messages are persistent.

```
census (exchange, direct)
  ├─ routing key "id-sweep"           → census.id-sweep           (queue)
  ├─ routing key "character-census"   → census.character-census   (queue)
  ├─ routing key "achievement-census" → census.achievement-census (queue)
  ├─ routing key "new-proxy"          → census.new-proxy          (queue)
  └─ routing key "scan-proxy"         → census.scan-proxy         (queue)

census.<type>.failed (queue, per event type)
  └─ x-dead-letter-exchange = census
  └─ x-dead-letter-routing-key = <type>
```

Each event type gets a **main queue** (`census.<type>`) bound to the `census` exchange with routing key equal to the event type, and a **failed queue** (`census.<type>.failed`) that dead-letters back to the main exchange. Retry messages in the failed queue have a TTL — when the TTL expires, RabbitMQ dead-letters them back to the main queue automatically. Permanent failures (no TTL) stay in the failed queue indefinitely.

## Message Flow

```
Publisher                     Broker                          Consumer
─────────                     ──────                          ────────
Publish(ctx, job)
  │
  ├─ exchange: census
  ├─ routing key: job.Type
  └─ body: JSON payload
                                census.<type> queue
                                  │
                                  ├─ push to consumer
                                  │
                                  │   handler(ctx, job)
                                  │     ├─ success → Ack
                                  │     └─ error   → handleFailure()
                                  │                    ├─ attempts < 5
                                  │                    │   publish to census.<type>.failed
                                  │                    │   with TTL (auto-retry)
                                  │                    └─ attempts >= 5
                                  │                        publish to census.<type>.failed
                                  │                        without TTL (permanent)
                                  │
                                census.<type>.failed
                                  ├─ TTL message: expires → dead-letter → census.<type>
                                  └─ no TTL message: stays permanently
```

## Retry Mechanism

When a handler returns an error, the adapter inspects the `x-attempts` header and decides:

| Condition | Action | TTL | Result |
|-----------|--------|-----|--------|
| `attempts < 5` | Publish to `census.<type>.failed` | `min(5 × 2^(attempts-1), 3600)` seconds | Auto-dead-letters back to main queue after TTL |
| `attempts >= 5` | Publish to `census.<type>.failed` | None | Stays in failed queue permanently |

Backoff schedule (seconds): **5, 10, 20, 40, 80**. After 5 failed attempts the message is parked permanently in the failed queue.

The attempt count is tracked in the `x-attempts` message header. On each retry the header is incremented. The original message is always acked after being forwarded to the failed queue — there is no requeue.

## Consumer Pattern

Consumption is **push-based**. The worker calls `queue.Consume` which blocks until the context is cancelled. RabbitMQ delivers messages to consumers as they arrive — no polling, no claim loops, no `FOR UPDATE SKIP LOCKED`.

```go
// contract.Queue — the simplified interface
type Queue interface {
    Publish(ctx context.Context, job QueueJob) error
    Consume(ctx context.Context, eventTypes []string, concurrency int, handler func(ctx context.Context, job QueueJob) error) error
    Close() error
}
```

**`Publish`** sends a single job to the `census` exchange with routing key = `job.Type`. The payload is JSON bytes. Returns error on failure.

**`Consume`** starts `concurrency` worker goroutines. Each worker opens a dedicated AMQP channel with `prefetch(1)`, consumes from all specified event type queues, and dispatches messages to the handler. On handler return:
- `nil` → message is acked
- `error` → message is published to the failed queue (retry or permanent), then acked

The original message is always acked — failed messages are forwarded, not requeued. This prevents redelivery loops.

**Worker usage:**

```go
processJob := func(ctx context.Context, job contract.QueueJob) error {
    h, ok := handlers.Get(job.Type)
    if !ok {
        return fmt.Errorf("no handler for %s", job.Type)
    }
    next, err := h.Handle(ctx, job.Payload)
    if err != nil {
        return err // queue handles retry/dead-letter
    }
    // Publish downstream jobs individually
    for _, j := range next {
        if err := queue.Publish(ctx, j); err != nil {
            return err
        }
    }
    return nil
}

err := queue.Consume(ctx, eventTypes, concurrency, processJob)
```

Handler panics are caught with `defer/recover`, formatted with stack traces, and returned as errors — the worker goroutine does not crash.

## Configuration

```toml
[rabbitmq]
url      = "amqp://guest:guest@localhost:5672/ffxiv-census"
host     = "localhost"
port     = 5672
user     = "guest"
password = "guest"
vhost    = "ffxiv-census"
```

| Field | Purpose |
|-------|---------|
| `url` | Full AMQP connection URL (takes precedence over individual fields) |
| `host` | RabbitMQ hostname |
| `port` | AMQP port (default 5672) |
| `user` | Authentication username |
| `password` | Authentication password |
| `vhost` | Virtual host (default `ffxiv-census`) |

If `url` is empty, it is constructed from the individual fields: `amqp://<user>:<password>@<host>:<port>/<vhost>`.

**Environment overrides** — dots become underscores, section name is the prefix:

| Variable | Overrides |
|----------|-----------|
| `RABBITMQ_URL` | `url` |
| `RABBITMQ_HOST` | `host` |
| `RABBITMQ_PORT` | `port` |
| `RABBITMQ_USER` | `user` |
| `RABBITMQ_PASSWORD` | `password` |
| `RABBITMQ_VHOST` | `vhost` |

In Kubernetes, `RABBITMQ_USER` and `RABBITMQ_PASSWORD` are injected from Vault (`rabbitmq/prod` secret) via External Secrets Operator. The URL is constructed in the deployment env:

```yaml
- name: RABBITMQ_URL
  value: "amqp://$(RABBITMQ_USER):$(RABBITMQ_PASSWORD)@rabbitmq.default.svc.cluster.local:5672/ffxiv-census"
```

## Kubernetes Deployment

Each event type runs as a **separate Deployment** with its own replica count and concurrency setting. This allows independent scaling and restart of each consumer type.

```yaml
# k8s/values.yaml — worker instances
workers:
  instances:
    - name: id-sweep
      replicaCount: 1
      command: [/app/ffxiv-census, consume, id-sweep, -c, "10"]
    - name: character-census
      replicaCount: 1
      command: [/app/ffxiv-census, consume, character-census, -c, "10"]
    - name: achievement-census
      replicaCount: 1
      command: [/app/ffxiv-census, consume, achievement-census, -c, "10"]
    - name: proxy-id-sweep
      replicaCount: 1
      command: [/app/ffxiv-census, consume, id-sweep, --proxy, -c, "10"]
    - name: proxy-character-census
      replicaCount: 1
      command: [/app/ffxiv-census, consume, character-census, --proxy, -c, "10"]
    - name: proxy-achievement-census
      replicaCount: 1
      command: [/app/ffxiv-census, consume, achievement-census, --proxy, -c, "10"]
    - name: proxy-new
      replicaCount: 1
      command: [/app/ffxiv-census, proxy, consume, -c, "10"]
    - name: proxy-scan
      replicaCount: 1
      command: [/app/ffxiv-census, proxy, consume, scan-proxy, -c, "5"]
```

Concurrency is set per-deployment via the `-c` flag. The default is 4 if not specified. Each worker goroutine opens its own AMQP channel with `prefetch(1)`.

**Scaling:** Increase `replicaCount` or `-c` to handle higher throughput. RabbitMQ distributes messages across consumers automatically (round-robin with prefetch=1). There is no risk of double-delivery — each message is delivered to exactly one consumer.

**Graceful shutdown:** On SIGTERM, the context is cancelled. Workers stop accepting new messages and finish their current handler calls. The `terminationGracePeriodSeconds` (default 180s) gives in-flight jobs time to complete.

## Monitoring

RabbitMQ exposes a **management UI** on port 15672. In the cluster it is accessible via NodePort 31672:

```
http://<node-ip>:31672
```

The management UI shows:
- Queue depths and message rates per queue
- Consumer connections and channel counts
- Exchange bindings and routing
- Message rates (publish, deliver, ack)

Useful CLI commands inside the pod:

```bash
# List all queues with message counts
kubectl exec rabbitmq-0 -- rabbitmqctl list_queues -p ffxiv-census name messages consumers

# List connections
kubectl exec rabbitmq-0 -- rabbitmqctl list_connections

# Purge a queue (e.g. to clear stuck messages)
kubectl exec rabbitmq-0 -- rabbitmqctl purge_queue census.id-sweep -p ffxiv-census
```

## Migration from PostgreSQL

The `migrate queue` command moves all pending, claimed, and failed jobs from the legacy `queue_jobs` table to RabbitMQ:

```bash
# Dry run — shows what would be migrated
./bin/ffxiv-census migrate queue --dry-run

# Execute migration
./bin/ffxiv-census migrate queue
```

The command:
1. Queries all non-done jobs from `queue_jobs` (`status IN ('pending', 'claimed', 'failed')`)
2. Publishes each job individually to RabbitMQ via `queue.Publish`
3. Logs counts per event type and status
4. Deletes the migrated rows from PostgreSQL (`DELETE FROM queue_jobs WHERE status != 'done'`)

**Pre-migration steps:**

```bash
# Scale down all workers
kubectl scale deploy/ffxiv-census-worker-id-sweep --replicas=0
kubectl scale deploy/ffxiv-census-worker-character-census --replicas=0
kubectl scale deploy/ffxiv-census-worker-achievement-census --replicas=0
kubectl scale deploy/ffxiv-census-worker-proxy-id-sweep --replicas=0
kubectl scale deploy/ffxiv-census-worker-proxy-character-census --replicas=0
kubectl scale deploy/ffxiv-census-worker-proxy-achievement-census --replicas=0
kubectl scale deploy/ffxiv-census-worker-proxy-new --replicas=0
kubectl scale deploy/ffxiv-census-worker-proxy-scan --replicas=0

# Suspend cronjobs
kubectl patch cronjob publish-character -p '{"spec":{"suspend":true}}'
kubectl patch cronjob publish-id-sweep -p '{"spec":{"suspend":true}}'
kubectl patch cronjob proxy-discover -p '{"spec":{"suspend":true}}'
kubectl patch cronjob proxy-scan -p '{"spec":{"suspend":true}}'
```

**Post-migration steps:**

```bash
# Scale workers back up
kubectl scale deploy/ffxiv-census-worker-id-sweep --replicas=1
kubectl scale deploy/ffxiv-census-worker-character-census --replicas=1
kubectl scale deploy/ffxiv-census-worker-achievement-census --replicas=1
kubectl scale deploy/ffxiv-census-worker-proxy-id-sweep --replicas=1
kubectl scale deploy/ffxiv-census-worker-proxy-character-census --replicas=1
kubectl scale deploy/ffxiv-census-worker-proxy-achievement-census --replicas=1
kubectl scale deploy/ffxiv-census-worker-proxy-new --replicas=1
kubectl scale deploy/ffxiv-census-worker-proxy-scan --replicas=1

# Resume cronjobs
kubectl patch cronjob publish-character -p '{"spec":{"suspend":false}}'
kubectl patch cronjob publish-id-sweep -p '{"spec":{"suspend":false}}'
kubectl patch cronjob proxy-discover -p '{"spec":{"suspend":false}}'
kubectl patch cronjob proxy-scan -p '{"spec":{"suspend":false}}'
```

## Event Types

The queue carries events for census and proxy contexts. See `docs/events.md` for payloads and chaining details.

| Context | Events | Consumer Command |
|---------|--------|-----------------|
| Census | `id-sweep`, `character-census`, `achievement-census` | `ffxiv-census consume` |
| Proxy | `new-proxy`, `scan-proxy` | `ffxiv-census proxy consume` |

**Handler chaining:** When a handler succeeds, it returns downstream jobs. The worker publishes each downstream job individually:
- `id-sweep` → `achievement-census` (per discovered character)
- `character-census` → `achievement-census` (per re-censused character)

## Contract

`port/contract.Queue` (see `port/contract/queue.go`) is implemented by `infrastructure/rabbitmq` and `mock/queue` (in-memory fake for tests). The simplified interface has three methods: `Publish`, `Consume`, and `Close`. All retry and dead-letter logic is internal to the adapter — callers only see success or error from the handler.

## Connection Resilience

The RabbitMQ adapter handles connection drops with automatic reconnect. On `Publish`, if the connection is closed, it dials a new connection and channel before retrying. The topology is re-declared on reconnect (idempotent). Consumer goroutines will error and the `Consume` call will return if the connection cannot be recovered.
