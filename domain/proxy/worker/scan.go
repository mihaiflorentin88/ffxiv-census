package worker

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Scanner is the narrow interface the scan worker needs from the proxy service.
type Scanner interface {
	ProcessScanProxy(ctx context.Context, p *contract.ProxyRecord) error
}

// ScanWorker reads priority-ordered database batches and scans proxies directly,
// without going through RabbitMQ.
type ScanWorker struct {
	repo      contract.ProxyRepository
	scanner   Scanner
	logger    contract.Logger
	idleDelay time.Duration
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

// RunScan runs the scan loop until the context is cancelled.
// Concurrency controls both the goroutine limit per batch and the SQL LIMIT.
// Non-positive concurrency defaults to 4.
func (w *ScanWorker) RunScan(ctx context.Context, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}

	for {
		batch, err := w.repo.ListForScan(ctx, concurrency)
		if err != nil {
			return fmt.Errorf("list proxies for scan: %w", err)
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
		var hadError bool

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
						w.logger.ErrorContext(ctx, "scan_worker.panic",
							"proxy_id", rec.ID,
							"error", fmt.Sprintf("%v", r),
							"stack", string(debug.Stack()))
					}
				}()

				if err := w.scanner.ProcessScanProxy(ctx, rec); err != nil {
					w.logger.WarnContext(ctx, "scan_worker.scan_error",
						"proxy_id", rec.ID,
						"error", err)
					hadError = true
				}
			}(&batch[i])
		}

		wg.Wait()

		// If any record had an error, wait before the next batch.
		if hadError {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.idleDelay):
			}
		}
	}
}
