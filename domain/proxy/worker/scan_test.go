package worker_test

import (
	"context"
	"errors"
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

// fakeScanRepo implements contract.ProxyRepository for scan worker tests.
// Only ListForScan and ListDeadForScan are used; other methods panic if called.
type fakeScanRepo struct {
	mu            sync.Mutex
	batches       [][]contract.ProxyRecord
	batchIdx      int
	deadBatches   [][]contract.ProxyRecord
	deadBatchIdx  int
	listCalls     int32
	deadListCalls int32
	listErr       error
	deadListErr   error
	limitSeen     int
	deadLimitSeen int
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
	f.limitSeen = limit
	if f.listErr != nil {
		f.mu.Unlock()
		return nil, f.listErr
	}
	if f.batchIdx >= len(f.batches) {
		f.mu.Unlock()
		return nil, nil
	}
	batch := f.batches[f.batchIdx]
	f.batchIdx++
	f.mu.Unlock()
	return batch, nil
}

func (f *fakeScanRepo) ListCallCount() int32 {
	return atomic.LoadInt32(&f.listCalls)
}

func (f *fakeScanRepo) ListDeadForScan(_ context.Context, limit int) ([]contract.ProxyRecord, error) {
	atomic.AddInt32(&f.deadListCalls, 1)
	f.mu.Lock()
	f.deadLimitSeen = limit
	if f.deadListErr != nil {
		f.mu.Unlock()
		return nil, f.deadListErr
	}
	if f.deadBatchIdx >= len(f.deadBatches) {
		f.mu.Unlock()
		return nil, nil
	}
	batch := f.deadBatches[f.deadBatchIdx]
	f.deadBatchIdx++
	f.mu.Unlock()
	return batch, nil
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

func TestRunScan_PassesConcurrencyToListForScan(t *testing.T) {
	repo := newFakeScanRepo(nil) // empty batch → stops
	scanner := &fakeScanner{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.RunScan(ctx, 7, 0)

	if repo.limitSeen != 7 {
		t.Errorf("ListForScan limit = %d, want 7", repo.limitSeen)
	}
}

func TestRunScan_NormalizesNonPositiveConcurrency(t *testing.T) {
	repo := newFakeScanRepo(nil)
	scanner := &fakeScanner{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.RunScan(ctx, 0, 0)

	if repo.limitSeen != 4 {
		t.Errorf("ListForScan limit = %d, want 4 (default)", repo.limitSeen)
	}
}

func TestRunScan_BatchBarrierBeforeNextFetch(t *testing.T) {
	// Two batches: first has 2 records, second has 1.
	batch1 := []contract.ProxyRecord{
		{ID: 1, Protocol: "http", IP: "1.1.1.1", Port: 80},
		{ID: 2, Protocol: "http", IP: "2.2.2.2", Port: 80},
	}
	batch2 := []contract.ProxyRecord{
		{ID: 3, Protocol: "http", IP: "3.3.3.3", Port: 80},
	}
	repo := newFakeScanRepo(batch1, batch2)
	scanner := &fakeScanner{delay: 50 * time.Millisecond}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.RunScan(ctx, 2, 0)

	// All 3 records should be scanned.
	if got := scanner.CallCount(); got != 3 {
		t.Errorf("scanned = %d, want 3", got)
	}
	// At least 2 ListForScan calls: one per batch (worker loops after last).
	if got := repo.ListCallCount(); got < 2 {
		t.Errorf("ListForScan calls = %d, want >= 2", got)
	}
}

func TestRunScan_EmptyBatchDoesNotHotPoll(t *testing.T) {
	// First batch empty → should wait idle, then context cancel stops it.
	repo := newFakeScanRepo(nil) // always returns empty
	scanner := &fakeScanner{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 200*time.Millisecond)
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 10*time.Millisecond)
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
	batch := []contract.ProxyRecord{
		{ID: 1, Protocol: "http", IP: "1.1.1.1", Port: 80},
		{ID: 2, Protocol: "http", IP: "2.2.2.2", Port: 80},
		{ID: 3, Protocol: "http", IP: "3.3.3.3", Port: 80},
	}
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.RunScan(ctx, 3, 0)

	// All 3 should have been attempted despite the error on ID 2.
	if got := atomic.LoadInt32(&scanCount); got != 3 {
		t.Errorf("scan attempts = %d, want 3 (error isolated)", got)
	}
}

// fakeScannerFunc is a scanner that calls a function — for flexible test behavior.
type fakeScannerFunc struct {
	fn func(context.Context, *contract.ProxyRecord) error
}

func (f *fakeScannerFunc) ProcessScanProxy(ctx context.Context, p *contract.ProxyRecord) error {
	return f.fn(ctx, p)
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
	// 10 concurrency, 20% dead → regular=8, dead=2
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.RunScan(ctx, 10, 20)

	if repo.limitSeen != 8 {
		t.Errorf("regular limit = %d, want 8", repo.limitSeen)
	}
	if repo.deadLimitSeen != 2 {
		t.Errorf("dead limit = %d, want 2", repo.deadLimitSeen)
	}
}

func TestRunScan_ZeroPercentage_NoDeadCalls(t *testing.T) {
	batch := []contract.ProxyRecord{
		{ID: 1, Protocol: "http", IP: "1.1.1.1", Port: 80},
	}
	repo := newFakeScanRepo(batch, nil)
	repo.deadBatches = nil // no dead batches
	scanner := &fakeScanner{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 10*time.Millisecond)
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 10*time.Millisecond)
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := worker.NewScanWorker(repo, scanner, logger, 200*time.Millisecond)
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
