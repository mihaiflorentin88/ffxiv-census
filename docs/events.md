# Event Model & Ingest Pipeline

The census ingests Lodestone data through a durable, event-driven pipeline. Publishers enqueue jobs into the RabbitMQ-backed queue (`docs/queue.md`); consumers receive jobs via push-based consumption, run a handler, and chain downstream events. See the design spec (`docs/superpowers/specs/2026-08-16-lodestone-census-design.md`) for the full picture.

## Events

| Event | Purpose | Status |
|---|---|---|
| `id-sweep` | Probe a range of character IDs; ingest any that exist | ✅ implemented |
| `character-census` | Re-census a known character's profile + jobs | ✅ implemented |
| `achievement-census` | Incrementally fetch tracked milestones and track the latest checked milestone | ✅ implemented |
| `new-proxy` | Register and test a newly discovered proxy | ✅ implemented |

Recurring proxy scans are performed directly by the `proxy scan` database worker, not via queue events. See `docs/proxy.md`.

## Payloads

Queue job payloads are JSON. Types are declared in `domain/census/handler/event.go`.

- **id-sweep**: `{"from": 1, "to": 1000, "source": "auto"}` — inclusive range of character IDs to probe, with optional source (`"auto"`, `"tomestone"`, `"lodestone"`).
- **character-census**: `{"character_id": 123}` — a character to re-census.
- **achievement-census**: `{"character_id": 123}` — a character to run an achievement census on.
- **new-proxy**: `{"protocol": "http", "ip": "1.2.3.4", "port": 8080, "country": "US", "anonymity": "elite", "source": "proxyscrape", "uptime_percent": 95.5}` — a proxy to register and test.

Proxy event types are declared in `domain/proxy/handler/handler.go`. The `new-proxy` event is a leaf event (no chaining).

## Chaining

Handlers return the downstream jobs they want published; the worker publishes each one individually via `queue.Publish(ctx, job)`. The queue's push-based `Consume` method handles retry and failure internally — no explicit `Complete`, `Retry`, or `Fail` calls.

- `id-sweep` → `achievement-census`
- `character-census` → `achievement-census`
- `achievement-census` → (leaf)

## Loop safety

Handlers are idempotent (`UpsertCharacter` is a conflict-upsert), so a retried `id-sweep` chunk re-probes safely without duplicating chained jobs. RabbitMQ does not deduplicate at the queue level — idempotency is enforced by the database layer. The `proxy discover` command checks PostgreSQL read-only before publishing each `new-proxy` event; existing tuples are counted as `skipped_existing` and never published. Lookup errors fail closed per provider. The consumer independently checks then inserts conflict-safely via `INSERT ... ON CONFLICT DO NOTHING`.

## Dual-source ingest, Fallback & Provider Rate-Limit Coordination

Both `id-sweep` and `character-census` are dual-source events, but they use different primary providers in `auto` source mode:

**`id-sweep` (character discovery):** Tomestone primary, Lodestone fallback.
1. Handlers probe **Tomestone.gg** first. Tomestone runs at 5 req/s (REST API) vs Lodestone's 1 req/s (scraper), making it the faster discovery path.
2. When Tomestone returns a 404 (`contract.ErrCharacterNotFound`), handlers fall back to **The Lodestone** — the character may exist but not be indexed by Tomestone.
3. When Tomestone encounters a transient error, handlers fall back to **The Lodestone** as the authoritative source.
4. If Tomestone returns 404 and Lodestone is unavailable/paused, the job returns an error to retry on Lodestone later.
5. If both providers return 404, the character is confirmed missing/deleted and skipped.
6. If Tomestone errors and Lodestone returns 404, the character is confirmed missing (Lodestone is authoritative for existence).

**`character-census` (profile re-census):** Lodestone primary, Tomestone fallback.
1. Handlers query **The Lodestone** first as the authoritative source of truth.
2. When Lodestone returns a 404, handlers check Tomestone.gg. If Tomestone also 404s (or is unavailable), the character is confirmed deleted.
3. When Lodestone encounters a transient error or is paused, Tomestone.gg is probed as a fallback. If Tomestone returns 404, the job retries on Lodestone with backoff.

**Shared rules:**
- Downstream dependent jobs (`achievement-census`) are uniformly chained using `BuildDependentCharacterJobs` regardless of which provider ingested the record.
- **Worker Rate-Limit Coordination**: When Lodestone encounters HTTP 429 or is paused in the `ProviderRateLimiter`, workers pause Lodestone-exclusive queues (`achievement-census`) and process dual-source queues (`id-sweep`, `character-census`) via Tomestone. If a character is not indexed on Tomestone, the job retries on Lodestone with backoff. When Tomestone is rate-limited, dual-source queues route to Lodestone. If all providers are rate-limited, workers sleep until the earliest cooldown expires without wasting CPU cycles.

### Lodestone-Only Event Rate-Limit Handling

Achievement checks request only missing milestones in chain order and stop at the first public unearned result; already stored checkpoints are not rechecked. HTTP 403 on that first required detail request marks achievements private, with no separate achievement-list privacy probe. A complete tracked history does not call Lodestone.

`achievement-census` is a Lodestone-exclusive event (no Tomestone fallback). When Lodestone is rate-limited or paused, the handler calls `WaitUntilAvailable(ctx, ProviderLodestone)` to block until the cooldown expires, then retries the fetch inline. This is more efficient than returning an error immediately (which triggers queue retry + backoff).

`WaitUntilAvailable` blocks exactly once per rate-limit cooldown. If the subsequent fetch fails again (new 429), the handler returns an error and the queue's exponential backoff kicks in. The handler does NOT loop on `WaitUntilAvailable`.

One worker goroutine blocks during the wait. With the default `concurrency=4`, the other 3 goroutines continue processing. Kubernetes' 180s termination grace period covers extended Lodestone cooldowns.

Explicit `tomestone` or `lodestone` source modes on `id-sweep` skip the other client entirely.

### Proxy Mode Behavior

When `consume --proxy` is used, the event pipeline runs through proxy-aware clients:

- **`id-sweep` in proxy mode**: Lodestone is the primary provider (not Tomestone). Proxies bypass Lodestone's per-IP rate limit, so the faster Tomestone-first strategy is unnecessary. Tomestone is used only as a fallback when Lodestone returns an error.
- **`achievement-census` in proxy mode**: Same as non-proxy — Lodestone-only, with rate-limit waiting. Achievement-only proxy processes omit the unused Tomestone HTTP transport per goroutine.
- **Proxy rotation on failure**: If a proxy fails during any request (connection refused, timeout, host unreachable), the worker immediately marks it as failed and acquires a fresh proxy from the pool. This ensures workers quickly rotate through bad proxies.
- **Per-goroutine isolation**: Each goroutine has its own proxy, its own LodestoneClient, its own TomestoneClient, and its own rate limiter. No shared state between goroutines.

## CLI

```bash
# Long-running consumer (one per event type; k8s deployment per consumer).
./bin/ffxiv-census consume id-sweep --concurrency 4
# Proxy mode: each goroutine acquires its own proxy
./bin/ffxiv-census consume --proxy --concurrency 8

# One-shot auto-discovery publisher (queries MaxID in DB, sweeps next 1000 IDs).
./bin/ffxiv-census publish id-sweep --count 1000 --chunk-size 100 --source auto

# Manual ID sweep over explicit range.
./bin/ffxiv-census publish id-sweep --from 1 --to 50000000 --chunk-size 100 --source auto

# Enqueue the 1000 oldest characters (no age filter, NULL first).
./bin/ffxiv-census publish character-census --limit 1000

# Re-census characters not seen in 30 days (recheck cron).
./bin/ffxiv-census publish character-census --older-than 720h --limit 1000
```

`consume` handles SIGINT/SIGTERM gracefully. `publish id-sweep` divides the sweep range into `chunk-size`-sized jobs.

**`publishAll` behaviour:** Each publish call waits for a broker confirmation before returning. If any publish is nacked or the connection drops, the command fails immediately rather than continuing to enqueue jobs that may never be delivered. After all publishes succeed, the queue connection is closed so the process exits cleanly.

**`proxy discover` callback publication:** Providers are invoked sequentially in configured order. Each provider streams records one at a time via a callback (`emit`). Inside the callback, each record is checked read-only against PostgreSQL; existing tuples are skipped and do not consume the publication quota. New records are published immediately as `new-proxy` events — the first queue publish happens before the provider fetches its next record/page. This keeps memory bounded regardless of provider response size. When the `--limit` publication cap is reached, the current provider's stream is aborted and all remaining providers are skipped. When a provider exhausts below the limit, the next provider is invoked to fill the remaining quota. `--limit 0` disables the cap. If a publish fails, that provider is stopped (logged as `proxy.discover.publish_failed`) and the command continues to the next provider. Partial provider failures are tolerated: the command reports per-provider fetched/published counts and returns an error only when every provider fails (`proxy discovery failed: all providers failed (%d errors)`).
