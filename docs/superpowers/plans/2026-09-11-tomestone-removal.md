# Tomestone removal: Lodestone-only census pipeline

Slug: `tomestone-removal`

## Context

Tomestone.gg (community-built index) caused a confirmed silent-skip bug: a Lodestone
ambiguous error (202 challenge/timeout) combined with a Tomestone 404 silently dropped
IDs because Tomestone's index is not authoritative for existence (fixed in `0c31b80`,
2026-09-10). The 48h window 2026-09-08→2026-09-10 lost characters to that bug and to the
queue routing losses fixed in `v1.15.0`. Owner decision (2026-09-11): **remove Tomestone
entirely** — The Lodestone is the single data source; transient Lodestone errors retry on
the queue ladder instead of consulting a non-authoritative fallback.

## Behavior contract

- The Lodestone is the only character-data source for `id-sweep`, `character-census`,
  `achievement-census`.
- A Lodestone transient error (challenge/timeout/429/5xx/dial) fails the delivery for the
  queue's infinite retry ladder — no fallback store, no skip.
- A genuine Lodestone 404 on `character-census` marks the character deleted immediately
  (Lodestone is authoritative). On `id-sweep` it skips the id without failing the chunk.
- Legacy queued payloads carrying `"source":"tomestone"` behave as `auto` (Lodestone
  authoritative). The `source` field becomes informational: `auto` | `lodestone`.
- `character-census` deletion no longer double-confirms with Tomestone: Lodestone 404 is
  final.
- Hidden/access-restricted profiles (Lodestone 403/hidden) are skipped as today
  (`census.ErrProfileHidden`); the Tomestone race-less fallback record goes away with it.

## Removal map

Delete:
- `port/contract/tomestone.go`, `infrastructure/tomestone/`, `mock/tomestone/`
- `cmd/cli/tomestone.go`, `docs/tomestone.md`
- `config/tomestone_test.go`, `container/tomestone_test.go`
- `TestProxyEventsNeedTomestone` (+ `proxyEventsNeedTomestone`)
- `contract.ProviderTomestone`

Modify:
- `config/config.go` + `config.toml`: drop `TomestoneConfig` + `[tomestone]` section
- `container/infrastructure.go` + `container/domain.go`: drop accessor + wiring
- `handler.NewIDSweep` / `NewCharacterCensus`: drop tomestone param; Lodestone-only logic
- `census.Service.UpsertTomestoneCharacter`: delete
- `worker.RunEventsWithProxy` / `replaceProxy` / `proxyWorkerLoop` /
  `waitForProviders` / `proxyWaitForProviders`: drop tomestone client param, dual waits
- `cmd/cli/consume.go`: drop tomestone client construction
- `cmd/cli/publish.go`: `--source` accepts `auto|lodestone`
- `cmd/http/ui/templates/character.html`: drop Tomestone link; `methodology.html`: reword
- Docs: README, events, architecture, census, container, lodestone, proxy, CONTEXT.md,
  AGENTS.md docs index

## Tests

- Delete Tomestone-specific handler/worker/config/container tests.
- Keep and re-run: Lodestone-authoritative tests (404-skip, mark-deleted, poison, retry
  semantics), worker rotation tests, queue tests.
- `go test ./... && go test -race` on touched packages; `make build`.

## Rollout

- No queue/topology migration. In-flight legacy payloads tolerated (source fallback).
- Deploy after the current backfill drains; no release-blocking dependency.
