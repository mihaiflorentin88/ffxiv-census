package contract

import "context"

// MigrationRunner executes schema migrations in either direction.
type MigrationRunner interface {
	Up(ctx context.Context) error
	Down(ctx context.Context) error
}
