package worker_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/worker"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// fakeScanner implements the scanner interface for scan worker tests.
type fakeScanner struct {
	mu      sync.Mutex
	calls   []contract.ProxyRecord
	scanErr error
	delay   time.Duration
}

func (f *fakeScanner) ProcessScanProxy(_ context.Context, p *contract.ProxyRecord) error {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	f.calls = append(f.calls, *p)
	f.mu.Unlock()
	return f.scanErr
}

func (f *fakeScanner) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeScanner) Calls() []contract.ProxyRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contract.ProxyRecord(nil), f.calls...)
}

// fakeScannerFunc is a scanner that calls a function — for flexible test behavior.
type fakeScannerFunc struct {
	mu    sync.Mutex
	count int
	fn    func(context.Context, *contract.ProxyRecord) error
}

func (f *fakeScannerFunc) ProcessScanProxy(ctx context.Context, p *contract.ProxyRecord) error {
	err := f.fn(ctx, p)
	f.mu.Lock()
	f.count++
	f.mu.Unlock()
	return err
}

func (f *fakeScannerFunc) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

// fakeScanRepo implements contract.ProxyRepository for scan worker tests.
// Only ListForScan and ListDeadForScan are used; other methods panic if called.
type fakeScanRepo struct {
	mu             sync.Mutex
	batches        [][]contract.ProxyRecord
	batchIdx       int
	pendingRegular []contract.ProxyRecord // unclaimed tail of the current batch
	deadBatches    [][]contract.ProxyRecord
	deadBatchIdx   int
	pendingDead    []contract.ProxyRecord
	listCalls      int32
	deadListCalls  int32
	listErr        error
	deadListErr    error
	limitSeen      int
	deadLimitSeen  int
	listTimes      []time.Time
}

func newFakeScanRepo(batches ...[]contract.ProxyRecord) *fakeScanRepo {
	return &fakeScanRepo{batches: batches, deadBatches: [][]contract.ProxyRecord{nil}}
}

func newFakeScanRepoWithDead(regularBatches, deadBatches [][]contract.ProxyRecord) *fakeScanRepo {
	return &fakeScanRepo{batches: regularBatches, deadBatches: deadBatches}
}

func (f *fakeScanRepo) ListForScan(_ context.Context, limit int) ([]contract.ProxyRecord, error) {
	atomic.AddInt32(&f.listCalls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limitSeen = limit
	f.listTimes = append(f.listTimes, time.Now())
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.pendingRegular == nil {
		if f.batchIdx >= len(f.batches) {
			return nil, nil
		}
		f.pendingRegular = f.batches[f.batchIdx]
		f.batchIdx++
	}
	out := f.pendingRegular
	if limit > 0 && len(out) > limit {
		// LIMIT is a hard cap; unclaimed rows stay eligible for the next call.
		out = out[:limit]
		f.pendingRegular = f.pendingRegular[limit:]
	} else {
		f.pendingRegular = nil
	}
	return out, nil
}

func (f *fakeScanRepo) ListCallCount() int32 {
	return atomic.LoadInt32(&f.listCalls)
}

// LimitSeen returns the limit passed to the most recent ListForScan call.
func (f *fakeScanRepo) LimitSeen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.limitSeen
}

// DeadLimitSeen returns the limit passed to the most recent ListDeadForScan.
func (f *fakeScanRepo) DeadLimitSeen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deadLimitSeen
}

// ListCallTime returns when the nth (1-based) ListForScan call happened.
func (f *fakeScanRepo) ListCallTime(n int) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listTimes[n-1]
}

func (f *fakeScanRepo) ListDeadForScan(_ context.Context, limit int) ([]contract.ProxyRecord, error) {
	atomic.AddInt32(&f.deadListCalls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deadLimitSeen = limit
	if f.deadListErr != nil {
		return nil, f.deadListErr
	}
	if f.pendingDead == nil {
		if f.deadBatchIdx >= len(f.deadBatches) {
			return nil, nil
		}
		f.pendingDead = f.deadBatches[f.deadBatchIdx]
		f.deadBatchIdx++
	}
	out := f.pendingDead
	if limit > 0 && len(out) > limit {
		out = out[:limit]
		f.pendingDead = f.pendingDead[limit:]
	} else {
		f.pendingDead = nil
	}
	return out, nil
}

func (f *fakeScanRepo) DeadListCallCount() int32 {
	return atomic.LoadInt32(&f.deadListCalls)
}

// Stub implementations for contract.ProxyRepository.
func (f *fakeScanRepo) Exists(context.Context, string, string, int) (bool, error) {
	panic("not implemented")
}

func (f *fakeScanRepo) InsertIfAbsent(context.Context, contract.ProxyRecord) (int64, bool, error) {
	panic("not implemented")
}

func (f *fakeScanRepo) Get(context.Context, int64) (*contract.ProxyRecord, error) {
	panic("not implemented")
}

func (f *fakeScanRepo) UpdateStatus(context.Context, int64, string, *int, int, *time.Time) error {
	panic("not implemented")
}

func (f *fakeScanRepo) UpdateScanTime(context.Context, int64) error {
	panic("not implemented")
}

func (f *fakeScanRepo) ListActive(context.Context, int) ([]contract.ProxyRecord, error) {
	panic("not implemented")
}

func (f *fakeScanRepo) Count(context.Context) (int64, error) {
	panic("not implemented")
}

func (f *fakeScanRepo) CountByStatus(context.Context) (map[string]int64, error) {
	panic("not implemented")
}

func (f *fakeScanRepo) ClaimProxy(context.Context, string, time.Duration) (*contract.ProxyRecord, error) {
	panic("not implemented")
}

func (f *fakeScanRepo) ExtendLock(context.Context, int64, string, time.Duration) (bool, error) {
	panic("not implemented")
}

func (f *fakeScanRepo) ReleaseProxy(context.Context, int64, string) error {
	panic("not implemented")
}

func (f *fakeScanRepo) MarkFailedProxy(context.Context, int64, string) error {
	panic("not implemented")
}

func (f *fakeScanRepo) RandomActive(context.Context, []int64) (*contract.ProxyRecord, error) {
	panic("not implemented")
}

func records(ids ...int64) []contract.ProxyRecord {
	out := make([]contract.ProxyRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, contract.ProxyRecord{ID: id, Protocol: "http", IP: fmt.Sprintf("10.0.0.%d", id), Port: 80})
	}
	return out
}

func newTestWorker(repo *fakeScanRepo, scanner worker.Scanner, idle time.Duration) *worker.ScanWorker {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return worker.NewScanWorker(repo, scanner, logger, idle)
}

func TestRunScan_PrefetchLimitIsBufferCap(t *testing.T) {
	// The prefetcher fetches up to the buffer cap (workers + 30%), not just the
	// worker count, so workers never wait on a database round trip.
	repo := newFakeScanRepo(nil) // empty batch → idle loop
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.RunScan(ctx, 7, 0)

	// 7 workers → buffer cap = ceil(7 × 1.3) = 10.
	if repo.LimitSeen() != 10 {
		t.Errorf("ListForScan limit = %d, want 10 (buffer cap)", repo.LimitSeen())
	}
}

func TestRunScan_NormalizesNonPositiveConcurrency(t *testing.T) {
	repo := newFakeScanRepo(nil)
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.RunScan(ctx, 0, 0)

	// Default 4 workers → buffer cap = ceil(4 × 1.3) = 6.
	if repo.LimitSeen() != 6 {
		t.Errorf("ListForScan limit = %d, want 6 (default cap)", repo.LimitSeen())
	}
}

func TestRunScan_PrefetchStaysWithinBufferCap(t *testing.T) {
	// While every worker is busy and the buffer is full, the prefetcher must
	// not fetch more: in-flight + buffered <= workers + 30%.
	many := records(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	repo := newFakeScanRepo(many, nil)

	gate := make(chan struct{})
	var gated int32
	scanner := &fakeScannerFunc{fn: func(_ context.Context, _ *contract.ProxyRecord) error {
		if atomic.AddInt32(&gated, 1) <= 4 { // gate one worker-load; let the rest through
			<-gate
		}
		return nil
	}}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.RunScan(ctx, 4, 0)
		close(done)
	}()

	// 4 workers → cap 6. The first fetch must be exactly the cap, and while
	// the gated workers hold records, no further fetch may happen.
	time.Sleep(150 * time.Millisecond)
	if got := repo.ListCallCount(); got != 1 {
		t.Errorf("ListForScan calls = %d, want 1 (buffer full, workers busy)", got)
	}
	if repo.LimitSeen() != 6 {
		t.Errorf("ListForScan limit = %d, want 6 (cap)", repo.LimitSeen())
	}

	close(gate) // let everything drain
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if scanner.CallCount() >= 10 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScan did not exit after cancellation")
	}

	if got := scanner.CallCount(); got != 10 {
		t.Errorf("scanned = %d, want 10", got)
	}
}

func TestRunScan_PrefetchContinuesWhileWorkersScan(t *testing.T) {
	// The prefetcher must run ahead: the next fetch happens while the previous
	// batch is still being scanned, instead of waiting for a batch barrier.
	repo := newFakeScanRepo(records(1, 2), records(3), nil)
	scanner := &fakeScanner{delay: 60 * time.Millisecond}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.RunScan(ctx, 4, 0)
		close(done)
	}()

	// Batch 1 has two 60ms scans; a barrier design would still be inside batch
	// 1 at 30ms. Lookahead must already have fetched batch 2.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if repo.ListCallCount() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := repo.ListCallCount(); got < 2 {
		t.Errorf("ListForScan calls = %d, want >= 2 while batch 1 still scanning (no lookahead)", got)
	}

	// Drain everything, then stop the idle loop.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if scanner.CallCount() >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScan did not exit after cancellation")
	}
	if got := scanner.CallCount(); got != 3 {
		t.Errorf("scanned = %d, want 3", got)
	}
}

func TestRunScan_ProcessesEachRecordExactlyOnce(t *testing.T) {
	repo := newFakeScanRepo(records(1, 2, 3), records(4, 5), nil)
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.RunScan(ctx, 4, 0)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if scanner.CallCount() >= 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScan did not exit after cancellation")
	}

	seen := map[int64]int{}
	for _, p := range scanner.Calls() {
		seen[p.ID]++
	}
	for _, id := range []int64{1, 2, 3, 4, 5} {
		if seen[id] != 1 {
			t.Errorf("proxy %d processed %d times, want 1", id, seen[id])
		}
	}
}

func TestRunScan_EmptyBatchDoesNotHotPoll(t *testing.T) {
	// First batch empty → should wait idle, then context cancel stops it.
	repo := newFakeScanRepo(nil) // always returns empty
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.RunScan(ctx, 2, 0)
		close(done)
	}()

	// Give it time to enter idle wait, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK — cancelled during idle wait
	case <-time.After(2 * time.Second):
		t.Fatal("RunScan did not exit after context cancellation during idle wait")
	}

	// Should have called ListForScan only once (the empty batch).
	if got := repo.ListCallCount(); got != 1 {
		t.Errorf("ListForScan calls = %d, want 1 (no hot-poll)", got)
	}
}

func TestRunScan_ListErrorIsReturned(t *testing.T) {
	repo := newFakeScanRepo()
	repo.listErr = errors.New("database connection refused")
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)

	err := w.RunScan(context.Background(), 2, 0)

	if err == nil {
		t.Fatal("expected error from ListForScan")
	}
	if !errors.Is(err, repo.listErr) {
		t.Errorf("error = %v, want %v", err, repo.listErr)
	}
}

func TestRunScan_PerRecordFailureIsolated(t *testing.T) {
	// Batch of 3: first and third succeed, second fails.
	batch := records(1, 2, 3)
	// After the first batch (with error), return empty to stop.
	repo := newFakeScanRepo(batch, nil)

	var scanCount int32
	scanner := &fakeScannerFunc{
		fn: func(_ context.Context, p *contract.ProxyRecord) error {
			atomic.AddInt32(&scanCount, 1)
			if p.ID == 2 {
				return errors.New("proxy unreachable")
			}
			return nil
		},
	}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.RunScan(ctx, 3, 0)

	// All 3 should have been attempted despite the error on ID 2.
	if got := atomic.LoadInt32(&scanCount); got != 3 {
		t.Errorf("scan attempts = %d, want 3 (error isolated)", got)
	}
}

func TestRunScan_ScanErrorPausesPrefetch(t *testing.T) {
	// After a record fails, the pool pauses feeding for the idle delay before
	// fetching again.
	repo := newFakeScanRepo(records(1, 2), nil)
	scanner := &fakeScannerFunc{fn: func(_ context.Context, p *contract.ProxyRecord) error {
		if p.ID == 1 {
			// Delay the failure so the prefetcher is parked waiting for a
			// credit while the record is in flight — the wake must observe
			// the failure flag before the next fetch.
			time.Sleep(30 * time.Millisecond)
			return errors.New("proxy unreachable")
		}
		return nil
	}}
	w := newTestWorker(repo, scanner, 80*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w.RunScan(ctx, 1, 0) // one worker → buffer cap 1 → prefetcher parked during the scan

	if got := repo.ListCallCount(); got < 2 {
		t.Fatalf("ListForScan calls = %d, want >= 2", got)
	}
	gap := repo.ListCallTime(2).Sub(repo.ListCallTime(1))
	if gap < 60*time.Millisecond {
		t.Errorf("gap between fetches after scan error = %v, want >= ~idle delay (80ms)", gap)
	}
}

func TestRunScan_PanicIsContained(t *testing.T) {
	// A panicking scanner must not take down the pool: the panic is recovered
	// and the remaining records are still processed.
	batch := records(1, 2, 3)
	repo := newFakeScanRepo(batch, nil)
	scanner := &fakeScannerFunc{fn: func(_ context.Context, p *contract.ProxyRecord) error {
		if p.ID == 2 {
			panic("scanner exploded")
		}
		return nil
	}}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.RunScan(ctx, 3, 0)

	if got := scanner.CallCount(); got != 2 {
		t.Errorf("scanned = %d, want 2 (panic on ID 2 contained)", got)
	}
}

func TestSplitScanConcurrency(t *testing.T) {
	tests := []struct {
		name        string
		concurrency int
		percentage  int
		wantRegular int
		wantDead    int
	}{
		{"20pct of 10", 10, 20, 8, 2},
		{"0pct", 10, 0, 10, 0},
		{"90pct of 10", 10, 90, 1, 9},
		{"91pct capped to 90", 10, 91, 1, 9},
		{"200pct capped to 90", 10, 200, 1, 9},
		{"negative pct", 10, -5, 10, 0},
		{"zero concurrency normalized to 4", 0, 20, 4, 0},
		{"negative concurrency normalized to 4", -1, 20, 4, 0},
		{"300 concurrency 20pct", 300, 20, 240, 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRegular, gotDead := worker.SplitScanConcurrency(tt.concurrency, tt.percentage)
			if gotRegular != tt.wantRegular {
				t.Errorf("regular = %d, want %d", gotRegular, tt.wantRegular)
			}
			if gotDead != tt.wantDead {
				t.Errorf("dead = %d, want %d", gotDead, tt.wantDead)
			}
		})
	}
}

func TestRunScan_DeadPercentage_SplitsConcurrency(t *testing.T) {
	// 10 concurrency, 20% dead → regular=8, dead=2. The first regular fetch is
	// the buffer cap (8 workers → ceil(8 × 1.3) = 11); dead fetch is its cap (ceil(2 × 1.3) = 3).
	regularBatch := []contract.ProxyRecord{
		{ID: 1, Protocol: "http", IP: "1.1.1.1", Port: 80},
	}
	deadBatch := []contract.ProxyRecord{
		{ID: 100, Protocol: "http", IP: "9.9.9.9", Port: 80, Status: contract.ProxyStatusDead},
	}
	repo := newFakeScanRepoWithDead(
		[][]contract.ProxyRecord{regularBatch, nil},
		[][]contract.ProxyRecord{deadBatch, nil},
	)
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.RunScan(ctx, 10, 20)

	if repo.LimitSeen() != 11 {
		t.Errorf("regular limit = %d, want 11 (buffer cap)", repo.LimitSeen())
	}
	if repo.DeadLimitSeen() != 3 {
		t.Errorf("dead limit = %d, want 3 (buffer cap)", repo.DeadLimitSeen())
	}
}

func TestRunScan_ZeroPercentage_NoDeadCalls(t *testing.T) {
	batch := []contract.ProxyRecord{
		{ID: 1, Protocol: "http", IP: "1.1.1.1", Port: 80},
	}
	repo := newFakeScanRepo(batch, nil)
	repo.deadBatches = nil // no dead batches
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	w.RunScan(ctx, 10, 0)

	if repo.DeadListCallCount() != 0 {
		t.Errorf("dead list calls = %d, want 0 (no dead pool)", repo.DeadListCallCount())
	}
}

func TestRunScan_EmptyDeadPoolDoesNotStopRegular(t *testing.T) {
	regularBatch := []contract.ProxyRecord{
		{ID: 1, Protocol: "http", IP: "1.1.1.1", Port: 80},
	}
	// Dead pool returns empty then nil to stop.
	repo := newFakeScanRepoWithDead(
		[][]contract.ProxyRecord{regularBatch, nil},
		[][]contract.ProxyRecord{nil},
	)
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.RunScan(ctx, 10, 20)

	// Regular pool should have scanned the record.
	if got := scanner.CallCount(); got < 1 {
		t.Errorf("scanned = %d, want >= 1 (regular pool should progress)", got)
	}
}

func TestRunScan_ContextCancellation_StopsBothPools(t *testing.T) {
	repo := newFakeScanRepoWithDead(
		[][]contract.ProxyRecord{nil},
		[][]contract.ProxyRecord{nil},
	)
	scanner := &fakeScanner{}
	w := newTestWorker(repo, scanner, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.RunScan(ctx, 10, 20)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("RunScan did not exit after context cancellation")
	}
}
