package fixtures

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const (
	// DefaultDirectory stores generated fixture SQL files.
	DefaultDirectory = "infrastructure/mysql/fixtures/files"
	fileTemplate      = "-- generated fixture\nINSERT INTO examples (name, created_at) VALUES %s;\n"
)

// NewGenerator returns a fixture generator that emits SQL insert statements.
func NewGenerator() contract.FixtureGenerator {
	return &generator{}
}

type generator struct{}

// Generate writes a SQL file containing INSERT statements for the examples table.
func (generator) Generate(ctx context.Context, dir string, count int) (string, error) {
	if dir == "" {
		dir = DefaultDirectory
	}
	if count <= 0 {
		count = 1
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create fixtures directory: %w", err)
	}

	path := filepath.Join(dir, fmt.Sprintf("examples_%d.sql", time.Now().Unix()))
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create fixture file: %w", err)
	}
	defer file.Close()

	values := make([]string, 0, count)
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := fmt.Sprintf("example-%d", i+1)
		values = append(values, fmt.Sprintf("('%s', '%s')", name, now))
	}

	if _, err := fmt.Fprintf(file, fileTemplate, strings.Join(values, ",\n")); err != nil {
		return "", fmt.Errorf("write fixture file: %w", err)
	}
	return path, nil
}
