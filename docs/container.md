# Container Wiring

The container package centralises dependency construction so business code stays declarative. At startup (`main.go`), a `ServiceContainer` is created and stored in `container.Load`. Subsequent calls fetch configuration, infrastructure singletons, or domain services.

```
serviceContainer := container.NewServiceContainer()
container.Load = serviceContainer
```

## Structure

- `infrastructure.go` — lazily instantiates outbound adapters such as the PostgreSQL driver, queue, StatsD, outbound HTTP clients, LodestoneClient, TomestoneClient, ProviderRateLimiter, ProxyRepository, ProxyHub behind `port/contract` interfaces.
- `infrastructure/postgres` — hosts the PostgreSQL driver and embedded goose migrations.
- `domain.go` — constructs domain services (added as features are built).
- `main.go` — fetches the embedded config and exposes helper methods.

## Usage Tips

1. Always request dependencies through container accessors (e.g., `container.Load.Database()`, `container.Load.Queue()`, `container.Load.LodestoneClient()`, `container.Load.TomestoneClient()`, `container.Load.ProxyHub(owner)`).
2. Keep constructors pure; inject interfaces from `port/contract`.
3. Infrastructure accessors are lazy singletons, so the first call constructs the adapter and subsequent calls reuse it.
4. When adding a new service, update `DomainContainer` and document it here.

## Removed Accessors

Previous versions used MySQL, then SQLite. The project now runs on PostgreSQL. The container exposes both `Postgres()` and a `SQLite()` compatibility alias that delegates to `Database()`. The PostgreSQL driver handles migrations automatically on first access — see [docs/external-postgres.md](external-postgres.md) for details.

**Proxy accessors:** `ProxyRepository()` returns the proxy persistence layer. `ProxyHub(owner)` creates a per-goroutine proxy acquisition hub. `ProxyScrapeProvider()` and `GeonodeProvider()` return proxy discovery providers. `ProxyChecker()` returns the proxy health checker.
