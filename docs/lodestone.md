# Lodestone Client

The Lodestone client reads character, achievement, and free-company data from **The Lodestone** (FFXIV's official community site) via [godestone v2](https://github.com/xivapi/godestone), backed by the [bingode](https://github.com/karashiiro/bingode) game-data provider and the **EN** locale.

## Contract

`port/contract.LodestoneClient` (see `port/contract/lodestone.go`) is implemented by `infrastructure/lodestone` (real godestone adapter) and `mock/lodestone` (in-memory fake for tests). Returned types are godestone's model types — the adapter wraps godestone directly, mirroring how `SQLiteDriver` exposes `*sql.DB`.

| Method | Signature | Notes |
| ------ | --------- | ----- |
| `FetchCharacter` | `(ctx, id uint32) (*godestone.Character, error)` | Character ID is the numeric Lodestone ID. |
| `FetchAchievements` | `(ctx, id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error)` | List of unlocked achievements + aggregate info. A private profile comes back as `AllAchievementInfo.Private = true` with no error. |
| `FetchFreeCompany` | `(ctx, id string) (*godestone.FreeCompany, error)` | FC ID is the 19-digit Lodestone string, **not** a numeric id. |

## Primary Provider & Fallback Integration

The Lodestone client serves as the **primary data provider** across the census ingest pipeline:
- **ID Sweep (`id-sweep`)**: Lodestone is queried first for character ranges in `auto` mode, falling back to Tomestone.gg when 404s, scrape errors, or rate limits occur.
- **Character Census (`character-census`)**: Lodestone is fetched first, falling back to Tomestone.gg when unresolvable or rate-limited.
- **Achievement Census (`achievement-census`) & Free Company (`fc-census`)**: Lodestone is the exclusive provider. When Lodestone is rate-limited or paused, these queues wait while dual-source queues continue on Tomestone.

## Rate limiting

A token bucket (`golang.org/x/time/rate`) gates **every** method call: one token, refilled at `rate_limit` per second.

```toml
[lodestone]
rate_limit = 1.0   # requests/second, burst 1
max_retries = 3
```

| Field | Default | Purpose |
| ----- | ------- | ------- |
| `rate_limit` | `1.0` | Token-bucket fill rate in requests/second (capped at `1.0` req/s ceiling to avoid Lodestone Cloudflare IP bans); burst is always 1. |
| `max_retries` | `3` | Retries per call on transient errors → up to `max_retries + 1` attempts. |

Environment overrides work like the other sections: `LODESTONE_RATE_LIMIT=0.5`, `LODESTONE_MAX_RETRIES=5`. Note that rates configured above `1.0` are clamped to `1.0` for safety.

**Limitation:** throttling is per *method call*, not per HTTP request. `FetchCharacter` issues 2 internal requests (profile page + class/job page), so character throughput is up to `2 × rate_limit`. A per-request throttle would require forking godestone — accepted for now.

## Error handling & Retry policy

Non-existent or banned/terminated character profiles (HTTP 404 "Not Found" and HTTP 403 "Forbidden") are immediately recognized as `contract.ErrCharacterNotFound` and are **never retried**. All other scraper errors are treated as transient and retried with exponential backoff: `500 ms · 2^attempt` (500 ms, 1 s, 2 s, …). With the default `max_retries = 3` a call makes up to 4 attempts.

## Context handling

`ctx` is honored only at the limiter/backoff/retry boundaries:

- `limiter.Wait(ctx)` returns `ctx.Err()` on cancellation/deadline.
- The backoff sleep is a `select` on `ctx.Done()` and can be aborted early.
- `ctx.Err()` is checked before each attempt.

godestone's methods take no `ctx` and its colly collectors expose no HTTP timeout, so an **in-flight request cannot be cancelled** — cancellation only prevents *starting* the next attempt.

## Why no `user_agent` / `timeout` config keys

godestone hardcodes its user-agent (colly's `UserAgent(s.meta.UserAgentDesktop)` from the embedded `meta.json`) and exposes no HTTP timeout through its public API. Neither key is implementable without a godestone fork/patch, so `rate_limit` and `max_retries` are the only cleanly configurable knobs.

## Container wiring

`container.Load.LodestoneClient()` lazily builds the adapter from `[lodestone]` config (which has defaults, so the accessor always works) and caches it. Like the other accessors, it degrades to a logged `nil` only if config is missing or construction fails.
