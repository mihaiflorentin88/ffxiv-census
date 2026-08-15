package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ExampleRepository persists example rows into the examples table.
type ExampleRepository struct {
	driver contract.MySQLDriver
}

// NewExampleRepository wires the mysql driver into a repository instance.
func NewExampleRepository(driver contract.MySQLDriver) contract.ExampleRepository {
	return &ExampleRepository{driver: driver}
}

// Insert stores a single example row and returns its identifier.
func (r *ExampleRepository) Insert(ctx context.Context, name string) (int64, error) {
	result, err := r.driver.Execute(ctx, `
		INSERT INTO examples (name, created_at)
		VALUES (?, ?)
	`, name, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("insert example row: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("obtain last insert id: %w", err)
	}
	return id, nil
}

// List fetches all records ordered by identifier.
func (r *ExampleRepository) List(ctx context.Context) ([]contract.ExampleRecord, error) {
	rows, err := r.driver.FetchMany(ctx, `
		SELECT id, name, created_at
		FROM examples
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query examples: %w", err)
	}
	defer rows.Close()

	var items []contract.ExampleRecord
	for rows.Next() {
		var rec contract.ExampleRecord
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan example row: %w", err)
		}
		items = append(items, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate examples: %w", err)
	}
	return items, nil
}
