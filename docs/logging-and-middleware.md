# Logging & Middleware

Middlewares live in `cmd/http/middleware` and compose the HTTP pipeline in `cmd/http/server.go`.

Default order:

1. **Logging** — emits single-line structured logs via `LoggingColorSimple` (overridable in config).
2. **Auth** *(optional)* — validates `X-Auth-Token` if enabled in `config.toml`.
3. **Max Requests** — gracefully drains the server after a configurable number of requests.
4. **Metrics** *(optional)* — forwards timings to StatsD.

## Logging Modes

Configure via `logging.server_default` in `config/config.toml`:

- `color` / `color-simple` — human friendly console output.
- `json` / `pretty-json` — machine-friendly structured logs.
- `simple` — plain text without colour.

Use `logging.Init` in `main.go` to set the default logger for the entire process.

## Structured Logging for Publishers, Consumers, and Handlers

The RabbitMQ queue adapter (`infrastructure/rabbitmq`), the census worker (`domain/census/worker`),
and the three census handlers (`domain/census/handler`) log through a `contract.Logger`
interface that mirrors `*log/slog.Logger` (`DebugContext`/`InfoContext`/`WarnContext`/
`ErrorContext` with alternating key/value args, plus `Enabled` for level checks).
`infrastructure/logging.Logger` is the concrete implementation — a compile-time assertion
in `infrastructure/logging/logger.go` pins it to the contract, so no adapter type exists.
A discard logger (returned when `nil` is injected) always returns `false` from `Enabled`,
allowing callers to skip expensive attribute construction.

Injection:

- Obtain the logger via `container.Load.Logger()`; never import `infrastructure/logging`
  outside the container and `main`.
- Constructors take `logger contract.Logger` as their last parameter. A `nil` logger is
  substituted with a discard logger, so forgetting injection is silent-and-safe.
- Level policy: `Info` = one completed queue job (`worker.job_done` after all downstream
  publishes succeed) plus lifecycle/provider/proxy state; `Warn` = transient retry/fetch
  errors; `Error` = terminal failure; `Debug` = fetch/store/probe detail, per-ID sweep
  probes, and `worker.job_start`. Debug is opt-in via `logging.level = "debug"`
  (`LOGGING_LEVEL=debug`); no new config keys were added. Per-ID ID-sweep logs and the
  achievement `latestAchievement` diagnostic are guarded with `logger.Enabled(ctx, slog.LevelDebug)`
  to avoid building slog attributes when Debug is disabled.

### Consumer log events (live)

A running `consume` worker emits one line per processing moment, with the identifiers
relevant to the event (IDs, names, worlds):

| Handler | Events |
|---|---|
| `id-sweep` | `handler.id_sweep.start` (Debug, range), `handler.id_sweep.probe` (Debug, guarded, `not_found`), `handler.id_sweep.discovered` (Debug, guarded, `character_id`/`name`/`world`), `handler.id_sweep.done` (Debug, `discovered`) |
| `character-census` | `handler.character_census` (Debug, `character_id`), `.fetched` (Debug, `name`/`world`/`fc_id`), `.stored` (Debug, `name`/`world`), `.deleted` (Debug), `.done` (Debug, `chained`) |
| `achievement-census` | `handler.achievement_census` (Debug, `character_id`), `.fetched` (Debug, guarded, `earned`/`latest_id`/`latest_name`), `.done` (Debug, `milestones`/`private`) |
| errors | `handler.<event>.fetch_error` (Warn), `handler.<event>.store_error` / `.process_error` (Error), plus the worker's `worker.job_retry` (Warn) and queue's `queue.retry` / `queue.failed` |

`worker.job_start` is Debug (event type only); `worker.job_done` is Info and emitted once
per fully successful job after all downstream publishes succeed. The full fetch → store →
complete flow is visible at Debug level; at Info level only one `worker.job_done` line
appears per completed job. Per-ID sweep probes and the achievement `latestAchievement`
diagnostic are guarded with `logger.Enabled(ctx, slog.LevelDebug)` to avoid building
slog attributes when Debug is disabled.
