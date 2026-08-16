# Phase 3: Lodestone client — Implementation Plan

**Goal:** Add the `LodestoneClient` port defined in the design spec (§10): a godestone-v2-backed adapter with token-bucket rate limiting and exponential-backoff retries, an in-memory fake for tests, `[lodestone]` config (rate limit, max retries), container wiring, and `docs/lodestone.md`. This is the hard dependency for every future domain handler (character census, achievements, FC census). Phase 3 also closed out Phase 2 by fixing the `mock/queue` Fake's missing `MaxAttempts` default (a job published without it was failed on first retry instead of retried).

**Architecture:** Hexagonal as per repo docs. `contract.LodestoneClient` port implemented by `infrastructure/lodestone` (wraps godestone v2 + bingode provider, EN locale) and `mock/lodestone` (func-field fake for tests — the "second adapter" per the two-adapters rule). The adapter exposes an unexported `scraper` seam (satisfied structurally by `*godestone.Scraper`) so retry/rate-limit behavior is tested without network access. Config: new `[lodestone]` section (`rate_limit`, `max_retries`); viper env overrides `LODESTONE_RATE_LIMIT` / `LODESTONE_MAX_RETRIES`.

**Tech Stack:** Go, `github.com/xivapi/godestone/v2` (v2.10.0), `github.com/karashiiro/bingode` (embedded game-data provider), `golang.org/x/time/rate` (token bucket). All pure Go — `CGO_ENABLED=0` cross-compile stays intact.

**Design decisions:**

- Rate limit is a token bucket (burst 1) applied per **method call**; `FetchCharacter` internally issues 2 HTTP requests, so character throughput is up to `2 × rate_limit` (documented limitation, per-request throttle would require forking godestone).
- Retry **every** scraper error (no 404/403 special-casing; godestone converts achievement privacy to `AllAchievementInfo.Private = true` without error). Backoff `500ms · 2^attempt`.
- `ctx` is honored only at limiter/backoff/retry boundaries — godestone takes no ctx and its colly collectors expose no HTTP timeout, so in-flight requests can't be cancelled.
- No `user_agent` / `timeout` config keys: godestone hardcodes its UA (embedded `meta.json`) and exposes no HTTP timeout.

**Verification commands (run from repo root):**

```bash
go test ./config/ -run Lodestone -v
go test ./infrastructure/lodestone/ -v && go test ./infrastructure/lodestone/ -race
go test ./container/ -run Lodestone -v
CGO_ENABLED=0 go build ./...
go test ./... -race
"$(go env GOPATH)/bin/golangci-lint" run ./...
gofmt -l port/contract infrastructure/lodestone mock/lodestone config container
```

No live-Lodestone network test (politeness; no network in CI) — the network path is covered via the injected `scraper` seam.
