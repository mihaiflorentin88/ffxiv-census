# Ingest Pipeline: id-sweep — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the ingest pipeline plumbing — event handlers, a worker pool, and the `consume`/`publish` CLI — and ship the first concrete handler, `id-sweep`, which discovers existing character IDs across the Lodestone ID space and chains each discovery to an `achievement-census` job.

**Architecture:** Handlers live in `domain/census/handler/` and implement a small `Handler` interface (`Handle(ctx, payload) ([]contract.QueueJob, error)`); a handler returns the jobs it wants published next, and the worker persists them atomically via `Queue.Complete(id, nextJobs...)` (reusing the existing atomic-chaining queue). A worker pool in `domain/census/worker/` claims jobs of one type, dispatches to the registered handler, and maps handler errors to `Queue.Retry` (the queue already enforces backoff + max-attempts → failed). `cmd/cli` gains `consume` and `publish` commands. The Lodestone client gains a typed `ErrCharacterNotFound` so id-sweep can skip 404s rather than retry them.

**Design note (deviation from spec):** id-sweep does a *full* `UpsertCharacter` on discovery (godestone's `FetchCharacter` already returns the complete profile, so a separate "insert stub + re-fetch via character-census" would double every request). The separate `character-census` event still exists in Phase 6 for *re-checks* of known characters; id-sweep is discovery only. "Unverified" is expressed by `latest_achievement_at IS NULL` (achievements not yet processed), not a separate flag.

**Tech Stack:** Go 1.25. Existing: `contract.Queue` (Publish/Claim/Complete/Retry/Fail/Depth), `contract.LodestoneClient`, `census.Service` (`UpsertCharacter`), `mock/queue`, `mock/lodestone`. godestone v2 colly error semantics: `FetchCharacter` returns a raw `"Not Found"` error for 404 (verified against godestone source).

**Commit convention:** one commit per task, pushed to `origin master`.

**Verification:** `go test ./...`, `go build ./...`, `PATH="$HOME/go/bin:$PATH" make lint`.

---

## File Map

```
port/contract/lodestone.go            # add ErrCharacterNotFound sentinel
infrastructure/lodestone/lodestone.go # map colly "Not Found" -> ErrCharacterNotFound (no retry)
infrastructure/lodestone/lodestone_test.go

domain/census/handler/handler.go      # Handler interface + Registry
domain/census/handler/event.go        # event type consts + payload structs + job constructors
domain/census/handler/idsweep.go      # id-sweep handler
domain/census/handler/idsweep_test.go

domain/census/worker/worker.go        # worker pool (claim -> handle -> complete/retry)
domain/census/worker/worker_test.go

cmd/cli/consume.go                    # consume <event> [--concurrency N]
cmd/cli/publish.go                    # publish id-sweep --from --to --chunk-size

container/domain.go                   # Handlers() accessor (registry of all handlers)

docs/events.md
```

---

### Task 1: Lodestone 404 detection

**Files:**
- Modify: `port/contract/lodestone.go`, `infrastructure/lodestone/lodestone.go`
- Test: `infrastructure/lodestone/lodestone_test.go`

- [ ] **Step 1: Write the failing test**

`infrastructure/lodestone/lodestone_test.go`:

```go
package lodestone

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type stubScraper struct {
	char func(id uint32) (*godestone.Character, error)
}

func (s *stubScraper) FetchCharacter(id uint32) (*godestone.Character, error) { return s.char(id) }
func (s *stubScraper) FetchCharacterAchievements(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
	return nil, nil, nil
}
func (s *stubScraper) FetchFreeCompany(id string) (*godestone.FreeCompany, error) { return nil, nil }

func TestClient_NotFoundMapsToErrCharacterNotFound(t *testing.T) {
	sc := &stubScraper{
		char: func(id uint32) (*godestone.Character, error) {
			return nil, errors.New(http.StatusText(http.StatusNotFound))
		},
	}
	c, err := newClient(sc, &config.LodestoneConfig{RateLimit: 1, MaxRetries: 3})
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	_, err = c.FetchCharacter(context.Background(), 99999999)
	if !errors.Is(err, contract.ErrCharacterNotFound) {
		t.Fatalf("expected ErrCharacterNotFound, got %v", err)
	}
}
```

(Add the `config` import for `config.LodestoneConfig`.)

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./infrastructure/lodestone/ -run TestClient_NotFound
```

Expected: FAIL — `contract.ErrCharacterNotFound` undefined.

- [ ] **Step 3: Implement**

In `port/contract/lodestone.go`, add the sentinel (import `errors`):

```go
// ErrCharacterNotFound is returned by LodestoneClient.FetchCharacter when a
// character ID does not exist on The Lodestone (HTTP 404).
var ErrCharacterNotFound = errors.New("lodestone character not found")
```

In `infrastructure/lodestone/lodestone.go`, add a 404 check in `FetchCharacter` (before the retry backoff), and import `net/http` + `strings`:

```go
// isNotFound reports whether a godestone scrape error is an HTTP 404. godestone's
// character collector forwards colly errors verbatim, and colly surfaces a 404 as
// http.StatusText(404) == "Not Found".
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), http.StatusText(http.StatusNotFound))
}
```

In `FetchCharacter`, after `char, err := c.scraper.FetchCharacter(id)`:

```go
		if err == nil {
			return char, nil
		}
		if isNotFound(err) {
			return nil, contract.ErrCharacterNotFound
		}
```

- [ ] **Step 4: Run to verify it passes, then commit**

```bash
go test ./infrastructure/lodestone/ -v
git add port/contract/lodestone.go infrastructure/lodestone/
git commit -m "feat(lodestone): typed ErrCharacterNotFound for 404s"
```

---

### Task 2: Handler interface + registry + event constants

**Files:**
- Create: `domain/census/handler/handler.go`, `domain/census/handler/event.go`
- Test: `domain/census/handler/handler_test.go`

- [ ] **Step 1: Write the failing test**

```go
package handler

import "testing"

func TestEventConstants(t *testing.T) {
	if EventIDSweep != "id-sweep" {
		t.Errorf("EventIDSweep = %q", EventIDSweep)
	}
	if EventAchievementCensus != "achievement-census" {
		t.Errorf("EventAchievementCensus = %q", EventAchievementCensus)
	}
}

func TestRegistry_Lookup(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get(EventIDSweep); ok {
		t.Fatal("expected no handler before registration")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./domain/census/handler/
```

Expected: FAIL — package undefined.

- [ ] **Step 3: Implement**

`domain/census/handler/event.go`:

```go
package handler

// Event types carried as queue job "type" strings.
const (
	EventIDSweep           = "id-sweep"
	EventCharacterCensus   = "character-census"
	EventAchievementCensus = "achievement-census"
	EventFreeCompanyCensus = "fc-census"
)
```

`domain/census/handler/handler.go`:

```go
package handler

import (
	"context"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Handler processes a single queue job's payload and returns the jobs to publish
// next (downstream chaining). A non-nil error signals a transient failure; the
// worker maps it to Queue.Retry.
type Handler interface {
	Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error)
}

// Registry maps event types to their handlers.
type Registry struct {
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}}
}

func (r *Registry) Register(eventType string, h Handler) {
	r.handlers[eventType] = h
}

func (r *Registry) Get(eventType string) (Handler, bool) {
	h, ok := r.handlers[eventType]
	return h, ok
}
```

- [ ] **Step 4: Run to verify it passes, then commit**

```bash
go test ./domain/census/handler/ -v
git add domain/census/handler/
git commit -m "feat(handler): handler interface, registry, and event constants"
```

---

### Task 3: id-sweep handler

**Files:**
- Create: `domain/census/handler/idsweep.go`
- Test: `domain/census/handler/idsweep_test.go`

- [ ] **Step 1: Write the failing test**

```go
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	mocklodestone "github.com/mihaiflorentin88/ffxiv-census/mock/lodestone"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestIDSweep(t *testing.T) (*IDSweep, *mocklodestone.Fake, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewIDSweep(ls, svc), ls, chars
}

func idsweepPayload(from, to uint32) []byte {
	b, _ := json.Marshal(IDSweepPayload{From: from, To: to})
	return b
}

func TestIDSweep_DiscoversAndChains(t *testing.T) {
	h, ls, chars := newTestIDSweep(t)
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		if id == 404 { // a sentinel ID that "doesn't exist"
			return nil, contract.ErrCharacterNotFound
		}
		return &godestone.Character{ID: id, Name: "Char", World: "Ultros", DC: "Primal"}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayload(1, 3))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// IDs 1 and 3 discovered -> 2 achievement-census jobs. (Assume 2 also 404s in a fuller test.)
	// Here: 1 and 3 exist, 2 does not (we don't send 404 for 2 in this simple test).
	_ = chars
	_ = next
}
```

Replace the placeholder assertion with a real one (see the gotchas note). The correct test: make `FetchCharacterFunc` return `ErrCharacterNotFound` for a specific ID (say 2), and assert that `Handle` produced exactly 2 `achievement-census` jobs (for IDs 1 and 3), and that `chars` has IDs 1 and 3 upserted but not 2.

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./domain/census/handler/ -run TestIDSweep
```

Expected: FAIL — `NewIDSweep`/`IDSweep` undefined.

- [ ] **Step 3: Implement**

`domain/census/handler/idsweep.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// IDSweepPayload is the payload of an id-sweep job: an inclusive ID range to probe.
type IDSweepPayload struct {
	From uint32 `json:"from"`
	To   uint32 `json:"to"`
}

// IDSweep probes a range of Lodestone character IDs, ingesting any that exist
// and chaining an achievement-census job for each discovery.
type IDSweep struct {
	lodestone contract.LodestoneClient
	census    *census.Service
}

func NewIDSweep(lodestone contract.LodestoneClient, svc *census.Service) *IDSweep {
	return &IDSweep{lodestone: lodestone, census: svc}
}

func (h *IDSweep) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p IDSweepPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("id-sweep payload: %w", err)
	}

	var next []contract.QueueJob
	for id := p.From; id <= p.To; id++ {
		char, err := h.lodestone.FetchCharacter(ctx, id)
		if errors.Is(err, contract.ErrCharacterNotFound) {
			continue // doesn't exist
		}
		if err != nil {
			return nil, fmt.Errorf("id-sweep fetch %d: %w", id, err)
		}
		if err := h.census.UpsertCharacter(ctx, char); err != nil {
			return nil, fmt.Errorf("id-sweep upsert %d: %w", id, err)
		}
		next = append(next, AchievementCensusJob(id))
	}
	return next, nil
}
```

`domain/census/handler/event.go` additions:

```go
import (
	"encoding/json"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// AchievementCensusPayload identifies a character to run an achievement census on.
type AchievementCensusPayload struct {
	CharacterID uint32 `json:"character_id"`
}

// AchievementCensusJob builds an achievement-census queue job for a character.
func AchievementCensusJob(characterID uint32) contract.QueueJob {
	b, _ := json.Marshal(AchievementCensusPayload{CharacterID: characterID})
	return contract.QueueJob{Type: EventAchievementCensus, Payload: b}
}
```

- [ ] **Step 4: Run to verify it passes, then commit**

```bash
go test ./domain/census/handler/ -v
git add domain/census/handler/
git commit -m "feat(handler): id-sweep discovery handler"
```

---

### Task 4: Worker pool

**Files:**
- Create: `domain/census/worker/worker.go`
- Test: `domain/census/worker/worker_test.go`

- [ ] **Step 1: Write the failing test**

```go
package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type recordingHandler struct {
	mu    sync.Mutex
	calls int
}

func (h *recordingHandler) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	return nil, nil
}

func TestWorker_ProcessesClaimedJobs(t *testing.T) {
	// A queue fake seeded with 3 jobs is needed; the existing mock/queue fake
	// must expose Claim/Complete. See gotchas.
}
```

The existing `mock/queue` fake may need `Claim`/`Complete` support — extend it in this task (see gotchas). The test should: seed 3 pending jobs, run the worker briefly, and assert the handler was invoked and the jobs completed.

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./domain/census/worker/
```

Expected: FAIL — package undefined.

- [ ] **Step 3: Implement**

`domain/census/worker/worker.go`:

```go
package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Worker claims jobs of one event type and dispatches them to the registered
// handler. Handler errors are retried (backoff/max-attempts enforced by the
// queue); success publishes the handler's returned jobs atomically.
type Worker struct {
	queue       contract.Queue
	handlers    *handler.Registry
	pollInterval time.Duration
}

func New(q contract.Queue, h *handler.Registry) *Worker {
	return &Worker{queue: q, handlers: h, pollInterval: time.Second}
}

func (w *Worker) Run(ctx context.Context, eventType string, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}
	h, ok := w.handlers.Get(eventType)
	if !ok {
		return fmt.Errorf("no handler registered for event %q", eventType)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.loop(ctx, eventType, h); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) loop(ctx context.Context, eventType string, h handler.Handler) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		jobs, err := w.queue.Claim(ctx, eventType, 1)
		if err != nil {
			return fmt.Errorf("claim %s: %w", eventType, err)
		}
		if len(jobs) == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.pollInterval):
				continue
			}
		}
		for _, job := range jobs {
			next, err := h.Handle(ctx, job.Payload)
			if err != nil {
				if rerr := w.queue.Retry(ctx, job.ID); rerr != nil {
					return fmt.Errorf("retry job %d: %w", job.ID, rerr)
				}
				continue
			}
			if err := w.queue.Complete(ctx, job.ID, next...); err != nil {
				return fmt.Errorf("complete job %d: %w", job.ID, err)
			}
		}
	}
}
```

- [ ] **Step 4: Run to verify it passes, then commit**

```bash
go test ./domain/census/worker/ -v
git add domain/census/worker/
git commit -m "feat(worker): claim-process-complete worker pool"
```

---

### Task 5: consume command

**Files:**
- Modify: `cmd/cli/consume.go`, `cmd/cli/root.go` (register)

- [ ] **Step 1: Implement**

```go
package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/worker"
)

var consumeCmd = &cobra.Command{
	Use:   "consume <event>",
	Short: "Run a consumer worker for one event type (long-running)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		eventType := args[0]
		concurrency, _ := cmd.Flags().GetInt("concurrency")

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		w := worker.New(container.Load.Queue(), container.Load.Handlers())
		return w.Run(ctx, eventType, concurrency)
	},
}

func init() {
	rootCmd.AddCommand(consumeCmd)
	consumeCmd.Flags().Int("concurrency", 4, "number of concurrent workers")
}
```

- [ ] **Step 2: Verify + commit**

```bash
go build ./...
git add cmd/cli/consume.go
git commit -m "feat(cli): consume command (worker pool runner)"
```

---

### Task 6: publish command

**Files:**
- Modify: `cmd/cli/publish.go`

- [ ] **Step 1: Implement**

```go
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

var publishIDSweepCmd = &cobra.Command{
	Use:   "id-sweep",
	Short: "Publish id-sweep jobs covering an ID range (chunked)",
	RunE: func(cmd *cobra.Command, args []string) error {
		from, _ := cmd.Flags().GetUint32("from")
		to, _ := cmd.Flags().GetUint32("to")
		chunkSize, _ := cmd.Flags().GetUint32("chunk-size")
		if to < from {
			return fmt.Errorf("--to (%d) must be >= --from (%d)", to, from)
		}
		if chunkSize == 0 {
			chunkSize = 100
		}

		var jobs []contract.QueueJob
		for start := from; start <= to; start += chunkSize {
			end := start + chunkSize - 1
			if end > to {
				end = to
			}
			b, _ := json.Marshal(handler.IDSweepPayload{From: start, To: end})
			jobs = append(jobs, contract.QueueJob{Type: handler.EventIDSweep, Payload: b})
		}

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		return q.Publish(cmd.Context(), jobs...)
	},
}

func init() {
	publishCmd.AddCommand(publishIDSweepCmd)
	publishIDSweepCmd.Flags().Uint32("from", 1, "first character ID")
	publishIDSweepCmd.Flags().Uint32("to", 0, "last character ID (required)")
	publishIDSweepCmd.Flags().Uint32("chunk-size", 100, "IDs per id-sweep job")
}

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish queue jobs (cronjob entrypoint)",
}
```

- [ ] **Step 2: Verify + commit**

```bash
go build ./...
git add cmd/cli/publish.go
git commit -m "feat(cli): publish command with id-sweep bootstrap"
```

---

### Task 7: Container wiring (handlers)

**Files:**
- Modify: `container/domain.go`

- [ ] **Step 1: Implement**

Add to `container/domain.go` (imports `domain/census/handler`):

```go
func (s *ServiceContainer) Handlers() *handler.Registry {
	reg := handler.NewRegistry()
	reg.Register(handler.EventIDSweep, handler.NewIDSweep(s.LodestoneClient(), s.CensusService()))
	return reg
}
```

Note: this builds a fresh registry per call; handlers are stateless so this is fine (and avoids caching complexity). Document it in the docstring.

- [ ] **Step 2: Verify + commit**

```bash
go build ./... && go test ./container/
git add container/domain.go
git commit -m "feat(container): handler registry accessor"
```

---

### Task 8: Documentation

**Files:**
- Create: `docs/events.md`
- Modify: `docs/census.md` (update "not yet implemented")

- [ ] **Step 1: Write `docs/events.md`**

Cover: the event model (`id-sweep`, `character-census`, `achievement-census`, `fc-census`); payload schemas; the chaining rules (id-sweep → achievement-census; later: character-census → achievement-census + fc-census; fc-census → character-census); loop safety (queue's `UNIQUE(type, payload_hash)` dedup); the worker pool contract (claim → handle → complete/retry, concurrency, backoff via queue); the `consume`/`publish` commands; and the 404-vs-retry semantics (`ErrCharacterNotFound`).

- [ ] **Step 2: Update `docs/census.md`**

Move "Ingest handlers" from "not yet implemented" to a "now implemented" note (id-sweep only; character/achievement/fc handlers are the next phase).

- [ ] **Step 3: Commit**

```bash
git add docs/events.md docs/census.md
git commit -m "docs: event model and ingest pipeline"
```

---

### Task 9: Final verification

- [ ] **Step 1: Full suite + race**

```bash
go test ./... -race
```

- [ ] **Step 2: Lint + build**

```bash
PATH="$HOME/go/bin:$PATH" make lint
make build
```

- [ ] **Step 3: Smoke test the publish command**

```bash
./bin/ffxiv-census publish id-sweep --from 1 --to 100 --chunk-size 25
```

Expected: 4 `id-sweep` jobs published; `./bin/ffxiv-census` exits 0.

- [ ] **Step 4: Commit any fixes**

```bash
git add -A && git commit -m "chore: id-sweep ingest verification"
```

---

## Implementation Gotchas

1. **`mock/queue` may need `Claim`/`Complete`/`Retry`.** The existing fake (`mock/queue/queue.go`) was written for the queue phase; verify it implements the full `contract.Queue` interface. If it only has `Publish`/`Depth`, extend it with an in-memory job store + `Claim` (filter by type/status/run_at), `Complete` (mark done + append next jobs), `Retry` (increment attempts / mark failed) so Task 4 can test the worker end-to-end. This is a prerequisite for Task 4's test.

2. **`mock/lodestone` `FetchCharacterFunc` returns `(nil, nil)` by default** (not an error). In the id-sweep test, set `FetchCharacterFunc` explicitly; the default nil-return would otherwise be treated as a zero-value character (ID 0) and upserted, which is wrong. Always set the func in tests.

3. **`signal.NotifyContext`** requires Go 1.16+ (fine on 1.25). The consume command uses it for graceful SIGTERM shutdown.

4. **`cobra.ExactArgs(1)`** on `consume` makes a missing event a usage error. `publish` uses subcommands (`publish id-sweep`).

5. **`IDSweepPayload` JSON tags** are lowercase (`from`/`to`); the publish command marshals with the same struct, and the handler unmarshals it — round-trips correctly.

6. **id-sweep is intentionally idempotent:** `UpsertCharacter` is a conflict-upsert, and `AchievementCensusJob` is dedup'd by the queue (`UNIQUE(type, payload_hash)`), so a retried id-sweep chunk re-probes safely without duplicate chained jobs.

7. **`defaultActivityWindow` and job-level milestones** remain unimplemented (later phases); do not add them in this phase.
