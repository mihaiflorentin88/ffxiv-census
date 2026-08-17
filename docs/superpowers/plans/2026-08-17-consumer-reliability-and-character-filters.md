# Consumer Reliability + Character List Filters — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Fix the queue losing `claimed` jobs when a consumer is killed mid-flight (so restarts resume cleanly), treat HTTP 403 Forbidden character profiles as non-existent (`ErrCharacterNotFound`) so banned/deleted characters do not abort `id-sweep` chunks, enforce a hard safety ceiling on the Lodestone request rate (never exceed 1 req/s), and add query-string filters (`world`, `datacenter`, `region`, `race`, `name`) to `GET /api/v1/census/characters`.

**Architecture:** Three focused tasks on the existing hexagonal codebase:
1. `Queue.ReclaimClaimed` added to `port/contract/queue.go` (SQLite + mock) and called at `worker.Run` startup.
2. `infrastructure/lodestone` clamps the rate limiter to `maxSafeRate = 1.0` req/s and updates `isNotFound` to recognize HTTP 403 Forbidden alongside 404 Not Found.
3. `contract.CharacterFilter` threaded through `CharacterRepository.List`/`Count` (SQLite + mock) → `CensusService.ListCharacters` → HTTP handler `List` → Swagger docs.

**Tech Stack:** Go 1.26 (`go1.26.6` toolchain), `modernc.org/sqlite` (CGO_ENABLED=0), `golang.org/x/time/rate`, swaggo embedded swagger (`cmd/http/resource/swagger`), strict TDD (write failing test, watch it fail, implement, watch it pass).

**Spec:** `docs/superpowers/specs/2026-08-16-lodestone-census-design.md` (worker pool + rate limiter §5/§12; REST API §8). The rate-limit decision here **overrides** the spec's "~1 req/s/worker" wording with a single global cap of 1.0 req/s, per explicit user requirement ("never exceed the safe rate to avoid bans").

## Global Constraints

- Go toolchain `go1.26.6`; `CGO_ENABLED=0` cross-compile must keep working (modernc.org/sqlite only, no CGO libs).
- Hexagonal: `domain/` and HTTP depend only on `port/contract`; concrete infra imports only in `container/` and `cmd/`.
- Every port method gets both adapters: `infrastructure/sqlite/repository` (or `infrastructure/queue`) **and** the `mock/` counterpart, with mirrored semantics.
- Strict TDD: write the failing test first, run it to see it fail (RED), then implement, then GREEN. No production code without a failing test.
- Timestamps stored/compared as TEXT in UTC `"2006-01-02T15:04:05.000Z"` (`repository.timeLayout`; queue uses its own `timeLayout`).
- Commit convention: one commit per task.

## Step 0 — Persist this plan to disk (do this FIRST)

This plan was authored in plan mode (working tree read-only), so it is not yet in the repo. The project convention (AGENTS.md) is that plans live in `docs/superpowers/plans/`.

- [ ] **Step 0.1** — Copy this plan to `docs/superpowers/plans/2026-08-17-consumer-reliability-and-character-filters.md` and commit it:
  ```bash
  git add docs/superpowers/plans/2026-08-17-consumer-reliability-and-character-filters.md
  git commit -m "docs: consumer reliability + character filters plan"
  ```

---

### Task 1: Reclaim `claimed` queue jobs on worker startup

**Files:**
- Modify: `port/contract/queue.go`, `infrastructure/queue/queue.go`, `mock/queue/queue.go`, `domain/census/worker/worker.go`
- Test: `infrastructure/queue/queue_test.go`, `mock/queue/queue_test.go`, `domain/census/worker/worker_test.go`

**Interfaces:**
- Consumes: existing `contract.Queue` (Publish/Claim/Complete/Retry/Fail/Depth); `QueueJobStatus` constants (`QueueJobPending` = `"pending"`, `QueueJobClaimed` = `"claimed"`); `domain/census/worker.Worker` fields `queue contract.Queue`, `logger contract.Logger`.
- Produces: `contract.Queue.ReclaimClaimed(ctx context.Context, jobType string) (int, error)` — both adapters — plus a startup call in `Worker.Run`.

**Root cause (verified):** `Claim` (infrastructure/queue/queue.go:83-93) only selects `status = 'pending' AND run_at <= now`. A consumer killed mid-flight leaves the job `status = 'claimed'` (with `claimed_at` set). Nothing on restart resets `claimed` → `pending`, so the job is invisible to future `Claim` calls forever. This is the "stopped while consuming and it didn't resume" bug.

- [ ] **Step 1.1: Write the failing tests** — `infrastructure/queue/queue_test.go`, add:

```go
func TestQueue_ReclaimClaimed(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()
	// Publish two jobs of the same type and one of a different type.
	if err := q.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"a":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"a":2}`)},
		contract.QueueJob{Type: "other", Payload: []byte(`{"a":3}`)},
	); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Claim one id-sweep job -> now 'claimed'.
	claimed, err := q.Claim(ctx, "id-sweep", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim: n=%d err=%v", len(claimed), err)
	}
	// Reclaim: exactly the one claimed id-sweep job returns to pending.
	n, err := q.ReclaimClaimed(ctx, "id-sweep")
	if err != nil || n != 1 {
		t.Fatalf("ReclaimClaimed: n=%d err=%v", n, err)
	}
	// Both id-sweep jobs are now claimable again; the 'other' job is untouched.
	got, err := q.Claim(ctx, "id-sweep", 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("Claim after reclaim: n=%d err=%v", len(got), err)
	}
	other, err := q.Claim(ctx, "other", 1)
	if err != nil || len(other) != 1 {
		t.Fatalf("Claim other: n=%d err=%v", len(other), err)
	}
}
```

- [ ] **Step 1.2: Run to verify it fails** — `go test ./infrastructure/queue/ -run TestQueue_ReclaimClaimed` → FAIL: `q.ReclaimClaimed undefined`.

- [ ] **Step 1.3: Implement** — add to `port/contract/queue.go` (interface `Queue`):

```go
	// ReclaimClaimed returns to 'pending' every job of jobType stuck in
	// 'claimed' status (a previous consumer was killed mid-flight). It clears
	// claimed_at and resets run_at to now so the job is immediately claimable.
	// Returns the number of jobs reclaimed.
	ReclaimClaimed(ctx context.Context, jobType string) (int, error)
```

In `infrastructure/queue/queue.go`, add (mirrors `Retry`'s `claimed` → `pending` UPDATE at lines 156-158):

```go
func (q *Queue) ReclaimClaimed(ctx context.Context, jobType string) (int, error) {
	res, err := q.driver.Execute(ctx,
		`UPDATE queue_jobs SET status = 'pending', run_at = ?, claimed_at = NULL
		  WHERE type = ? AND status = 'claimed'`,
		q.now().UTC().Format(timeLayout), jobType)
	if err != nil {
		return 0, fmt.Errorf("reclaim claimed: %w", err)
	}
	n, _ := res.RowsAffected()
	q.logger.InfoContext(ctx, "queue.reclaim", slog.String("event_type", jobType), slog.Int("reclaimed", int(n)))
	return int(n), nil
}
```

In `mock/queue/queue.go`, add (mirror the map-based semantics; `time` is already imported):

```go
func (f *Fake) ReclaimClaimed(ctx context.Context, jobType string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for id, j := range f.jobs {
		if j.Type == jobType && j.Status == contract.QueueJobClaimed {
			j.Status = contract.QueueJobPending
			j.ClaimedAt = nil
			j.RunAt = time.Now().UTC()
			f.jobs[id] = j
			n++
		}
	}
	return n, nil
}
```

- [ ] **Step 1.4: Run to verify the repo + mock pass** — `go test ./infrastructure/queue/ -run ReclaimClaimed`; add a mirror test `TestFake_ReclaimClaimed` to `mock/queue/queue_test.go`, then `go test ./mock/queue/ -run ReclaimClaimed`.

- [ ] **Step 1.5: Write the failing worker test** — `domain/census/worker/worker_test.go`, add a test that proves a `claimed` job is processed after a fresh `Run` (the user's exact scenario):

```go
func TestWorker_ReclaimsClaimedOnStart(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	var processed int32
	reg.Register("id-sweep", handler.HandlerFunc(func(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
		atomic.AddInt32(&processed, 1)
		return nil, nil
	}))
	// Publish one job and claim it manually so it is 'claimed' (simulating a
	// previous consumer that died mid-flight).
	if err := q.Publish(context.Background(), contract.QueueJob{Type: "id-sweep", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := q.Claim(context.Background(), "id-sweep", 1); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	w := worker.New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Run for a short window; the startup reclaim must return the claimed job
	// to pending, after which the loop claims and processes it.
	go func() { _ = w.Run(ctx, "id-sweep", 1) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&processed) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if atomic.LoadInt32(&processed) == 0 {
		t.Fatal("claimed job was not reclaimed and processed after restart")
	}
}
```

- [ ] **Step 1.6: Run to verify it fails** — `go test ./domain/census/worker/ -run TestWorker_ReclaimsClaimedOnStart` → FAIL (job stays `claimed`, never processed).

- [ ] **Step 1.7: Implement the startup reclaim** — in `domain/census/worker/worker.go`, inside `Run`, immediately after the `no handler registered` guard (after `w.logger.InfoContext(... "worker.start" ...)`), before spawning goroutines:

```go
	if n, err := w.queue.ReclaimClaimed(ctx, eventType); err != nil {
		return fmt.Errorf("reclaim claimed jobs: %w", err)
	} else if n > 0 {
		w.logger.InfoContext(ctx, "worker.reclaimed", slog.String("event_type", eventType), slog.Int("reclaimed", n))
	}
```

- [ ] **Step 1.8: Verify pass + commit** — `go test ./infrastructure/queue/ ./mock/queue/ ./domain/census/worker/ -v`; `go build ./...`; then:
  ```bash
  git add port/contract/queue.go infrastructure/queue/queue.go infrastructure/queue/queue_test.go mock/queue/queue.go mock/queue/queue_test.go domain/census/worker/worker.go domain/census/worker/worker_test.go
  git commit -m "fix(queue): reclaim claimed jobs on worker startup"
  ```

---

### Task 2: Lodestone 1 req/s rate cap + HTTP 403 Forbidden handling

**Files:**
- Modify: `infrastructure/lodestone/lodestone.go`, `docs/lodestone.md`, `README.md`
- Test: `infrastructure/lodestone/lodestone_test.go`, `domain/census/handler/idsweep_test.go`, `domain/census/handler/character_test.go`

**Interfaces:**
- Consumes: `config.LodestoneConfig.RateLimit` (float64); `contract.ErrCharacterNotFound`; `godestone` colly errors.
- Produces: `maxSafeRate = 1.0` constant, clamped rate limiter, and `isNotFound` recognizing HTTP 403 Forbidden as `contract.ErrCharacterNotFound`.

**Root cause (verified):**
1. **Rate cap:** The safe request rate for the FFXIV Lodestone (Cloudflare-fronted) is **1 req/s**. Above ~2 req/s risks HTTP 429; ≥3 req/s triggers IP bans. The rate limiter is clamped to `maxSafeRate = 1.0` req/s regardless of user config.
2. **HTTP 403 Forbidden bug:** In the user's log, character 75 returned `Forbidden` (Square Enix returns 403 on banned/terminated/legacy character profiles). Because `isNotFound` only checked for 404 "Not Found", HTTP 403 was treated as a transient failure, causing `FetchCharacter` to back off and retry 3 times (~30s delay), `idsweep.Handle` to return an error, and the worker to retry the whole chunk 5 times before marking it permanently `failed`. Extending `isNotFound` to treat 403 as `ErrCharacterNotFound` allows `id-sweep` to skip character 75 and continue sweeping the rest of the chunk, while `character-census` marks it deleted.

- [ ] **Step 2.1: Write failing tests** — in `infrastructure/lodestone/lodestone_test.go`:

```go
func TestNewClient_ClampsRateToMaxSafe(t *testing.T) {
	sc := &fakeScraper{}
	t.Run("above cap clamps", func(t *testing.T) {
		c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 50.0})
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		if got := c.limiter.Limit(); got != rate.Limit(maxSafeRate) {
			t.Fatalf("limiter rate = %v, want %v (clamped)", got, maxSafeRate)
		}
	})
	t.Run("below cap respected", func(t *testing.T) {
		c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 0.5})
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		if got := c.limiter.Limit(); got != rate.Limit(0.5) {
			t.Fatalf("limiter rate = %v, want 0.5", got)
		}
	})
	t.Run("zero defaults to cap", func(t *testing.T) {
		c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 0})
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		if got := c.limiter.Limit(); got != rate.Limit(maxSafeRate) {
			t.Fatalf("limiter rate = %v, want %v (default)", got, maxSafeRate)
		}
	})
}

func TestClient_FetchCharacter_ForbiddenTreatedAsNotFound(t *testing.T) {
	sc := &fakeScraper{
		charErr: errors.New("fetch character 75: Forbidden"),
	}
	c, _ := newClient(sc, &config.LodestoneConfig{RateLimit: 1.0})
	_, err := c.FetchCharacter(context.Background(), 75)
	if !errors.Is(err, contract.ErrCharacterNotFound) {
		t.Fatalf("err = %v, want ErrCharacterNotFound", err)
	}
}
```

- [ ] **Step 2.2: Run to verify it fails** — `go test ./infrastructure/lodestone/ -run 'TestNewClient_ClampsRateToMaxSafe|TestClient_FetchCharacter_Forbidden'` → FAIL.

- [ ] **Step 2.3: Implement** — in `infrastructure/lodestone/lodestone.go`:

Add constant and update `newClient`:
```go
// maxSafeRate is the hard ceiling on the Lodestone request rate (method calls
// per second). Lodestone is Cloudflare-fronted; 1 req/s is the established safe
// pace for FFXIV tooling. Higher rates risk HTTP 429s and IP bans. The
// configured rate is clamped to this ceiling.
const maxSafeRate = 1.0
```

Inside `newClient` (replace lines 54-57):
```go
	rps := cfg.RateLimit
	if rps <= 0 {
		rps = maxSafeRate
	}
	if rps > maxSafeRate {
		rps = maxSafeRate
	}
```

Update `isNotFound` (line ~162):
```go
// isNotFound reports whether a godestone scrape error indicates the resource
// does not exist on Lodestone (HTTP 404 "Not Found" or HTTP 403 "Forbidden").
// Lodestone returns 403 for deleted/banned/terminated legacy character profiles.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, http.StatusText(http.StatusNotFound)) ||
		strings.Contains(msg, http.StatusText(http.StatusForbidden))
}
```

- [ ] **Step 2.4: Add handler tests** — in `domain/census/handler/idsweep_test.go` and `character_test.go`, add tests confirming `ErrCharacterNotFound` skips non-existent characters during `id-sweep` without failing the chunk, and marks characters deleted in `character_census`.

- [ ] **Step 2.5: Verify pass + update docs** — `go test ./infrastructure/lodestone/ ./domain/census/handler/ -v`; update `docs/lodestone.md` and `README.md` (rate limit table row: "capped at 1.0 req/s to avoid IP bans; 403/404 handled as non-existent").

- [ ] **Step 2.6: Commit** —
  ```bash
  git add infrastructure/lodestone/lodestone.go infrastructure/lodestone/lodestone_test.go domain/census/handler/idsweep_test.go domain/census/handler/character_test.go docs/lodestone.md README.md
  git commit -m "fix(lodestone): clamp rate to 1 req/s ceiling and treat 403 as not found"
  ```

---

### Task 3: Character list filters

**Files:**
- Modify: `port/contract/character_repository.go`, `infrastructure/sqlite/repository/character.go`, `mock/repository/character.go`, `domain/census/service.go`, `domain/census/service_test.go`, `cmd/http/app/census/handler/census.go`, `cmd/http/app/census/handler/census_test.go`, `infrastructure/sqlite/repository/character_test.go`, `cmd/http/resource/swagger/swagger.json`, `cmd/http/resource/swagger/swagger.yaml`, `cmd/http/resource/swagger/docs.go`, `docs/http-api.md`, `docs/census.md`

**Interfaces:**
- Consumes: existing `List(ctx, limit, offset)` / `Count(ctx)` (signatures below change), `ListCharacters(ctx, limit, offset)`, handler `List` (census.go:51-95), `breakdownColumns` whitelist pattern.
- Produces: `contract.CharacterFilter` struct; new `List(ctx, filter, limit, offset)` and `Count(ctx, filter)` signatures; `ListCharacters(ctx, filter, limit, offset)`.

**Decision (user-chosen):** filters are `world`, `datacenter`, `region`, `race` (exact match) and `name` (case-insensitive substring), all optional, AND-combined, single value each. Missing/empty = no filter. SQLite `LIKE` is ASCII-case-insensitive, which matches Lodestone character names (`[A-Za-z' -]`); the mock mirrors with `strings.ToLower` + `strings.Contains`.

- [ ] **Step 3.1: Add `CharacterFilter` and change signatures** — in `port/contract/character_repository.go`:

```go
// CharacterFilter is an optional AND-combined filter for List/Count. Empty
// fields are ignored. World/Datacenter/Region/Race match exactly; Name is a
// case-insensitive substring match.
type CharacterFilter struct {
	World      string
	Datacenter string
	Region     string
	Race       string
	Name       string
}
```

Change the interface methods:

```go
	List(ctx context.Context, filter CharacterFilter, limit, offset int) ([]CharacterRecord, error)
	Count(ctx context.Context, filter CharacterFilter) (int64, error)
```

- [ ] **Step 3.2: Write the failing repo tests** — in `infrastructure/sqlite/repository/character_test.go`. Update existing call sites to pass `contract.CharacterFilter{}` (`List` at ~224, `~232`, `~241`; `Count` at ~274). Add:

```go
func TestCharacterRepository_ListFilter(t *testing.T) {
	repo := newTestCharacterRepo(t)
	ctx := context.Background()
	seed := func(id uint32, world, dc, region, race, name string) {
		rec := contract.CharacterRecord{ID: id, Name: name, World: world, Datacenter: dc, Region: region, Race: race, FirstSeenAt: time.Now().UTC()}
		if err := repo.Upsert(ctx, rec, nil); err != nil {
			t.Fatalf("Upsert %d: %v", id, err)
		}
	}
	seed(1, "Louisoix", "Chaos", "EU", "Au Ra", "Feed How")
	seed(2, "Louisoix", "Chaos", "EU", "Miqo'te", "Ninto Thegen")
	seed(3, "Zodiark", "Light", "EU", "Miqo'te", "Ahribella White")
	seed(4, "Ultros", "Primal", "NA", "Hyur", "Alpha Test")

	cases := []struct {
		name   string
		filter contract.CharacterFilter
		want   []uint32 // expected ids in order
	}{
		{"world exact", contract.CharacterFilter{World: "Louisoix"}, []uint32{1, 2}},
		{"race exact", contract.CharacterFilter{Race: "Miqo'te"}, []uint32{2, 3}},
		{"name substring case-insensitive", contract.CharacterFilter{Name: "feed"}, []uint32{1}},
		{"combined AND", contract.CharacterFilter{World: "Louisoix", Race: "Miqo'te"}, []uint32{2}},
		{"no match", contract.CharacterFilter{World: "Balmung"}, nil},
		{"empty filter returns all", contract.CharacterFilter{}, []uint32{1, 2, 3, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.List(ctx, tc.filter, 10, 0)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var ids []uint32
			for _, c := range got {
				ids = append(ids, c.ID)
			}
			if len(ids) != len(tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
			for i := range ids {
				if ids[i] != tc.want[i] {
					t.Fatalf("ids = %v, want %v", ids, tc.want)
				}
			}
			n, err := repo.Count(ctx, tc.filter)
			if err != nil {
				t.Fatalf("Count: %v", err)
			}
			if n != int64(len(tc.want)) {
				t.Fatalf("Count = %d, want %d", n, len(tc.want))
			}
		})
	}
}
```

- [ ] **Step 3.3: Run to verify it fails** — `go test ./infrastructure/sqlite/repository/ -run TestCharacterRepository_ListFilter` → FAIL: signatures don't match.

- [ ] **Step 3.4: Implement SQLite** — `infrastructure/sqlite/repository/character.go`. Add `"strings"` import. Add the helper and rewrite `List`/`Count`:

```go
// characterFilterWhere returns the additional " AND ..." conditions for a
// filter, plus their args. Returns "" (and nil args) when the filter is empty.
func characterFilterWhere(f contract.CharacterFilter) (string, []any) {
	var conds []string
	var args []any
	if f.World != "" {
		conds = append(conds, "world = ?")
		args = append(args, f.World)
	}
	if f.Datacenter != "" {
		conds = append(conds, "datacenter = ?")
		args = append(args, f.Datacenter)
	}
	if f.Region != "" {
		conds = append(conds, "region = ?")
		args = append(args, f.Region)
	}
	if f.Race != "" {
		conds = append(conds, "race = ?")
		args = append(args, f.Race)
	}
	if f.Name != "" {
		conds = append(conds, "name LIKE ?")
		args = append(args, "%"+f.Name+"%")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(conds, " AND "), args
}

func (r *CharacterRepository) List(ctx context.Context, f contract.CharacterFilter, limit, offset int) ([]contract.CharacterRecord, error) {
	conds, args := characterFilterWhere(f)
	q := `SELECT ` + characterColumns + ` FROM characters WHERE deleted_at IS NULL` + conds + ` ORDER BY id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := r.driver.FetchMany(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.CharacterRecord
	for rows.Next() {
		rec, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func (r *CharacterRepository) Count(ctx context.Context, f contract.CharacterFilter) (int64, error) {
	conds, args := characterFilterWhere(f)
	row, err := r.driver.FetchOne(ctx, `SELECT COUNT(*) FROM characters WHERE deleted_at IS NULL`+conds, args...)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
```

- [ ] **Step 3.5: Implement the mock** — `mock/repository/character.go`. Update `List`/`Count` to accept `filter contract.CharacterFilter`, keeping existing ordering and limit/offset semantics (limit==0 → empty, negative → unlimited), and filtering via a helper:

```go
func matchesFilter(rec contract.CharacterRecord, f contract.CharacterFilter) bool {
	if f.World != "" && rec.World != f.World {
		return false
	}
	if f.Datacenter != "" && rec.Datacenter != f.Datacenter {
		return false
	}
	if f.Region != "" && rec.Region != f.Region {
		return false
	}
	if f.Race != "" && rec.Race != f.Race {
		return false
	}
	if f.Name != "" && !strings.Contains(strings.ToLower(rec.Name), strings.ToLower(f.Name)) {
		return false
	}
	return true
}
```

- [ ] **Step 3.6: Implement service** — `domain/census/service.go`. Update `Summary` (line 238) and `ListCharacters` (lines 251-261):

```go
func (s *Service) Summary(ctx context.Context) (total, active int64, err error) {
	total, err = s.characters.Count(ctx, contract.CharacterFilter{})
	if err != nil {
		return 0, 0, err
	}
	active, err = s.characters.CountActive(ctx, s.activitySince())
	if err != nil {
		return 0, 0, err
	}
	return total, active, nil
}

func (s *Service) ListCharacters(ctx context.Context, f contract.CharacterFilter, limit, offset int) ([]contract.CharacterRecord, int64, error) {
	chars, err := s.characters.List(ctx, f, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.characters.Count(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	return chars, total, nil
}
```

Update `domain/census/service_test.go` call sites to pass `contract.CharacterFilter{}` and add a filter test.

- [ ] **Step 3.7: Implement handler** — `cmd/http/app/census/handler/census.go`, in `List`, add query param parsing:

```go
	f := contract.CharacterFilter{
		World:      query.Get("world"),
		Datacenter: query.Get("datacenter"),
		Region:     query.Get("region"),
		Race:       query.Get("race"),
		Name:       query.Get("name"),
	}
	chars, total, err := c.svc.ListCharacters(r.Context(), f, limit, offset)
```

Add handler tests in `cmd/http/app/census/handler/census_test.go` for `?world=...`, `?name=...`, combined filters, and limit=0 remaining 400.

- [ ] **Step 3.8: Swagger + Docs** — in `cmd/http/resource/swagger/swagger.json` (and `swagger.yaml`, `docs.go`), add the 5 query parameters (`world`, `datacenter`, `region`, `race`, `name` — all type `string`, in `query`, `required: false`). In `docs/http-api.md`, update the `GET /api/v1/census/characters` parameter table. In `docs/census.md`, update the `ListCharacters` signature.

- [ ] **Step 3.9: Verify pass + commit** —
  ```bash
  go test ./infrastructure/sqlite/repository/ ./domain/census/ ./cmd/http/app/census/... -v
  go build ./...
  git add port/contract/character_repository.go infrastructure/sqlite/repository/character.go infrastructure/sqlite/repository/character_test.go mock/repository/character.go domain/census/service.go domain/census/service_test.go cmd/http/app/census/handler/census.go cmd/http/app/census/handler/census_test.go cmd/http/resource/swagger/ docs/http-api.md docs/census.md
  git commit -m "feat(census): character list filters (world/datacenter/region/race/name)"
  ```

---

## Critical Files & Anchors

- `domain/census/worker/worker.go` — `Run` and `loop`; insert startup `w.queue.ReclaimClaimed(ctx, eventType)`.
- `infrastructure/queue/queue.go` — `Claim` (lines 83-93) and `Retry` (lines 156-158) for the `claimed → pending` pattern.
- `infrastructure/lodestone/lodestone.go` — `maxSafeRate = 1.0` clamp and `isNotFound` checking 404 + 403.
- `infrastructure/sqlite/repository/character.go` — `characterFilterWhere` helper, `List` (lines 190-207) and `Count` (lines 210-220).
- `cmd/http/app/census/handler/census.go` — `List` (lines 51-95) query param extraction into `contract.CharacterFilter`.

## Verification

1. **Build:** `go build ./...`.
2. **Race test suite:** `go test ./... -race`.
3. **Lint:** `PATH="$HOME/go/bin:$PATH" make lint`.
4. **Queue reclaim (deterministic):** `go test ./domain/census/worker/ -run TestWorker_ReclaimsClaimedOnStart -v` — proves pre-claimed jobs are reclaimed and processed on start.
5. **Rate cap & 403 handling:** `go test ./infrastructure/lodestone/ -run 'TestNewClient_ClampsRateToMaxSafe|TestClient_FetchCharacter_Forbidden' -v`.
6. **Filters live smoke:**
   ```bash
   make build
   SQLITE_PATH=$DB ./bin/ffxiv-census server --start --port 18083 &
   curl -s 'localhost:18083/api/v1/census/characters?world=Louisoix'   # -> only Louisoix items, matching total
   curl -s 'localhost:18083/api/v1/census/characters?name=feed'        # -> substring match
   curl -s 'localhost:18083/api/v1/census/characters?race=Miqo%27te'   # -> exact race
   curl -s 'localhost:18083/api/v1/census/characters?limit=0'          # -> 400 {"error":"invalid limit"}
   curl -s localhost:18083/docs/swagger.json | jq '.paths["/api/v1/census/characters"].get.parameters'
   ```

## Assumptions & Contingencies

- **Rate cap value is 1.0 req/s** (method calls/sec), the conservative researched ceiling. If 429s appear, lower `maxSafeRate` to `0.5` in `lodestone.go`.
- **403 Forbidden is treated as non-existent character** (`ErrCharacterNotFound`) across the entire pipeline, preventing banned/deleted profiles from aborting sweeps or looping endlessly in worker retries.
- **Single-consumer-per-event-type** is assumed for `ReclaimClaimed` (resets all `claimed` jobs of that type at worker startup).
- **Name search is ASCII case-insensitive** via SQLite `LIKE`; Lodestone names are `[A-Za-z' -]`, so no special wildcard escaping is added.
