package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// consumerPolicy mirrors the production [proxy.scan] defaults the consumer
// paths need: failure classification thresholds and recovery scheduling.
var consumerPolicy = contract.ProxyScanPolicy{
	VerifyInterval:    time.Minute,
	FreshnessTTL:      2 * time.Minute,
	RecoveryBase:      time.Minute,
	RecoveryCap:       15 * time.Minute,
	RecoveryHorizon:   time.Hour,
	LeaseDuration:     30 * time.Second,
	MinRepeatInterval: time.Second,
	InconclusiveRetry: time.Minute,
	DeadAfter:         48 * time.Hour,
	FailThreshold:     3,
}

// newClockRepo returns a fake repository on a controllable clock so freshness
// boundaries are deterministic.
func newClockRepo() (*repository.FakeProxyRepository, *time.Time) {
	repo := repository.NewFakeProxyRepository(consumerPolicy)
	clock := time.Now().UTC().Truncate(time.Second)
	repo.Now = func() time.Time { return clock }
	return repo, &clock
}

// seedVerificationSuccess drives one verification-queue success through the
// store pipeline, the way a scanner revalidates an already-healthy row.
func seedVerificationSuccess(t *testing.T, repo *repository.FakeProxyRepository, id int64) bool {
	t.Helper()
	leases, err := repo.ClaimScans(context.Background(), contract.ScanVerification, 10)
	if err != nil {
		t.Fatalf("ClaimScans: %v", err)
	}
	for _, lease := range leases {
		if lease.Record.ID != id {
			if err := repo.ReleaseScan(context.Background(), lease); err != nil {
				t.Fatalf("ReleaseScan: %v", err)
			}
			continue
		}
		ok, err := repo.CompleteScan(context.Background(), lease, contract.ScanUpdate{
			Outcome: contract.ScanSuccess, LatencyMS: 80,
		})
		return ok && err == nil
	}
	return false
}

func TestProxy_CanUse_ActiveAndOwned(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: clock,
		LockedBy:       &owner,
		LockedAt:       clock,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected CanUse to return true for fresh proxy owned by caller")
	}
	if p.Record().ObservationVersion != rec.ObservationVersion {
		t.Fatalf("expected captured record version %d, got %d", rec.ObservationVersion, p.Record().ObservationVersion)
	}
}

func TestProxy_CanUse_StaleEvidenceRejected(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	// Verified 3 minutes ago — outside the 2-minute freshness TTL.
	stale := clock.Add(-3 * time.Minute)
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: &stale,
		LockedBy:       &owner,
		LockedAt:       clock,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for stale general evidence")
	}
}

func TestProxy_CanUse_Unlocked(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: clock,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for unlocked proxy")
	}
}

func TestProxy_CanUse_ExpiredLock(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	// Lock was acquired 10 minutes ago — exceeds the 5-minute TTL.
	expiredTime := clock.Add(-10 * time.Minute)
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: clock,
		LockedBy:       &owner,
		LockedAt:       &expiredTime,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for expired lock")
	}
}

func TestProxy_CanUse_ExtendsLock(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	// Lock was acquired 4 minutes ago — within the 5-minute TTL but close to expiry.
	oldTime := clock.Add(-4 * time.Minute)
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: clock,
		LockedBy:       &owner,
		LockedAt:       &oldTime,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected CanUse to return true for proxy within TTL")
	}
	// Verify the lock time was extended to the current clock tick.
	current, err := repo.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if current.LockedAt == nil || !current.LockedAt.Equal(*clock) {
		t.Fatalf("expected lock extended to %v, got %v", *clock, current.LockedAt)
	}
	if p.LockedAt() == nil || !p.LockedAt().Equal(*clock) {
		t.Fatalf("expected proxy to store the refreshed record with lock at %v, got %v", *clock, p.LockedAt())
	}
}

func TestProxy_CanUse_WrongOwner(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	other := "test-g2"
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: clock,
		LockedBy:       &owner,
		LockedAt:       clock,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	ok, err := p.CanUse(context.Background(), other, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for wrong owner")
	}
}

func TestProxy_CanUse_DestinationCooldownBlocks(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	cooldown := clock.Add(time.Minute)
	rec := contract.ProxyRecord{
		ID:                       1,
		Protocol:                 "http",
		IP:                       "1.2.3.4",
		Port:                     8080,
		GeneralHealthy:           true,
		LastVerifiedAt:           clock,
		LockedBy:                 &owner,
		LockedAt:                 clock,
		DestinationCooldownUntil: &cooldown,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false while a destination cooldown is live")
	}
}

func TestProxy_Release(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: clock,
		LockedBy:       &owner,
		LockedAt:       clock,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	if err := p.Release(context.Background(), owner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.LockedBy() != nil {
		t.Fatal("expected LockedBy to be nil after release")
	}
	if p.LockedAt() != nil {
		t.Fatal("expected LockedAt to be nil after release")
	}
}

func TestProxy_MarkFailed_AppliesFailureTransition(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: clock,
		LockedBy:       &owner,
		LockedAt:       clock,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	accepted, err := p.MarkFailed(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !accepted {
		t.Fatal("expected consumer failure to be accepted on the captured version")
	}

	current, err := repo.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if current.GeneralHealthy {
		t.Error("expected general health to be cleared")
	}
	if current.Status != contract.ProxyStatusInactive {
		t.Errorf("expected status inactive, got %s", current.Status)
	}
	if current.FailCount != 1 {
		t.Errorf("expected fail_count 1, got %d", current.FailCount)
	}
	if current.RecoveryStep != 1 {
		t.Errorf("expected recovery step 1, got %d", current.RecoveryStep)
	}
	if current.ObservationVersion != rec.ObservationVersion+1 {
		t.Errorf("expected observation version %d, got %d", rec.ObservationVersion+1, current.ObservationVersion)
	}
	if current.ScanToken != nil || current.ScanLeaseUntil != nil || current.ClaimedVersion != nil {
		t.Error("expected older scan lease to be invalidated")
	}
}

func TestConsumerFailure_StaleConsumerFailureRejectedAfterScannerSuccess(t *testing.T) {
	repo, _ := newClockRepo()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Source:   "test",
	}
	repo.InsertIfAbsent(context.Background(), rec)
	if !repo.SeedSuccess(1, 50) {
		t.Fatal("failed to seed proxy evidence")
	}

	claim, err := repo.ClaimProxy(context.Background(), owner, 5*time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimProxy: proxy=%v err=%v", claim, err)
	}
	p := New(claim, repo, consumerPolicy)

	// A scanner completes a new success while the consumer job runs.
	if !seedVerificationSuccess(t, repo, 1) {
		t.Fatal("failed to seed scanner success during the consumer job")
	}

	// The consumer's failure carries the pre-scan version — it must be
	// rejected and must not overwrite the newer evidence.
	accepted, err := p.MarkFailed(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accepted {
		t.Fatal("expected stale consumer failure to be rejected")
	}

	current, err := repo.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if !current.GeneralHealthy {
		t.Error("expected scanner success evidence to be preserved")
	}
	if current.FailCount != 0 {
		t.Errorf("expected fail_count 0, got %d", current.FailCount)
	}
	if current.ObservationVersion != claim.ObservationVersion+1 {
		t.Errorf("expected version %d (scanner success only), got %d", claim.ObservationVersion+1, current.ObservationVersion)
	}
	if current.LockedBy != nil {
		t.Errorf("expected stale consumer lock released, still locked by %v", *current.LockedBy)
	}
}

func TestConsumerFailure_FencesOlderScanCompletion(t *testing.T) {
	repo, _ := newClockRepo()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID: 1, Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	}
	repo.InsertIfAbsent(context.Background(), rec)
	if !repo.SeedSuccess(1, 50) {
		t.Fatal("failed to seed proxy evidence")
	}

	// A scanner leases the row before the consumer observes a failure.
	leases, err := repo.ClaimScans(context.Background(), contract.ScanVerification, 10)
	if err != nil {
		t.Fatalf("ClaimScans: %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("expected 1 scan lease, got %d", len(leases))
	}
	scanVersion := leases[0].Version

	// The consumer claims and records a conclusive failure on the same version.
	claim, err := repo.ClaimProxy(context.Background(), owner, 5*time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimProxy: proxy=%v err=%v", claim, err)
	}
	if claim.ObservationVersion != scanVersion {
		t.Fatalf("expected consumer claim on version %d, got %d", scanVersion, claim.ObservationVersion)
	}
	p := New(claim, repo, consumerPolicy)
	accepted, err := p.MarkFailed(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if !accepted {
		t.Fatal("expected consumer failure to be accepted")
	}

	// The scanner's older completion must be fenced out by the version bump.
	completed, err := repo.CompleteScan(context.Background(), leases[0], contract.ScanUpdate{Outcome: contract.ScanSuccess})
	if err != nil {
		t.Fatalf("CompleteScan: %v", err)
	}
	if completed {
		t.Fatal("expected older scanner completion to be rejected after consumer failure")
	}

	current, err := repo.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if current.GeneralHealthy || current.FailCount != 1 {
		t.Errorf("expected consumer failure evidence preserved, got healthy=%v fail_count=%d", current.GeneralHealthy, current.FailCount)
	}
}

func TestProxy_CooldownDestination_SetsCooldownAndReleasesLock(t *testing.T) {
	repo, clock := newClockRepo()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID:             1,
		Protocol:       "http",
		IP:             "1.2.3.4",
		Port:           8080,
		GeneralHealthy: true,
		LastVerifiedAt: clock,
		LockedBy:       &owner,
		LockedAt:       clock,
	}
	repo.InsertIfAbsent(context.Background(), rec)

	p := New(&rec, repo, consumerPolicy)
	if err := p.CooldownDestination(context.Background(), owner, 5*time.Minute, 90*time.Second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	current, err := repo.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if current.DestinationCooldownUntil == nil || !current.DestinationCooldownUntil.Equal(clock.Add(90*time.Second)) {
		t.Fatalf("expected destination cooldown until %v, got %v", clock.Add(90*time.Second), current.DestinationCooldownUntil)
	}
	if current.LockedBy != nil {
		t.Errorf("expected lock released by destination cooldown, still locked by %v", *current.LockedBy)
	}
	if !current.GeneralHealthy {
		t.Error("expected general evidence untouched by destination cooldown")
	}
	if current.ObservationVersion != rec.ObservationVersion {
		t.Errorf("expected observation version unchanged at %d, got %d", rec.ObservationVersion, current.ObservationVersion)
	}
}

func TestProxy_Getters(t *testing.T) {
	latency := 100
	rec := contract.ProxyRecord{
		ID:        42,
		Protocol:  "socks5",
		IP:        "10.0.0.1",
		Port:      1080,
		LatencyMS: &latency,
		Status:    contract.ProxyStatusActive,
	}
	p := New(&rec, nil, consumerPolicy)

	if p.ID() != 42 {
		t.Fatalf("expected ID 42, got %d", p.ID())
	}
	if p.Protocol() != "socks5" {
		t.Fatalf("expected protocol socks5, got %s", p.Protocol())
	}
	if p.IP() != "10.0.0.1" {
		t.Fatalf("expected IP 10.0.0.1, got %s", p.IP())
	}
	if p.Port() != 1080 {
		t.Fatalf("expected port 1080, got %d", p.Port())
	}
	if p.Address() != "socks5://10.0.0.1:1080" {
		t.Fatalf("expected address socks5://10.0.0.1:1080, got %s", p.Address())
	}
	if p.LatencyMS() == nil || *p.LatencyMS() != 100 {
		t.Fatal("expected latency 100")
	}
}
