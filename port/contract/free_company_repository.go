package contract

import "context"

// FreeCompanyRepository persists free-company snapshots.
type FreeCompanyRepository interface {
	Upsert(ctx context.Context, rec FreeCompanyRecord) error
	// Get returns the FC or nil (no error) when absent.
	Get(ctx context.Context, id string) (*FreeCompanyRecord, error)
}
