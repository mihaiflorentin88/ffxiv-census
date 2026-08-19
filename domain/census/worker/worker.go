package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type jobCategory int

const (
	catRetries jobCategory = iota
	catUpdates
	catSecondary
	catPrimary
)

func classifyJob(j contract.QueueJob) jobCategory {
	if j.Attempts > 1 {
		return catRetries
	}
	switch j.Type {
	case handler.EventCharacterCensus:
		return catUpdates
	case handler.EventFreeCompanyCensus, handler.EventAchievementCensus:
		return catSecondary
	case handler.EventIDSweep:
		return catPrimary
	default:
		return catPrimary
	}
}

// Worker claims jobs from configured event types and dispatches them to registered
// handlers using a dynamic Dispatcher and Worker Pool pattern.
type Worker struct {
	queue        contract.Queue
	handlers     *handler.Registry
	logger       contract.Logger
	rateLimiter  contract.ProviderRateLimiter
	pollInterval time.Duration
}

func New(q contract.Queue, h *handler.Registry, logger contract.Logger, rateLimiter ...contract.ProviderRateLimiter) *Worker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	var rl contract.ProviderRateLimiter
	if len(rateLimiter) > 0 {
		rl = rateLimiter[0]
	}
	return &Worker{
		queue:        q,
		handlers:     h,
		logger:       logger,
		rateLimiter:  rl,
		pollInterval: 500 * time.Millisecond,
	}
}

// SetPollInterval configures the worker's polling interval.
func (w *Worker) SetPollInterval(d time.Duration) {
	if d > 0 {
		w.pollInterval = d
	}
}

func (w *Worker) Run(ctx context.Context, eventType string, concurrency int) error {
	return w.RunEvents(ctx, []string{eventType}, concurrency)
}

// RunEvents runs the worker consuming jobs from multiple event types concurrently.
func (w *Worker) RunEvents(ctx context.Context, eventTypes []string, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}
	if len(eventTypes) == 0 {
		eventTypes = []string{
			handler.EventIDSweep,
			handler.EventCharacterCensus,
			handler.EventAchievementCensus,
			handler.EventFreeCompanyCensus,
		}
	}

	for _, eventType := range eventTypes {
		if _, ok := w.handlers.Get(eventType); !ok {
			return fmt.Errorf("no handler registered for event %q", eventType)
		}
	}

	w.logger.InfoContext(ctx, "worker.start", slog.Any("event_types", eventTypes), slog.Int("concurrency", concurrency))
	for _, eventType := range eventTypes {
		if n, err := w.queue.CountJobs(ctx, contract.QueueJobFilter{Type: eventType, Status: contract.QueueJobPending}); err == nil && n == 0 {
			w.logger.InfoContext(ctx, "worker.queue_status", slog.String("event_type", eventType), slog.Int64("pending_jobs", 0), slog.String("notice", "no pending jobs in queue, waiting for new publications..."))
		}
		if n, err := w.queue.ReclaimClaimed(ctx, eventType); err != nil {
			return fmt.Errorf("reclaim claimed jobs for %s: %w", eventType, err)
		} else if n > 0 {
			w.logger.InfoContext(ctx, "worker.reclaimed", slog.String("event_type", eventType), slog.Int("reclaimed", n))
		}
	}

	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobCh := make(chan contract.QueueJob, concurrency)
	doneCh := make(chan jobCategory, concurrency)

	var wg sync.WaitGroup

	// Start Worker Pool
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			w.workerLoop(childCtx, jobCh, doneCh, workerID)
		}(i)
	}

	// Run Dispatcher in current goroutine
	err := w.runDispatcher(childCtx, eventTypes, jobCh, doneCh, concurrency)
	cancel() // cancel workers when dispatcher finishes

	wg.Wait()
	w.logger.InfoContext(ctx, "worker.stop", slog.Any("event_types", eventTypes))

	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func (w *Worker) isEventTypeAvailable(eventType string) bool {
	if w.rateLimiter == nil {
		return true
	}
	switch eventType {
	case handler.EventAchievementCensus, handler.EventFreeCompanyCensus:
		return w.rateLimiter.IsAvailable(contract.ProviderLodestone)
	case handler.EventCharacterCensus, handler.EventIDSweep:
		// Character census and ID sweep are dual-source; claim if at least one provider is available
		return w.rateLimiter.IsAvailable(contract.ProviderLodestone) || w.rateLimiter.IsAvailable(contract.ProviderTomestone)
	default:
		return true
	}
}

func (w *Worker) runDispatcher(
	ctx context.Context,
	eventTypes []string,
	jobCh chan<- contract.QueueJob,
	doneCh <-chan jobCategory,
	concurrency int,
) error {
	defer close(jobCh)

	// Calculate category ceilings
	maxPrimary := concurrency
	maxRetries := max(1, concurrency/4)
	maxUpdates := max(1, concurrency/4)
	maxSecondary := max(1, concurrency/4)

	var primaryInFlight, updatesInFlight, retriesInFlight, secondaryInFlight int
	lastCategoryIndex := 0

	decrement := func(cat jobCategory) {
		switch cat {
		case catRetries:
			if retriesInFlight > 0 {
				retriesInFlight--
			}
		case catUpdates:
			if updatesInFlight > 0 {
				updatesInFlight--
			}
		case catSecondary:
			if secondaryInFlight > 0 {
				secondaryInFlight--
			}
		case catPrimary:
			if primaryInFlight > 0 {
				primaryInFlight--
			}
		}
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		// Drain finished notifications
	drainLoop:
		for {
			select {
			case cat := <-doneCh:
				decrement(cat)
			default:
				break drainLoop
			}
		}

		totalInFlight := primaryInFlight + updatesInFlight + retriesInFlight + secondaryInFlight
		freeCapacity := concurrency - totalInFlight

		if freeCapacity <= 0 {
			select {
			case <-ctx.Done():
				return nil
			case cat := <-doneCh:
				decrement(cat)
				continue
			case <-time.After(w.pollInterval):
				continue
			}
		}

		// Filter available event types based on provider rate limits
		var availableTypes []string
		for _, et := range eventTypes {
			if w.isEventTypeAvailable(et) {
				availableTypes = append(availableTypes, et)
			}
		}

		if len(availableTypes) == 0 {
			if w.rateLimiter != nil {
				earliest := w.rateLimiter.EarliestAvailable()
				waitDuration := time.Until(earliest)
				if waitDuration <= 0 {
					waitDuration = w.pollInterval
				}
				w.logger.WarnContext(
					ctx, "worker.all_providers_paused",
					slog.Duration("wait_duration", waitDuration),
					slog.Time("earliest_available", earliest),
				)
				select {
				case <-ctx.Done():
					return nil
				case cat := <-doneCh:
					decrement(cat)
				case <-time.After(waitDuration):
				}
			} else {
				select {
				case <-ctx.Done():
					return nil
				case cat := <-doneCh:
					decrement(cat)
				case <-time.After(w.pollInterval):
				}
			}
			continue
		}

		claimedTotal := 0
		categories := []jobCategory{catRetries, catUpdates, catSecondary, catPrimary}

		for step := 0; step < len(categories) && freeCapacity > 0; step++ {
			cat := categories[(lastCategoryIndex+step)%len(categories)]

			switch cat {
			case catRetries:
				allowed := min(freeCapacity, maxRetries-retriesInFlight)
				if allowed > 0 && len(availableTypes) > 0 {
					jobs, err := w.queue.ClaimMultiple(ctx, availableTypes, allowed, contract.ClaimModeRetriesOnly)
					if err != nil && ctx.Err() == nil {
						w.logger.ErrorContext(ctx, "worker.claim_retries_error", slog.Any("error", err))
					}
					for _, j := range jobs {
						retriesInFlight++
						freeCapacity--
						claimedTotal++
						select {
						case jobCh <- j:
						case <-ctx.Done():
							return nil
						}
					}
					if len(jobs) > 0 {
						lastCategoryIndex = (lastCategoryIndex + step + 1) % len(categories)
					}
				}

			case catUpdates:
				allowed := min(freeCapacity, maxUpdates-updatesInFlight)
				if allowed > 0 && contains(availableTypes, handler.EventCharacterCensus) {
					jobs, err := w.queue.Claim(ctx, handler.EventCharacterCensus, allowed, contract.ClaimModeNewOnly)
					if err != nil && ctx.Err() == nil {
						w.logger.ErrorContext(ctx, "worker.claim_updates_error", slog.Any("error", err))
					}
					for _, j := range jobs {
						updatesInFlight++
						freeCapacity--
						claimedTotal++
						select {
						case jobCh <- j:
						case <-ctx.Done():
							return nil
						}
					}
					if len(jobs) > 0 {
						lastCategoryIndex = (lastCategoryIndex + step + 1) % len(categories)
					}
				}

			case catSecondary:
				secondaryTypes := filterMatching(availableTypes, []string{
					handler.EventFreeCompanyCensus,
					handler.EventAchievementCensus,
				})
				allowed := min(freeCapacity, maxSecondary-secondaryInFlight)
				if allowed > 0 && len(secondaryTypes) > 0 {
					jobs, err := w.queue.ClaimMultiple(ctx, secondaryTypes, allowed, contract.ClaimModeNewOnly)
					if err != nil && ctx.Err() == nil {
						w.logger.ErrorContext(ctx, "worker.claim_secondary_error", slog.Any("error", err))
					}
					for _, j := range jobs {
						secondaryInFlight++
						freeCapacity--
						claimedTotal++
						select {
						case jobCh <- j:
						case <-ctx.Done():
							return nil
						}
					}
					if len(jobs) > 0 {
						lastCategoryIndex = (lastCategoryIndex + step + 1) % len(categories)
					}
				}

			case catPrimary:
				allowed := min(freeCapacity, maxPrimary-primaryInFlight)
				// Primary handles id-sweep and any other non-secondary/non-update types
				primaryTypes := filterNotMatching(availableTypes, []string{
					handler.EventCharacterCensus,
					handler.EventFreeCompanyCensus,
					handler.EventAchievementCensus,
				})
				if allowed > 0 && len(primaryTypes) > 0 {
					jobs, err := w.queue.ClaimMultiple(ctx, primaryTypes, allowed, contract.ClaimModeNewOnly)
					if err != nil && ctx.Err() == nil {
						w.logger.ErrorContext(ctx, "worker.claim_primary_error", slog.Any("error", err))
					}
					for _, j := range jobs {
						primaryInFlight++
						freeCapacity--
						claimedTotal++
						select {
						case jobCh <- j:
						case <-ctx.Done():
							return nil
						}
					}
					if len(jobs) > 0 {
						lastCategoryIndex = (lastCategoryIndex + step + 1) % len(categories)
					}
				}
			}
		}

		if claimedTotal == 0 {
			select {
			case <-ctx.Done():
				return nil
			case cat := <-doneCh:
				decrement(cat)
			case <-time.After(w.pollInterval):
			}
		}
	}
}

func (w *Worker) workerLoop(
	ctx context.Context,
	jobCh <-chan contract.QueueJob,
	doneCh chan<- jobCategory,
	workerID int,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobCh:
			if !ok {
				return
			}
			w.processJob(ctx, job, workerID)
			select {
			case doneCh <- classifyJob(job):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (w *Worker) processJob(ctx context.Context, job contract.QueueJob, workerID int) {
	h, ok := w.handlers.Get(job.Type)
	if !ok {
		w.logger.ErrorContext(ctx, "worker.missing_handler", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID))
		_ = w.queue.Fail(ctx, job.ID, fmt.Sprintf("no handler registered for event %s", job.Type))
		return
	}

	start := time.Now()
	w.logger.InfoContext(ctx, "worker.job_start", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts))
	var next []contract.QueueJob
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("worker panic: %v\nstack: %s", r, debug.Stack())
			}
		}()
		next, err = h.Handle(ctx, job.Payload)
	}()

	if err != nil {
		w.logger.WarnContext(ctx, "worker.job_retry", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Any("error", err))
		if rerr := w.queue.Retry(ctx, job.ID, err.Error()); rerr != nil {
			w.logger.ErrorContext(ctx, "worker.retry_error", slog.Int64("job_id", job.ID), slog.Any("error", rerr))
		}
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		return
	}

	w.logger.InfoContext(ctx, "worker.job_done", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Int("chained", len(next)))
	if err := w.queue.Complete(ctx, job.ID, next...); err != nil {
		w.logger.ErrorContext(ctx, "worker.complete_error", slog.Int64("job_id", job.ID), slog.Any("error", err))
	}
}

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

func filterMatching(slice []string, targets []string) []string {
	var out []string
	for _, s := range slice {
		if contains(targets, s) {
			out = append(out, s)
		}
	}
	return out
}

func filterNotMatching(slice []string, excluded []string) []string {
	var out []string
	for _, s := range slice {
		if !contains(excluded, s) {
			out = append(out, s)
		}
	}
	return out
}
