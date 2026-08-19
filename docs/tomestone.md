# Tomestone.gg Integration

`ffxiv-census` provides an integration with [tomestone.gg](https://tomestone.gg) REST API to fetch character profiles, equipment, and class/job progression.

## Configuration

Tomestone.gg endpoints require Laravel Sanctum Bearer authentication tokens. You can obtain a token from your Tomestone.gg Account Settings.

Tokens and configuration can be set in `config.toml`, via a local `.env` file (loaded automatically by `config.NewConfig()` when present), or via system environment variables:
```toml
[tomestone]
api_token = "your-bearer-token"
base_url = "https://tomestone.gg"
rate_limit = 10.0
timeout = "10s"
```

| Field        | Default               | Env Override           | Description                                                        |
| ------------ | --------------------- | ---------------------- | ------------------------------------------------------------------ |
| `api_token`  | `""`                  | `TOMESTONE_API_TOKEN`  | Laravel Sanctum Bearer token for authentication.                   |
| `base_url`   | `https://tomestone.gg`| `TOMESTONE_BASE_URL`   | Base URL for Tomestone.gg REST API.                                |
| `rate_limit` | `10.0`                | `TOMESTONE_RATE_LIMIT` | Token-bucket rate limiter ceiling in requests per second.         |
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

Tomestone.gg is used as the high-throughput fallback data provider for character discovery (`id-sweep`) and profile re-census (`character-census`).

- When `--source auto` (default) is set, handlers query The Lodestone first. If Lodestone returns a 404, scrape error, or encounters rate limits, handlers seamlessly fall back to Tomestone.gg.
- Explicit `--source tomestone` on `id-sweep` queries Tomestone.gg directly without querying Lodestone.
- Ingested characters are persisted via `CensusService.UpsertTomestoneCharacter` and immediately chained into downstream jobs (`achievement-census`, and `fc-census` when affiliated with a free company) via `BuildDependentCharacterJobs`.
- When Lodestone is rate-limited, workers automatically switch dual-source queues (`id-sweep`, `character-census`) to Tomestone while pausing Lodestone-exclusive queues.
## Error Handling & Mapping

- `401 Unauthorized` / `403 Forbidden` → maps to `contract.ErrTomestoneUnauthenticated`.
- `404 Not Found` → maps to `contract.ErrCharacterNotFound`.
- `429 Too Many Requests` → logs a warning and returns rate limit error.
- Context cancellation and timeouts are strictly respected.
