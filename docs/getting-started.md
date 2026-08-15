# Getting Started

Welcome! This guide walks through local development, configuration, and deployment for **ffxiv-census**. The codebase embraces a hexagonal (ports & adapters) architecture so transports remain thin and the domain stays object-oriented.

## Prerequisites

- Go 1.25+
- Make (optional but recommended)
- Docker (for containerized builds)

## Bootstrap

```bash
go mod tidy
cp config/config.toml config/config.local.toml # optional overrides
make build
./bin/ffxiv-census server --start --port 8080
```

The CLI ships with a handful of commands:

```bash
./bin/ffxiv-census --help
./bin/ffxiv-census server --help
```

## Configuration

`config/config.toml` contains the default settings:

```toml
[app]
name = "ffxiv-census"
env  = "development"

[http]
host = "0.0.0.0"
port = 8080
```

Override via environment variables using Viper's uppercase-dotted syntax:

```bash
./bin/ffxiv-census server --start --port 9090
```

Clone or fork the repository at https://github.com/mihaiflorentin88/ffxiv-census.git before making changes.

When you need infrastructure dependencies, resolve them through the service container instead of constructing adapters inline. HTTP handlers and CLI commands should ask for the accessor methods generated for the enabled adapters and pass the returned `port/contract` interfaces into domain code.

## Project Layout

```
├── cmd
│   ├── cli          # Cobra commands
│   └── http         # HTTP server, routes, middleware, swagger
├── config           # Viper-powered config loader + defaults
├── container        # Service locator wiring (infrastructure + domain)
├── domain           # Business logic (behaviour-rich types)
├── docs             # Living documentation (update frequently!)
├── infrastructure   # Adapters (logging, redis, metrics)
├── mock             # Test doubles for contracts
├── port             # Contracts and DTO definitions
└── main.go          # Thin entrypoint bootstrapping the container then running the CLI
```

## Testing

```bash
go test ./...
```

Use table-driven tests for handlers and domain services. Mock external dependencies via interfaces in `port/contract`.

## Docker Builds

Create binaries without installing the toolchain locally:

```bash
make docker-build
ls dist/
```

To produce a runtime image:

```bash
make docker-image
docker run --rm -p 8080:8080 ffxiv-census:latest
```

## Next Steps

1. Flesh out domain services under `domain`.
2. Add interfaces in `port/contract` to enforce boundaries.
3. Extend `container` to wire new dependencies and expose them through lazy accessors.
4. Document significant changes in `docs/`.

Enjoy building within a disciplined hexagonal boundary 🚀
