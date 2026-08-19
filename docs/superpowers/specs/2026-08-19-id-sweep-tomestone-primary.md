# Spec: id-sweep Tomestone-Primary Source Strategy

## Date
2026-08-19

## Problem

The `id-sweep` handler's `auto` source mode probes The Lodestone first, falling back to Tomestone.gg on 404 or errors. Since Lodestone is rate-limited at 1 req/s (scraper, 2 internal HTTP calls per character) and Tomestone runs at 10 req/s (REST API), this ordering is the bottleneck for id-sweep throughput — the dominant event type in the queue.

## Design Decision

**Make Tomestone.gg the primary provider for `id-sweep` in auto mode.**

### Rationale

1. **id-sweep's role is discovery, not freshness.** The goal is to find which character IDs exist and ingest basic profiles. Stale data is acceptable because `character-census` will re-census with Lodestone later.

2. **Tomestone is 10x faster.** 10 req/s REST API vs 1 req/s scraper. For a discovery workload that probes millions of IDs, this is a 10x throughput improvement.

3. **Tomestone indexes a subset of characters.** Not all Lodestone characters are indexed by Tomestone. This is handled by the Tomestone-404 → Lodestone fallback path. If Tomestone doesn't have the character, we try Lodestone. If Lodestone is also unavailable, we retry the job later.

4. **No data loss.** Tomestone data is accepted as stale for id-sweep. The character-census cronjob handles authoritative, up-to-date profiles using Lodestone.

### character-census stays Lodestone-primary

`character-census` re-censuses known characters and needs authoritative, up-to-date profiles. Lodestone is the source of truth for character data. No change to character-census behavior.

## Behavioral Matrix

| Scenario | Tomestone | Lodestone | Behavior |
|---|---|---|---|
| Tomestone hit | ✅ | — | Upsert from Tomestone, chain jobs. No Lodestone call. |
| Tomestone 404, Lodestone hit | ❌ 404 | ✅ | Upsert from Lodestone, chain jobs. |
| Tomestone 404, Lodestone 404 | ❌ 404 | ❌ 404 | Confirmed not found. Skip ID. |
| Tomestone 404, Lodestone unavailable | ❌ 404 | ⏸️ | Return error to retry on Lodestone later. |
| Tomestone error, Lodestone hit | ❌ err | ✅ | Upsert from Lodestone, chain jobs. |
| Tomestone error, Lodestone 404 | ❌ err | ❌ 404 | Confirmed not found (Lodestone authoritative). Skip. |
| Tomestone error, Lodestone unavailable | ❌ err | ⏸️ | Return Tomestone error for queue retry. |
| Tomestone unavailable, Lodestone hit | ⏸️ | ✅ | Upsert from Lodestone, chain jobs. |
| Tomestone unavailable, Lodestone 404 | ⏸️ | ❌ 404 | Not found. Skip ID. |
| Both unavailable | ⏸️ | ⏸️ | Return error: all providers unavailable. |

## Source Logging

- Tomestone hit: `"source": "tomestone"`
- Lodestone hit (fallback): `"source": "lodestone"`
- Double 404: `"source": "tomestone+lodestone"`, `"status": "not_found"`

## Scope

- `domain/census/handler/idsweep.go` — auto-mode logic swap only
- `domain/census/handler/idsweep_test.go` — test updates
- Documentation updates (events.md, tomestone.md, lodestone.md, README.md)
- No changes to character-census, worker, rate limiter, container wiring, or CLI
