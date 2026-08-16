# Foundation: SQLite + goose runtime migrations — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the MySQL boilerplate with a pure-Go SQLite driver (modernc.org/sqlite) whose schema is managed by pressly/goose/v3 migrations embedded in the binary and applied automatically at runtime on first use.

**Architecture:** Hexagonal as per repo docs. New `contract.SQLiteDriver` port (same shape as the old `MySQLDriver`, plus `MigrateUp`/`MigrateDown`), implemented by `infrastructure/sqlite` with an injected `fs.FS` of migrations (embed in production, `fstest.MapFS` in tests). The container exposes a lazy `SQLite()` accessor. All MySQL/example/fixtures boilerplate is removed.

**Tech Stack:** Go 1.25, modernc.org/sqlite, pressly/goose/v3, cobra, viper. Spec: `docs/superpowers/specs/2026-08-16-lodestone-census-design.md`.

**Verification commands:** `go build ./...`, `go test ./...`, `make lint` (golangci-lint). Run from repo root.

---

### Task 1: Remove MySQL/example/fixtures boilerplate

**Files:**
- Delete: `infrastructure/mysql/` (whole tree), `mock/mysql/`, `mock/neo4j/`, `domain/example/`, `cmd/http/app/example/`, `cmd/cli/fixtures.go`
- Delete: `port/contract/mysql.go`, `port/contract/migration.go`, `port/contract/fixture.go`, `port/contract/example_repository_contract.go`, `port/contract/example.go`, `docs/mysql.md`
- Modify: `container/infrastructure.go` (remove MySQL accessors), `container/domain.go` (remove ExampleService), `cmd/http/router.go` (remove example route), `cmd/cli/migrate.go` (temporarily comment the body — rewired in Task 6), `config/config.go` (remove MySQLConfig — done in Task 3, so for now leave it; this task only removes code that references deleted packages)
- Modify: `go.mod`/`go.sum` via `go mod tidy`

- [ ] **Step 1: Delete the directories and files listed above**

```bash
git rm -r infrastructure/mysql mock/mysql mock/neo4j domain/example cmd/http/app/example
git rm cmd/cli/fixtures.go port/contract/mysql.go port/contract/migration.go port/contract/fixture.go port/contract/example_repository_contract.go port/contract/example.go docs/mysql.md
```

- [ ] **Step 2: Empty `container/domain.go` to a placeholder**

```go
package container

// DomainContainer wires domain services. Census services are added in later phases.
type DomainContainer struct{}
```

- [ ] **Step 3: Rewrite `container/infrastructure.go` without MySQL**

```go
package container

import (
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type InfrastructureContainer struct {
	httpClient contract.HTTPClient
	statsd     contract.StatsdClient
}

func (s *ServiceContainer) HTTPClient() contract.HTTPClient {
	if s.infrastructure.httpClient != nil {
		return s.infrastructure.httpClient
	}
	client := httpclient.New(nil)
	s.infrastructure.httpClient = client
	return client
}

func (s *ServiceContainer) Statsd() contract.StatsdClient {
	if s.infrastructure.statsd != nil {
		return s.infrastructure.statsd
	}
	cfg := s.Config().Metrics
	if cfg == nil {
		logging.Warn("container.metrics", "metrics config missing")
		return nil
	}
	client, err := metrics.New(cfg.Endpoint, cfg.Prefix)
	if err != nil {
		logging.Error("container.metrics", fmt.Sprintf("failed to create statsd client: %v", err))
		return nil
	}
	s.infrastructure.statsd = client
	return client
}
```

- [ ] **Step 4: Update `cmd/http/router.go` (drop example handler)**

```go
package http

import (
	"net/http"

	roothandler "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/root/handler"
	ui "github.com/mihaiflorentin88/ffxiv-census/cmd/http/ui"
)

const RouteHealth = "/health"

type Router struct {
	HealthController roothandler.Controller
}

func NewRouter() Router {
	return Router{
		HealthController: roothandler.NewController(),
	}
}

func (r *Router) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc(RouteHealth, r.HealthController.Check)
	ui.Register(mux)
}
```

- [ ] **Step 5: Temporarily stub `cmd/cli/migrate.go` so the tree compiles**

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Runs SQLite schema migrations (rewired in Task 6)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("migrate command is being reworked")
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
```

- [ ] **Step 6: Tidy modules and verify the build**

```bash
go mod tidy && go build ./... && go vet ./...
```

Expected: build succeeds; `go.mod` no longer lists go-sql-driver/mysql, golang-migrate, or cactus/go-statsd-client.

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "chore: remove mysql/example/fixtures boilerplate"
```

---

### Task 2: Toolchain + new dependencies

**Files:**
- Modify: `go.mod` (go directive, new deps), `Makefile` (docker image)

- [ ] **Step 1: Bump go directive and fetch dependencies**

```bash
go mod edit -go=1.25
go get modernc.org/sqlite@latest
go get github.com/pressly/goose/v3@latest
go mod tidy
```

- [ ] **Step 2: Update Makefile docker-build image**

In `Makefile`, change `golang:1.22` to `golang:1.25` (docker-build target only).

- [ ] **Step 3: Verify**

```bash
go build ./...
```

Expected: success; go.mod requires modernc.org/sqlite and github.com/pressly/goose/v3.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum Makefile && git commit -m "chore: bump go 1.25, add sqlite and goose dependencies"
```

---

### Task 3: Config — SQLite section

**Files:**
- Modify: `config/config.go`, `config/config.toml`
- Test: `config/sqlite_test.go`

- [ ] **Step 1: Write the failing tests**

`config/sqlite_test.go`:

```go
package config

import (
	"path/filepath"
	"testing"
)

func TestNewConfig_SQLiteDefaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.SQLite == nil {
		t.Fatal("expected sqlite section to be present")
	}
	if cfg.SQLite.Path != "data/ffxiv-census.db" {
		t.Errorf("path = %q, want data/ffxiv-census.db", cfg.SQLite.Path)
	}
	if cfg.SQLite.JournalMode != "WAL" {
		t.Errorf("journal_mode = %q, want WAL", cfg.SQLite.JournalMode)
	}
	if cfg.SQLite.MaxOpenConns != 4 {
		t.Errorf("max_open_conns = %d, want 4", cfg.SQLite.MaxOpenConns)
	}
}

func TestSQLiteConfig_EnvOverride(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "test.db"))
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	got := cfg.SQLite.Path
	want := filepath.Join(t.TempDir(), "test.db")
	if got != want {
		t.Errorf("SQLITE_PATH override: got %q, want %q", got, want)
	}
}
```

Note: `t.Setenv` + `t.TempDir` in the same test is fine; compute the path once:

```go
func TestSQLiteConfig_EnvOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("SQLITE_PATH", path)
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.SQLite.Path != path {
		t.Errorf("SQLITE_PATH override: got %q, want %q", cfg.SQLite.Path, path)
	}
}
```

Use the second version (compute path before Setenv).

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./config/
```

Expected: FAIL — `cfg.SQLite` undefined (compile error).

- [ ] **Step 3: Implement**

In `config/config.go`: remove `MySQLConfig`, its method, and the `MySQL` field; add:

```go
type Config struct {
	App     AppConfig      `mapstructure:"app"`
	Logging *LoggingConfig `mapstructure:"logging"`
	HTTP    HTTPConfig     `mapstructure:"http"`
	Auth    *AuthConfig    `mapstructure:"auth"`
	Metrics *MetricsConfig `mapstructure:"metrics"`
	SQLite  *SQLiteConfig  `mapstructure:"sqlite"`
}

type SQLiteConfig struct {
	Path         string `mapstructure:"path"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
	BusyTimeout  string `mapstructure:"busy_timeout"`
	JournalMode  string `mapstructure:"journal_mode"`
}
```

In `config/config.toml`: remove the `[mysql]` section; add:

```toml
[sqlite]
path = "data/ffxiv-census.db"
max_open_conns = 4
max_idle_conns = 4
busy_timeout = "5s"
journal_mode = "WAL"
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./config/ -v
```

Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add config/ && git commit -m "feat(config): sqlite config section replaces mysql"
```

---

### Task 4: SQLiteDriver contract + driver with runtime goose migrations

**Files:**
- Create: `port/contract/sqlite.go`
- Create: `infrastructure/sqlite/driver.go`
- Create: `infrastructure/sqlite/migration/embed.go`
- Create: `infrastructure/sqlite/migration/query/` (empty for now — holds `.sql` files from phase 2)
- Test: `infrastructure/sqlite/driver_test.go`

- [ ] **Step 1: Write the contract**

`port/contract/sqlite.go`:

```go
package contract

import (
	"context"
	"database/sql"
)

// SQLiteDriver defines access to the SQLite database, including runtime migrations.
type SQLiteDriver interface {
	Acquire(ctx context.Context) (*sql.DB, error)
	Close() error
	Execute(ctx context.Context, query string, args ...any) (sql.Result, error)
	FetchOne(ctx context.Context, query string, args ...any) (*sql.Row, error)
	FetchMany(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	MigrateUp(ctx context.Context) error
	MigrateDown(ctx context.Context) error
}
```

- [ ] **Step 2: Write the failing tests**

`infrastructure/sqlite/driver_test.go`:

```go
package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mihaiflorentin88/ffxiv-census/config"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"00001_create_probe.sql": &fstest.MapFile{
			Data: []byte("-- +goose Up\nCREATE TABLE probe (id INTEGER PRIMARY KEY, name TEXT);\n-- +goose Down\nDROP TABLE probe;\n"),
		},
	}
}

func testConfig(t *testing.T) *config.SQLiteConfig {
	t.Helper()
	return &config.SQLiteConfig{
		Path:         filepath.Join(t.TempDir(), "test.db"),
		MaxOpenConns: 2,
		MaxIdleConns: 2,
		BusyTimeout:  "2s",
		JournalMode:  "WAL",
	}
}

func TestDriver_InitAppliesMigrations(t *testing.T) {
	driver, err := NewDriver(testConfig(t), testFS())
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	defer driver.Close()

	var name string
	err = driver.FetchOne(context.Background(), "SELECT name FROM probe WHERE id = ?", 999).Scan(&name)
	// Scan must surface "no rows" (table exists, query ran) — NOT "no such table".
	if err == nil || strings.Contains(err.Error(), "no such table") {
		t.Fatalf("probe table missing after init (migrations not applied?): %v", err)
	}
}

func TestDriver_MigrateDownRollsBack(t *testing.T) {
	driver, err := NewDriver(testConfig(t), testFS())
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	defer driver.Close()

	if err := driver.MigrateDown(context.Background()); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}
	var n int
	err = driver.FetchOne(context.Background(), "SELECT COUNT(*) FROM probe").Scan(&n)
	if err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("expected probe table dropped after MigrateDown, got: %v", err)
	}
}

func TestDriver_ExecuteRoundtrip(t *testing.T) {
	driver, err := NewDriver(testConfig(t), testFS())
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	defer driver.Close()

	ctx := context.Background()
	if _, err := driver.Execute(ctx, "INSERT INTO probe (name) VALUES (?)", "alpha"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var name string
	if err := driver.FetchOne(ctx, "SELECT name FROM probe WHERE id = ?", 1).Scan(&name); err != nil {
		t.Fatalf("select one: %v", err)
	}
	if name != "alpha" {
		t.Errorf("name = %q, want alpha", name)
	}
	rows, err := driver.FetchMany(ctx, "SELECT name FROM probe")
	if err != nil {
		t.Fatalf("select many: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestDriver_NilConfigFails(t *testing.T) {
	if _, err := NewDriver(nil, testFS()); err == nil {
		t.Fatal("expected error for nil config")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./infrastructure/sqlite/
```

Expected: FAIL — package does not exist / NewDriver undefined.

- [ ] **Step 4: Implement the driver**

`infrastructure/sqlite/driver.go`:

```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
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
	cfg     *config.SQLiteConfig
	migFS   fs.FS
	applied bool

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
		if err := d.migrateUp(ctx); err != nil {
			d.err = err
			return
		}
		d.applied = true
	})
	return d.err
}

func (d *Driver) migrateUp(ctx context.Context) error {
	if dir := filepath.Dir(d.cfg.Path); dir != "" && dir != "." {
		if err := mkdirAll(dir); err != nil {
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
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
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
```

`infrastructure/sqlite/migration/embed.go`:

```go
package migration

import (
	"embed"
	"io/fs"
)

//go:embed query/*.sql
var embedded embed.FS

// FS exposes the embedded goose migrations.
func FS() fs.FS {
	return embedded
}
```

Note: `mkdirAll` — use `os.MkdirAll(dir, 0o755)` directly instead of a helper; delete the helper reference and import `os`.

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./infrastructure/sqlite/ -v
```

Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
git add port/contract/sqlite.go infrastructure/sqlite/ && git commit -m "feat(sqlite): driver with runtime goose migrations"
```

---

### Task 5: Container wiring

**Files:**
- Modify: `container/infrastructure.go`
- Test: `container/sqlite_test.go`

- [ ] **Step 1: Write the failing test**

`container/sqlite_test.go`:

```go
package container

import (
	"path/filepath"
	"testing"
)

func TestServiceContainer_SQLiteDriver(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	driver := Load.SQLite()
	if driver == nil {
		t.Fatal("expected non-nil sqlite driver")
	}
	defer driver.Close()
}

func TestServiceContainer_SQLiteDriverCached(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	first := Load.SQLite()
	second := Load.SQLite()
	if first != second {
		t.Fatal("expected cached driver instance")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./container/
```

Expected: FAIL — `Load.SQLite` undefined.

- [ ] **Step 3: Implement**

In `container/infrastructure.go`, add the import `sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"`, `"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"`, add field `sqliteDriver contract.SQLiteDriver`, and accessor:

```go
func (s *ServiceContainer) SQLite() contract.SQLiteDriver {
	if s.infrastructure.sqliteDriver != nil {
		return s.infrastructure.sqliteDriver
	}
	cfg := s.Config().SQLite
	if cfg == nil {
		logging.Warn("container.sqlite", "sqlite config missing")
		return nil
	}
	driver, err := sqlite.NewDriver(cfg, sqlitemigration.FS())
	if err != nil {
		logging.Error("container.sqlite", fmt.Sprintf("failed to create sqlite driver: %v", err))
		return nil
	}
	s.infrastructure.sqliteDriver = driver
	return driver
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./container/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add container/ && git commit -m "feat(container): sqlite driver accessor with runtime migrations"
```

---

### Task 6: Rework the migrate CLI command

**Files:**
- Modify: `cmd/cli/migrate.go`
- Test: `cmd/cli/migrate_test.go`

- [ ] **Step 1: Write the failing test**

`cmd/cli/migrate_test.go`:

```go
package cli

import "testing"

func TestMigrateCmdRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "migrate" {
			found = true
		}
	}
	if !found {
		t.Fatal("migrate command not registered on root")
	}
}

func TestRunMigrateInvalidDirection(t *testing.T) {
	if err := runMigrate("sideways"); err == nil {
		t.Fatal("expected error for invalid direction")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./cmd/cli/
```

Expected: FAIL — `runMigrate` undefined.

- [ ] **Step 3: Implement**

`cmd/cli/migrate.go`:

```go
package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
)

func runMigrate(direction string) error {
	driver := container.Load.SQLite()
	if driver == nil {
		return fmt.Errorf("sqlite driver not initialised (check config/sqlite)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch direction {
	case "up":
		return driver.MigrateUp(ctx)
	case "down":
		return driver.MigrateDown(ctx)
	default:
		return fmt.Errorf("invalid direction %q, use up or down", direction)
	}
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Runs SQLite schema migrations (up applies all pending; down rolls back all)",
	RunE: func(cmd *cobra.Command, args []string) error {
		direction, _ := cmd.Flags().GetString("direction")
		return runMigrate(direction)
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateCmd.Flags().String("direction", "up", "Migration direction: up or down")
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./cmd/cli/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/cli/migrate.go cmd/cli/migrate_test.go && git commit -m "feat(cli): migrate command on sqlite goose driver"
```

---

### Task 7: Documentation

**Files:**
- Create: `docs/sqlite.md`
- Modify: `docs/container.md`, `docs/architecture.md`, `docs/getting-started.md`, `README.md`

- [ ] **Step 1: Write `docs/sqlite.md`**

Content must explain: SQLite as the single datastore (data + queue in later phases); modernc.org/sqlite pure-Go driver (why: CGO_ENABLED=0 cross-compile); DSN pragmas (busy_timeout, WAL, foreign_keys); pool settings; goose runtime migrations — embedded `infrastructure/sqlite/migration/query/*.sql`, applied automatically on first `SQLite()` use by any binary (server, consumers, publishers); `migrate --direction up|down` for manual ops (down = full rollback, destructive); file location from `[sqlite]` config + `SQLITE_PATH` env override.

- [ ] **Step 2: Update existing docs**

- `docs/container.md`: replace MySQL accessor documentation with `SQLite()`; note removed accessors (fixtures, example repository).
- `docs/architecture.md`: replace MySQL/Redis references in layer descriptions with SQLite + future queue; keep hexagonal rules unchanged.
- `docs/getting-started.md`: replace MySQL prerequisite/commands with SQLite notes (`data/` dir auto-created); update project layout listing.
- `README.md`: replace `go 1.22`/MySQL mentions if present; keep command list accurate (`server`, `migrate`).

- [ ] **Step 3: Commit**

```bash
git add docs/ README.md && git commit -m "docs: sqlite storage and runtime migrations"
```

---

### Task 8: Final verification

- [ ] **Step 1: Full test suite with race detector**

```bash
go test ./... -race
```

Expected: all PASS.

- [ ] **Step 2: Lint**

```bash
make lint
```

Expected: no issues (fix any reported inline).

- [ ] **Step 3: Build + smoke test**

```bash
make build && ./bin/ffxiv-census migrate --direction up && ls data/
```

Expected: `data/ffxiv-census.db` created, command exits 0.

- [ ] **Step 4: Commit any fixes**

```bash
git add -A && git commit -m "chore: foundation phase verification"
```
