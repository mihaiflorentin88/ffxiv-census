# Automatic Character Discovery & Dual-Source Scanner (Tomestone + Lodestone) — Implementation Plan

## Context
Currently, `ffxiv-census publish id-sweep` requires operators to manually specify both `--from` and `--to` ID ranges, and `IDSweep` only probes the Lodestone scraping client which is rate-limited and slower. We need automated character ID discovery that queries the database for the highest known character ID to automatically sweep `[max_id + 1, max_id + count]`, and a dual-source ingest engine that probes the high-throughput Tomestone.gg REST API first with automatic fallback to Lodestone HTML scraping on 404s.

---

## Approach

### 1. Document Plan in Repository
- Write this complete implementation plan to `docs/superpowers/plans/2026-08-17-auto-discovery-dual-source.md` to adhere to repository conventions.

### 2. Environment Variable & Secret Management
- **Target:** `config/config.go`, `config/config_test.go`, `.env` (gitignored).
- Add lightweight `.env` reader in `config.NewConfig()` that checks for a `.env` file in the working directory before reading Viper configuration, parsing `KEY=VALUE` lines and setting them via `os.Setenv` if currently unset.
- Create `.env` file in repository root with `TOMESTONE_API_TOKEN=<token>`.
- Ensure `.env` is listed in `.gitignore`.

### 3. Character Repository `MaxID` & Domain Service `MaxCharacterID`
- **Target:** `port/contract/character_repository.go`, `infrastructure/sqlite/repository/character.go`, `mock/repository/character.go`, `domain/census/service.go`.
- **Contract:** Add `MaxID(ctx context.Context) (uint32, error)` to `contract.CharacterRepository`.
- **SQLite Implementation:** Execute `SELECT COALESCE(MAX(id), 0) FROM characters WHERE deleted_at IS NULL`. Return `uint32(maxID), nil`.
- **Mock Implementation:** Calculate max ID from in-memory map keys in `mock/repository/character.go`.
- **Domain Service:** Add `MaxCharacterID(ctx context.Context) (uint32, error)` to `census.Service`, calling `s.characters.MaxID(ctx)`.
- **Tests:** Real SQLite table tests in `infrastructure/sqlite/repository/character_test.go` and unit tests in `domain/census/service_test.go`.

### 4. Domain Service `UpsertTomestoneCharacter`
- **Target:** `domain/census/service.go`, `domain/census/service_test.go`.
- **Method Signature:** `(s *Service) UpsertTomestoneCharacter(ctx context.Context, char *contract.TomestoneCharacter) error`.
- **Mapping Logic:**
  - Map `TomestoneCharacter` to `contract.CharacterRecord`:
    - `ID: char.ID`, `Name: char.Name`, `World: char.Server`, `Datacenter: char.Datacenter`, `Region: datacenter.RegionForDC(char.Datacenter)`
    - `Race: char.Race`, `Tribe: char.Tribe`, `Gender: char.Gender`, `GrandCompany: char.GrandCompany`
    - `FreeCompanyID: char.FreeCompanyID`, `FreeCompanyName: char.FreeCompanyName`
    - `UpdatedAt: time.Now().UTC()`
  - Map `TomestoneCharacter.Jobs` slice to `[]contract.ClassJobRecord`:
    - Iterate `char.Jobs`, map `Name`, `Level`, `Exp`, `ExpMax` to `contract.ClassJobRecord{CharacterID: char.ID, JobName: job.Name, Level: job.Level, ...}`
  - Persist via `s.characters.Upsert(ctx, rec, jobs)`.
- **Tests:** Table-driven test in `domain/census/service_test.go` validating correct field mapping, region derivation, job extraction, and persistence.

### 5. Dual-Source ID Sweep Ingest Handler
- **Target:** `domain/census/handler/event.go`, `domain/census/handler/idsweep.go`, `container/domain.go`, `domain/census/handler/idsweep_test.go`.
- **Payload:** Add `Source string `json:"source,omitempty"`` to `IDSweepPayload` (values: `"auto"`, `"tomestone"`, `"lodestone"`).
- **Constructor:** Update `NewIDSweep` in `domain/census/handler/idsweep.go`:
  ```go
  func NewIDSweep(lodestone contract.LodestoneClient, tomestone contract.TomestoneClient, svc *census.Service, logger contract.Logger) *IDSweep
  ```
- **Execution Flow in `Handle`:**
  - For each ID in `[payload.From, payload.To]`:
  - Check source mode:
    1. If `source == "tomestone"` or (`(source == "auto" || source == "")` and `tomestone != nil && tomestone.IsConfigured()`):
       - Call `tomestone.FetchCharacterProfile(ctx, id, false)`.
       - If found (`err == nil`): call `h.census.UpsertTomestoneCharacter(ctx, tChar)`, append `contract.QueueJob{Type: "achievement-census", Payload: ...}`.
       - If `errors.Is(err, contract.ErrNotFound)`:
         - If `source == "tomestone"`: record miss and proceed to next ID.
         - If `source == "auto"`: fall back to Lodestone probe.
       - If transient error: return error to trigger worker retry.
    2. Fallback / Direct Lodestone Probe (`source == "lodestone"` or fallback from auto 404):
       - Call `lodestone.FetchCharacter(ctx, id)`.
       - If found (`err == nil`): call `h.census.UpsertCharacter(ctx, lChar)`, append `achievement-census` job.
       - If `errors.Is(err, contract.ErrNotFound)`: record miss and continue.
       - If transient error: return error.
- **Container Wiring:** Update `container/domain.go` to pass `s.TomestoneClient()` to `handler.NewIDSweep`.
- **Tests:** Comprehensive tests in `domain/census/handler/idsweep_test.go` covering Tomestone-hit, Tomestone-404-fallback-to-Lodestone, double-404, explicit `"lodestone"`, and explicit `"tomestone"`.

### 6. Auto-Discovery CLI Publisher
- **Target:** `cmd/cli/publish.go`, `cmd/cli/publish_test.go`.
- **Flags:**
  - `--from uint32` (default 0)
  - `--to uint32` (default 0)
  - `--count uint32` (default 1000)
  - `--chunk-size uint32` (default 100)
  - `--source string` (default `"auto"`, options: `"auto"`, `"tomestone"`, `"lodestone"`)
- **Range Computation:**
  - If `--from == 0` and `--to == 0`:
    - Call `container.Load.CharacterRepository().MaxID(ctx)`.
    - `from = maxID + 1`
    - `to = maxID + count`
  - If `--from > 0` and `--to == 0`:
    - `to = from + count - 1`
  - If `--to > 0` and `--from == 0`:
    - `from = 1`
- **Publishing:** Divide `[from, to]` into chunks of `--chunk-size`, publishing `contract.QueueJob` with `IDSweepPayload{From: chunkStart, To: chunkEnd, Source: source}`.
- **Tests:** Unit tests in `cmd/cli/publish_test.go` verifying range calculations for all combinations of flags.

---

## Critical Files & Anchors

1. `config/config.go`: `NewConfig()` - adds .env file reader before Viper init.
2. `port/contract/character_repository.go`: `CharacterRepository` - adds `MaxID(ctx context.Context) (uint32, error)`.
3. `domain/census/service.go`: `Service` - adds `UpsertTomestoneCharacter` and `MaxCharacterID`.
4. `domain/census/handler/idsweep.go`: `IDSweep` - dual-source ingest probing logic with Tomestone priority and Lodestone fallback.
5. `cmd/cli/publish.go`: `newPublishIDSweepCmd` - dynamic ID boundary detection and chunk dispatch.

---

## Verification

### Unit & Race Tests
```bash
go test -race ./...
```

### Code Formatting & Linting
```bash
make fmt && PATH="$HOME/go/bin:$PATH" make lint
```

### Live CLI Verification
```bash
# 1. Build binary
make build

# 2. Test live Tomestone character fetch using token from .env
./bin/ffxiv-census tomestone character 36795950

# 3. Test automatic discovery publisher without manual --to
./bin/ffxiv-census publish id-sweep --count 200 --chunk-size 50 --source auto

# 4. Run ID sweep consumer and verify ingest
./bin/ffxiv-census consume id-sweep --max-jobs 4 --workers 2
```
