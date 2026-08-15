package mockmysql

import (
	"context"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ExampleRepository is an in-memory implementation of the example repository contract.
type ExampleRepository struct {
	mu      sync.Mutex
	nextID  int64
	records []contract.ExampleRecord

	InsertErr error
	ListErr   error
}

// Insert persists the provided name in memory and returns a generated identifier.
func (r *ExampleRepository) Insert(ctx context.Context, name string) (int64, error) {
	if r.InsertErr != nil {
		return 0, r.InsertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	rec := contract.ExampleRecord{
		ID:        r.nextID,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	r.records = append(r.records, rec)
	return rec.ID, nil
}

// List returns all stored records, copying the slice to keep tests deterministic.
func (r *ExampleRepository) List(ctx context.Context) ([]contract.ExampleRecord, error) {
	if r.ListErr != nil {
		return nil, r.ListErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]contract.ExampleRecord, len(r.records))
	copy(out, r.records)
	return out, nil
}
