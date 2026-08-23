package worker

import (
	"context"
	"fmt"
	"log/slog"
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

// ScanWorker reads priority-ordered database batches and scans proxies directly,
// without going through RabbitMQ.
type ScanWorker struct {
	repo      contract.ProxyRepository
	scanner   Scanner
	logger    contract.Logger
	idleDelay time.Duration
	notifier  func() // called after a proxy becomes active (best-effort)
}

// NewScanWorker creates a ScanWorker. The idleDelay controls how long the worker
// waits after an empty batch or per-record error before querying again.
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

// RunScan runs the scan loop until the context is cancelled.
// Concurrency controls both the goroutine limit per batch and the SQL LIMIT.
// Non-positive concurrency defaults to 4.
// deadScanPercentage (0-90) reserves a portion of concurrency for dead proxy scanning.
// Values below 0 are treated as 0; values above 90 are capped to 90.
func (w *ScanWorker) RunScan(ctx context.Context, concurrency, deadScanPercentage int) error {
	regular, dead := SplitScanConcurrency(concurrency, deadScanPercentage)

	w.logger.InfoContext(ctx, "Allocated scan worker pool",
		slog.Int("pool_size", regular+dead))

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

// runScanPool runs a single scan pool loop until the context is cancelled.
func (w *ScanWorker) runScanPool(ctx context.Context, pool string, concurrency int, list scanListFunc) error {
	for {
		batch, err := list(ctx, concurrency)
		if err != nil {
			return fmt.Errorf("list proxies for scan [%s]: %w", pool, err)
		}

		if len(batch) == 0 {
			// Empty batch — wait before retrying.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.idleDelay):
			}
			continue
		}

		// Scan the batch with bounded concurrency.
		var wg sync.WaitGroup
		sem := make(chan struct{}, concurrency)
		var hadError atomic.Bool

		for i := range batch {
			select {
			case <-ctx.Done():
				wg.Wait()
				return nil
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(rec *contract.ProxyRecord) {
				defer wg.Done()
				defer func() { <-sem }()
				defer func() {
					if r := recover(); r != nil {
						w.logger.ErrorContext(ctx, "Scan worker panicked",
							slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", rec.Protocol, rec.IP, rec.Port)),
							slog.Any("error", fmt.Errorf("%v", r)),
							slog.String("stack", string(debug.Stack())))
					}
				}()

				if err := w.scanner.ProcessScanProxy(ctx, rec); err != nil {
					w.logger.WarnContext(ctx, "Proxy scan failed",
						slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", rec.Protocol, rec.IP, rec.Port)),
						slog.Any("error", err))
					hadError.Store(true)
				} else if w.notifier != nil {
					w.notifier()
				}
			}(&batch[i])
		}

		wg.Wait()

		// If any record had an error, wait before the next batch.
		if hadError.Load() {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.idleDelay):
			}
		}
	}
}
