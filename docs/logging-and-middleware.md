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
