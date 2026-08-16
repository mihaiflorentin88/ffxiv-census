# Container Wiring

The container package centralises dependency construction so business code stays declarative. At startup (`main.go`), a `ServiceContainer` is created and stored in `container.Load`. Subsequent calls fetch configuration, infrastructure singletons, or domain services.

```
serviceContainer := container.NewServiceContainer()
container.Load = serviceContainer
```

## Structure

- `infrastructure.go` — lazily instantiates outbound adapters such as the SQLite driver, StatsD, outbound HTTP clients, and system helpers behind `port/contract` interfaces.
- `infrastructure/sqlite` — hosts the SQLite driver and embedded goose migrations.
- `domain.go` — constructs domain services (added as features are built).
- `main.go` — fetches the embedded config and exposes helper methods.

## Usage Tips

1. Always request dependencies through container accessors (e.g., `container.Load.SQLite()`, `container.Load.HTTPClient()`, `container.Load.Statsd()`).
2. Keep constructors pure; inject interfaces from `port/contract`.
3. Infrastructure accessors are lazy singletons, so the first call constructs the adapter and subsequent calls reuse it.
4. When adding a new service, update `DomainContainer` and document it here.

## Removed Accessors

Previous versions of the container exposed a `MySQL()` accessor along with fixtures and an example repository. These have been removed in favour of the SQLite driver (`SQLite()`). The SQLite driver handles migrations automatically on first access — see [docs/sqlite.md](sqlite.md) for details.
