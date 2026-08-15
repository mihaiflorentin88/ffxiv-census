package mockmysql

import (
	"context"
	"database/sql"
)

// Driver is a lightweight test double for the MySQL driver contract.
type Driver struct {
	DB  *sql.DB
	Err error
}

// Acquire returns the configured sql.DB or the preset error.
func (d *Driver) Acquire(ctx context.Context) (*sql.DB, error) {
	if d.Err != nil {
		return nil, d.Err
	}
	return d.DB, nil
}

// Close closes the underlying sql.DB when present.
func (d *Driver) Close() error {
	if d.DB == nil {
		return nil
	}
	return d.DB.Close()
}
