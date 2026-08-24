# Web UI & Census Dashboards

`ffxiv-census` provides a server-rendered, interactive Web UI backed by a bounded statistics snapshot, pure Go templates, HTMX, vendored Chart.js, and a dark FFXIV-themed design system.

## 1. Architecture & Design Principles

- **Zero CDN Dependencies**: All CSS and JavaScript (HTMX v2.0.4, Chart.js v4.4.7 UMD) are vendored in `cmd/http/ui/assets/` and compiled directly into the binary via `//go:embed`.
- **Pure Go Rendering**: Built with Go standard library `html/template` and `net/http` ServeMux. No Node.js, Webpack, npm, or frontend build toolchains are required.
- **Hexagonal Isolation**: UI controllers live in `cmd/http/ui/` and resolve aggregate data through `container.Load.UIStatsService()`. Character list/detail routes continue to use `CensusService`; queue health uses `Queue`. Domain logic remains tech-agnostic and decoupled from HTML presentation.
- **Progressive Enhancement via HTMX**: Client-side interactions (such as regional datacenter/world drill-downs and navigation search) use HTMX partial swaps without full page reloads.
- **Bounded Request Work**: Aggregate pages never scan `characters`, `character_jobs`, or achievement tables during an HTTP request. A scheduled read-model refresh does the expensive work once, while each process serves a cached immutable snapshot.

## 2. Route Inventory

| Route | Method | Description |
|---|---|---|
| `/` | `GET` | Redirects to `/ui/dashboard` |
| `/ui/dashboard` | `GET` | Executive overview: responsive stat-card grid (total population, 30-day active ratio, ingest status), race distribution doughnut chart, expansion MSQ completion card, 30-day new-character line chart, and region summary with world drill-down. All aggregate data comes from the cached statistics snapshot. |
| `/ui/partials/world-breakdown` | `GET` | HTMX partial returning world and datacenter rows for a requested region (`?region=NA`) |
| `/ui/races` | `GET` | Playable race demographics with cascading region/DC/world filters, global percentage shares, active ratios, and demographic charts. Filtering selects precomputed snapshot groups and performs no aggregate database query. |
| `/ui/worlds` | `GET` | Global server rankings table with interactive region/datacenter filters |
| `/ui/worlds/{world}` | `GET` | World detail page: total population, active players (30d), new characters (chocobo milestone 590 in last 30 days), race breakdown, MSQ completions, and 30-day new-character timeline |
| `/ui/expansions` | `GET` | MSQ story completion funnel (A Realm Reborn, Heavensward, Stormblood, Shadowbringers, Endwalker, Dawntrail) with retention and drop-off metrics |
| `/ui/characters/{id}` | `GET` | Detailed character profile with Dawntrail Lv 100 job matrix, story milestone timeline, Free Company badge, and external links |
| `/ui/characters` | `GET` | Paginated directory browser of discovered player characters |
| `/ui/characters/search` | `GET` | Global search handler: numeric IDs redirect directly to `/ui/characters/{id}`, text queries filter by character name |
| `/ui/assets/*` | `GET` | Static asset file server (`styles.css`, `htmx.min.js`, `chart.umd.min.js`) |

## 3. Statistics Snapshot

`refresh ui-stats` builds one versioned JSON read model in `ui_stats_snapshots`. It computes global, region, datacenter, and world population totals; demographic groups; expansion completion counts; and the bounded 30-day Chocobo-milestone series. Refreshes use a PostgreSQL advisory lock, a repeatable-read transaction, and an atomic single-row upsert, so readers see either the old complete snapshot or the new complete snapshot.

```bash
# Build/replace the current snapshot immediately
./bin/ffxiv-census refresh ui-stats
```

The Helm chart runs this command hourly at minute 17 with `concurrencyPolicy: Forbid`. Production measurements at roughly 208,000 characters showed a 6.9-second refresh, supporting the shorter interval with substantial headroom. Each server process reloads the database row no more frequently than `cache_ttl`; concurrent cold loads are coalesced. If a reload fails after a successful load, the last good snapshot remains available. A cold process with no snapshot returns `503 Service Unavailable` with `Retry-After`; there is deliberately no fallback to unbounded aggregate queries.

```toml
[census.ui_stats]
cache_ttl       = "1m"
stale_warning   = "12h"
refresh_timeout = "2h"
```

Aggregate HTML and HTMX responses include a query-aware `ETag`, `Cache-Control`, and `Vary: HX-Request, Accept-Encoding`. The page shell displays the snapshot generation time and warns when it exceeds `stale_warning`. This response contract is suitable for a future reverse proxy such as Varnish without making it a runtime dependency.

Operational checks:

```bash
./bin/ffxiv-census refresh ui-stats
curl -i http://localhost:8080/ui/dashboard
curl -I http://localhost:8080/ui/races
curl -s http://localhost:8080/metrics | grep ui_stats
```

Investigate `ui_stats_refresh_total{result="error"}`, a growing `ui_stats_snapshot_age_seconds`, or repeated HTTP 503s. The most recent complete snapshot is safe to serve during a failed refresh.

## 4. Directory Layout

```
cmd/http/ui/
├── assets/
│   ├── chart.umd.min.js    # Vendored Chart.js v4.4.7 UMD bundle
│   ├── htmx.min.js         # Vendored HTMX v2.0.4 library
│   └── styles.css          # Dark FFXIV design system & responsive layout styles
├── templates/
│   ├── layout.html         # Base shell (navbar, search, header, footer, script tags)
│   ├── dashboard.html      # Headline stats, 30-day time-series chart, region table
│   ├── races.html          # Race & clan demographics with race, tribe, gender, and race×gender doughnut charts
│   ├── worlds.html         # Global worlds & datacenters table with active ratios
│   ├── expansions.html     # MSQ expansion completion funnel & drop-off rates
│   ├── character.html      # Full character profile (jobs grid, milestones, FC, badges)
│   ├── characters_list.html# Paginated character directory and search results
│   └── partials/
│       ├── world_drilldown.html # HTMX partial for region -> DC -> world drilldown
│       └── search_results.html  # HTMX partial for live character search
├── routes.go               # ServeMux route registration and embedded FS handler
├── controller.go           # Core UI controller & template renderer
├── dashboard.go            # Controller for /ui/dashboard & /ui/partials/world-breakdown
├── races.go                # Controller for /ui/races
├── worlds.go               # Controller for /ui/worlds
├── expansions.go           # Controller for /ui/expansions
├── character.go            # Controller for /ui/characters/{id} & /ui/characters/search
├── world_data.go           # World, Datacenter, and Region mapping + cascading filter helpers
├── template_helpers.go     # Template helper functions (formatting numbers, dates, job roles)
└── ui_test.go              # Table-driven HTTP handler test suite
```

## 5. Theme & Styling System

The custom dark theme (`styles.css`) is inspired by the FINAL FANTASY XIV UI aesthetic:
- **Backgrounds**: `#0b0e14` (Deep Aether Void) and `#141923` (Card Surface)
- **Accents**: `#d4af37` (Eorzean Gold) and `#38bdf8` (Aether Cyan)
- **Status Colors**: `#22c55e` (Active Green) and `#ef4444` (Inactive/Deleted Red)
- **Role Colors**: Distinct color coding for Tank (`#3b82f6`), Healer (`#10b981`), Melee DPS (`#ef4444`), Physical Ranged (`#f97316`), Magic Ranged (`#a855f7`), Crafter (`#14b8a6`), and Gatherer (`#eab308`).

## 6. Cascading Filters

The `/ui/races` and `/ui/worlds` pages support cascading filter dropdowns that narrow
options based on parent selections. The hierarchy is **Region → Datacenter → World**.

### Races Page (`/ui/races`)

Three `<select>` dropdowns in a single form. Selecting a region updates the Datacenter
dropdown to show only DCs in that region; selecting a DC updates the World dropdown to
show only worlds in that DC. When no parent filter is selected, all options are shown.

### Worlds Page (`/ui/worlds`)

Region is selected via button pills; the Datacenter `<select>` dropdown shows only DCs
belonging to the selected region. When no region is selected, all DCs are shown.

### Implementation

Filter lists are derived server-side using `DCsForRegion()` and `WorldsForDC()` helper
functions in `cmd/http/ui/world_data.go`, which map the static FFXIV world→datacenter→region
hierarchy from the `worldDatacenter` map. Each filter change triggers a full page reload
via `onchange="this.form.submit()"`.
