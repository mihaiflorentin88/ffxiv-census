package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Driver wraps a pooled *sql.DB and satisfies the SQLiteDriver contract.
// Migrations run automatically (goose Up) the first time the pool is opened.
type Driver struct {
	cfg   *config.SQLiteConfig
	migFS fs.FS

	once sync.Once
	db   *sql.DB
	err  error
}

// NewDriver builds a lazy SQLite driver. migrationsFS holds goose .sql files.
func NewDriver(cfg *config.SQLiteConfig, migrationsFS fs.FS) (contract.SQLiteDriver, error) {
	if cfg == nil {
		return nil, errors.New("sqlite config is nil")
	}
	if migrationsFS == nil {
		return nil, errors.New("sqlite migrations fs is nil")
	}
	d := &Driver{cfg: cfg, migFS: migrationsFS}
	if err := d.initialise(context.Background()); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Driver) Acquire(ctx context.Context) (*sql.DB, error) {
	if err := d.initialise(ctx); err != nil {
		return nil, err
	}
	return d.db, nil
}

func (d *Driver) Close() error {
	d.once.Do(func() {}) // ensure initialization is complete
	if d.db == nil {
		return nil
	}
	return d.db.Close()
}

func (d *Driver) Execute(ctx context.Context, query string, args ...any) (sql.Result, error) {
	db, err := d.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return db.ExecContext(ctx, query, args...)
}

func (d *Driver) FetchOne(ctx context.Context, query string, args ...any) (*sql.Row, error) {
	db, err := d.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return db.QueryRowContext(ctx, query, args...), nil
}

func (d *Driver) FetchMany(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db, err := d.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return db.QueryContext(ctx, query, args...)
}

// MigrateUp applies all pending migrations.
func (d *Driver) MigrateUp(ctx context.Context) error {
	db, err := d.Acquire(ctx)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(database.DialectSQLite3, db, d.migFS)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// MigrateDown rolls back all migrations (manual ops only).
func (d *Driver) MigrateDown(ctx context.Context) error {
	db, err := d.Acquire(ctx)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(database.DialectSQLite3, db, d.migFS)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("goose down: %w", err)
	}
	return nil
}

func (d *Driver) initialise(ctx context.Context) error {
	d.once.Do(func() {
		d.err = d.migrateUp(ctx)
	})
	return d.err
}

func (d *Driver) migrateUp(ctx context.Context) error {
	if dir := filepath.Dir(d.cfg.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}
	}
	dsn := d.makeDSN()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	d.db = db
	provider, err := goose.NewProvider(database.DialectSQLite3, db, d.migFS)
	if err != nil {
		db.Close()
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		db.Close()
		return fmt.Errorf("goose up: %w", err)
	}
	d.applyPoolSettings()
	return nil
}

func (d *Driver) makeDSN() string {
	busyMs := 5000
	if dur, err := time.ParseDuration(d.cfg.BusyTimeout); err == nil && dur > 0 {
		busyMs = int(dur.Milliseconds())
	}
	dsn := "file:" + d.cfg.Path +
		"?_pragma=busy_timeout(" + strconv.Itoa(busyMs) + ")" +
		"&_pragma=foreign_keys(1)"
	if d.cfg.JournalMode != "" {
		dsn += "&_pragma=journal_mode(" + d.cfg.JournalMode + ")"
	}
	return dsn
}

func (d *Driver) applyPoolSettings() {
	if d.cfg.MaxOpenConns > 0 {
		d.db.SetMaxOpenConns(d.cfg.MaxOpenConns)
	}
	if d.cfg.MaxIdleConns > 0 {
		d.db.SetMaxIdleConns(d.cfg.MaxIdleConns)
	}
}
