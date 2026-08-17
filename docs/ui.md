# Web UI & Census Dashboards

`ffxiv-census` provides a server-rendered, interactive Web UI and real-time census dashboard built with pure Go templates, HTMX, vendored Chart.js, and a dark FFXIV-themed design system.

## 1. Architecture & Design Principles

- **Zero CDN Dependencies**: All CSS and JavaScript (HTMX v2.0.4, Chart.js v4.4.7 UMD) are vendored in `cmd/http/ui/assets/` and compiled directly into the binary via `//go:embed`.
- **Pure Go Rendering**: Built with Go standard library `html/template` and `net/http` ServeMux. No Node.js, Webpack, npm, or frontend build toolchains are required.
- **Hexagonal Isolation**: UI controllers live in `cmd/http/ui/` and resolve domain data through `container.Load.CensusService()` and `container.Load.Queue()`. Domain logic remains tech-agnostic and decoupled from HTML presentation.
- **Progressive Enhancement via HTMX**: Client-side interactions (such as regional datacenter/world drill-downs and navigation search) use HTMX partial swaps without full page reloads.

## 2. Route Inventory

| Route | Method | Description |
|---|---|---|
| `/` | `GET` | Redirects to `/ui/dashboard` |
| `/ui/dashboard` | `GET` | Executive overview: total population, 30-day active ratio, ingest status, 30-day time-series line chart, and region summary |
| `/ui/partials/world-breakdown` | `GET` | HTMX partial returning world and datacenter rows for a requested region (`?region=NA`) |
| `/ui/races` | `GET` | Playable race demographics, global percentage shares, active ratios, and Chart.js doughnut chart |
| `/ui/worlds` | `GET` | Global server rankings table with interactive region/datacenter filters |
| `/ui/expansions` | `GET` | MSQ story completion funnel (A Realm Reborn, Heavensward, Stormblood, Shadowbringers, Endwalker, Dawntrail) with retention and drop-off metrics |
| `/ui/characters/{id}` | `GET` | Detailed character profile with Dawntrail Lv 100 job matrix, story milestone timeline, Free Company badge, and external links |
| `/ui/characters` | `GET` | Paginated directory browser of discovered player characters |
| `/ui/characters/search` | `GET` | Global search handler: numeric IDs redirect directly to `/ui/characters/{id}`, text queries filter by character name |
| `/ui/assets/*` | `GET` | Static asset file server (`styles.css`, `htmx.min.js`, `chart.umd.min.js`) |

## 3. Directory Layout

```
cmd/http/ui/
├── assets/
│   ├── chart.umd.min.js    # Vendored Chart.js v4.4.7 UMD bundle
│   ├── htmx.min.js         # Vendored HTMX v2.0.4 library
│   └── styles.css          # Dark FFXIV design system & responsive layout styles
├── templates/
│   ├── layout.html         # Base shell (navbar, search, header, footer, script tags)
│   ├── dashboard.html      # Headline stats, 30-day time-series chart, region table
│   ├── races.html          # Race & clan breakdown tables and progress bars
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
├── world_data.go           # World, Datacenter, and Region mapping utilities
├── template_helpers.go     # Template helper functions (formatting numbers, dates, job roles)
└── ui_test.go              # Table-driven HTTP handler test suite
```

## 4. Theme & Styling System

The custom dark theme (`styles.css`) is inspired by the FINAL FANTASY XIV UI aesthetic:
- **Backgrounds**: `#0b0e14` (Deep Aether Void) and `#141923` (Card Surface)
- **Accents**: `#d4af37` (Eorzean Gold) and `#38bdf8` (Aether Cyan)
- **Status Colors**: `#22c55e` (Active Green) and `#ef4444` (Inactive/Deleted Red)
- **Role Colors**: Distinct color coding for Tank (`#3b82f6`), Healer (`#10b981`), Melee DPS (`#ef4444`), Physical Ranged (`#f97316`), Magic Ranged (`#a855f7`), Crafter (`#14b8a6`), and Gatherer (`#eab308`).
