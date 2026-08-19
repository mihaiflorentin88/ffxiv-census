# Plan: Make Tomestone Primary for id-sweep Auto Mode

## Date
2026-08-19

## Summary

Swap the provider priority in `id-sweep` auto mode from Lodestone-primary to Tomestone-primary. `character-census` remains Lodestone-primary.

## Rationale

- **Tomestone**: 10 req/s REST API — fast character discovery.
- **Lodestone**: 1 req/s scraper (2 internal HTTP calls per character) — bottleneck for id-sweep throughput.
- **id-sweep role**: character ID discovery, not data freshness. Stale data is acceptable because `character-census` re-censuses with Lodestone later.
- **character-census role**: authoritative profile updates. Stays Lodestone-primary.

## Implementation Steps

### 1. Swap auto-mode logic in `idsweep.go`

**File:** `domain/census/handler/idsweep.go`, the `else` branch (~lines 120–205).

New flow:
1. If Tomestone available → probe Tomestone first
2. Tomestone 404 → try Lodestone fallback (character may exist but not be indexed by Tomestone)
3. Tomestone error → try Lodestone fallback
4. Tomestone 404 + Lodestone unavailable → return error for retry on Lodestone later
5. Tomestone error + Lodestone 404 → confirmed not found (Lodestone is authoritative for existence)
6. Tomestone unavailable/paused → probe Lodestone directly
7. Double 404 → confirmed missing, skip

### 2. Update tests in `idsweep_test.go`

Flip all dual-source test expectations:

| Old Test Name | New Test Name | Key Change |
|---|---|---|
| `TestIDSweep_LodestonePrimary_Success` | `TestIDSweep_TomestonePrimary_Success` | Tomestone hit, no Lodestone call |
| `TestIDSweep_Lodestone404_FallbackToTomestoneHit` | `TestIDSweep_Tomestone404_FallbackToLodestoneHit` | Tomestone 404, Lodestone hit |
| `TestIDSweep_LodestoneError_FallbackToTomestone_Success` | `TestIDSweep_TomestoneError_FallbackToLodestone_Success` | Tomestone error, Lodestone hit |
| `TestIDSweep_LodestonePaused_UsesTomestoneDirectly` | `TestIDSweep_TomestonePaused_UsesLodestoneDirectly` | Tomestone paused, Lodestone direct |
| `TestIDSweep_LodestoneError_Tomestone404_ReturnsErrorForLodestoneRetry` | `TestIDSweep_Tomestone404_LodestonePaused_ReturnsErrorForRetry` | Tomestone 404, Lodestone paused → error |
| `TestIDSweep_LodestonePaused_Tomestone404_ReturnsErrorForLodestoneRetry` | `TestIDSweep_TomestoneError_Lodestone404_ConfirmedNotFound` | Tomestone error, Lodestone 404 → skip |
| `TestIDSweep_DualSource_Double404` | (same name) | Explicit Tomestone 404 first, then Lodestone 404 |
| `TestIDSweep_TomestoneTransientError` | `TestIDSweep_TomestoneTransientError_FallbackToLodestone` | Tomestone error falls back to Lodestone |

New test added:
- `TestIDSweep_TomestoneHit_NoLodestoneCall` — verifies Lodestone is never called when Tomestone succeeds.

### 3. Update documentation

- `docs/events.md` — Split dual-source section into id-sweep (Tomestone primary) and character-census (Lodestone primary) descriptions.
- `docs/tomestone.md` — Update "Dual-Source Ingest & Fallback" to reflect Tomestone as primary for id-sweep.
- `docs/lodestone.md` — Update "Primary Provider & Fallback Integration" to reflect Lodestone as fallback for id-sweep.
- `README.md` — Update Ingest Events table and Provider Coordination section.

### 4. Write plan and spec documents

- `docs/superpowers/plans/2026-08-19-id-sweep-tomestone-primary.md` (this file)
- `docs/superpowers/specs/2026-08-19-id-sweep-tomestone-primary.md`

## Files Modified

- `domain/census/handler/idsweep.go` — auto-mode logic swap
- `domain/census/handler/idsweep_test.go` — test updates + new test
- `docs/events.md` — documentation update
- `docs/tomestone.md` — documentation update
- `docs/lodestone.md` — documentation update
- `README.md` — documentation update

## Files NOT Modified

- `domain/census/handler/character.go` — character-census stays Lodestone-primary
- `domain/census/worker/worker.go` — `isEventTypeAvailable` already correct
- `infrastructure/provider/limiter.go` — rate limiter unchanged
- `container/domain.go` — wiring unchanged
- `cmd/cli/` — CLI commands unchanged
- `config/` — configuration unchanged
