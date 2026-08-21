# Tomestone.gg Integration

`ffxiv-census` provides an integration with [tomestone.gg](https://tomestone.gg) REST API to fetch character profiles, equipment, and class/job progression.

## Configuration

Tomestone.gg endpoints require Laravel Sanctum Bearer authentication tokens. You can obtain a token from your Tomestone.gg Account Settings.

Tokens and configuration can be set in `config.toml`, via a local `.env` file (loaded automatically by `config.NewConfig()` when present), or via system environment variables:
```toml
[tomestone]
api_token = "your-bearer-token"
base_url = "https://tomestone.gg"
rate_limit = 5.0
timeout = "10s"
```

| Field        | Default               | Env Override           | Description                                                        |
| ------------ | --------------------- | ---------------------- | ------------------------------------------------------------------ |
| `api_token`  | `""`                  | `TOMESTONE_API_TOKEN`  | Laravel Sanctum Bearer token for authentication.                   |
| `base_url`   | `https://tomestone.gg`| `TOMESTONE_BASE_URL`   | Base URL for Tomestone.gg REST API.                                |
| `rate_limit` | `5.0`                 | `TOMESTONE_RATE_LIMIT` | Token-bucket rate limiter ceiling in requests per second. Shared across all proxy clients in a process. |
| `timeout`    | `10s`                 | `TOMESTONE_TIMEOUT`    | HTTP client timeout per request.                                   |

## Port & Architecture

Hexagonal architecture is preserved with the port contract defined in `port/contract/tomestone.go`:

```go
type TomestoneClient interface {
    FetchCharacterProfile(ctx context.Context, id uint32, update bool) (*TomestoneCharacter, error)
    FetchCharacterProfileByName(ctx context.Context, server, name string, update bool) (*TomestoneCharacter, error)
    IsConfigured() bool
}
```

- **Adapter**: `infrastructure/tomestone` implements `contract.TomestoneClient` using standard `net/http` with Bearer auth injection, rate limiting via `golang.org/x/time/rate`, and structured logging.
- **Service Locator**: `container.Load.TomestoneClient()` lazily instantiates and caches the adapter.
- **Mock**: `mock/tomestone` provides an in-memory thread-safe fake for unit and domain tests.

## CLI Usage

Inspect live Tomestone character profiles directly via the CLI:

```bash
# Fetch character profile by Lodestone numeric ID
./bin/ffxiv-census tomestone character 36795950

# Request an on-demand update from Lodestone
./bin/ffxiv-census tomestone character 36795950 --update

# Fetch character profile by server and name
./bin/ffxiv-census tomestone character Balmung "Tataru Taru"

# Output compact raw JSON
./bin/ffxiv-census tomestone character 36795950 --raw
```

## Dual-Source Ingest & Fallback

Tomestone.gg serves as the **primary provider for `id-sweep`** (character discovery) and the **fallback provider for `character-census`** (profile re-census).

- **`id-sweep` (Tomestone primary):** When `--source auto` (default) is set, handlers probe Tomestone.gg first (5 req/s REST API) for maximum discovery throughput. If Tomestone returns a 404 or transient error, handlers fall back to The Lodestone. If both return 404, the character is confirmed missing.
- **`character-census` (Lodestone primary, Tomestone fallback):** Handlers query The Lodestone first as the authoritative source. If Lodestone returns a 404, scrape error, or encounters rate limits, handlers fall back to Tomestone.gg.
- Explicit `--source tomestone` on `id-sweep` queries Tomestone.gg directly without querying Lodestone.
- Ingested characters are persisted via `CensusService.UpsertTomestoneCharacter` and immediately chained into downstream jobs (`achievement-census`, and `fc-census` when affiliated with a free company) via `BuildDependentCharacterJobs`.
- When Lodestone is rate-limited, workers automatically switch dual-source queues (`id-sweep`, `character-census`) to Tomestone while pausing Lodestone-exclusive queues.
### Rate Limiting in Proxy Mode

All proxy Tomestone clients in a process share a single `RequestRateController` at the configured `rate_limit` (default 5 req/s). The `RequestRateController` manages the token bucket, configured/clamped rate, and global consecutive-429 adaptive backoff state. This prevents N proxy goroutines from each creating an independent 5 req/s bucket, which would allow N×5 requests/second. Tokens are charged per HTTP attempt — each retry acquires a new token.

Per-worker `ProviderRateLimiter` handles cooldown pauses: when a proxy goroutine receives a 429 from Tomestone, it pauses Tomestone in its local limiter without affecting other goroutines. Lodestone 429s pause only Lodestone, and Tomestone 429s pause only Tomestone — provider cooldowns are isolated.

**Typed functional options:** `NewClientWithProxy` accepts typed `ClientOption` values:
- `WithProviderRateLimiter(l contract.ProviderRateLimiter)` — injects the per-worker cooldown limiter
- `WithRequestRateController(c *RequestRateController)` — injects the shared process-wide rate controller

### Proxy-Aware Client

`NewClientWithProxy(cfg, proxyURL, logger, rateLimiter...)` creates a TomestoneClient that routes all requests through the given proxy URL. The proxyURL must include the protocol (`http://`, `socks4://`, `socks5://`). The `rateLimiter` parameter is the shared process-wide Tomestone bucket.

**Protocol support:**
- **HTTP/HTTPS**: Uses `http.Transport.Proxy` for standard HTTP proxy tunneling
- **SOCKS4**: Uses `golang.org/x/net/proxy` to create a SOCKS dialer (same as SOCKS5 — the library handles the protocol difference)
- **SOCKS5**: Uses `golang.org/x/net/proxy` to create a SOCKS dialer, wrapped in `http.Transport.DialContext`

Used by `consume --proxy` — each worker goroutine creates its own proxy-aware client instance. The proxy consumer config `[proxy.consumer].request_timeout` overrides the base `[tomestone].timeout` for proxy-mode clients.

## Error Handling & Mapping

- `401 Unauthorized` / `403 Forbidden` → maps to `contract.ErrTomestoneUnauthenticated`.
- `404 Not Found` → maps to `contract.ErrCharacterNotFound`.
- `429 Too Many Requests` → logs a warning and returns rate limit error.
- Context cancellation and timeouts are strictly respected.
