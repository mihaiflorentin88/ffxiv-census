# achievement-census handler — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development.

**Goal:** Ship the `achievement-census` handler, which fetches a character's achievements and runs the milestone filter + latest-achievement tracking via `CensusService.ProcessAchievements`. Also wire the one-time milestone-registry sync so `ProcessAchievements` never runs against an empty registry.

**Architecture:** A thin handler in `domain/census/handler/` that delegates to `CensusService.ProcessAchievements` (already built and tested in the domain-service phase). The milestone registry (`MilestoneSet`) is synced once at boot via the `CensusService()` container accessor — idempotent `INSERT OR IGNORE`, consistent with `SQLite()` running migrations on first use.

**Tech Stack:** Go 1.25. Existing: `contract.LodestoneClient.FetchAchievements`, `census.Service.ProcessAchievements` + `SyncMilestones`, `mock/lodestone`, `mock/repository`.

---

## Tasks

### Task 1: Milestone registry sync at boot

**Files:** `container/domain.go`

Add to the `CensusService()` accessor, after constructing the service (import `context`, `fmt`, `logging`):

```go
	// Seed the milestone registry (idempotent) so achievement processing never
	// runs against an empty registry.
	if err := svc.SyncMilestones(context.Background()); err != nil {
		logging.Error("container.census", fmt.Sprintf("failed to sync milestones: %v", err))
	}
```

Commit: `feat(container): sync milestone registry at boot`.

### Task 2: achievement-census handler

**Files:** `domain/census/handler/achievement.go`, `domain/census/handler/achievement_test.go`

```go
package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// AchievementCensus fetches a character's achievements and runs the milestone
// filter + latest-achievement tracking. It is a leaf event (no chained jobs).
type AchievementCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
}

func NewAchievementCensus(lodestone contract.LodestoneClient, svc *census.Service) *AchievementCensus {
	return &AchievementCensus{lodestone: lodestone, census: svc}
}

func (h *AchievementCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p AchievementCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("achievement-census payload: %w", err)
	}
	list, all, err := h.lodestone.FetchAchievements(ctx, p.CharacterID)
	if err != nil {
		return nil, fmt.Errorf("achievement-census fetch %d: %w", p.CharacterID, err)
	}
	if _, err := h.census.ProcessAchievements(ctx, p.CharacterID, list, all); err != nil {
		return nil, fmt.Errorf("achievement-census process %d: %w", p.CharacterID, err)
	}
	return nil, nil
}
```

Test covers: fetch → milestone recorded, latest set, private flag propagated; a fetch error returns an error (→ worker Retry).

Commit: `feat(handler): achievement-census handler`.

### Task 3: Register the handler

**Files:** `container/domain.go`

```go
	reg.Register(handler.EventAchievementCensus, handler.NewAchievementCensus(s.LodestoneClient(), s.CensusService()))
```

Commit: `feat(container): register achievement-census handler`.

### Task 4: Docs

**Files:** `docs/events.md`

Mark `achievement-census` as implemented in the status table; note the leaf-event behavior.

Commit: `docs: mark achievement-census implemented`.

### Task 5: Verification + oracle review

`go test ./... -race`, `make lint`, `make build`; smoke test `consume achievement-census --help`.

---

## Gotchas

1. **Registry sync is a side effect in the accessor**, matching `SQLite()`'s migration-on-first-use precedent. It runs once (lazy memoization) and is idempotent.
2. **`FetchAchievements` on a private profile** returns an empty list + `AllAchievementInfo{Private: true}` (godestone maps 403→Private). `ProcessAchievements` already handles the private case (preserve milestones + latest, mark private).
3. **The handler is a leaf** — `ProcessAchievements` returns the milestones, but the handler discards them (returns `nil, nil`) since nothing chains off achievement-census.
