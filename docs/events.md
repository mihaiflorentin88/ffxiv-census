# Event Model & Ingest Pipeline

The census ingests Lodestone data through a durable, event-driven pipeline. Publishers enqueue jobs into the PostgreSQL-backed queue (`docs/queue.md`); consumers claim jobs of one event type, run a handler, and chain downstream events. See the design spec (`docs/superpowers/specs/2026-08-16-lodestone-census-design.md`) for the full picture.

## Events

| Event | Purpose | Status |
|---|---|---|
| `id-sweep` | Probe a range of character IDs; ingest any that exist | ✅ implemented |
| `character-census` | Re-census a known character's profile + jobs | ✅ implemented |
| `achievement-census` | Fetch achievements, filter milestones, track latest | ✅ implemented |
| `fc-census` | Fetch a free company's basic info | ✅ implemented (member chaining deferred) |
| `new-proxy` | Register and test a newly discovered proxy | ✅ implemented |
| `scan-proxy` | Re-test an existing proxy and update its status | ✅ implemented |

## Payloads

Queue job payloads are JSON. Types are declared in `domain/census/handler/event.go`.

- **id-sweep**: `{"from": 1, "to": 1000, "source": "auto"}` — inclusive range of character IDs to probe, with optional source (`"auto"`, `"tomestone"`, `"lodestone"`).
- **character-census**: `{"character_id": 123}` — a character to re-census.
- **achievement-census**: `{"character_id": 123}` — a character to run an achievement census on.
- **fc-census**: `{"fc_id": "9234567890123456789"}` — a free company to census.
- **new-proxy**: `{"protocol": "http", "ip": "1.2.3.4", "port": 8080, "country": "US", "anonymity": "elite", "source": "proxyscrape", "uptime_percent": 95.5}` — a proxy to register and test.
- **scan-proxy**: `{"proxy_id": 42}` — an existing proxy to re-scan.

Proxy event types are declared in `domain/proxy/handler/handler.go`. Both are leaf events (no chaining).

## Chaining

Handlers return the jobs they want published next; the worker persists them atomically with the current job's completion via `Queue.Complete(id, nextJobs...)` (same transaction). This is how downstream work is scheduled without losing atomicity.

- `id-sweep` → `achievement-census` (+ `fc-census` when the character is affiliated with a free company)
- `character-census` → `achievement-census` (+ `fc-census` when the character is affiliated with a free company)
- `achievement-census` → (leaf)
- `fc-census` → (leaf)
Member-list re-census (`fc-census` → `character-census` for stale members) is deferred until `FetchFreeCompanyMembers` is exposed by the `LodestoneClient` contract.

## Loop safety

The queue deduplicates on `UNIQUE(type, payload_hash)`, so re-publishing an identical job is a no-op. Handlers are idempotent (`UpsertCharacter` is a conflict-upsert), so a retried `id-sweep` chunk re-probes safely without duplicating chained jobs.

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
- Downstream dependent jobs (`achievement-census`, plus `fc-census` when in an FC) are uniformly chained using `BuildDependentCharacterJobs` regardless of which provider ingested the record.
- **Worker Rate-Limit Coordination**: When Lodestone encounters HTTP 429 or is paused in the `ProviderRateLimiter`, workers pause Lodestone-exclusive queues (`achievement-census`, `fc-census`) and process dual-source queues (`id-sweep`, `character-census`) via Tomestone. If a character is not indexed on Tomestone, the job retries on Lodestone with backoff. When Tomestone is rate-limited, dual-source queues route to Lodestone. If all providers are rate-limited, workers sleep until the earliest cooldown expires without wasting database claims or CPU cycles.

### Lodestone-Only Event Rate-Limit Handling

`achievement-census` and `fc-census` are Lodestone-exclusive events (no Tomestone fallback). When Lodestone is rate-limited or paused, these handlers call `WaitUntilAvailable(ctx, ProviderLodestone)` to block until the cooldown expires, then retry the fetch inline. This is more efficient than returning an error immediately (which triggers 30s idle + queue retry + 5s backoff = ~35s wasted).

`WaitUntilAvailable` blocks exactly once per rate-limit cooldown. If the subsequent fetch fails again (new 429), the handler returns an error and the queue's exponential backoff kicks in. The handler does NOT loop on `WaitUntilAvailable`.

One worker goroutine blocks during the wait. With the default `concurrency=4`, the other 3 goroutines continue processing. Kubernetes' 180s termination grace period covers extended Lodestone cooldowns.

Explicit `tomestone` or `lodestone` source modes on `id-sweep` skip the other client entirely.
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

# Re-census characters not seen in 30 days (recheck cron).
./bin/ffxiv-census publish character-census --older-than 720h --limit 1000
```

`consume` handles SIGINT/SIGTERM gracefully. `publish id-sweep` divides the sweep range into `chunk-size`-sized jobs with deduplication.
