package contract

import (
	"context"
	"database/sql"
)

// SQLiteDriver defines access to the SQLite database, including runtime migrations.
type SQLiteDriver interface {
	Acquire(ctx context.Context) (*sql.DB, error)
	Close() error
	Execute(ctx context.Context, query string, args ...any) (sql.Result, error)
	FetchOne(ctx context.Context, query string, args ...any) (*sql.Row, error)
	FetchMany(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	MigrateUp(ctx context.Context) error
	MigrateDown(ctx context.Context) error
}
