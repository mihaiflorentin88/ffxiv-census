package contract

import (
	"context"
	"time"
)

// ExampleRecord mirrors the columns stored in the examples table.
type ExampleRecord struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

// ExampleRepository exposes simple data-access helpers for the examples table.
type ExampleRepository interface {
	Insert(ctx context.Context, name string) (int64, error)
	List(ctx context.Context) ([]ExampleRecord, error)
}
