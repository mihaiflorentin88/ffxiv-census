# UI Statistics Audit — Bug Fixes

## Objective

Fix four confirmed bugs affecting dashboard stats, world detail pages, and character directory filters. Correct documentation to reflect the canonical "new character" definition (Chocobo milestone, achievement 590).

## Bug 1: ActiveOnly filter is a no-op

`CharacterFilter.ActiveOnly` adds `deleted_at IS NULL` to the WHERE clause, but `Count()` already hardcodes `deleted_at IS NULL`. The flag has no effect — world detail and character directory show all non-deleted characters as "active."

**Fix:** Add `Since *time.Time` field to `CharacterFilter`. When set, SQL adds `latest_achievement_at >= $N`. `WorldDetail` and `CharacterList` use `Since` for activity-window filtering.

## Bug 2: Max Level shows total population

`Summary()` calls `Count(CharacterFilter{MinLevel: maxLevel})` but `MinLevel` was never implemented in the Postgres repository's filter builder. Every character is counted.

**Fix:** Add `MinLevel` handling: `id IN (SELECT character_id FROM character_jobs WHERE level >= $N)`.

## Bug 3: CountChocoboMilestones uses LEFT JOIN with first_seen_at fallback

The SQL uses `LEFT JOIN character_milestones` and falls back to `first_seen_at` for characters without a chocobo milestone. This inflates "New Characters (30d)" with recently-discovered characters who haven't earned the milestone. The mock also falls back to `first_seen_at` / `Count()` instead of checking actual milestone data.

**Fix:** Change to `INNER JOIN` — only count characters with achievement 590 and `achieved_at >= since`. Fix mock to iterate actual milestone data. Update REST API test to seed milestones instead of relying on `first_seen_at`.

## Bug 4 (minor): Dead SQL branch in CountExpansionsFiltered

`m.kind = 'expansion'` never matches (milestones use `expansion_msq`). Kept as defense-in-depth.

## Canonical Definitions

- **New character**: A character who earned the Chocobo milestone (achievement 590, "My Little Chocobo").
- **Active character**: A character whose `latest_achievement_at` is within the activity window (default 30 days).
- **Max level character**: A character with at least one job at or above `max_level` (default 100).
