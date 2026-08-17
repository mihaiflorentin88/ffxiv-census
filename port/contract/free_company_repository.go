package contract

import "context"

// FreeCompanyFilter is an optional filter for List and Count queries on free companies.
type FreeCompanyFilter struct {
	World        string
	Datacenter   string
	Name         string
	Tag          string
	GrandCompany string
	SortBy       string // "name", "world", "active_member_count", "member_count", "formed"
	SortOrder    string // "asc", "desc"
}

// FreeCompanyRepository persists free-company snapshots.
type FreeCompanyRepository interface {
	Upsert(ctx context.Context, rec FreeCompanyRecord) error
	// Get returns the FC or nil (no error) when absent.
	Get(ctx context.Context, id string) (*FreeCompanyRecord, error)
	// List returns free companies matching filter up to limit starting at offset.
	List(ctx context.Context, filter FreeCompanyFilter, limit, offset int) ([]FreeCompanyRecord, error)
	// Count returns the number of free companies matching filter.
	Count(ctx context.Context, filter FreeCompanyFilter) (int64, error)
}
