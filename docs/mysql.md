# MySQL Toolkit

This service ships ready-to-run MySQL helpers when the _MySQL data access scaffold_ feature is enabled.

- **Configuration:** configure credentials under `config.mysql` inside `config/config.toml`. Update `database`, `username`, and `password` along with pool tuning knobs (`max_open_conns`, `max_idle_conns`, etc.). The driver generates DSNs with `charset=utf8mb4&parseTime=true&loc=Local` by default.
- **Driver usage:** access the shared driver via `container.Load.MySQL()`; it exposes a `Acquire(ctx)` helper returning a pooled `*sql.DB`. Call `Close()` during shutdown if you build an explicit teardown.
- **Migrations:** store `.sql` files in `infrastructure/mysql/migration/query/`. Run `./bin/ffxiv-census migrate --direction up` (or `down`) to apply schema changes.
- **Fixtures:** SQL fixture files live under `infrastructure/mysql/fixtures/files`. Generate examples with `./bin/ffxiv-census fixtures generate --count 5` and load them via `./bin/ffxiv-census fixtures load`.
- **Repositories:** the example repository at `infrastructure/mysql/repository/example.go` demonstrates how to satisfy `contract.ExampleRepository`, using context-aware calls and returning typed rows.
