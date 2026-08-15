package fixtures

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// NewLoader returns a fixture loader that executes raw SQL statements sequentially.
func NewLoader(driver contract.MySQLDriver) contract.FixtureLoader {
	return &loader{driver: driver}
}

type loader struct {
	driver contract.MySQLDriver
}

// Load executes every .sql file found in the provided directory.
func (l *loader) Load(ctx context.Context, dir string) error {
	if dir == "" {
		dir = DefaultDirectory
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read fixtures directory: %w", err)
	}

	db, err := l.driver.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire mysql connection: %w", err)
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := l.executeFile(ctx, db, path); err != nil {
			return err
		}
	}
	return nil
}

func (l *loader) executeFile(ctx context.Context, db *sql.DB, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fixture %s: %w", path, err)
	}

	statements := splitStatements(string(content))
	for _, stmt := range statements {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("execute fixture %s: %w", path, err)
		}
	}
	return nil
}

func splitStatements(raw string) []string {
	// naive split on semicolons; adequate for seed data.
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part+";")
		}
	}
	return out
}
