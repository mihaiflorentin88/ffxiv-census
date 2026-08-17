# HTTP API

ffxiv-census exposes a small versioned read API for the ingested census data and queue depth. Every endpoint is a `GET` returning JSON (`application/json`). Data endpoints live under `/api/v1/`; the unversioned `/health` probe is the only exception. The same surface is documented in the embedded Swagger 2.0 spec served at `/docs/swagger.json` by the HTTP app.

This is living documentation — when the API changes, update this file with it.

## Conventions

### Errors

All errors use a single envelope:

```json
{"error": "message"}
```

| Status | Meaning |
|---|---|
| 400 | Bad request — missing/invalid query or path parameter |
| 404 | Resource not found — unknown character id (`{"error": "character not found"}`) |
| 500 | Internal error — database failure or unavailable service; the message is the underlying error |

### Pagination

`GET /api/v1/census/characters` returns a pagination envelope:

```json
{"items": [], "total": 1234, "limit": 100, "offset": 0}
```

- `items` — the requested page of `CharacterListItem`s.
- `total` — total number of non-deleted characters, independent of `limit`/`offset`.
- `limit` — the applied page size (default 100, maximum 500).
- `offset` — the applied offset (default 0).

### Timestamps

Times are RFC 3339 UTC (Go `time.Time` marshaling). The exception is `NewCharactersDay.day`, a plain `YYYY-MM-DD` string naming a UTC day.

### Activity window

"Active" means the character's `latest_achievement_at` falls within the activity window; characters without any achievements yet (`latest_achievement_at` NULL) are never active. The window is configured under `[census]`:

```toml
[census]
activity_window_days = 30
```

Environment override uses the same rule as `[sqlite]`/`[queue]` (dots become underscores, section name is the prefix — see `docs/queue.md`): `CENSUS_ACTIVITY_WINDOW_DAYS=45`.

The window drives `active_characters`/`active_ratio` on `GET /api/v1/census/latest` and the `active` count on `GET /api/v1/stats/breakdown`.

## Endpoints

| Method | Path | Query / path params | Success shape | Error shape |
|---|---|---|---|---|
| GET | `/health` | — | `{"status": "ok"}` | — |
| GET | `/api/v1/census/latest` | — | `CensusSummary` | 500 |
| GET | `/api/v1/census/characters` | `limit` (int, default 100, max 500), `offset` (int, default 0), `world` (string), `datacenter` (string), `region` (string), `race` (string), `name` (string) | `PaginatedCharacters` | 400 (invalid `limit`/`offset`), 500 |
| GET | `/api/v1/census/characters/{id}` | `id` (path, uint32 Lodestone character id) | `CharacterDetail` | 400 (invalid id), 404 (not found), 500 |
| GET | `/api/v1/stats/breakdown` | `by` (required, `race`\|`world`\|`datacenter`\|`region`) | `[BreakdownGroup]` | 400 (missing/unknown `by`), 500 |
| GET | `/api/v1/stats/new-characters` | `since` (required, `YYYY-MM-DD`), `until` (optional, `YYYY-MM-DD`, default now) | `[NewCharactersDay]` | 400 (missing/invalid `since`/`until`), 500 |
| GET | `/api/v1/stats/expansion` | `name` (optional, exact match) | `[ExpansionStat]` | 500 |
| GET | `/api/v1/queue` | — | `[QueueDepthItem]` | 500 |

### GET /health

Liveness probe.

```json
{"status": "ok"}
```

### GET /api/v1/census/latest

Total and active character counts plus the active ratio (`active / total`; `0` when `total` is `0`).

```json
{
  "total_characters": 1234,
  "active_characters": 900,
  "active_ratio": 0.7293
}
```

### GET /api/v1/census/characters

One page of characters. Supports optional AND-combined filters: `world`, `datacenter`, `region`, `race` (exact match), and `name` (case-insensitive substring). `limit` defaults to 100 and is clamped to 500; `offset` defaults to 0. Missing/empty parameters fall back to the defaults (no filter / full list); non-numeric or non-positive `limit` (<= 0) and negative `offset` (< 0) are rejected with 400.
```json
{
  "items": [
    {
      "id": 36795950,
      "name": "Example Name",
      "world": "Louisoix",
      "datacenter": "Chaos",
      "region": "EU",
      "race": "Miqo'te",
      "gender": 1,
      "free_company_id": "9234567890123456789",
      "free_company_name": "Example Free Company",
      "achievements_private": false,
      "latest_achievement_id": 1139,
      "first_seen_at": "2026-08-17T10:00:00Z",
      "last_census_at": "2026-08-17T10:05:00Z"
    }
  ],
  "total": 1234,
  "limit": 100,
  "offset": 0
}
```

The nullable fields `free_company_id`, `free_company_name`, `latest_achievement_id`, and `last_census_at` are omitted when null.

### GET /api/v1/census/characters/{id}

Full detail for one character. A non-numeric id is a 400 (`{"error": "invalid character id"}`); an unknown id is a 404 (`{"error": "character not found"}`).

```json
{
  "character": {
    "id": 36795950,
    "name": "Example Name",
    "world": "Louisoix",
    "datacenter": "Chaos",
    "region": "EU",
    "race": "Miqo'te",
    "gender": 1,
    "free_company_id": "9234567890123456789",
    "free_company_name": "Example Free Company",
    "achievements_private": false,
    "first_seen_at": "2026-08-17T10:00:00Z",
    "last_census_at": "2026-08-17T10:05:00Z"
  },
  "jobs": [
    {"class_job_id": 21, "name": "Paladin", "level": 90, "exp_level": 204735}
  ],
  "milestones": [
    {"achievement_id": 1139, "achieved_at": "2026-08-17T10:04:00Z"}
  ],
  "free_company": {
    "id": "9234567890123456789",
    "name": "Example Free Company",
    "world": "Louisoix",
    "datacenter": "Chaos",
    "member_count": 128
  }
}
```

`free_company` is omitted when the character is not in a free company. `jobs` and `milestones` are always arrays (possibly empty).

### GET /api/v1/stats/breakdown?by=...

Population per group, sorted by `total` descending. `by` is required; a missing parameter is a 400 (`{"error": "missing by parameter"}`) and an unknown dimension is a 400 (`{"error": "invalid breakdown dimension: want race|world|datacenter|region"}`).

```json
[
  {"key": "Louisoix", "total": 42, "active": 31},
  {"key": "Cerberus", "total": 38, "active": 25}
]
```

`active` counts characters whose `latest_achievement_at` is within the activity window (see [Activity window](#activity-window)).

### GET /api/v1/stats/new-characters?since=YYYY-MM-DD[&until=YYYY-MM-DD]

Characters first seen per UTC day in `[since, until)`, ordered ascending by day. `since` is required and must parse as a date; `until` is optional and defaults to now (UTC). Parse failures are 400s.

```json
[
  {"day": "2026-08-16", "count": 1},
  {"day": "2026-08-17", "count": 3}
]
```

### GET /api/v1/stats/expansion[?name=...]

How many distinct characters completed each expansion's MSQ. The optional `name` filter narrows the list to that expansion (exact match); no match returns an empty array, not a 404.

```json
[
  {"expansion": "Heavensward", "count": 2},
  {"expansion": "Stormblood", "count": 1}
]
```

### GET /api/v1/queue

Work-queue depth per job status, sorted by status (see `docs/queue.md`).

```json
[
  {"status": "done", "count": 3},
  {"status": "pending", "count": 1}
]
```

## See also

- `docs/census.md` — the data model behind these endpoints.
- `docs/queue.md` — the queue lifecycle behind `GET /api/v1/queue`.
- `/docs/swagger.json` — machine-readable spec (Swagger 2.0).
