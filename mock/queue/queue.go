package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

func (f *Fake) Publish(ctx context.Context, jobs ...contract.QueueJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishLocked(jobs)
	return nil
}

// publishLocked inserts jobs, deduplicating on (type, payload_hash) to mirror
// the UNIQUE constraint. Like the SQLite adapter, the hash is derived from the
// payload (sha256), the status is forced to pending, and MaxAttempts defaults
// to 5 — callers never supply any of the three. Caller must hold f.mu.
func (f *Fake) publishLocked(jobs []contract.QueueJob) {
	for _, j := range jobs {
		j.PayloadHash = payloadHash(j.Payload)
		j.Status = contract.QueueJobPending
		if j.MaxAttempts <= 0 {
			j.MaxAttempts = 5
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
	}
}

// payloadHash mirrors the SQLite adapter: sha256 of the raw payload, hex-encoded.
func payloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (f *Fake) Claim(ctx context.Context, jobType string, n int) ([]contract.QueueJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	var out []contract.QueueJob
	for _, j := range f.jobs {
		if j.Type == jobType && j.Status == contract.QueueJobPending && !j.RunAt.After(now) {
			j.Status = contract.QueueJobClaimed
			j.Attempts++
			f.jobs[j.ID] = j
			out = append(out, j)
			if len(out) == n {
				break
			}
		}
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
	j.Status = contract.QueueJobDone
	f.jobs[id] = j
	f.publishLocked(nextJobs)
	return nil
}

func (f *Fake) Retry(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	if j.Attempts >= j.MaxAttempts {
		j.Status = contract.QueueJobFailed
	} else {
		j.Status = contract.QueueJobPending
	}
	f.jobs[id] = j
	return nil
}

func (f *Fake) Fail(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	j.Status = contract.QueueJobFailed
	f.jobs[id] = j
	return nil
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
