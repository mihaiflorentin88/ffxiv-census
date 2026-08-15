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
