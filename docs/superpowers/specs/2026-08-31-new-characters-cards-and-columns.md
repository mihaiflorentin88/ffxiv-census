# New Characters (30d) Cards and Columns Across Analytics Pages

Date: 2026-08-31
Status: ready-for-agent

## Problem Statement

The census answers "how many characters are there?" well, but it never answers "how fast is it growing?". Every analytics page shows population and 30-day activity, yet a visitor cannot tell whether a world, datacenter, or region is gaining characters or stagnating. The influx signal exists only as a chart line on the dashboard — never as a number, and nowhere scoped by the filters a visitor has already chosen. Operators reading overall health and players choosing a world both lack a growth signal. Whatever we add must keep every analytics page on precomputed aggregates: per-request scans are exactly what the statistics snapshot removed, and the census is expected to grow toward 90 million characters.

## Solution

Every analytics surface gains a "New characters (30d)" figure with its trend: the count of characters that earned the chocobo milestone in the trailing 30 days, compared against the preceding 30 days. The dashboard gets a fourth stat card and a per-world column in its world drill-down; the races page gets a card that follows the cascading filters; the worlds page gets the same card plus a "New (30d)" column in the server rankings table; the existing per-world cards gain the trend. All numbers derive from the statistics snapshot's daily new-character series — whose stored lookback extends to 60 days so the comparison window exists — and no page ever queries raw character data.

## User Stories

1. As a site visitor on the dashboard, I want a "New characters (30d)" stat card, so that I can immediately see how many characters joined the census population this month.
2. As a site visitor on the dashboard, I want that card to compare the last 30 days against the previous 30, so that I can tell whether growth is accelerating or slowing.
3. As a site visitor expanding a region's world drill-down, I want a "New (30d)" column per world row, so that I can spot which worlds inside a region are growing.
4. As a site visitor on the races page, I want a new-characters card that reflects my selected region, so that the influx number matches the population charts I am looking at.
5. As a site visitor on the races page narrowing to a datacenter, I want the card to follow that selection, so that the number describes exactly the characters I filtered to.
6. As a site visitor on the races page narrowing to a single world, I want the card to show that world's influx, so that the number agrees with the rest of the page.
7. As a site visitor on the worlds page, I want a new-characters card that reflects my region and datacenter selections, so that the influx number describes my current selection.
8. As a site visitor scanning the server rankings table, I want a "New (30d)" column next to the active count, so that I can compare influx across all worlds at a glance.
9. As a site visitor on a per-world page, I want the existing new-characters card to also show its trend versus the previous 30 days, so that every surface tells the same growth story.
10. As a site visitor, I want the same new-character definition behind every number, so that figures agree across pages instead of contradicting each other.
11. As a site visitor, I want a shrinking influx rendered as clearly as a growing one, so that decline is not hidden.
12. As a site visitor during the census's early days, I want the card to stay honest when the previous window had no data, so that I am never shown a meaningless percentage.
13. As a site visitor while the snapshot refreshes, I want pages to serve the explicit unavailable state rather than zeros or errors, so that I always know whether the numbers are real.
14. As a site visitor, I want the trend to state both the absolute and the relative change, so that small and large populations are both readable.
15. As the site operator, I want all new-character numbers computed once per snapshot refresh, so that page latency stays flat as the census grows toward 90 million characters.
16. As the site operator, I want the extended daily-series lookback handled by the snapshot refresh itself, so that trend comparisons need no live queries and no extra scheduled jobs.
17. As the site operator, I want a snapshot schema version bump for the new lookback, so that an old binary can never misread a new snapshot and silently mislabel numbers.
18. As a developer, I want all page-level behavior tested at the HTTP seam with fixture snapshots, so that regressions on any surface fail as rendered-output diffs.
19. As a developer, I want the extended daily series and the schema migration covered by temp-database tests, so that the refresh SQL's new bound is proven against real Postgres.
20. As a developer, I want one shared window-sum computation behind every surface, so that dashboard, races, worlds, and per-world numbers can never drift apart.

## Implementation Decisions

- **Definition (locked during grilling):** a new character is one that earned the chocobo milestone, counted per UTC day — the glossary's New character. No new metric; first-seen-based counts are rejected because they measure ID-sweep progress, not player influx.
- **Metric source:** the statistics snapshot's scope-tagged daily new-character series (global- and world-scope granularity). Its stored lookback extends from 30 to 60 days; the series shape is unchanged.
- **Refresh:** the milestone daily query gets its own lookback bound (60 days) independent of the Activity window, which stays at 30 days and continues to bound the active and expansion aggregates. The snapshot schema version bumps to 2; a migration replaces the table's version check, and stored v1 snapshots are treated as stale — existing readers already reject unknown versions and serve the explicit unavailable state until the next refresh succeeds.
- **Window math:** current window = the trailing 30 UTC days ending on the snapshot's generation day; previous window = the 30 days before that; missing days count as zero.
- **Rollup:** one domain helper computes (current, previous) sums for any stats scope. Global and world scopes read their exact-scope series rows; region and datacenter scopes sum the member worlds implied by the world hierarchy (a datacenter belongs to exactly one region, so a combined region + datacenter selection reduces to the datacenter's world set).
- **Delta presentation:** signed absolute change plus percentage, marked as growth or shrink. When the previous window's total is zero, show the absolute count only; when both windows are zero, omit the trend line.
- **Surfaces:** dashboard — fourth stat card (global scope) plus a "New (30d)" column in the region drill-down table; races page — an additional card in the census-highlights panel, fully scoped by the cascading filters; worlds page — a fourth stat card plus a "New (30d)" column in the server rankings table; per-world pages — the existing card gains the trend line.
- **Render discipline:** cards and columns render only from the snapshot (snapshot-cache semantics unchanged); no live character queries in any render path; no new HTMX endpoints (filters are full-page GETs already); JSON API responses are unchanged.
- **Presentation:** cards follow the existing stat-card grid idiom and reuse the existing number and percent formatting helpers.

## Testing Decisions

- Good tests assert external behavior only: what a real HTTP request renders for a given snapshot — never internal helpers or template internals.
- **HTTP seam (primary):** controller tests drive real requests with fixture snapshots holding 60 days of known global- and world-scope dailies. Coverage: every surface; filter permutations (region, datacenter, world — precedence and combination); delta math (growth, decline, zero-previous, both-zero); the explicit unavailable state. Prior art: the existing dashboard handler tests that build fixture snapshots and assert rendered pages.
- **Postgres seam (refresh and schema):** temp-database tests running real SQL. Coverage: the daily series at the 60-day bound (rows emitted for global and world scopes, UTC day bucketing), the snapshot store/load/validate round-trip at schema version 2, and the migration (applies cleanly; stored v1 snapshots no longer load). Prior art: the existing repository temp-database tests.
- Strict TDD per repo discipline: failing test first, minimal code to pass, table-driven cases; `go test -race` and lint clean before commit.

## Out of Scope

- New-character counts sliced by race, tribe, or gender (would need new snapshot grouping branches).
- New-characters info on the expansions or characters pages.
- Sorting the worlds or drill-down tables by the new column.
- Changes to the JSON API; the existing global new-characters endpoint stays as is.
- Any first-seen-based metric, or any change to the Activity window's meaning.
- Backfilling history beyond the 60-day lookback.

## Further Notes

- The 90-million-character mandate is the reason for the snapshot-only rule: nothing here may reintroduce per-request scans. The snapshot shape is unchanged; only the daily-series lookback grows (roughly one extra month of world-per-day rows).
- With hourly refreshes, cards are at most about an hour stale. UTC-day buckets make the current day a partial bucket, so headline numbers are slightly conservative by design.
- UI copy says "New characters" — never "new players" (players own characters; the census counts characters). The glossary's avoid-list already records this.
