# Event Model & Ingest Pipeline

The census ingests Lodestone data through a durable, event-driven pipeline. Publishers enqueue jobs into the SQLite-backed queue (`docs/queue.md`); consumers claim jobs of one event type, run a handler, and chain downstream events. See the design spec (`docs/superpowers/specs/2026-08-16-lodestone-census-design.md`) for the full picture.

## Events

| Event | Purpose | Status |
|---|---|---|
| `id-sweep` | Probe a range of character IDs; ingest any that exist | ✅ implemented |
| `character-census` | Re-census a known character's profile + jobs | ✅ implemented |
| `achievement-census` | Fetch achievements, filter milestones, track latest | ✅ implemented |
| `fc-census` | Fetch a free company's basic info | ✅ implemented (member chaining deferred) |

## Payloads

Queue job payloads are JSON. Types are declared in `domain/census/handler/event.go`.

- **id-sweep**: `{"from": 1, "to": 1000}` — inclusive range of character IDs to probe.
- **character-census**: `{"character_id": 123}` — a character to re-census.
- **achievement-census**: `{"character_id": 123}` — a character to run an achievement census on.
- **fc-census**: `{"fc_id": "9234567890123456789"}` — a free company to census.

## Chaining

Handlers return the jobs they want published next; the worker persists them atomically with the current job's completion via `Queue.Complete(id, nextJobs...)` (same transaction). This is how downstream work is scheduled without losing atomicity.

- `id-sweep` → `achievement-census` (one per discovered character)
- `character-census` → `achievement-census` (+ `fc-census` when the character is in a free company)
- `achievement-census` → (leaf)
- `fc-census` → (leaf)

Member-list re-census (`fc-census` → `character-census` for stale members) is deferred until `FetchFreeCompanyMembers` is exposed by the `LodestoneClient` contract.

## Loop safety

The queue deduplicates on `UNIQUE(type, payload_hash)`, so re-publishing an identical job is a no-op. Handlers are idempotent (`UpsertCharacter` is a conflict-upsert), so a retried `id-sweep` chunk re-probes safely without duplicating chained jobs.

## 404 vs retry

`LodestoneClient.FetchCharacter` returns `contract.ErrCharacterNotFound` for a non-existent character (HTTP 404) and a wrapped transient error for network/rate-limit failures. Handlers must treat 404 as "skip" and any other error as "retry" (the worker maps a handler error to `Queue.Retry`, which enforces exponential backoff and max-attempts).

## Worker pool

`domain/census/worker` runs N concurrent goroutines, each looping: claim one job of the event type → dispatch to the registered handler → on error `Retry`, on success `Complete(id, next...)`. It polls (1s) when the queue is empty and stops cleanly on context cancellation.

## CLI

```bash
# Long-running consumer (one per event type; k8s deployment per consumer).
./bin/ffxiv-census consume id-sweep --concurrency 4

# One-shot publisher (cronjob entrypoint).
./bin/ffxiv-census publish id-sweep --from 1 --to 50000000 --chunk-size 100

# Re-census characters not seen in 30 days (recheck cron).
./bin/ffxiv-census publish character-census --older-than 720h --limit 1000
```

`consume` handles SIGINT/SIGTERM gracefully. `publish id-sweep` chunks the ID range into `chunk-size`-sized `id-sweep` jobs.
