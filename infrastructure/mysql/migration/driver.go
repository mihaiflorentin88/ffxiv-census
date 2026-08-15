package migration

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

//go:embed query/*.sql
var migrations embed.FS

// Runner executes schema migrations bundled under query/.
type Runner struct {
	driver contract.MySQLDriver
}

// NewRunner wires a migration runner against the shared MySQL driver.
func NewRunner(driver contract.MySQLDriver) contract.MigrationRunner {
	return &Runner{driver: driver}
}

// Up migrates the schema to the latest version.
func (r *Runner) Up(ctx context.Context) error {
	return r.exec(ctx, func(m *migrate.Migrate) error {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

// Down rolls the schema back by one version.
func (r *Runner) Down(ctx context.Context) error {
	return r.exec(ctx, func(m *migrate.Migrate) error {
		if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

func (r *Runner) exec(ctx context.Context, action func(*migrate.Migrate) error) (err error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire mysql connection: %w", err)
	}

	driver, err := mysql.WithInstance(db, &mysql.Config{})
	if err != nil {
		return fmt.Errorf("prepare mysql migrate driver: %w", err)
	}

	source, err := iofs.New(migrations, "query")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "mysql", driver)
	if err != nil {
		return fmt.Errorf("initialise migrator: %w", err)
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		if err == nil && sourceErr != nil {
			err = sourceErr
		}
		if err == nil && dbErr != nil {
			err = dbErr
		}
	}()

	if err := action(m); err != nil {
		return err
	}
	return nil
}
