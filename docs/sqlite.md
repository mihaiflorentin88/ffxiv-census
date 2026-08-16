# SQLite Storage & Runtime Migrations

SQLite is the single datastore for ffxiv-census. It holds application data today and will host the message queue in later phases, so the service runs with a single embedded file rather than separate infrastructure processes.

## Why modernc.org/sqlite?

The project cross-compiles with `CGO_ENABLED=0` (see `Makefile` targets `build-linux-amd64`, etc.). The canonical `mattn/go-sqlite3` driver requires CGO, which blocks static builds for other architectures. We use **modernc.org/sqlite** — a pure-Go SQLite implementation that registers under the `database/sql` driver name `"sqlite"` and works with `CGO_ENABLED=0`.

## Configuration

The `[sqlite]` section in `config/config.toml` controls the datastore:

```toml
[sqlite]
path           = "data/ffxiv-census.db"
max_open_conns = 4
max_idle_conns = 4
busy_timeout   = "5s"
journal_mode   = "WAL"
```

| Field            | Purpose                                                                                       |
| ---------------- | --------------------------------------------------------------------------------------------- |
| `path`           | File path for the database. Parent directory is created automatically (`os.MkdirAll`).        |
| `max_open_conns` | Passed to `sql.DB.SetMaxOpenConns`. Keep low — SQLite serialises writes.                      |
| `max_idle_conns` | Passed to `sql.DB.SetMaxIdleConns`.                                                           |
| `busy_timeout`   | Duration string parsed by `time.ParseDuration`; mapped to the `busy_timeout` pragma (ms).     |
| `journal_mode`   | Set via the `journal_mode` pragma. Default `WAL` enables concurrent readers with one writer.  |

### Environment override

Set `SQLITE_PATH` to redirect the database file without editing the config:

```bash
SQLITE_PATH=/var/lib/ffxiv-census/production.db ./bin/ffxiv-census server --start
```

Viper's `SetEnvKeyReplacer` maps `SQLITE_PATH` → `sqlite.path` (dots become underscores, all upper-case).

## DSN Pragmas

The driver builds a DSN with these pragmas baked in:

- `busy_timeout(5000)` — waits up to 5 s when the database is locked instead of returning `SQLITE_BUSY`.
- `foreign_keys(1)` — enforces FK constraints (off by default in SQLite).
- `journal_mode(WAL)` — write-ahead log; safe for concurrent reads and a single writer.

Example DSN produced by the driver:

```
file:data/ffxiv-census.db?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)
```

## Pool Settings

SQLite is an embedded database — there is no connection pool in the traditional sense. `max_open_conns` and `max_idle_conns` cap concurrent `*sql.DB` handles. Defaults from `config.toml`:

```
max_open_conns = 4
max_idle_conns = 4
```

Keep these low. SQLite serialises writes through a file-level lock, so more connections rarely improve throughput.

## Runtime Migrations (goose)

Schema changes are managed with [goose](https://github.com/pressly/goose) using SQL migrations embedded into the binary at compile time.

### Embedded migration files

Migrations live in `infrastructure/sqlite/migration/query/*.sql` and are embedded via `//go:embed query/*.sql` in `infrastructure/sqlite/migration/embed.go`. The `FS()` function returns an `fs.FS` scoped to the `query/` subdirectory, which is passed to the goose provider.

### Automatic migration on first use

Every binary that calls `container.Load.SQLite()` — the HTTP server, consumers, publishers — triggers `goose.Up()` automatically the first time the driver is acquired. This is implemented with `sync.Once` inside `Driver.initialise()`:

```go
func (d *Driver) initialise(ctx context.Context) error {
    d.once.Do(func() {
        d.err = d.migrateUp(ctx)
    })
    return d.err
}
```

No separate migration step is needed. The schema is always current when the process starts.

### Manual operations

The `migrate` CLI command provides manual control:

```bash
# Apply all pending migrations (same as automatic)
./bin/ffxiv-census migrate --direction up

# Roll back ALL migrations (destructive — drops everything)
./bin/ffxiv-census migrate --direction down
```

`--direction down` calls `goose.DownTo(0)` — it rolls back every migration to version 0. This is destructive and intended for development/testing only.

## The SQLiteDriver Contract

The `port/contract.SQLiteDriver` interface defines the surface area:

```go
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

- **`Acquire`** returns the underlying `*sql.DB` for code that needs direct access (e.g., transactions).
- **`Execute` / `FetchOne` / `FetchMany`** are convenience wrappers for common query patterns.
- **`MigrateUp` / `MigrateDown`** apply or reverse goose migrations.
- **`Close`** shuts down the database handle.

Infrastructure code in `infrastructure/sqlite/driver.go` implements this contract. Domain and application code depend only on the interface.

## Container Wiring

The `container.Load.SQLite()` accessor lazily constructs the driver:

```go
func (s *ServiceContainer) SQLite() contract.SQLiteDriver {
    // ... reads config, passes embedded migrations FS, caches the instance
}
```

First call opens the database, runs migrations, and applies pool settings. Subsequent calls return the cached driver.

## File Location

By default, the database lives at `data/ffxiv-census.db` relative to the working directory. The parent `data/` directory is created automatically on first use. In production, override with `SQLITE_PATH` to point at a persistent volume.

## Adding a New Migration

1. Create a new file in `infrastructure/sqlite/migration/query/` following goose naming: `NNNNN_description.sql`.
2. Add `-- +goose Up` and `-- +goose Down` sections with `-- +goose StatementBegin` / `-- +goose StatementEnd` delimiters.
3. Commit. The next binary start applies it automatically.

Example:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE characters (
    id    INTEGER PRIMARY KEY,
    name  TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE characters;
-- +goose StatementEnd
```
