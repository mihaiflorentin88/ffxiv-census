package repository_test

import (
	"context"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres"
	postgresmigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/migration"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestDriver(t *testing.T) contract.DatabaseDriver {
	t.Helper()
	cfg := &config.PostgresConfig{
		Host:         "localhost",
		Port:         5432,
		User:         "census",
		Password:     "secret",
		Database:     "ffxiv_census",
		SSLMode:      "disable",
		MaxOpenConns: 5,
		MaxIdleConns: 2,
	}
	driver, err := postgres.NewDriver(cfg, postgresmigration.FS())
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	t.Cleanup(func() {
		cleanTables(driver)
		_ = driver.Close()
	})
	cleanTables(driver)
	return driver
}

func cleanTables(driver contract.DatabaseDriver) {
	_, _ = driver.Execute(context.Background(), `
		TRUNCATE proxies, characters, character_jobs, character_gear, character_milestones,
		         milestone_achievements, census_runs
		RESTART IDENTITY CASCADE
	`)
	_, _ = driver.Execute(context.Background(), `TRUNCATE id_sweep_state`)
	_, _ = driver.Execute(context.Background(), `TRUNCATE ui_stats_snapshots`)
}
