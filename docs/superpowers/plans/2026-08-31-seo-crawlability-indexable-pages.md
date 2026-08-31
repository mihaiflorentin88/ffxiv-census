# SEO Crawlability — Implementation Plan

> **Status:** Implemented and released in v1.11.16 (2026-08-31); all five tickets closed. Search Console submission and re-indexing remain operator actions.
Implements [docs/superpowers/specs/2026-08-31-seo-crawlability-indexable-pages.md](../specs/2026-08-31-seo-crawlability-indexable-pages.md) (parent issue: [#3](https://github.com/mihaiflorentin88/ffxiv-census/issues/3)).

Tracer-bullet tickets live in the issue tracker, each labeled `ready-for-agent`. Work the frontier: start with the unblocked ticket, then any ticket whose blockers are all done.

| # | Ticket | Blocked by | Delivers |
|---|--------|------------|----------|
| 1 | [Public base URL configuration](https://github.com/mihaiflorentin88/ffxiv-census/issues/4) | — | Prefactor: public-origin config key, default + env override, config tests |
| 2 | [Head metadata and root URL serves dashboard](https://github.com/mihaiflorentin88/ffxiv-census/issues/5) | #4 | Canonical/description/OG on every indexable page; root renders dashboard, canonical `/` |
| 3 | [Sitemap.xml endpoint from statistics snapshot](https://github.com/mihaiflorentin88/ffxiv-census/issues/6) | #4 | `/sitemap.xml`: static pages + per-world URLs, snapshot `lastmod`, degraded-safe |
| 4 | [Favicon asset and serving](https://github.com/mihaiflorentin88/ffxiv-census/issues/7) | #5 | Generated icon served at the conventional path, linked from the layout head |
| 5 | [Release and live verification](https://github.com/mihaiflorentin88/ffxiv-census/issues/8) | #5, #6, #7 | Semver release, live production checks, Search Console checklist handed to operator |

Sequencing notes:

- Tickets 2 and 3 are independent siblings once 1 lands; either order, or in parallel.
- Ticket 4's blocker on 5 exists only to serialize edits to the shared layout head.
- All tickets test at the single HTTP seam per the spec's testing decisions; strict TDD (red → green) per repo discipline.
- Post-deploy Search Console submission is an operator action, documented in ticket 5.
