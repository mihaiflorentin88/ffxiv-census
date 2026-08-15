package contract

import "context"

// FixtureGenerator produces SQL fixtures on disk for manual review.
type FixtureGenerator interface {
	Generate(ctx context.Context, dir string, count int) (string, error)
}

// FixtureLoader executes the generated fixtures against the target database.
type FixtureLoader interface {
	Load(ctx context.Context, dir string) error
}
