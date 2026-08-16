# Architecture Overview

This document records the core design decisions baked into **ffxiv-census**. The service follows a hexagonal (ports & adapters) architecture with an object-oriented flavour: domain services expose behaviour-rich methods, while transports/adapters satisfy interfaces defined in `port/contract`.

## Hexagonal Recap

The Hexagonal Architecture (Ports and Adapters) places the application core inside a boundary protected by ports. Adapters interact with the outside world—HTTP, CLIs, data stores—while the core remains technology agnostic.

- **Ports** are technology-neutral entry points that define how external actors talk to the application. A port is implemented via an interface and should have at least two adapters hooked to it—one being a test harness.
- **Application (Hexagon)** hosts use-case orchestration and the domain model (aggregates, entities, value objects). Commands/queries enter through ports; outgoing calls to external actors also pass through ports.
- **Driving Adapters** initiate work (e.g., HTTP handlers). They depend on port interfaces supplied by the application.
- **Driven Adapters** implement port interfaces to fulfil requests from the application (e.g., databases).

### Dependency Inversion

High-level modules (application/domain) depend on abstractions, never concrete infrastructure. Adapters implement interfaces declared inside the hexagon, ensuring details depend on abstractions.

## Layers & Modules

- `cmd/cli`: Cobra-based control surface. `main.go` forwards execution here to keep the root tidy.
- `cmd/http`: HTTP server wiring. The server reads configuration via the container, attaches standard middleware (logging, recovery, request ID), and mounts route groups. APIs must accept request DTOs and return response DTOs; convert them to internal DTOs before passing work into the domain.
- `config`: Configuration loader powered by Viper with an embedded `config.toml`. Environment variables override file values using the `APP_`, `HTTP_`, and feature-specific prefixes.
- `container`: Simple service locator that bootstraps configuration, logging, and optional infrastructure clients (SQLite, queue, StatsD, outbound HTTP, system helpers, UI assets). Generated code resolves these adapters through `ServiceContainer` accessors instead of constructing them inside handlers or domain services.
- `domain`: Reserved for pure business logic. Keep this folder clean; avoid direct references to HTTP or CLI packages. Domain objects can be instantiated from `cmd/` but they interact with infrastructure solely via contracts and DTOs.
- `infrastructure`: Adapters that speak to the outside world (logging, SQLite, metrics, etc.). Code here implements interfaces defined in `port/contract` to honour dependency inversion.
- `docs`: Living documentation. Extend these markdown files alongside code changes so future-you knows how to operate the system.
- `cmd/` never talks to infrastructure directly. Resolve adapters through the container accessors exposed by the generated project and keep the core flow in the domain layer.

## Request Flow

```text
┌────────┐   HTTP request   ┌──────────────┐   routing   ┌──────────────┐
│ Client │ ───────────────▶ │ cmd/http     │ ───────────▶│ domain layer │
└────────┘                  │ - middleware │             │ - services   │
                            │ - handlers   │             └──────────────┘
                            └──────────────┘
                                   │
                                   ▼
                             ┌──────────────┐
                             │ infrastructure│
                             │ (sqlite, etc.)│
                             └──────────────┘
```

### Middleware Defaults

1. **Request ID** — attaches a UUID so downstream logs correlate events.
2. **Real IP** — pulls the client IP from common proxy headers.
3. **Logging** — emits structured `INFO` lines keyed by method, path, status, and latency.
4. **Recover** — catches panics and surfaces a 500 without killing the process.
6. **Metrics (optional)** — StatsD timings for each route group. Customize the namespace in `container/metrics.go`.

## Configuration Philosophy

- Embed sane defaults so the binary runs with zero flags.
- Override via environment variables in CI/CD (`HTTP_PORT=8081 ./bin/ffxiv-census serve`).
- For sensitive secrets, prefer external secret stores; never commit them to Git.

## Queue

Durable async work lives in the same SQLite datastore (`queue_jobs` table) with a claim-based lifecycle — see [docs/queue.md](queue.md) for the lifecycle, atomic claim semantics, dedup, and backoff. Resolve it via `container.Load.Queue()`.

## Future Hooks

- Add domain service constructors under `container/domain.go` to keep wiring explicit.
- Document decisions in `docs/decisions/` when architecture changes (ADR format).
- Stay away from "service-oriented" anemic models; keep the domain expressive and behaviour-rich.

## Directory Cheat Sheet

```
├── cmd/                - Entry points (CLI/HTTP). May construct domain objects directly; resolve adapters via the container.
├── config/             - Application configuration loader.
├── container/          - Service container exposing interfaces to infrastructure adapters.
├── docs/               - Documentation resources.
├── domain/             - Domain logic; avoids direct infrastructure dependencies.
├── infrastructure/     - External clients (datastores, APIs) implementing ports.
└── port/               - Contracts (interfaces) and DTOs exchanged across layers.
```

Happy shipping!
