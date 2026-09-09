package repository_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres"
	postgresmigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/migration"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// testScanPolicy is the canonical scheduler policy used by repository tests.
// It mirrors the values the scan dispatcher will run with in production.
func testScanPolicy() contract.ProxyScanPolicy {
	return contract.ProxyScanPolicy{
		VerifyInterval:    time.Minute,
		FreshnessTTL:      2 * time.Minute,
		RecoveryBase:      time.Minute,
		RecoveryCap:       15 * time.Minute,
		RecoveryHorizon:   time.Hour,
		LeaseDuration:     30 * time.Second,
		MinRepeatInterval: time.Second,
		InconclusiveRetry: time.Minute,
		DeadAfter:         48 * time.Hour,
		FailThreshold:     5,
	}
}

// newTestDriver returns a database driver bound to a dedicated, uniquely
// named database that exists only for the duration of one test.
//
// Every test creates its own database (fixed prefix + random hex identifier),
// runs the full embedded migration set against it, and drops it in cleanup
// after closing all connections. No state is ever shared between tests, so
// no truncation is needed and tests cannot observe each other's rows.
//
// Connection and migration failures are fatal when REQUIRE_TEST_POSTGRES=1
// (CI / pre-merge verification); without it, an unreachable server skips the
// test so ordinary development runs stay green without integration
// prerequisites.
func newTestDriver(t *testing.T) contract.DatabaseDriver {
	t.Helper()
	required := os.Getenv("REQUIRE_TEST_POSTGRES") == "1"
	// TEST_POSTGRES_PORT lets a developer point the tests at a Postgres on a
	// non-default local port (e.g. a dedicated container on 5433).
	port := 5432
	if v := os.Getenv("TEST_POSTGRES_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			port = p
		}
	}

	ctx := context.Background()
	adminCfg := &config.PostgresConfig{
		Host:         "localhost",
		Port:         port,
		User:         "census",
		Password:     "secret",
		Database:     "postgres",
		SSLMode:      "disable",
		MaxOpenConns: 2,
		MaxIdleConns: 1,
	}
	admin, err := sql.Open("pgx", adminCfg.GetDSN())
	if err != nil {
		testPostgresUnavailable(t, required, err)
	}
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		testPostgresUnavailable(t, required, err)
	}

	name, err := randomTestDatabaseName()
	if err != nil {
		t.Fatalf("generate test database name: %v", err)
	}
	// CREATE DATABASE cannot run inside a transaction.
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		_ = admin.Close()
		t.Fatalf("create test database %s: %v", name, err)
	}
	_ = admin.Close()

	cfg := &config.PostgresConfig{
		Host:         "localhost",
		Port:         port,
		User:         "census",
		Password:     "secret",
		Database:     name,
		SSLMode:      "disable",
		MaxOpenConns: 5,
		MaxIdleConns: 2,
	}
	driver, err := postgres.NewDriver(cfg, postgresmigration.FS())
	if err != nil {
		t.Fatalf("migrate test database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = driver.Close()
		drop, err := sql.Open("pgx", adminCfg.GetDSN())
		if err != nil {
			return
		}
		defer drop.Close()
		// WITH (FORCE) also terminates leaked connections (PG >= 13); the
		// plain fallback covers older servers.
		if _, err := drop.ExecContext(ctx, `DROP DATABASE `+name+` WITH (FORCE)`); err != nil {
			_, _ = drop.ExecContext(ctx, `DROP DATABASE `+name)
		}
	})
	return driver
}

func testPostgresUnavailable(t *testing.T, required bool, err error) {
	t.Helper()
	if required {
		t.Fatalf("test postgres required but unavailable: %v", err)
	}
	t.Skipf("postgres not available: %v", err)
}

// randomTestDatabaseName generates an identifier composed of a fixed prefix
// and hex characters only, so it is always a valid bare SQL identifier.
func randomTestDatabaseName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "census_test_" + hex.EncodeToString(b), nil
}

// mustInsertProxy inserts a proxy and returns its assigned ID.
func mustInsertProxy(t *testing.T, repo contract.ProxyRepository, ip string, port int) int64 {
	t.Helper()
	id, inserted, err := repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: ip, Port: port, Source: "test",
	})
	if err != nil {
		t.Fatalf("insert proxy %s: %v", ip, err)
	}
	if !inserted {
		t.Fatalf("proxy %s/%d not inserted", ip, port)
	}
	return id
}

// mustExec runs a write statement or fails the test.
func mustExec(t *testing.T, driver contract.DatabaseDriver, query string, args ...any) {
	t.Helper()
	if _, err := driver.Execute(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// seedScanState applies raw scheduling-state setters to one proxy. Setters use
// SQL-relative timestamps so boundary tests never depend on wall-clock sleeps.
func seedScanState(t *testing.T, driver contract.DatabaseDriver, id int64, setters string) {
	t.Helper()
	mustExec(t, driver, `UPDATE proxies SET `+setters+` WHERE id = $1`, id)
}

// seedLegacyActiveProxy inserts a row the way pre-scheduling writers did:
// historical status only, no scheduling columns. It returns the new ID.
func seedLegacyActiveProxy(t *testing.T, driver contract.DatabaseDriver, ip string) int64 {
	t.Helper()
	db, err := driver.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	var id int64
	err = db.QueryRowContext(context.Background(), `
		INSERT INTO proxies (protocol, ip, port, status, source, first_seen_at, created_at, updated_at)
		VALUES ('http', $1, 80, 'active', 'test', statement_timestamp(), statement_timestamp(), statement_timestamp())
		RETURNING id`, ip).Scan(&id)
	if err != nil {
		t.Fatalf("seed legacy active proxy: %v", err)
	}
	return id
}

// scanUpdate builds a relative ScanUpdate carrying the canonical policy
// copies (freshness TTL, dead-after, fail threshold) for one outcome.
func scanUpdate(outcome contract.ScanOutcome, next, repeat time.Duration) contract.ScanUpdate {
	return contract.ScanUpdate{
		Outcome:       outcome,
		LatencyMS:     42,
		NextDelay:     next,
		RepeatDelay:   repeat,
		FreshnessTTL:  2 * time.Minute,
		DeadAfter:     48 * time.Hour,
		FailThreshold: 5,
	}
}

// mustGetProxy fetches a proxy or fails the test when it is missing.
func mustGetProxy(t *testing.T, repo contract.ProxyRepository, id int64) *contract.ProxyRecord {
	t.Helper()
	p, err := repo.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get proxy %d: %v", id, err)
	}
	if p == nil {
		t.Fatalf("proxy %d missing", id)
	}
	return p
}
