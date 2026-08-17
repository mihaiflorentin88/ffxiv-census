package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Fake is an in-memory Queue for tests. Publish/Claim/Complete/Retry/Fail
// mirror the SQLite adapter's observable semantics.
type Fake struct {
	mu   sync.Mutex
	jobs map[int64]contract.QueueJob
	next int64
}

func NewFake() *Fake {
	return &Fake{jobs: make(map[int64]contract.QueueJob)}
}

func (f *Fake) Publish(ctx context.Context, jobs ...contract.QueueJob) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inserted := f.publishLocked(jobs)
	return inserted, nil
}

// publishLocked inserts jobs, deduplicating on (type, payload_hash) to mirror
// the UNIQUE constraint. Like the SQLite adapter, the hash is derived from the
// payload (sha256), the status is forced to pending, and MaxAttempts defaults
// to 5 — callers never supply any of the three. Caller must hold f.mu.
func (f *Fake) publishLocked(jobs []contract.QueueJob) int {
	var inserted int
	now := time.Now().UTC()
	for _, j := range jobs {
		j.PayloadHash = payloadHash(j.Payload)
		j.Status = contract.QueueJobPending
		if j.MaxAttempts <= 0 {
			j.MaxAttempts = 5
		}
		if j.CreatedAt.IsZero() {
			j.CreatedAt = now
		}
		if j.RunAt.IsZero() {
			j.RunAt = now
		}
		dup := false
		for _, existing := range f.jobs {
			if existing.Type == j.Type && existing.PayloadHash == j.PayloadHash {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		f.next++
		j.ID = f.next
		f.jobs[j.ID] = j
		inserted++
	}
	return inserted
}

// payloadHash mirrors the SQLite adapter: sha256 of the raw payload, hex-encoded.
func payloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (f *Fake) Claim(ctx context.Context, jobType string, n int) ([]contract.QueueJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	var candidates []contract.QueueJob
	for _, j := range f.jobs {
		if j.Type == jobType && j.Status == contract.QueueJobPending && !j.RunAt.After(now) {
			candidates = append(candidates, j)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].RunAt.Equal(candidates[j].RunAt) {
			return candidates[i].RunAt.Before(candidates[j].RunAt)
		}
		return candidates[i].ID < candidates[j].ID
	})

	if len(candidates) > n {
		candidates = candidates[:n]
	}

	var out []contract.QueueJob
	for _, j := range candidates {
		j.Status = contract.QueueJobClaimed
		j.Attempts++
		claimedAt := now
		j.ClaimedAt = &claimedAt
		f.jobs[j.ID] = j
		out = append(out, j)
	}
	return out, nil
}

func (f *Fake) Complete(ctx context.Context, id int64, nextJobs ...contract.QueueJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	now := time.Now().UTC()
	j.Status = contract.QueueJobDone
	j.CompletedAt = &now
	f.jobs[id] = j
	f.publishLocked(nextJobs)
	return nil
}

func (f *Fake) Retry(ctx context.Context, id int64, lastErr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	if lastErr != "" {
		errCopy := lastErr
		j.LastError = &errCopy
	}
	now := time.Now().UTC()
	if j.Attempts >= j.MaxAttempts {
		j.Status = contract.QueueJobFailed
		j.FailedAt = &now
	} else {
		j.Status = contract.QueueJobPending
		j.ClaimedAt = nil
		backoff := 5 * time.Second
		if j.Attempts >= 2 {
			backoff *= time.Duration(1 << (j.Attempts - 1))
		}
		j.RunAt = now.Add(backoff)
	}
	f.jobs[id] = j
	return nil
}

func (f *Fake) Fail(ctx context.Context, id int64, lastErr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	if lastErr != "" {
		errCopy := lastErr
		j.LastError = &errCopy
	}
	now := time.Now().UTC()
	j.Status = contract.QueueJobFailed
	j.FailedAt = &now
	f.jobs[id] = j
	return nil
}

func (f *Fake) RetryFailed(ctx context.Context, jobType string, limit int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	var failedIDs []int64
	for id, j := range f.jobs {
		if j.Status == contract.QueueJobFailed && (jobType == "" || j.Type == jobType) {
			failedIDs = append(failedIDs, id)
		}
	}
	sort.Slice(failedIDs, func(i, j int) bool {
		return failedIDs[i] < failedIDs[j]
	})
	if len(failedIDs) > limit {
		failedIDs = failedIDs[:limit]
	}
	for _, id := range failedIDs {
		j := f.jobs[id]
		j.Status = contract.QueueJobPending
		j.FailedAt = nil
		j.Attempts = 0
		j.RunAt = time.Now().UTC()
		f.jobs[id] = j
	}
	return len(failedIDs), nil
}

func (f *Fake) PurgeJobs(ctx context.Context, status contract.QueueJobStatus, olderThan time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cutoff := time.Now().UTC().Add(-olderThan)
	var count int64
	for id, j := range f.jobs {
		if j.Status != status {
			continue
		}
		var ts time.Time
		if j.Status == contract.QueueJobDone && j.CompletedAt != nil {
			ts = *j.CompletedAt
		} else if j.Status == contract.QueueJobFailed && j.FailedAt != nil {
			ts = *j.FailedAt
		} else {
			ts = j.CreatedAt
		}
		if !ts.After(cutoff) {
			delete(f.jobs, id)
			count++
		}
	}
	return count, nil
}

func (f *Fake) GetEventDetails(ctx context.Context, sampleLimit int) ([]contract.QueueEventDetail, error) {
	if sampleLimit <= 0 {
		sampleLimit = 5
	}
	stats, err := f.StatsByType(ctx)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	details := make([]contract.QueueEventDetail, 0, len(stats))
	for _, s := range stats {
		d := contract.QueueEventDetail{
			Type:       s.Type,
			Pending:    s.Pending,
			Claimed:    s.Claimed,
			Done:       s.Done,
			Failed:     s.Failed,
			Total:      s.Total,
			ActiveJobs: []contract.QueueJob{},
			NextJobs:   []contract.QueueJob{},
			FailedJobs: []contract.QueueJob{},
		}

		var active, next, failed []contract.QueueJob
		for _, j := range f.jobs {
			if j.Type != s.Type {
				continue
			}
			switch j.Status {
			case contract.QueueJobClaimed:
				active = append(active, j)
			case contract.QueueJobPending:
				next = append(next, j)
			case contract.QueueJobFailed:
				failed = append(failed, j)
			}
		}

		sort.Slice(active, func(i, j int) bool {
			var ti, tj time.Time
			if active[i].ClaimedAt != nil {
				ti = *active[i].ClaimedAt
			}
			if active[j].ClaimedAt != nil {
				tj = *active[j].ClaimedAt
			}
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
			return active[i].ID < active[j].ID
		})

		sort.Slice(next, func(i, j int) bool {
			if !next[i].RunAt.Equal(next[j].RunAt) {
				return next[i].RunAt.Before(next[j].RunAt)
			}
			return next[i].ID < next[j].ID
		})

		sort.Slice(failed, func(i, j int) bool {
			var ti, tj time.Time
			if failed[i].FailedAt != nil {
				ti = *failed[i].FailedAt
			} else {
				ti = failed[i].CreatedAt
			}
			if failed[j].FailedAt != nil {
				tj = *failed[j].FailedAt
			} else {
				tj = failed[j].CreatedAt
			}
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
			return failed[i].ID > failed[j].ID
		})

		if len(active) > sampleLimit {
			active = active[:sampleLimit]
		}
		if len(next) > sampleLimit {
			next = next[:sampleLimit]
		}
		if len(failed) > sampleLimit {
			failed = failed[:sampleLimit]
		}

		if len(active) > 0 {
			d.ActiveJobs = active
		}
		if len(next) > 0 {
			d.NextJobs = next
		}
		if len(failed) > 0 {
			d.FailedJobs = failed
		}

		details = append(details, d)
	}
	return details, nil
}

func (f *Fake) Depth(ctx context.Context) (map[contract.QueueJobStatus]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[contract.QueueJobStatus]int{}
	for _, j := range f.jobs {
		out[j.Status]++
	}
	return out, nil
}

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

// ListJobs returns non-deleted queue jobs matching filter, ordered by id desc (newest first).
func (f *Fake) ListJobs(ctx context.Context, filter contract.QueueJobFilter, limit, offset int) ([]contract.QueueJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var filtered []contract.QueueJob
	for _, j := range f.jobs {
		if filter.Type != "" && j.Type != filter.Type {
			continue
		}
		if filter.Status != "" && j.Status != filter.Status {
			continue
		}
		filtered = append(filtered, j)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ID > filtered[j].ID
	})

	if offset < 0 {
		offset = 0
	}
	if offset > len(filtered) {
		return []contract.QueueJob{}, nil
	}
	filtered = filtered[offset:]
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	if filtered == nil {
		filtered = []contract.QueueJob{}
	}
	return filtered, nil
}

// CountJobs returns the number of queue jobs matching filter.
func (f *Fake) CountJobs(ctx context.Context, filter contract.QueueJobFilter) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	for _, j := range f.jobs {
		if filter.Type != "" && j.Type != filter.Type {
			continue
		}
		if filter.Status != "" && j.Status != filter.Status {
			continue
		}
		count++
	}
	return count, nil
}

// GetJob returns one queue job by id, or nil if not found.
func (f *Fake) GetJob(ctx context.Context, id int64) (*contract.QueueJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return nil, nil
	}
	jobCopy := j
	return &jobCopy, nil
}

// StatsByType returns aggregated job counts (pending, claimed, done, failed, total) grouped by event type.
func (f *Fake) StatsByType(ctx context.Context) ([]contract.QueueTypeStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	statsMap := map[string]*contract.QueueTypeStats{}
	for _, j := range f.jobs {
		s, ok := statsMap[j.Type]
		if !ok {
			s = &contract.QueueTypeStats{Type: j.Type}
			statsMap[j.Type] = s
		}
		s.Total++
		switch j.Status {
		case contract.QueueJobPending:
			s.Pending++
		case contract.QueueJobClaimed:
			s.Claimed++
		case contract.QueueJobDone:
			s.Done++
		case contract.QueueJobFailed:
			s.Failed++
		}
	}

	var stats []contract.QueueTypeStats
	for _, s := range statsMap {
		stats = append(stats, *s)
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Type < stats[j].Type
	})
	if stats == nil {
		stats = []contract.QueueTypeStats{}
	}
	return stats, nil
}
