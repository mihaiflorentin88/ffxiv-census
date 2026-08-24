# Contracts

Define inbound and outbound interfaces here. These abstractions form the "ports" in the hexagonal model.

Example:

```go
package contract

type ClusterService interface {
    Health(ctx context.Context) (Stats, error)
}

type Cache interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte) error
}
```

Adapters in `infrastructure/` should satisfy these contracts.

Aggregate UI and statistics API data crosses the `UIStatsRepository` port. Its
versioned `UIStatsSnapshot` is technology-neutral; JSONB encoding and PostgreSQL
advisory locking remain implementation details of the adapter. Every new port must
also have an in-memory fake under `mock/` so domain and transport tests do not depend
on infrastructure.
