# Fill-gaps cursor mode: capped, stateful gap scanning

Slug: `fill-gaps-cursor`
Date: 2026-09-11
Status: design approved in session (cursor + event cap, no ledger); implementation pending "proceed"

## Goal

Rework the (currently disabled) `publish-fill-gaps` cronjob mode:

- Each run queues at most `--count` id-sweep **events** (jobs) — hard cap, stops mid-gap if needed.
- A dedicated `fill_gaps_state` table stores the last queued ID; the next run resumes from `cursor + 1`.
- When the cursor reaches `MAX(characters.id)`, it resets to 0 and the sweep starts over.
- `--min-id <N>` = explicit manual mode: scan gaps from N, publish (still capped), **never touch the cursor**.

Example (user's): `--count 500`, cursor=0 → queues 500 jobs covering gaps up to id 1723 → stores `last_queued_id = 1723` → next run scans from 1724.

## Deliberately excluded: a 404 ledger

A permanent `id_not_found` ledger was considered and **rejected** in session: Square Enix may recycle old IDs or restore deleted characters (character-recovery), so a "not found" memory could hide a resurrected character forever. Cursor-only re-probes every dead ID once per cycle — the recurring cost *is* the resurrection safety net, and the `--count` cap bounds it to the operator-chosen monthly budget. If budget drain ever becomes a real problem, the safe future variant is a TTL ledger (negative cache expiring after ~6 months, probed rows re-queued capped); `docs` note below records this. No schema or code for it now.

## Verified facts this session

- `id_sweep_state` (migration `00014`) is used **exclusively** by auto mode (`publishAutoIDSweep`, `cmd/cli/publish.go:97-115`). Fill-gaps never touches it — no risk to the auto feature.
- `FindIDGaps` callers: fill-gaps branch only (`publish.go:189`); `domain/census/service.go:188` is a dead wrapper. Clean cutover: delete both, replace with `FindIDGapsAfter`.
- `publishAll` returns `(published int, err)` — supports partial-success cursor advance.
- Latest migration: `00017_proxy_scan_scheduling.sql`. New migration: `00018`.
- Test patterns to follow: `TestCharacterRepository_IDSweepCursorInitializesAndAdvances` / `...NeverRewinds` (`character_test.go:143,171`, temp Postgres); `TestPublishAutoIDSweep_PublishFailureDoesNotAdvanceCursor` (`publish_test.go:249`, mock fake).
- Current `FindIDGaps` SQL anchors gaps on stored rows only — IDs below the lowest stored character are a permanent blind spot; the new query fixes this with a seed row.

## Design

### 1. Migration `00018_create_fill_gaps_state.sql`

```sql
-- +goose Up
CREATE TABLE fill_gaps_state (
    singleton      BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    last_queued_id BIGINT      NOT NULL CHECK (last_queued_id BETWEEN 0 AND 4294967295),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS fill_gaps_state;
```

`0` = nothing queued yet (first scan starts from id 1). Runtime goose migration auto-applies on next Postgres access.

### 2. Repository + port + fake

On `CharacterRepository` (same port already owns `IDSweepCursor`), replacing `FindIDGaps`:

- `FillGapsCursor(ctx) (uint32, error)` — init singleton at 0 on first use, read `last_queued_id`.
- `AdvanceFillGapsCursor(ctx, expected, next uint32) error` — CAS `WHERE last_queued_id = $expected`; `expected = 0` valid (unlike auto's).
- `FindIDGapsAfter(ctx, afterID, maxID uint32, limit int) ([][2]uint32, error)` — gaps-and-islands over the characters table (single pass, no correlated subqueries):

```sql
WITH anchored AS (
    SELECT id FROM (SELECT 0 AS id UNION SELECT id FROM characters WHERE id <= $2) s
    ORDER BY id
)
SELECT id + 1 AS gap_start,
       LEAST(COALESCE(LEAD(id) OVER (ORDER BY id) - 1, $2), $2) AS gap_end
  FROM anchored
```

Then in Go: drop `gap_start > gap_end` rows, trim `start = max(gap_start, afterID+1)`, skip empty, `LIMIT`. Properties: straddling gaps handled (no lost tail at the cursor), the region below the lowest stored character is now scannable (seed 0), and everything above `maxID` stays auto-mode territory. `afterID = 0` degenerates to a full scan.

Update `mock/repository/character.go` fake (drop `FindIDGaps*` plumbing, add three new methods + error fields), and delete the dead `service.go:188` wrapper.

### 3. CLI fill-gaps branch (`publish.go:177-198`)

```
maxID = repo.MaxID()
if maxID == 0 → log none_found, return

if minID > 0:                        // manual mode
    cursorMode = false; after = minID
else:
    cursorMode = true
    c = repo.FillGapsCursor()
    if c >= maxID:                   // wrap rule (user-specified)
        repo.AdvanceFillGapsCursor(c, 0)
        c = 0
    after = c

gaps = repo.FindIDGapsAfter(after, maxID, count)   // page = count (worst case 1 job/gap)
if len(gaps) == 0 → log none_found, return

jobs = buildGapSweepJobs(gaps, chunkSize, source)
if len(jobs) > count: jobs = jobs[:count]          // hard event cap (user decision)

published, err := publishAll(...)
if cursorMode && published > 0:
    endID = end id of job[published-1]
    repo.AdvanceFillGapsCursor(cursorBeforeRun, endID)
```

- Partial publish failure: advance only over the published prefix; zero published → no advance. Id-sweep is idempotent, so a rare double-publish only re-probes.
- `--count` required `> 0` in fill-gaps mode (matches auto's contract; cron sets it explicitly).
- **Remove `--max-gaps`** (page size derives from `--count`); update the flag-list assertion at `publish_test.go:134`.
- `--min-id` help: "manual gap scan start (skips the persistent cursor)".

### 4. `k8s/values.yaml` (stays disabled)

```yaml
    # - name: publish-fill-gaps commented for now, please do not remove or uncomment
    #   schedule: "0 3 1 * *"
    #   command:
    #     - /app/ffxiv-census
    #     - publish
    #     - id-sweep
    #     - --fill-gaps
    #     - --count
    #     - "500"
    #     - --chunk-size
    #     - "100"
```

### 5. Docs

- `README.md` id-sweep/fill-gaps section: cursor semantics, event cap, wrap-at-MaxID reset, manual `--min-id` (no cursor update), and a one-paragraph note on why 404s are intentionally re-probed each cycle (resurrection safety; TTL-ledger documented as the future optimization if ever needed).
- `docs/queue.md` untouched.

## TDD steps (red → green, suite green after each)

1. **Postgres repo** (temp DB):
   - `TestCharacterRepository_FillGapsCursorInitializesAtZeroAndAdvances`
   - `TestCharacterRepository_FillGapsCursorCAS` (stale expected → error, no rewind)
   - `TestCharacterRepository_FindIDGapsAfter` — (a) gap below the lowest stored character found (seed row); (b) straddling gap trimmed to `afterID+1`; (c) nothing above cursor → empty; (d) empty table → empty; (e) `limit` respected.
2. **CLI** (`publish_test.go`, mock fake):
   - `TestFillGapsCursorMode_CapsJobsAtCount_AndAdvancesCursorToLastJobEnd`
   - `TestFillGapsCursorMode_WrapsCursorAtMaxID`
   - `TestFillGapsCursorMode_ManualMinID_DoesNotTouchCursor`
   - `TestFillGapsCursorMode_PublishFailure_AdvancesOnlyPublishedPrefix`
   - Update flag-list assertion + `TestBuildIDSweepJobs`.
3. Mock fake unit tests updated for the new surface.

## Verification

1. `make fmt && make test`; `go test -race ./infrastructure/postgres/... ./cmd/cli/... ./domain/...`.
2. Local e2e (docker Postgres+RabbitMQ): `publish id-sweep --fill-gaps --count 5 --chunk-size 2` twice → second run resumes after the first run's last queued id; `--min-id 100` leaves `fill_gaps_state` untouched (psql check).
3. Grep sweep: no `FindIDGaps` references remain; `id_sweep_state` untouched by the fill-gaps path.

## Notes

- Worker-reliability plan (`local://worker-reliability-plan.md`) stays queued; land sequentially — both touch `cmd/cli/publish.go`.
- TTL-ledger future option (only if dead-ID budget drain becomes real): `id_not_found (id, first_not_found_at)`, gap query excludes rows newer than ~6 months, expired rows re-probe via the normal scan and delete on discovery.
