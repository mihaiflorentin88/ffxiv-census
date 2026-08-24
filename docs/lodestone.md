# Lodestone Client

The Lodestone client reads character and achievement data from **The Lodestone** (FFXIV's official community site) with `CustomClient`, a direct HTTP/HTML scraper using the EN site.

## Contract

`port/contract.LodestoneClient` is implemented by `infrastructure/lodestone` (`CustomClient`) and `mock/lodestone` (an in-memory fake). It returns repository-owned contract types.

| Method | Signature | Notes |
| ------ | --------- | ----- |
| `FetchCharacter` | `(ctx, id uint32) (*contract.CharacterProfile, error)` | Character ID is the numeric Lodestone ID. |
| `FetchAchievements` | `(ctx, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error)` | Sequential tracked-milestone detail check. |

## Achievement milestone checks

Achievement census requests are incremental: only missing milestones are requested, in chronological order. Persisted checkpoints are skipped, and the client stops at the first public unearned missing milestone. A complete tracked history makes **zero** Lodestone requests; no milestones known starts at Chocobo; a known prefix starts at its first missing checkpoint; and a historical gap requests only that gap rather than rechecking later stored milestones. Privacy is inferred from HTTP 403 on the first necessary detail request and costs no separate `/achievement/` request. HTTP 200 without the completed marker is public but unearned.

Earned timestamps are extracted only from the completed achievement row, so new writes use the achievement-specific date. Historical incorrectly stored dates are not backfilled. `latest_achievement_*` reflects the latest checked tracked milestone, not arbitrary activity from the complete achievement history.

## Primary Provider & Fallback Integration

The Lodestone client serves as the **primary data provider for `character-census`** and the **fallback provider for `id-sweep`** across the census ingest pipeline:
- **ID Sweep (`id-sweep`)**: Tomestone.gg is queried first for character ranges in `auto` mode (5 req/s REST API vs Lodestone's 1 req/s scraper). Lodestone is the fallback when Tomestone returns 404 or transient errors — characters may exist on Lodestone but not be indexed by Tomestone.
- **Character Census (`character-census`)**: Lodestone is fetched first as the authoritative source, falling back to Tomestone.gg when unresolvable or rate-limited.
- **Achievement Census (`achievement-census`)**: Lodestone is the exclusive provider. When Lodestone is rate-limited or paused, achievement messages remain queued in RabbitMQ while dual-source event types continue on Tomestone.

## Rate limiting

A token bucket (`golang.org/x/time/rate`) gates **every** method call: one token, refilled at `rate_limit` per second. Tokens are charged per HTTP attempt — each retry acquires a new token from the bucket.

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

**Process-wide vs per-proxy buckets:** The non-proxy consumer shares a single process-wide token bucket at `rate_limit` req/s. In proxy mode, each owner-locked proxy goroutine gets its own independent token bucket at `[proxy.consumer].lodestone_rate_limit` req/s (default 1.0). This means N proxy goroutines can collectively make up to N requests/second to Lodestone, each through a different IP.

Tokens are charged per HTTP attempt, including every achievement detail request and retry.

## Error handling & Retry policy

Non-existent or banned/terminated character profiles (HTTP 404 "Not Found" and HTTP 403 "Forbidden") are immediately recognized as `contract.ErrCharacterNotFound` and are **never retried**. All other scraper errors are treated as transient and retried with exponential backoff: `500 ms · 2^attempt` (500 ms, 1 s, 2 s, …). With the default `max_retries = 3` a call makes up to 4 attempts.

The `backoffBase` is set to **500 ms** by default in `newClient()`. Combined with ±10% jitter and the 1 req/s token bucket, retry timing is:

| Attempt | Backoff Delay | Cumulative |
|---------|---------------|------------|
| 0 → 1 | 500 ms | 500 ms |
| 1 → 2 | 1 s | 1.5 s |
| 2 → 3 | 2 s | 3.5 s |

The token bucket (1 req/s, burst 1) remains the primary rate defense. The backoff only *increases* the gap between retries on errors, never decreases it.

## Context handling

`ctx` is honored only at the limiter/backoff/retry boundaries:

- `limiter.Wait(ctx)` returns `ctx.Err()` on cancellation/deadline.
- The backoff sleep is a `select` on `ctx.Done()` and can be aborted early.
- `ctx.Err()` is checked before each attempt.

The HTTP client has a timeout and each request is created with the supplied context, so cancellation also reaches in-flight requests.

## Why no `user_agent` / `timeout` config keys

`CustomClient` uses a fixed census user agent and a fixed request timeout. `rate_limit` and `max_retries` are the supported Lodestone knobs; proxy consumers have a separate request timeout setting.

### Proxy-Aware Client

`NewCustomClient(cfg, logger, rateLimiter, WithProxy(proxyURL))` creates a `LodestoneClient` that routes all direct HTTP requests through the proxy. The proxy URL must include `http://`, `https://`, `socks4://`, or `socks5://`.

Used by `consume --proxy` — each worker goroutine creates its own proxy-aware client instance.

`newProxyTransport` configures HTTP/HTTPS proxies with `http.Transport.Proxy` and SOCKS4/SOCKS5 proxies with `golang.org/x/net/proxy`; no scraper-specific proxy adapter is involved.

## HTML Sanitization (`stripTags`)

Lodestone pages wrap field values (character names, worlds, FC names) in HTML tags (`<a>`, `<i>`, etc.). The client applies a `stripTags` helper to every text value extracted by `extractTextBetween` and `extractAllTextBetween`:

1. Removes all HTML tags via regex `<[^>]*>`.
2. Decodes common HTML entities: `&#39;` → `'`, `&amp;` → `&`, `&lt;` → `<`, `&gt;` → `>`, `&quot;` → `"`, `&nbsp;` → space.
3. Collapses multiple whitespace into single spaces and trims.

This ensures fields like `Name`, `World`, `Bio`, `GrandCompany`, `FreeCompanyName`, `Race`, and `Tribe` contain clean text regardless of Lodestone's HTML structure. Attribute-based extractors (`extractAttr`, `extractAlt`, `extractHref`) read tag attributes directly and are not affected.

## Removed Fields

`avatar_url` and `portrait_url` have been removed from the character profile. These fields are no longer extracted from Lodestone or stored in the database (see migration `00013_drop_avatar_portrait`).

## Container wiring

`container.Load.LodestoneClient()` lazily builds the adapter from `[lodestone]` config (which has defaults, so the accessor always works) and caches it. Like the other accessors, it degrades to a logged `nil` only if config is missing or construction fails.

### Job Level Parsing

`parseClassJobs` extracts class/job entries from the `character__level__list` HTML
section. The Lodestone HTML provides job names (e.g. "Paladin") but not numeric IDs.
A static lookup table (`lodestoneJobIDs`) maps each known job name to its official
`ClassJobID`. Unknown job names are skipped to avoid inserting rows with
`class_job_id=0` (which would collide in the primary key).

When a new expansion adds jobs, add entries to `lodestoneJobIDs` in
`infrastructure/lodestone/client.go`.
