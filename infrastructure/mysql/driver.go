package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Driver wraps a pooled *sql.DB and exposes helpers that satisfy the MySQLDriver contract.
type Driver struct {
	cfg *config.MySQLConfig

	once sync.Once
	db   *sql.DB
	err  error
}

// NewDriver builds a lazy MySQL driver that opens a connection pool on first use.
func NewDriver(cfg *config.MySQLConfig) (contract.MySQLDriver, error) {
	if cfg == nil {
		return nil, errors.New("mysql config is nil")
	}
	driver := &Driver{cfg: cfg}
	if err := driver.initialise(context.Background()); err != nil {
		return nil, err
	}
	return driver, nil
}

// Acquire returns a healthy sql.DB pool, opening it on the first call.
func (d *Driver) Acquire(ctx context.Context) (*sql.DB, error) {
	if err := d.initialise(ctx); err != nil {
		return nil, err
	}
	if err := d.db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return d.db, nil
}

// Close releases the underlying pool when the service shuts down.
func (d *Driver) Close() error {
	if d.db == nil {
		return nil
	}
	return d.db.Close()
}

// Execute runs a statement that modifies data (INSERT/UPDATE/DELETE).
func (d *Driver) Execute(ctx context.Context, query string, args ...any) (sql.Result, error) {
	db, err := d.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return db.ExecContext(ctx, query, args...)
}

// FetchOne runs a query that returns a single row.
func (d *Driver) FetchOne(ctx context.Context, query string, args ...any) (*sql.Row, error) {
	db, err := d.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return db.QueryRowContext(ctx, query, args...), nil
}

// FetchMany runs a query that returns multiple rows; caller must close the rows.
func (d *Driver) FetchMany(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db, err := d.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return db.QueryContext(ctx, query, args...)
}

func (d *Driver) initialise(ctx context.Context) error {
	d.once.Do(func() {
		dsn := d.makeDSN()
		db, openErr := sql.Open("mysql", dsn)
		if openErr != nil {
			d.err = fmt.Errorf("open mysql: %w", openErr)
			return
		}
		applyPoolSettings(db, d.cfg)
		ctx, cancel := context.WithTimeout(ctx, d.cfg.DialTimeoutDuration())
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			d.err = fmt.Errorf("ping mysql: %w", err)
			_ = db.Close()
			return
		}
		d.db = db
	})
	return d.err
}

func (d *Driver) makeDSN() string {
	params := d.cfg.Params
	if params == "" {
		params = "parseTime=true&charset=utf8mb4&loc=Local"
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?%s",
		d.cfg.Username,
		d.cfg.Password,
		d.cfg.Host,
		d.cfg.Port,
		d.cfg.Database,
		params,
	)
}

func applyPoolSettings(db *sql.DB, cfg *config.MySQLConfig) {
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if lifetime := parseDuration(cfg.ConnMaxLifetime); lifetime > 0 {
		db.SetConnMaxLifetime(lifetime)
	}
	if idle := parseDuration(cfg.ConnMaxIdleTime); idle > 0 {
		db.SetConnMaxIdleTime(idle)
	}
}

func parseDuration(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return d
}
