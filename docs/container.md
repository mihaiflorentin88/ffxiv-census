# Container Wiring

The container package centralises dependency construction so business code stays declarative. At startup (`main.go`), a `ServiceContainer` is created and stored in `container.Load`. Subsequent calls fetch configuration, infrastructure singletons, or domain services.

```
serviceContainer := container.NewServiceContainer()
container.Load = serviceContainer
```

## Structure

- `infrastructure.go` — lazily instantiates outbound adapters such as Redis, StatsD, outbound HTTP clients, and system helpers behind `port/contract` interfaces.
- `infrastructure/mysql` — hosts the MySQL driver, migrations, fixtures, and repository examples.
- `domain.go` — constructs domain services (see `domain/example` for a starting point).
- `main.go` — fetches the embedded config and exposes helper methods.

## Usage Tips

1. Always request dependencies through container accessors (e.g., `container.Load.Redis()`, `container.Load.MySQL()`, `container.Load.HTTPClient()`).
2. Keep constructors pure; inject interfaces from `port/contract`.
3. Infrastructure accessors are lazy singletons, so the first call constructs the adapter and subsequent calls reuse it.
4. When adding a new service, update `DomainContainer` and document it here.
