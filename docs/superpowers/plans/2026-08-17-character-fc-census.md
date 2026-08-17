# character-census + fc-census handlers — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development.

**Goal:** Ship the last two ingest handlers. `character-census` re-censuses a known character (fetch → upsert, or mark deleted on 404) and chains `achievement-census` + `fc-census`. `fc-census` fetches a free company and upserts its record. Also add a `publish character-census --older-than` recheck cron.

**Architecture:** Both are thin handlers in `domain/census/handler/` delegating to `census.Service`. Two small service methods are added (`MarkCharacterDeleted`, `UpsertFreeCompany`). Member-list re-census (fc-census → character-census for stale members) is **deferred**: it needs `FetchFreeCompanyMembers`, which the `LodestoneClient` contract does not yet expose.

**Tech Stack:** Go 1.25. Existing: `contract.LodestoneClient` (FetchCharacter/FetchAchievements/FetchFreeCompany), `census.Service` (UpsertCharacter/ProcessAchievements/SyncMilestones), `CharacterRepository.ListStale`, `FreeCompanyRepository`, `mock/lodestone`, `mock/repository`.

---

## Tasks

### Task 1: CensusService — MarkCharacterDeleted + UpsertFreeCompany

`domain/census/service.go`:

```go
// MarkCharacterDeleted records that a character no longer exists on Lodestone.
func (s *Service) MarkCharacterDeleted(ctx context.Context, id uint32, at time.Time) error {
	return s.characters.MarkDeleted(ctx, id, at)
}

// UpsertFreeCompany converts a Lodestone free company into a record and persists it.
func (s *Service) UpsertFreeCompany(ctx context.Context, fc *godestone.FreeCompany) error {
	return s.freeCompanies.Upsert(ctx, toFreeCompanyRecord(fc))
}

func toFreeCompanyRecord(fc *godestone.FreeCompany) contract.FreeCompanyRecord {
	rec := contract.FreeCompanyRecord{
		ID:          fc.ID,
		Name:        fc.Name,
		World:       fc.World,
		Datacenter:  fc.DC,
		MemberCount: fc.ActiveMemberCount,
		LastSeenAt:  time.Now().UTC(),
	}
	if !fc.Formed.IsZero() {
		rec.FormedAt = &fc.Formed
	}
	return rec
}
```

Commit: `feat(census): mark-deleted and free-company upsert`.

### Task 2: Payload types + job constructors

`domain/census/handler/event.go`:

```go
type CharacterCensusPayload struct {
	CharacterID uint32 `json:"character_id"`
}

func CharacterCensusJob(characterID uint32) contract.QueueJob {
	b, _ := json.Marshal(CharacterCensusPayload{CharacterID: characterID})
	return contract.QueueJob{Type: EventCharacterCensus, Payload: b}
}

type FreeCompanyCensusPayload struct {
	FCID string `json:"fc_id"`
}

func FreeCompanyCensusJob(fcID string) contract.QueueJob {
	b, _ := json.Marshal(FreeCompanyCensusPayload{FCID: fcID})
	return contract.QueueJob{Type: EventFreeCompanyCensus, Payload: b}
}
```

Commit: `feat(handler): character-census and fc-census payloads`.

### Task 3: character-census handler

`domain/census/handler/character.go`:

```go
type CharacterCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
}

func NewCharacterCensus(lodestone contract.LodestoneClient, svc *census.Service) *CharacterCensus {
	return &CharacterCensus{lodestone: lodestone, census: svc}
}

func (h *CharacterCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p CharacterCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("character-census payload: %w", err)
	}
	char, err := h.lodestone.FetchCharacter(ctx, p.CharacterID)
	if errors.Is(err, contract.ErrCharacterNotFound) {
		if derr := h.census.MarkCharacterDeleted(ctx, p.CharacterID, time.Now().UTC()); derr != nil {
			return nil, derr
		}
		return nil, nil // deleted: no chained jobs
	}
	if err != nil {
		return nil, fmt.Errorf("character-census fetch %d: %w", p.CharacterID, err)
	}
	if err := h.census.UpsertCharacter(ctx, char); err != nil {
		return nil, fmt.Errorf("character-census upsert %d: %w", p.CharacterID, err)
	}
	next := []contract.QueueJob{AchievementCensusJob(p.CharacterID)}
	if char.FreeCompanyID != "" {
		next = append(next, FreeCompanyCensusJob(char.FreeCompanyID))
	}
	return next, nil
}
```

Commit: `feat(handler): character-census handler`.

### Task 4: fc-census handler

`domain/census/handler/free_company.go`:

```go
type FreeCompanyCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
}

func NewFreeCompanyCensus(lodestone contract.LodestoneClient, svc *census.Service) *FreeCompanyCensus {
	return &FreeCompanyCensus{lodestone: lodestone, census: svc}
}

func (h *FreeCompanyCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p FreeCompanyCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("fc-census payload: %w", err)
	}
	fc, err := h.lodestone.FetchFreeCompany(ctx, p.FCID)
	if err != nil {
		return nil, fmt.Errorf("fc-census fetch %s: %w", p.FCID, err)
	}
	if err := h.census.UpsertFreeCompany(ctx, fc); err != nil {
		return nil, fmt.Errorf("fc-census upsert %s: %w", p.FCID, err)
	}
	return nil, nil // leaf (member chaining deferred)
}
```

Commit: `feat(handler): fc-census handler`.

### Task 5: Register handlers + tests

Register in `container/domain.go` `Handlers()`. Add handler tests (character: upsert+chain, 404→deleted; fc: upsert, fetch error).

Commit: `feat(container): register character-census and fc-census`.

### Task 6: publish character-census recheck

`cmd/cli/publish.go` — subcommand `character-census --older-than <duration> --limit N`: query `ListStale` and enqueue `character-census` jobs.

Commit: `feat(cli): publish character-census recheck`.

### Task 7: Docs

`docs/events.md` — mark both handlers implemented; note deferred member-list chaining.

Commit: `docs: mark character-census and fc-census implemented`.

### Task 8: Verification + oracle review

`go test ./... -race`, `make lint`, `make build`.

---

## Gotchas

1. **`godestone.FreeCompany.Formed` is `time.Time` (not a pointer)** — guard with `IsZero()` before setting `FormedAt`. `ActiveMemberCount` is `uint32`.
2. **FC 404 is not mapped to a sentinel** (only `FetchCharacter` is). An fc-census on a deleted FC retries then fails — acceptable for now (deferred).
3. **Member-list chaining is deferred** — it needs `FetchFreeCompanyMembers` (channel-based godestone API) added to the contract/adapter/mock, which is a larger change than this phase.
