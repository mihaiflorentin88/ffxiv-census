# Implementation Plan: New Characters (30d) Cards and Columns

Implements [docs/superpowers/specs/2026-08-31-new-characters-cards-and-columns.md](../specs/2026-08-31-new-characters-cards-and-columns.md) (parent issue: [#9](https://github.com/mihaiflorentin88/ffxiv-census/issues/9)).

Tracer-bullet tickets live in the issue tracker as sub-issues of #9, each labeled `ready-for-agent`. Work the frontier: start with the unblocked ticket, then any ticket whose blockers are all done.

| # | Ticket | Blocked by | Delivers |
|---|--------|------------|----------|
| 1 | [Shared window-sum helper + per-world card trend](https://github.com/mihaiflorentin88/ffxiv-census/issues/10) | — | One domain window computation (current/previous 30 days for any stats scope) and the per-world card upgraded to count + trend; with only 30 days stored the previous window reads zero and the card honestly shows the absolute count |
| 2 | [60-day new-character lookback + snapshot schema v2](https://github.com/mihaiflorentin88/ffxiv-census/issues/11) | #10 | The snapshot stores 60 days of daily new-character series under its own lookback bound; schema version 2 migration; stored v1 snapshots are rejected by readers until the next refresh; trend lines light up with real data |
| 3 | [Dashboard new-characters card + drill-down column](https://github.com/mihaiflorentin88/ffxiv-census/issues/12) | #10, #11 | Fourth dashboard stat card (global count + trend) and a per-world New (30d) column in the region drill-down table |
| 4 | [Races page filter-scoped new-characters card](https://github.com/mihaiflorentin88/ffxiv-census/issues/13) | #10, #11 | Census-highlights card whose number follows the region → datacenter → world selection |
| 5 | [Worlds page card + rankings New (30d) column](https://github.com/mihaiflorentin88/ffxiv-census/issues/14) | #10, #11 | Selection-scoped card (combined region + datacenter reduces to the datacenter's world set) and a per-world New (30d) column in the server rankings table |
| 6 | [Release and live verification](https://github.com/mihaiflorentin88/ffxiv-census/issues/15) | #12, #13, #14 | Semver release, arm64 image, Helm upgrade, rollout and live checks of every surface, documentation sync |

Sequencing notes:

- Ticket 1 before ticket 2 is deliberate: the helper windows every read, so the longer series can never leak a 60-day sum into a 30-day figure.
- Tickets 3–5 are independent siblings once 1 and 2 land; they touch disjoint page handlers and templates.
- All tickets test at the two pre-agreed seams: HTTP handler tests with fixture snapshots, and temp-database Postgres tests for refresh SQL, schema round-trip, and migration. Strict TDD (red → green) per repo discipline.
- Everything renders from the statistics snapshot; no live character queries in any render path (90M-character mandate).

## Status

- Tickets 1–5: implemented (one feature commit per review gate: helper, data layer, three page surfaces).
- Ticket 6: pending release.
