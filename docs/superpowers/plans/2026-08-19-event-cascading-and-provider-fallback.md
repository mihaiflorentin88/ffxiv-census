# Event Cascading, Dual-Provider Fallback, Rate Limit Coordination, and Dedicated Worker Pools

## Context
Following recent refactors, the ingest pipeline required complete restoration and unification of core features:
1. **Downstream Event Cascading**: Both `id-sweep` and `character-census` publish dependent events (`achievement-census` + `fc-census` when in FC) **immediately in real-time** as each character is discovered or re-censused.
2. **Primary Lodestone with Tomestone.gg Fallback**: Both handlers use Lodestone as primary and automatically fall back to Tomestone.gg when Lodestone fails.
3. **Provider Rate Limit Pausing & Worker Switching**: When Lodestone encounters HTTP 429, workers pause Lodestone-exclusive queues and switch dual-source queues to Tomestone. When Tomestone hits rate limits, workers switch back. If both are rate-limited, workers sleep until the earliest cooldown expires.
4. **Dedicated Parallel Worker Pools**: Each event type gets its own dedicated pool of goroutines, ensuring zero starvation. Workers dynamically borrow work from other queues when their primary queue is empty.
5. **Queue API Enhancement**: `/api/v1/queue` now returns `summary.by_event` with per-event-type breakdowns alongside the full `events` array.

## Architecture

### Real-Time Event Cascading
- **File**: `domain/census/handler/event.go`
  - `BuildDependentCharacterJobs(characterID, freeCompanyID) []contract.QueueJob`
- **File**: `domain/census/handler/character.go`
  - `CharacterCensus` now accepts `contract.Queue` and calls `queue.Publish(ctx, jobs...)` immediately after each character upsert.
- **File**: `domain/census/handler/idsweep.go`
  - `IDSweep` now accepts `contract.Queue` and calls `queue.Publish(ctx, jobs...)` immediately after each character discovery.

### Dedirect Worker Pools (`domain/census/worker/worker.go`)
- `RunEvents()` divides concurrency evenly across registered event types.
- Each worker runs `eventWorkerLoop(ctx, primaryType, allTypes, workerID)`:
  1. Claims from its dedicated primary event queue first.
  2. If primary is empty or paused, dynamically borrows work from other available queues.
  3. If all queues are empty/paused, sleeps until the earliest rate limit cooldown or poll interval.

### Worker Rate-Limit Coordination
- `isEventTypeAvailable(eventType)`:
  - `achievement-census`, `fc-census`: Lodestone-only (`rateLimiter.IsAvailable(ProviderLodestone)`).
  - `character-census`, `id-sweep`: Dual-source (`IsAvailable(Lodestone) || IsAvailable(Tomestone)`).
- Workers for Lodestone-only events pause when Lodestone is rate-limited, while dual-source workers continue via Tomestone.

### Container Wiring (`container/domain.go`)
- `NewIDSweep` and `NewCharacterCensus` receive `s.Queue()` for real-time downstream publishing.
- Both receive `s.ProviderRateLimiter()` for rate-limit coordination.

## Verification
- `go test -count=1 -race ./...` — all 22 packages pass
- `make lint` — 0 errors
- `make build` — success
- Live Kubernetes verification: all 4 event types processing simultaneously, real-time cascading confirmed via consumer logs.
