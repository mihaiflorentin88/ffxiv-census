# ID Sweep Cursor and Achievement Chain Verification

Date: 2026-08-24

Status: Implemented after explicit user approval

## Goal

Make automatic ID discovery advance through the Lodestone ID space even when a
batch contains no public character, while preserving reliable downstream
`achievement-census` publication for every public character discovered by
`id-sweep` or refreshed by `character-census`.

## Production Evidence and Root Cause

The reported milestone maximum is not evidence of a broken downstream chain:

- `characters.MAX(id) = 1584838`.
- `character_milestones.MAX(character_id) = 1584734`.
- All eight character rows above `1584734` have
  `achievements_private = 1`, no latest achievement, and no milestone rows.
- `characters.achievements_private` defaults to `0`; only
  `achievement-census` changed those eight rows to `1`.
- A direct request for character `1584838` returned HTTP 403 from the
  achievement list.
- RabbitMQ had zero ready `achievement-census` messages and four actively
  consumed messages, so there was no achievement backlog.

The actual discovery failure is the stateless automatic range calculation:

```text
from = MAX(characters.id) + 1
to   = MAX(characters.id) + count
```

The production cron runs `--auto --count 550 --chunk-size 1` every minute. If
that 550-ID window has no discoverable public character, `MAX(characters.id)`
does not change. Every later cron run republishes the same 550 IDs indefinitely.
At diagnosis time the newest discovery was more than six hours old while the
cron and consumers remained healthy.

`character_milestones` is intentionally sparse: private characters, public
characters with no earned Chocobo milestone, and characters whose first missing
chain achievement is incomplete do not receive a row. Operational discovery
progress must therefore be measured with the sweep cursor and queue state, not
by comparing the maximum IDs of `characters` and `character_milestones`.

## Final Design

### Persistent monotonic cursor

Add PostgreSQL migration `00014_create_id_sweep_state.sql` with a singleton
`id_sweep_state` row containing `next_id BIGINT NOT NULL` and `updated_at`.
Initialize it lazily and atomically to `MAX(characters.id) + 1` so deployment
continues from the current frontier without rescanning the entire database.

Extend the character repository port with two narrowly scoped operations:

```go
IDSweepCursor(ctx context.Context) (uint32, error)
AdvanceIDSweepCursor(ctx context.Context, expected, next uint32) error
```

Implement both PostgreSQL and in-memory fake adapters. Advancement uses a
compare-and-set update. If another publisher already advanced to the same or a
later value, the operation succeeds without rewinding; an unexpected lower or
incompatible value returns an error.

### Publish first, advance second

For `publish id-sweep --auto`:

1. Read/initialize the persistent cursor.
2. Build `[cursor, cursor+count-1]`, with explicit `uint32` overflow handling.
3. Publish every chunk.
4. Advance the cursor to `to+1` only after all publishes succeed.

If publication fails partway through, the cursor remains unchanged. The next
cron run republishes the whole range; duplicated jobs are safe because handlers
and database writes are idempotent. This favors duplicates over permanently
skipped IDs. Kubernetes already uses `concurrencyPolicy: Forbid`; compare-and-set
also protects manual or accidental concurrent publishers.

Explicit `--from`/`--to` and `--fill-gaps` behavior remains unchanged and does
not mutate the forward cursor.

### Chaining guarantees

Keep the existing direct downstream rules:

- A successful public `id-sweep` upsert returns one `achievement-census` job.
- A successful public `character-census` upsert returns one
  `achievement-census` job.
- A private profile that cannot provide race/profile data is not stored or
  chained.
- An achievement-list 403 records `achievements_private = 1` and correctly
  creates no milestone row.

Add explicit regression tests for both public-success paths. Repair existing
character handler fixtures that accidentally use an empty race and therefore
exercise the intentional private-profile early return; production behavior must
not be weakened merely to satisfy those fixtures.

### Observability

Add structured fields to the publisher completion log:

- `from_id`
- `to_id`
- `next_id`
- `auto`
- published job count

This makes consecutive range advancement directly verifiable without consuming
or inspecting RabbitMQ message bodies.

## Strict TDD Tasks

1. Add a failing command-level test showing that two successful automatic
   publications advance to consecutive, non-overlapping ranges even when no new
   character is inserted.
2. Add a failing test showing a partial queue publication error leaves the
   cursor unchanged and causes the full range to be retried.
3. Add failing repository tests for initialization from `MAX(characters.id)`,
   compare-and-set advancement, stale concurrent advancement, and overflow.
4. Add/confirm failing public-success chain tests for `id-sweep` and
   `character-census`, then correct invalid empty-race fixtures.
5. Implement the migration, contract, fake, PostgreSQL adapter, publisher flow,
   and structured range logging with the minimum code needed for green tests.
6. Update `docs/events.md`, `docs/queue.md`, and CLI operational documentation to
   distinguish discovery cursor progress from milestone-row coverage.

For every production-code change, capture the focused red result before editing
production code, then run the same test green before refactoring.

## Verification

Run:

```bash
go test -count=50 -run 'Test.*IDSweep.*Cursor|Test.*IDSweep.*Advance|Test.*ChainsAchievement' ./cmd/cli ./domain/census/handler
go test -count=1 ./infrastructure/postgres/repository ./mock/repository
go test -race -count=1 ./domain/census/handler ./domain/census/worker
make test
make fmt
make lint
make build
git diff --check
```

Any unrelated baseline failure must be reported precisely; affected cursor,
publisher, chain, repository, and worker tests must all pass.

## Release and Production Smoke

After approval and verification:

1. Commit and push code, migration, tests, and documentation.
2. Confirm the next Git and Docker patch tag is unused.
3. Build/push ARM64 release and `latest`, deploy with Helm, and run the rollout
   check described in `README.md`.
4. Record the current cursor and character maximum.
5. Observe two successful minute cron runs and verify `from_id`/`to_id` ranges
   are consecutive even if neither finds a character.
6. Verify RabbitMQ `id-sweep`, `character-census`, and `achievement-census`
   ready/unacknowledged counts remain healthy.
7. When the sweep discovers a public character, verify its downstream
   achievement summary or privacy flag. Require a milestone row only when the
   public character has actually earned the first missing chain achievement.

No historical milestone backfill, queue purge, or destructive database action
is part of this change.

## Acceptance Criteria

- Empty discovery batches never pin automatic scanning to the same range.
- Successful automatic runs advance exactly `count` IDs without overlap.
- Partial publication failures cannot create an unscanned hole.
- Concurrent publishers cannot move the cursor backward.
- Public discoveries from both relevant handlers publish one
  `achievement-census` job.
- Private or incomplete achievement histories remain valid reasons for having
  no `character_milestones` row.
- Production logs expose the scanned and next ranges.
- Documentation explains why the milestone maximum may trail the character
  maximum.
