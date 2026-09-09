package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// The scan worker leases due rows from the three scan queues and runs them
// inside a bounded executor. One dispatcher goroutine owns every scheduling
// counter — free capacity, per-queue in-flight work and the admission state
// — so no helper goroutine ever picks work on its own. Guard health gates
// claiming only; in-flight checks always run to completion.

const (
	// claimPauseFloor is the first pause after a store error; each further
	// error doubles it up to claimPauseCeiling. Pauses are context-aware:
	// the dispatcher keeps draining completions and stops on cancellation.
	claimPauseFloor = 100 * time.Millisecond
	claimPauseCeil  = time.Second
	// shutdownDrainBound bounds the wait for in-flight checks to deliver
	// their results after the parent context was cancelled.
	shutdownDrainBound = 5 * time.Second
	// shutdownCleanupBound bounds the fresh context that releases leases the
	// dispatcher still owns after the drain. A release whose persistence
	// fails is recovered by lease expiry, never retried in a loop.
	shutdownCleanupBound = 5 * time.Second
	// executorExitBound bounds the final wait for executor goroutines that
	// are not stuck inside a checker call.
	executorExitBound = time.Second
	// defaultStoreTimeout applies when the policy carries no usable lease
	// duration to derive store-call bounds from.
	defaultStoreTimeout = 5 * time.Second
)

// scanPollInterval bounds how often an idle dispatcher re-polls queues that
// returned no due rows, re-checks the guard and resumes after a store-error
// pause. Completions trigger admission passes immediately; the timer only
// guarantees progress while nothing completes. It is a variable so tests can
// tighten it.
var scanPollInterval = time.Second

// ScanWorker leases scan work through a ProxyScanStore, runs each check
// against a ProxyChecker and persists guard-fenced observations.
type ScanWorker struct {
	store   contract.ProxyScanStore
	checker contract.ProxyChecker
	guard   contract.ProxyEndpointGuard
	policy  contract.ProxyScanPolicy
	weights [3]int
	logger  contract.Logger

	mu       sync.Mutex
	notifier func()
}

// NewScanWorker creates a ScanWorker. The weights reserve long-run capacity
// shares for verification, recovery and background scanning; all three must
// be positive. The caller owns the guard lifecycle and must run Guard.Run
// for health observations to advance.
func NewScanWorker(store contract.ProxyScanStore, checker contract.ProxyChecker,
	guard contract.ProxyEndpointGuard, policy contract.ProxyScanPolicy,
	weights [3]int, logger contract.Logger,
) *ScanWorker {
	return &ScanWorker{
		store:   store,
		checker: checker,
		guard:   guard,
		policy:  policy,
		weights: weights,
		logger:  logger,
	}
}

// SetNotifier registers a callback invoked once per accepted successful
// completion, after the store accepted the observation. Used to wake waiting
// workers via ProxyHub.NotifyAvailable.
func (w *ScanWorker) SetNotifier(fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.notifier = fn
}

func (w *ScanWorker) fireNotifier() {
	w.mu.Lock()
	notify := w.notifier
	w.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// scanJob is one claimed row handed to the executor.
type scanJob struct {
	lease contract.ScanLease
	queue contract.ScanQueue
}

// scanResult reports one finished check back to the dispatcher.
type scanResult struct {
	lease      contract.ScanLease
	queue      contract.ScanQueue
	latency    int
	err        error
	startSnap  contract.GuardSnapshot
	finishSnap contract.GuardSnapshot
	cancelled  bool // the parent context ended while the check ran
	panicked   bool // the checker panicked; the lease is released, never classified
}

// dispatcher is the single owner of the scheduling state. All its fields are
// confined to the dispatcher goroutine: executors only touch the job and
// result channels.
type dispatcher struct {
	jobs      chan scanJob
	results   chan scanResult
	drainDone chan struct{}

	targets  [3]int // reserved capacity per queue; zeros in small mode
	small    bool   // concurrency below three: persistent round robin owns admission
	free     int
	inFlight [3]int
	adm      admission
	owned    map[int64]contract.ScanLease
}

func newDispatcher(concurrency int, targets [3]int) *dispatcher {
	return &dispatcher{
		jobs:      make(chan scanJob, concurrency),
		results:   make(chan scanResult, concurrency),
		drainDone: make(chan struct{}),
		targets:   targets,
		small:     concurrency < scanQueueCount,
		free:      concurrency,
		owned:     make(map[int64]contract.ScanLease, concurrency),
	}
}

// RunScan runs the dispatch loop until the context is cancelled and returns
// nil on cancellation. Concurrency must be positive; it bounds both the
// executor population and every claim. Store failures pause claiming
// briefly and are otherwise never fatal: the worker keeps its observations
// to itself rather than reclassifying proxies on a broken database.
func (w *ScanWorker) RunScan(ctx context.Context, concurrency int) error {
	if concurrency <= 0 {
		return fmt.Errorf("proxy scan: concurrency must be positive, got %d", concurrency)
	}
	for i, weight := range w.weights {
		if weight <= 0 {
			return fmt.Errorf("proxy scan: queue weight %d must be positive, got %d", i, weight)
		}
	}

	checkCtx, cancelChecks := context.WithCancel(ctx)
	defer cancelChecks()

	d := newDispatcher(concurrency, reservationTargets(concurrency, w.weights))
	var executors sync.WaitGroup
	executors.Add(concurrency)
	for range concurrency {
		go func() {
			defer executors.Done()
			w.execute(checkCtx, d)
		}()
	}

	w.dispatchLoop(ctx, d)

	// Shutdown: stop claims, cancel checks, wait a bounded time for network
	// completion, then release every lease we still own on a fresh bounded
	// context.
	close(d.jobs)
	cancelChecks()
	d.drain(w)
	d.releaseOutstanding(w)
	close(d.drainDone)

	exited := make(chan struct{})
	go func() {
		executors.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(executorExitBound):
		// A check stuck inside the checker beyond the drain bound leaves its
		// executor parked on drainDone; RunScan still returns on time.
	}
	return nil
}

// execute is the fixed executor body: one goroutine per concurrency slot,
// reused across jobs, with no per-result goroutine accumulation.
func (w *ScanWorker) execute(ctx context.Context, d *dispatcher) {
	for job := range d.jobs {
		res := w.runCheck(ctx, job)
		select {
		case d.results <- res:
		case <-d.drainDone:
			return
		}
	}
}

// runCheck performs the network attempt. A panic in the checker is recovered
// and reported as a local error so one bad record can neither take down the
// loop nor mark the proxy dead.
func (w *ScanWorker) runCheck(ctx context.Context, job scanJob) (res scanResult) {
	res = scanResult{lease: job.lease, queue: job.queue}
	defer func() {
		if recovered := recover(); recovered != nil {
			res.panicked = true
			res.err = nil
			w.logger.ErrorContext(ctx, "proxy_scan.panic_recovered",
				"proxy_id", job.lease.Record.ID,
				"panic", fmt.Sprint(recovered))
		}
	}()
	res.startSnap = w.guard.Snapshot()
	res.latency, res.err = w.checker.Check(ctx, job.lease.Record.Protocol, job.lease.Record.IP, job.lease.Record.Port)
	res.finishSnap = w.guard.Snapshot()
	res.cancelled = ctx.Err() != nil
	return res
}

// claimPause is the brief context-aware backoff after store errors. A paused
// dispatcher keeps draining completions; the poll timer ends the pause.
type claimPause struct {
	until   time.Time
	backoff time.Duration
}

func (p *claimPause) active() bool { return time.Now().Before(p.until) }

func (p *claimPause) engage() {
	if p.backoff <= 0 {
		p.backoff = claimPauseFloor
	} else {
		p.backoff *= 2
		if p.backoff > claimPauseCeil {
			p.backoff = claimPauseCeil
		}
	}
	p.until = time.Now().Add(p.backoff)
}

func (p *claimPause) lift() {
	p.backoff = 0
	p.until = time.Time{}
}

func (w *ScanWorker) dispatchLoop(ctx context.Context, d *dispatcher) {
	ticker := time.NewTicker(scanPollInterval)
	defer ticker.Stop()
	var pause claimPause
	for ctx.Err() == nil {
		d.handlePending(w, &pause)
		w.admit(ctx, d, &pause)
		select {
		case <-ctx.Done():
			return
		case res := <-d.results:
			d.handle(w, res, &pause)
		case <-ticker.C:
		}
	}
}

// handlePending processes completions that arrived without blocking.
func (d *dispatcher) handlePending(w *ScanWorker, pause *claimPause) {
	for {
		select {
		case res := <-d.results:
			d.handle(w, res, pause)
		default:
			return
		}
	}
}

// admit runs one admission pass: fill under-reservation queues with due rows
// first, then lend the remaining free capacity by round-robin borrowing. An
// empty queue never blocks the others; its re-poll is bounded by the poll
// timer and triggered early by every completion. The guard gates claiming
// only — in-flight checks continue unaffected.
func (w *ScanWorker) admit(ctx context.Context, d *dispatcher, pause *claimPause) {
	if d.free <= 0 || pause.active() {
		return
	}
	if snap := w.guard.Snapshot(); !snap.Healthy {
		return
	}
	empty := [3]bool{}
	w.fillReservations(ctx, d, pause, &empty)
	w.borrow(ctx, d, pause, &empty)
}

// fillReservations tops up queues whose in-flight work is below their
// reservation, verification first.
func (w *ScanWorker) fillReservations(ctx context.Context, d *dispatcher, pause *claimPause, empty *[3]bool) {
	for queue := range d.targets {
		deficit := d.targets[queue] - d.inFlight[queue]
		if deficit <= 0 || d.free <= 0 {
			continue
		}
		if limit := min(deficit, d.free); limit > 0 {
			if !w.claimDue(ctx, d, pause, contract.ScanQueue(queue), limit, empty) {
				return
			}
		}
	}
}

// borrow lends free capacity beyond the reservations. At concurrency below
// three the persistent small-worker round robin owns every admission;
// otherwise a round-robin cursor rotates one claim at a time across the
// queues that still returned rows in this pass.
func (w *ScanWorker) borrow(ctx context.Context, d *dispatcher, pause *claimPause, empty *[3]bool) {
	for d.free > 0 {
		if d.small {
			available := [3]bool{}
			for i := range empty {
				available[i] = !empty[i]
			}
			queue, ok := d.adm.chooseSmall(available, w.weights)
			if !ok {
				return
			}
			if !w.claimDue(ctx, d, pause, contract.ScanQueue(queue), 1, empty) {
				return
			}
			continue
		}
		advanced := false
		for step := 0; step < scanQueueCount && d.free > 0; step++ {
			queue := (d.adm.borrower + 1 + step) % scanQueueCount
			if empty[queue] {
				continue
			}
			if !w.claimDue(ctx, d, pause, contract.ScanQueue(queue), 1, empty) {
				return
			}
			d.adm.borrower = queue
			advanced = true
			break
		}
		if !advanced {
			return
		}
	}
}

// claimDue leases up to limit due rows from one queue and starts them
// immediately: claimed rows count against free capacity and are handed to
// the executor channel without buffering beyond concurrency. The claim is
// bounded by the caller's context and a timeout shorter than the lease. It
// reports whether the store interaction succeeded; a failure engages the
// pause and aborts the pass.
func (w *ScanWorker) claimDue(ctx context.Context, d *dispatcher, pause *claimPause,
	queue contract.ScanQueue, limit int, empty *[3]bool,
) bool {
	claimCtx, cancel := context.WithTimeout(ctx, w.claimTimeout())
	leases, err := w.store.ClaimScans(claimCtx, queue, limit)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "proxy_scan.claim_error",
				"queue", int(queue), "error", err)
			pause.engage()
		}
		return false
	}
	pause.lift()
	if len(leases) == 0 {
		empty[queue] = true
		return true
	}
	for _, lease := range leases {
		d.inFlight[queue]++
		d.free--
		d.owned[lease.Record.ID] = lease
		d.jobs <- scanJob{lease: lease, queue: queue}
	}
	return true
}

// handle returns one finished check to the scheduler: capacity is freed
// immediately, then the outcome is persisted, released or discarded.
func (d *dispatcher) handle(w *ScanWorker, res scanResult, pause *claimPause) {
	d.inFlight[res.queue]--
	d.free++
	delete(d.owned, res.lease.Record.ID)
	switch {
	case res.panicked:
		w.releaseLease(res.lease)
	case res.cancelled:
		// The parent context ended mid-check: release ownership and never
		// claim a network failure for a cancelled attempt.
		w.releaseLease(res.lease)
	default:
		w.finish(res, pause)
	}
}

// finish attributes one completed attempt and persists it.
//
// The guard is re-checked immediately before persistence: a generation
// change anywhere between the attempt's start snapshot and this latest local
// observation fences conclusive failure attribution. This fences on local
// observation only — it is deliberately not globally synchronized outage
// detection. A guard change landing in the small window between the finish
// snapshot and this recheck downgrades the attempt exactly like one landing
// during the check itself; two replicas may briefly attribute the same
// endpoint differently, and the conservative outcome (inconclusive) never
// increments failure history.
func (w *ScanWorker) finish(res scanResult, pause *claimPause) {
	effectiveFinish := res.finishSnap
	if recheck := w.guard.Snapshot(); recheck.Generation != res.startSnap.Generation {
		effectiveFinish = recheck
	}
	update := proxy.MakeScanUpdate(w.policy, res.lease.Record, res.latency, res.err, res.startSnap, effectiveFinish)

	completeCtx, cancel := context.WithTimeout(context.Background(), w.storeTimeout())
	accepted, err := w.store.CompleteScan(completeCtx, res.lease, update)
	cancel()
	if err != nil {
		// The write failed; the lease expires and the row is retried later.
		// Never fabricate a completed attempt from a failed write.
		w.logger.ErrorContext(context.Background(), "proxy_scan.complete_error",
			"proxy_id", res.lease.Record.ID, "error", err)
		pause.engage()
		return
	}
	pause.lift()
	if !accepted {
		// Fenced or discarded: the observation lost its lease in the
		// meantime. Nothing to persist, nobody to notify.
		w.logger.DebugContext(context.Background(), "proxy_scan.completion_discarded",
			"proxy_id", res.lease.Record.ID)
		return
	}
	if update.Outcome == contract.ScanSuccess {
		w.fireNotifier()
	}
}

// releaseLease hands one owned lease back on a fresh bounded context so it
// also works after the parent context was cancelled.
func (w *ScanWorker) releaseLease(lease contract.ScanLease) {
	ctx, cancel := context.WithTimeout(context.Background(), w.storeTimeout())
	defer cancel()
	if err := w.store.ReleaseScan(ctx, lease); err != nil {
		w.logger.ErrorContext(ctx, "proxy_scan.release_error",
			"proxy_id", lease.Record.ID, "error", err)
	}
}

// drain waits a bounded time for in-flight checks to deliver their results
// after cancellation and processes them like normal completions.
func (d *dispatcher) drain(w *ScanWorker) {
	var pause claimPause
	timer := time.NewTimer(shutdownDrainBound)
	defer timer.Stop()
	for len(d.owned) > 0 {
		select {
		case res := <-d.results:
			d.handle(w, res, &pause)
		case <-timer.C:
			w.logger.WarnContext(context.Background(), "proxy_scan.shutdown_incomplete",
				"outstanding", len(d.owned))
			return
		}
	}
}

// releaseOutstanding releases every lease the dispatcher still owns after
// the drain, bounded by one fresh cleanup context. Failures are logged once;
// lease expiry recovers the rows.
func (d *dispatcher) releaseOutstanding(w *ScanWorker) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownCleanupBound)
	defer cancel()
	for id, lease := range d.owned {
		if err := w.store.ReleaseScan(ctx, lease); err != nil {
			w.logger.ErrorContext(ctx, "proxy_scan.release_error",
				"proxy_id", id, "error", err)
		}
	}
}

// claimTimeout bounds each claim RPC to half the lease so a claim that
// finishes still leaves its holder time to complete the check.
func (w *ScanWorker) claimTimeout() time.Duration {
	if w.policy.LeaseDuration <= 0 {
		return defaultStoreTimeout
	}
	return w.policy.LeaseDuration / 2
}

// storeTimeout bounds persistence calls well inside the lease so a timed-out
// write still leaves the lease-expiry path room to recover the row.
func (w *ScanWorker) storeTimeout() time.Duration {
	limit := defaultStoreTimeout
	if w.policy.LeaseDuration > 0 {
		limit = min(w.policy.LeaseDuration/2, 2*time.Second)
	}
	if limit < 250*time.Millisecond {
		limit = 250 * time.Millisecond
	}
	return limit
}
