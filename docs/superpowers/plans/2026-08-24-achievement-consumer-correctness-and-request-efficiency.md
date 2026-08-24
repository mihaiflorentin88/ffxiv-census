# Achievement Consumer Correctness and Lodestone Request Efficiency

Date: 2026-08-24

Status: Proposed — implementation requires explicit user approval

## Goal

Repair the `achievement-census` path so that it deterministically discovers and
persists milestone achievements, stores the achievement-specific earned date,
and makes only the Lodestone requests needed to discover missing milestones.

Implementation must use strict red/green/refactor TDD, update the operational
documentation in the same change, run the focused and full verification suites,
commit every changed file, and push the completed branch.

## Verified Current State

### 1. The fixed `achieved_at` timestamp is present in `v1.11.8`

Commit `4582679` changed `extractTimestamp` from the first
`ldst_strftime(...)` match to the last match. A read-only check against a live,
public Lodestone achievement detail page confirmed the relevant HTML ordering:

```html
<!-- global navigation dates appear first -->
<script>... ldst_strftime(1785225600, 'YMD');</script>

<!-- the achievement-specific date appears later, inside the achievement row -->
<div class="entry__achievement__view entry__achievement__view--complete">
  <p class="entry__activity__txt">My Little Chocobo</p>
  <time class="entry__activity__time">
    <script>... ldst_strftime(1486702764, 'YMD');</script>
  </time>
</div>
```

The old parser selected `1785225600`, which is the reported constant
`2026-07-28 08:00:00+00`. The current parser selects `1486702764`, the earned
timestamp. The repository passes `time.Time` directly to PostgreSQL, so the SQL
adapter is not replacing the value.

The current unit test is weaker than the live HTML shape: it merely asserts that
the final timestamp in an arbitrary string wins. Implementation must replace or
extend it with an achievement-row-scoped fixture so unrelated footer/header
timestamps cannot silently become `achieved_at` after a future HTML change.

### 2. The persistence adapter is functional, but often receives no milestones

`ProcessMilestoneResults` maps earned client results into
`contract.CharacterMilestone`, and PostgreSQL performs one parameterized batch
upsert:

```sql
INSERT INTO character_milestones (character_id, achievement_id, achieved_at)
VALUES (...)
ON CONFLICT (character_id, achievement_id)
DO UPDATE SET achieved_at = excluded.achieved_at
```

The observed loss happens before this query. `MilestoneIDs` returns a Go map,
and the handler converts that map directly to a slice:

```go
for id := range milestoneIDSet {
    milestoneIDs = append(milestoneIDs, id)
}
```

Go map iteration order is unspecified. The Lodestone client assumes the slice
is chronological and stops at the first unearned milestone. If a late expansion
ID happens to be first, a normal character can return an empty summary even
though earlier milestones were earned. That empty summary produces no milestone
insert. This is the primary cause of “achievements are no longer being saved.”

### 3. The existing staleness check causes repeated unnecessary requests

When all milestones are already stored, the handler decides whether to rescan by
comparing `latest_achievement_at` with `achievement_staleness_days`. That field
means “when the latest checked milestone was earned,” not “when Lodestone was
last checked.” A character who completed their latest expansion more than seven
days ago is therefore permanently stale and can incur all seven detail requests
on every achievement job.

The custom client checks milestone details only; it no longer scans the full
achievement history. Rechecking already-correct milestones cannot discover
unrelated recent activity. Therefore `latest_achievement_at` must not be used as
a request-cache timestamp.

### 4. Privacy needs no second HTTP call

The separate `/achievement/` privacy probe was already removed in `v1.11.8`.
Keep that optimization. The first required achievement detail request is enough:

- HTTP 403: return `AchievementSummary{Private: true}` and stop after that one
  request.
- HTTP 200 with no `entry__achievement__view--complete`: the achievement is
  public but unearned; return `Earned: false` and stop sequential discovery.
- HTTP 200 with the complete class and a valid earned timestamp: return the
  earned result.

A live public unearned milestone detail returned HTTP 200 and omitted the
`--complete` class. Therefore HTTP 403 must not be treated as the ordinary
“unearned” response. There are uncommitted edits in
`infrastructure/lodestone/client.go` that currently do that; implementation must
re-read and reconcile those user-owned edits rather than blindly overwrite the
file.

## Design

### Ordered, incremental milestone discovery

Use one canonical ordered milestone slice throughout the handler. The current
registry IDs happen to increase chronologically, but ordering should be explicit
at the handler boundary so a map can never reach the sequential client.

For each character:

1. Load the configured/registered milestone IDs and sort them ascending.
2. Load already-persisted character milestones and index them by achievement ID.
3. Find the earliest milestone that is missing.
4. If none needs work, skip Lodestone entirely.
5. Pass only the suffix beginning at that earliest required milestone to
   `FetchAchievements`.
6. The client checks the suffix sequentially and stops after the first public,
   unearned milestone or the first 403.
7. Upsert returned earned milestones without deleting previously stored rows.

This gives the following request bounds:

| Character state | Lodestone detail requests |
|---|---:|
| All milestones known and correct | 0 |
| No milestones earned | 1 |
| First three known, fourth unearned | 1 |
| First three known, next two newly earned, sixth unearned | 3 |
| Achievements private | 1 |

Historical rows containing the old `2026-07-28 08:00:00Z` value are explicitly
left unchanged. This change guarantees correct timestamps for newly discovered
milestones and conflict updates that occur naturally; it does not spend
rate-limited requests on a historical backfill.

Do not add an `/achievement/` request before the detail loop.

### Timestamp parsing must be scoped and fail closed

Do not persist a zero `time.Time` or a global navigation timestamp for an earned
achievement. Extract the date from the completed achievement block (or its
`entry__activity__time` element), and return an error if an earned row has no
valid epoch. Returning an error causes the queue delivery to retry instead of
writing corrupt analytics data.

A suitable internal signature is:

```go
func extractAchievementTimestamp(html string) (time.Time, error)
```

The implementation may use a narrowly scoped regular expression or a small
HTML-boundary helper already used by the custom scraper. It must not select a
page-global timestamp based solely on first/last position.

### Persistence semantics

Preserve additive/idempotent milestone persistence:

- Newly returned earned milestones are batch-upserted.
- Existing milestones not included in an incremental suffix response remain.
- A private response changes only `achievements_private`; it does not erase
  prior milestone or latest-achievement data.
- A public response clears `achievements_private`.
- An earned result with an invalid timestamp returns an error before repository
  writes.

`LatestAchievement` currently means the latest checked milestone, not the latest
achievement across the full Lodestone history. Update misleading contract and
documentation language; do not claim that it represents arbitrary activity.

## Detailed TDD Implementation Tasks

### Task 1: Lock down ordered and incremental handler input

Files:

- Test: `domain/census/handler/achievement_test.go`
- Modify: `domain/census/handler/achievement.go`

#### Red

Add table-driven tests that capture `milestoneIDs` in the Lodestone fake. At
minimum cover:

```go
func TestAchievementCensus_RequestsOrderedSuffix(t *testing.T) {
    tests := []struct {
        name          string
        known         []contract.CharacterMilestone
        wantRequested []uint32
        wantCalls     int
    }{
        {
            name:          "nothing known starts from chocobo",
            wantRequested: []uint32{590, 1129, 1139, 1794, 2298, 2958, 3496},
            wantCalls:     1,
        },
        {
            name: "known prefix starts at first missing",
            known: []contract.CharacterMilestone{
                {AchievementID: 590, AchievedAt: validTime},
                {AchievementID: 1129, AchievedAt: validTime},
                {AchievementID: 1139, AchievedAt: validTime},
            },
            wantRequested: []uint32{1794, 2298, 2958, 3496},
            wantCalls:     1,
        },
        {
            name: "complete correct history makes no request",
            known: allValidMilestones,
            wantCalls: 0,
        },
    }
    // Arrange repository state, call Handle, and compare the exact slice.
}
```

Run the test repeatedly to prove it is deterministic and observe it fail before
the production change:

```bash
go test -count=50 -run TestAchievementCensus_RequestsOrderedSuffix ./domain/census/handler
```

Also replace the existing fresh/stale tests. The new invariant is based on
missing milestone rows, not `LatestAchievementAt` age:

```go
func TestAchievementCensus_AllKnownOldAchievementsDoNotRefetch(t *testing.T)
```

#### Green

Build the canonical slice and locate the first required index. The exact helper
name is flexible, but keep the logic independently testable:

```go
func milestoneRequestSuffix(
    ids map[uint32]bool,
    known []contract.CharacterMilestone,
) []uint32 {
    ordered := make([]uint32, 0, len(ids))
    for id := range ids {
        ordered = append(ordered, id)
    }
    sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

    persisted := make(map[uint32]bool, len(known))
    for _, milestone := range known {
        persisted[milestone.AchievementID] = true
    }

    for i, id := range ordered {
        if !persisted[id] {
            return ordered[i:]
        }
    }
    return nil
}
```

If the known-milestone query fails, retain correctness by requesting the full
ordered slice. Never interpret a repository error as “everything is known.”

Remove the `LatestAchievementAt`/`AchievementStalenessDays` request decision.
After all references are removed, delete dead service/config plumbing only if it
is truly unused; preserve the TOML key as a documented deprecated no-op for one
release if removing it would surprise deployed configuration.

#### Refactor

- Keep request planning out of the HTTP client.
- Keep chronological ordering explicit at the boundary.
- Avoid logging a guessed request count in the handler; the client already logs
  `requests_made`, where retries and early stops can be measured accurately.

### Task 2: Prove one-call privacy handling and public-unearned behavior

Files:

- Test: `infrastructure/lodestone/client_test.go`
- Modify: `infrastructure/lodestone/client.go`

#### Red

Use an injected `RoundTripper`/test HTTP client to count requests without using
the network. Add these tests:

```go
func TestCustomClient_FetchAchievements_403MarksPrivateWithOneRequest(t *testing.T)
func TestCustomClient_FetchAchievements_200IncompleteMeansUnearned(t *testing.T)
func TestCustomClient_FetchAchievements_StopsAfterFirstUnearned(t *testing.T)
```

The privacy assertion must include:

```go
if gotRequests != 1 {
    t.Fatalf("HTTP requests = %d, want 1; no separate privacy probe", gotRequests)
}
if summary == nil || !summary.Private {
    t.Fatalf("summary = %+v, want private", summary)
}
```

The public-unearned fixture must return HTTP 200 and include an achievement
block without `entry__achievement__view--complete`; assert `Private == false`
and that later milestone URLs are never requested.

#### Green

Retain the `v1.11.8` control flow:

```go
result, err := c.checkSingleAchievement(ctx, charID, id)
if errors.Is(err, contract.ErrAchievementsPrivate) {
    return &contract.AchievementSummary{Private: true}, nil
}
if err != nil {
    return nil, fmt.Errorf("check achievement %d for character %d: %w", id, charID, err)
}
```

And in `checkSingleAchievement`:

```go
if statusCode == http.StatusForbidden {
    return result, contract.ErrAchievementsPrivate
}
```

Do not restore `checkPrivacy`, do not fetch `/achievement/`, and do not treat a
normal HTTP 200 incomplete detail page as private.

### Task 3: Scope timestamp extraction to the completed achievement row

Files:

- Test: `infrastructure/lodestone/client_test.go`
- Modify: `infrastructure/lodestone/client.go`

#### Red

Add realistic fixtures based on the verified Lodestone structure:

```go
func TestExtractAchievementTimestamp_IgnoresGlobalPageDates(t *testing.T) {
    html := `
      <script>ldst_strftime(1785225600, 'YMD')</script>
      <div class="entry__achievement__view entry__achievement__view--complete">
        <p class="entry__activity__txt">My Little Chocobo</p>
        <time class="entry__activity__time">
          <script>ldst_strftime(1486702764, 'YMD')</script>
        </time>
      </div>
      <script>ldst_strftime(1777363200, 'YMD')</script>`

    got, err := extractAchievementTimestamp(html)
    // want time.Unix(1486702764, 0).UTC()
}
```

Also add:

```go
func TestCheckSingleAchievement_CompleteWithoutDateReturnsError(t *testing.T)
func TestCheckSingleAchievement_InvalidEpochReturnsError(t *testing.T)
func TestCheckSingleAchievement_UnearnedDoesNotParseGlobalTimestamp(t *testing.T)
```

#### Green

Scope extraction to `entry__activity__time` inside the achievement view. Return
a descriptive error when the complete row has no valid epoch:

```go
if strings.Contains(html, "entry__achievement__view--complete") {
    earnedAt, err := extractAchievementTimestamp(html)
    if err != nil {
        return contract.AchievementResult{}, fmt.Errorf(
            "parse earned timestamp for achievement %d: %w",
            achievementID,
            err,
        )
    }
    result.Earned = true
    result.EarnedAt = earnedAt.UTC()
}
```

This hardens the already-effective `v1.11.8` fix without relying on “last match
on the whole page.”

### Task 4: Verify service and PostgreSQL persistence end to end

Files:

- Test: `domain/census/service_test.go`
- Test: `infrastructure/postgres/repository/achievement_test.go`
- Modify only if a failing test exposes a real defect:
  `domain/census/service.go` or
  `infrastructure/postgres/repository/achievement.go`

#### Red/verification tests

Add or strengthen tests proving:

```go
func TestService_ProcessMilestoneResults_IncrementalResultsPreserveKnownRows(t *testing.T)
func TestService_ProcessMilestoneResults_UsesEarnedTimestampExactly(t *testing.T)
func TestAchievementRepository_UpsertCharacterMilestones_UsesExactTimestamp(t *testing.T)
```

The PostgreSQL test must insert a representative earned timestamp, read it back,
and compare instants:

```go
correct := time.Date(2017, 2, 10, 3, 39, 24, 0, time.UTC)

// insert correct, ListCharacterMilestones, assert Equal(correct)
```

These tests should pass with the current batch adapter. If so, do not rewrite
the SQL. The defect is in request/result construction, not PostgreSQL.

Run PostgreSQL tests against the repository's temporary-database harness; do not
replace real SQL coverage with mocks.

### Task 5: Update documentation to match the custom client

Files:

- Modify: `docs/lodestone.md`
- Modify: `docs/census.md`
- Modify: `docs/events.md`
- Modify: `docs/logging-and-middleware.md` if event levels/fields change
- Modify: `config/config.toml` comments and config docs if
  `achievement_staleness_days` is deprecated or removed

Required corrections:

1. Remove stale claims that `infrastructure/lodestone` wraps godestone or returns
   godestone model types. Document `CustomClient`, direct HTML requests,
   request-level token charging, timeout/cancellation, and proxy transports.
2. Document ordered incremental milestone checking and its exact request bounds.
3. Explicitly state that privacy is inferred from the first necessary detail
   request's HTTP 403 and costs no additional request.
4. Document public-unearned as HTTP 200 without the complete marker.
5. Document that `latest_achievement_*` now reflects the latest checked tracked
   milestone, not an arbitrary latest achievement from the full history.
6. State that historical bad timestamps are not backfilled; the parser guarantee
   applies to new writes.
7. Remove the incorrect freshness description based on earned timestamps.
8. Update log-field documentation to use the client-reported actual request
   count rather than the handler's inferred count, if Task 1 removes that field.

Suggested documentation text:

```markdown
Achievement census requests are incremental. Milestones are checked in canonical
chronological order, beginning at the earliest missing row, and the
client stops at the first unearned milestone. A character with a complete
milestone history makes no Lodestone request. Achievement
privacy is detected when the first required detail request returns HTTP 403; no
separate privacy probe is issued.
```

### Task 6: Full local verification, commit, and push

Run in this order:

```bash
# Focused red/green suites
go test -count=50 -run 'TestAchievementCensus_(RequestsOrderedSuffix|AllKnownOldAchievementsDoNotRefetch)' ./domain/census/handler
go test -count=1 -run 'Test(CustomClient_FetchAchievements|ExtractAchievementTimestamp|CheckSingleAchievement)' ./infrastructure/lodestone
go test -count=1 -run 'TestService_ProcessMilestoneResults' ./domain/census
go test -count=1 -run 'TestAchievementRepository_UpsertCharacterMilestones' ./infrastructure/postgres/repository

# Proportional integration checks
go test -race ./domain/census/handler ./domain/census/worker
make test
make fmt
make lint
make build
git diff --check
```

The current worktree has pre-existing uncommitted edits in:

```text
domain/census/handler/achievement.go
infrastructure/lodestone/client.go
```

Before implementation, re-run `git status --short` and `git diff` and preserve
those edits. Integrate the required behavior deliberately; do not reset or
overwrite user work. If unrelated test failures remain, record their exact names
and prove the focused achievement suites pass, but do not silently label new
failures as pre-existing.

After all required verification passes:

```bash
git add <only the files belonging to this implementation>
git commit -m "fix(achievement): restore milestone persistence and reduce requests"
git push origin HEAD
```

Do not commit `.scratch/tmp_prompt` or temporary live-response fixtures. Use
small synthetic HTML fixtures in tests so the suite remains deterministic and
offline.

### Task 7: Release and verify against the configured database

The user authorized commit, push, release, and `.env`-backed database
verification. Follow `README.md`'s release workflow without reusing a tag.

#### Pre-release database snapshot

Load `.env` without printing secret values and use `POSTGRES_DSN` for read-only
queries. Record:

```sql
SELECT COUNT(*) AS milestone_rows FROM character_milestones;

SELECT achievement_id, COUNT(*) AS rows,
       COUNT(*) FILTER (
           WHERE achieved_at = TIMESTAMPTZ '2026-07-28 08:00:00+00'
       ) AS historical_bad_rows
FROM character_milestones
GROUP BY achievement_id
ORDER BY achievement_id;
```

The historical bad-row count is informational only. Do not update or delete
those rows.

Select one existing non-deleted character that is missing an early tracked
milestone as the post-deploy smoke candidate. Prefer a candidate already known
to have public achievements; do not make broad live probes to discover one.

#### Version and artifact release

Run the documented checks and derive the next unused patch tag (expected
`v1.11.9` only if `v1.11.8` remains the newest Git and Docker tag):

```bash
git fetch --tags origin
git tag -l --sort=-v:refname
git ls-remote --tags origin

git tag -a <next-version> -m "Release <next-version>"
git push origin <next-version>

make docker-build
make docker-tag TAG=<next-version>
make docker-push TAG=<next-version>
make docker-push TAG=latest

make k8s-release TAG=<next-version>
make -C k8s post-deploy-check
```

Also inspect existing Docker tags before choosing the version, as required by
the README. If registry authentication, Docker buildx, Helm, Kubernetes, or
network access fails, stop at the failed release stage and report exactly which
artifacts were already published; never overwrite a tag to retry.

#### Post-deploy functional smoke

Use the configured RabbitMQ URL/credentials and the built CLI to publish exactly
one `achievement-census` job for the selected candidate. Do not run a bulk
publisher:

```bash
set -a
source .env
set +a
./bin/ffxiv-census publish achievement-census --character-id <candidate-id>
```

Observe the achievement consumer rollout/logs until that character completes,
then query PostgreSQL for only that character:

```sql
SELECT character_id, achievement_id, achieved_at
FROM character_milestones
WHERE character_id = <candidate-id>
ORDER BY achievement_id;
```

Acceptance for the live smoke:

- At least one newly discovered earned milestone is inserted when the candidate
  has one.
- Its `achieved_at` is the achievement-specific date and is not
  `2026-07-28 08:00:00+00`.
- Logs show chronological milestone checks and no `/achievement/` privacy probe.
- A second job for the same now-complete known prefix starts at the next missing
  milestone (or makes zero requests when all tracked milestones are known).
- The aggregate historical bad-row count is unchanged except for incidental
  conflict updates caused by the single candidate; no backfill is performed.

If the candidate genuinely has no earned milestone, a one-request public
unearned result is valid but does not prove insertion. Select at most one second
candidate already supported by existing data/evidence; do not perform an
unbounded live search that consumes Lodestone capacity.

## Acceptance Criteria

- Milestone request IDs are deterministic and chronological on every run.
- A character's earned earlier milestones are saved even when later milestones
  are unearned.
- Existing correctly stored complete milestone histories generate zero
  Lodestone calls.
- Existing rows with `achieved_at = 2026-07-28 08:00:00Z` are left untouched;
  new timestamp writes are correct.
- The stored timestamp comes from the completed achievement row, not global page
  timestamps.
- An earned row without a parseable date fails and retries without a database
  write.
- Private achievements require exactly one detail request and no separate
  privacy request.
- Public unearned achievements are not classified as private.
- Incremental processing never deletes previously stored milestones.
- Focused tests, race tests, full tests, formatting, lint, and build are run and
  their outcomes reported.
- `docs/lodestone.md`, `docs/census.md`, and `docs/events.md` describe the final
  implementation accurately.
- All implementation/test/documentation changes are committed and pushed only
  after explicit user approval to proceed.
- The next unused release tag is pushed, Docker release and `latest` images are
  published, Helm rollout completes, and the targeted `.env`-backed database
  smoke is reported.

## Out of Scope

- Returning to a full paginated achievement-history scrape.
- Using Tomestone as an achievement fallback.
- Treating tracked milestone dates as a complete general player-activity signal.
- Deleting historical milestone rows when a later response omits them.
- Making live Lodestone calls from automated tests.
