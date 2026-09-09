package worker

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Scanner is the narrow interface the scan worker needs from the proxy service.
type Scanner interface {
	ProcessScanProxy(ctx context.Context, p *contract.ProxyRecord) error
}

// scanListFunc is the function signature for fetching proxy batches.
type scanListFunc func(context.Context, int) ([]contract.ProxyRecord, error)

// ScanWorker reads priority-ordered database batches through a prefetcher and
// scans them concurrently, without going through RabbitMQ.
type ScanWorker struct {
	repo      contract.ProxyRepository
	scanner   Scanner
	logger    contract.Logger
	idleDelay time.Duration
	notifier  func() // called after a proxy becomes active (best-effort)
}

// NewScanWorker creates a ScanWorker. The idleDelay controls how long the
// worker waits after an empty batch or per-record error before querying again.
// Production callers should pass time.Minute.
func NewScanWorker(repo contract.ProxyRepository, scanner Scanner, logger contract.Logger, idleDelay time.Duration) *ScanWorker {
	return &ScanWorker{
		repo:      repo,
		scanner:   scanner,
		logger:    logger,
		idleDelay: idleDelay,
	}
}

// SetNotifier registers a callback invoked after a proxy is successfully scanned
// and marked active. Used to wake waiting workers via ProxyHub.NotifyAvailable.
func (w *ScanWorker) SetNotifier(fn func()) {
	w.notifier = fn
}

// SplitScanConcurrency divides total concurrency into regular and dead pools.
// Non-positive concurrency defaults to 4. Negative percentages clamp to 0;
// values above 90 cap to 90. Integer-floor division is used for the dead pool.
func SplitScanConcurrency(concurrency, deadScanPercentage int) (regular, dead int) {
	if concurrency <= 0 {
		concurrency = 4
	}
	if deadScanPercentage < 0 {
		deadScanPercentage = 0
	}
	if deadScanPercentage > 90 {
		deadScanPercentage = 90
	}
	dead = concurrency * deadScanPercentage / 100
	regular = concurrency - dead
	return regular, dead
}

// prefetchBufferCap returns how many records one pool keeps in memory — queued
// in the buffer or in flight inside a worker: ceil(workers × 1.3). The
// headroom keeps every worker fed while the prefetcher round-trips to the
// database for the next batch, and stays above the worker count even for
// small pools so there is always at least one record ready to feed.
func prefetchBufferCap(workers int) int {
	if workers <= 0 {
		workers = 1
	}
	return (workers*13 + 9) / 10
}

// creditPool is an exact counting semaphore: avail + credits held by the
// prefetcher or workers always equals the initial capacity. A channel was
// tried first, but worker releases could refill it while the prefetcher still
// held drained credits, deadlocking the blocking refunds — a mutex counter
// cannot overflow or lose credits.
type creditPool struct {
	mu    sync.Mutex
	wake  chan struct{} // signaled on release; spurious wakeups are fine
	avail int
}

func newCreditPool(n int) *creditPool {
	return &creditPool{wake: make(chan struct{}, 1), avail: n}
}

func (c *creditPool) take() int {
	c.mu.Lock()
	n := c.avail
	c.avail = 0
	c.mu.Unlock()
	return n
}

// release returns credits; never blocks, never loses count.
func (c *creditPool) release(n int) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	c.avail += n
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// wait blocks until a credit may be available, the context ends, or a failure
// wake arrives. The caller must re-check with take().
func (c *creditPool) wait(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-c.wake:
		return true
	}
}

// RunScan runs the scan loop until the context is cancelled.
// Concurrency controls the number of scan workers per pool; each pool also
// keeps prefetchBufferCap(workers) records in memory (workers + 30%) so the
// workers never wait on a database round trip. The prefetcher claims batches
// ahead of the workers, and claimed rows are stamped last_scanned_at at claim
// time so in-flight rows can never be re-fetched.
// deadScanPercentage (0-90) reserves a portion of concurrency for dead proxy
// scanning. Values below 0 are treated as 0; values above 90 are capped to 90.
func (w *ScanWorker) RunScan(ctx context.Context, concurrency, deadScanPercentage int) error {
	regular, dead := SplitScanConcurrency(concurrency, deadScanPercentage)

	w.logger.InfoContext(ctx, "scan_worker.pool_allocation",
		"regular_workers", regular,
		"dead_workers", dead,
		"regular_buffer", prefetchBufferCap(regular),
		"dead_buffer", prefetchBufferCap(dead))

	poolCtx, poolCancel := context.WithCancel(ctx)
	defer poolCancel()

	// Count launched pools for the result channel.
	poolCount := 1 // regular pool always launches
	if dead > 0 {
		poolCount++
	}
	results := make(chan error, poolCount)

	// Regular pool — always launches.
	go func() {
		results <- w.runScanPool(poolCtx, "regular", regular, w.repo.ListForScan)
	}()

	// Dead pool — only when dead > 0.
	if dead > 0 {
		go func() {
			results <- w.runScanPool(poolCtx, "dead", dead, w.repo.ListDeadForScan)
		}()
	}

	// Collect results; cancel sibling on first error.
	var firstErr error
	for range poolCount {
		if err := <-results; err != nil && firstErr == nil {
			firstErr = err
			poolCancel()
		}
	}
	return firstErr
}

// runScanPool feeds persistent workers from a prefetcher-owned buffer. Credits
// bound produced-but-unfinished records to prefetchBufferCap(workers): the
// prefetcher consumes one credit per record it fetches, and each worker
// releases its credit after the record's scan and write complete. The database
// is only touched by the prefetcher (reads) and by workers between scans
// (writes) — never held open during a proxy check.
func (w *ScanWorker) runScanPool(ctx context.Context, pool string, workers int, list scanListFunc) error {
	capacity := prefetchBufferCap(workers)
	jobs := make(chan contract.ProxyRecord, capacity)
	credits := newCreditPool(capacity)
	// failFlag records that a worker saw a record failure since the last
	// fetch, so the prefetcher can pause feeding for the idle delay. failWake
	// wakes the prefetcher when it is waiting for credits; the flag is the
	// source of truth (the wake is best-effort).
	var failFlag atomic.Bool
	failWake := make(chan struct{}, 1)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case rec, ok := <-jobs:
					if !ok {
						return
					}
					w.scanOne(ctx, pool, rec, credits, &failFlag, failWake)
				}
			}
		}()
	}

	defer func() {
		close(jobs)
		wg.Wait()
	}()

	for {
		// Consume available credits; each fetched record spends one.
		free := credits.take()
		if free == 0 {
			// Buffer full and all workers busy — wait for a completion.
			if !credits.wait(ctx) {
				return nil
			}
			free = credits.take()
		}

		// Pause after record failures — backpressure for database trouble.
		// Refund the drained credits first: the fetch they reserved will not
		// happen this iteration.
		if failFlag.CompareAndSwap(true, false) {
			credits.release(free)
			if !w.waitIdle(ctx) {
				return nil
			}
			continue
		}

		batch, err := list(ctx, free)
		if err != nil {
			return fmt.Errorf("list proxies for scan [%s]: %w", pool, err)
		}

		for _, rec := range batch {
			select {
			case <-ctx.Done():
				return nil
			case jobs <- rec:
			}
		}
		// Refund unconsumed credits: short batch or empty batch. This must
		// run for every fetch — including empty ones — or the pool slowly
		// leaks its credits and starves.
		credits.release(free - len(batch))

		if len(batch) == 0 {
			if !w.waitIdle(ctx) {
				return nil
			}
		}
	}
}

// scanOne runs a single scan and write, recovering panics so one bad record
// cannot take down the pool, and releasing the record's credit afterwards.
func (w *ScanWorker) scanOne(ctx context.Context, pool string, rec contract.ProxyRecord, credits *creditPool, failFlag *atomic.Bool, failWake chan struct{}) {
	defer credits.release(1)
	defer func() {
		if r := recover(); r != nil {
			w.logger.ErrorContext(ctx, "scan_worker.panic",
				"pool", pool,
				"proxy_id", rec.ID,
				"error", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()))
		}
	}()

	if err := w.scanner.ProcessScanProxy(ctx, &rec); err != nil {
		w.logger.WarnContext(ctx, "scan_worker.scan_error",
			"pool", pool,
			"proxy_id", rec.ID,
			"error", err)
		failFlag.Store(true)
		select {
		case failWake <- struct{}{}:
		default:
		}
	} else if w.notifier != nil {
		w.notifier()
	}
}

// waitIdle blocks for the idle delay, returning false if the context ended.
func (w *ScanWorker) waitIdle(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(w.idleDelay):
		return true
	}
}
