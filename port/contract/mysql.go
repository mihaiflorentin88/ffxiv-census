package contract

import (
	"context"
	"database/sql"
)

// MySQLDriver defines the minimal methods required by repositories and tasks to access MySQL.
type MySQLDriver interface {
	Acquire(ctx context.Context) (*sql.DB, error)
	Close() error
	Execute(ctx context.Context, query string, args ...any) (sql.Result, error)
	FetchOne(ctx context.Context, query string, args ...any) (*sql.Row, error)
	FetchMany(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}
