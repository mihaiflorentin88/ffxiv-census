# Cascading UI Filters — Implementation Plan

## Context

Filter dropdowns on `/ui/races` and `/ui/worlds` show all options regardless of parent
filter selection. Need cascading: Region narrows DCs, DC narrows Worlds.

## Step 1: Add helper functions to `cmd/http/ui/world_data.go`

Add `DCsForRegion(region string) []string` and `WorldsForDC(dc string) []string`.
Both derive from `worldDatacenter` map, deduplicate, sort, return nil for unknown input.

## Step 2: Fix DC dropdown in `cmd/http/ui/races.go`

When `selectedRegion != ""`, use `DCsForRegion(selectedRegion)` instead of building dcSet
from all worldDatacenter entries.

## Step 3: Fix World dropdown in `cmd/http/ui/races.go`

When `selectedDC != ""`, use `WorldsForDC(selectedDC)`. When only `selectedRegion != ""`,
aggregate worlds from all DCs in that region.

## Step 4: Fix DC dropdown in `cmd/http/ui/worlds.go`

When `selectedRegion != ""`, use `DCsForRegion(selectedRegion)`.

## Step 5: Add tests

- `cmd/http/ui/world_data_test.go`: table-driven tests for DCsForRegion and WorldsForDC
- `cmd/http/ui/races_test.go`: region=EU shows only EU DCs; region=EU&dc=Chaos shows only Chaos worlds
- `cmd/http/ui/worlds_test.go`: region=NA shows only NA DCs

## Step 6: Update documentation

- `docs/ui.md`: document cascading filter behavior for races and worlds pages
- `docs/superpowers/specs/2026-08-19-cascading-ui-filters.md`: design spec
- `docs/superpowers/plans/2026-08-19-cascading-ui-filters.md`: this plan

## Step 7: Verify

- `make test && make lint`
- Manual: visit /ui/races, select Region=EU, confirm DC dropdown shows only Chaos & Light

## Step 8: Commit & Release

- Commit all changes (code, tests, specs, plans)
- Follow release procedure: fetch tags, bump version, push tag, build Docker, deploy
