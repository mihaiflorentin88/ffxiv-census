# Max Level Gauge and Configurable Expansions

## Context
The user requested adding a new gauge on the dashboard, similar to the "Total Population" card, to display the total number of characters with at least one job at the maximum level. To ensure the application is future-proof, the max level and the list of expansion milestones must be driven by `config.toml`, allowing new expansions or level cap increases to be applied without code changes.

## Approach

1.  **Configuration (`config/config.go`, `config/config.toml`)**:
    -   Add `MaxLevel uint32` and `Expansions []ExpansionConfig` to `CensusConfig`.
    -   Define `ExpansionConfig` with fields: `Name`, `Version`, `FinalQuest`, `Icon`, `LevelCap`, and `AchievementID` (uint32).
    -   Update `config.toml` with `max_level = 100` and `[[census.expansions]]` blocks matching the currently hardcoded expansions.

2.  **Domain & Contracts**:
    -   **Character Filter**: Add `MinLevel uint32` to `contract.CharacterFilter` in `port/contract/character_repository.go`.
    -   **SQLite Repo**: Update `characterFilterWhere` in `infrastructure/sqlite/repository/character.go`. If `f.MinLevel > 0`, append an `EXISTS (SELECT 1 FROM character_jobs cj WHERE cj.character_id = characters.id AND cj.level >= ?)` clause.
    -   **Domain Config Model**: Add `ExpansionConfig` struct in `domain/census/service.go`. Add `func (s *Service) SetConfig(maxLevel uint32, expansions []ExpansionConfig)` and getters `MaxLevel()` and `Expansions()`.
    -   **Service Summary**: Update `func (s *Service) Summary(ctx context.Context)` signature to return `(total, active, maxLevelCount int64, err error)`. Compute `maxLevelCount` using `s.characters.Count(ctx, contract.CharacterFilter{MinLevel: s.maxLevel})`.
    -   **Milestone Syncing**: Update `func (s *Service) SyncMilestones(ctx)` to dynamically build the registry array using `s.Expansions()` (plus the static Chocobo milestone 590) instead of relying on a global slice. Remove the hardcoded `MilestoneSet` from `domain/census/milestone.go`.

3.  **UI & REST API Wiring**:
    -   **Container Wiring**: In `container/domain.go`, call `svc.SetConfig(...)` right after creating the `Service`, translating `config.ExpansionConfig` to `census.ExpansionConfig`.
    -   **REST API**: Update `cmd/http/app/census/handler/census.go` to handle the new `Summary` return tuple and update `response.CensusSummary` to include `MaxLevelCharacters` (json: `max_level_characters`).
    -   **Dashboard Template**: In `cmd/http/ui/dashboard.go`, update `DashboardViewData` to hold `MaxLevelCharacters` and `MaxLevel`. In `dashboard.html`, add a new stat card mirroring "Total Population" to show this count and `Lv. {{.Data.MaxLevel}}`.
    -   **Methodology & Expansions Templates**: Update `cmd/http/ui/expansions.go` and `cmd/http/ui/controller.go` to fetch expansions via `c.svc.Expansions()`. Pass them to `templates/expansions.html` and `templates/methodology.html`. Update `methodology.html` to range over `.Data.Expansions` rather than using hardcoded HTML rows. Replace the hardcoded `lvl >= 100` in `cmd/http/ui/character.go` with `c.svc.MaxLevel()`.

## Critical files & anchors
- `config/config.go` - Add `MaxLevel` and `Expansions` slices to `CensusConfig`.
- `infrastructure/sqlite/repository/character.go` - Add `EXISTS` subquery to `characterFilterWhere` for `MinLevel`.
- `domain/census/service.go` - `Summary` signature update; dynamic `SyncMilestones`.
- `cmd/http/ui/templates/dashboard.html` - Add Max Level character stat card.
- `cmd/http/ui/templates/methodology.html` - Range over `.Data.Expansions` in the MSQ table.

## Verification
- Run `make test` to ensure `Summary`, config loading, and SQL query builder logic is validated. 
- Ensure `TestCharacterRepository_Counts` checks the `MinLevel` filter logic.
- Run `make build` and execute `./bin/ffxiv-census server --start`.
- Verify the dashboard page displays the new Max Level Characters stat card accurately.
- Verify the methodology page MSQ table renders dynamically from the config file.

## Assumptions & contingencies
- The Chocobo Milestone (`ID: 590`) remains a hardcoded foundational milestone not meant to be modified by users; only MSQ progression capstones are moved to `config.toml`.
- Changing `max_level` in the configuration will immediately reflect globally across all UI views and REST API responses upon service restart.
