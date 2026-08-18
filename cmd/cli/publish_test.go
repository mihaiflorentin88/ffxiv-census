package cli

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
)

func TestComputeIDSweepRange_AutoDiscovery(t *testing.T) {
	maxIDFunc := func() (uint32, error) {
		return 500, nil
	}

	from, to, err := computeIDSweepRange(0, 0, 100, false, maxIDFunc)
	if err != nil {
		t.Fatalf("computeIDSweepRange: %v", err)
	}
	if from != 501 || to != 600 {
		t.Errorf("got [%d, %d], want [501, 600]", from, to)
	}

	// Explicit auto flag overrides explicit from/to
	from, to, err = computeIDSweepRange(10, 20, 100, true, maxIDFunc)
	if err != nil {
		t.Fatalf("computeIDSweepRange with auto=true: %v", err)
	}
	if from != 501 || to != 600 {
		t.Errorf("got [%d, %d], want [501, 600] with auto=true", from, to)
	}
}

func TestComputeIDSweepRange_AutoDiscovery_EmptyDB(t *testing.T) {
	maxIDFunc := func() (uint32, error) {
		return 0, nil
	}

	from, to, err := computeIDSweepRange(0, 0, 1000, false, maxIDFunc)
	if err != nil {
		t.Fatalf("computeIDSweepRange: %v", err)
	}
	if from != 1 || to != 1000 {
		t.Errorf("got [%d, %d], want [1, 1000]", from, to)
	}
}

func TestComputeIDSweepRange_FromOnly(t *testing.T) {
	from, to, err := computeIDSweepRange(100, 0, 50, false, nil)
	if err != nil {
		t.Fatalf("computeIDSweepRange: %v", err)
	}
	if from != 100 || to != 149 {
		t.Errorf("got [%d, %d], want [100, 149]", from, to)
	}
}

func TestComputeIDSweepRange_ToOnly(t *testing.T) {
	from, to, err := computeIDSweepRange(0, 50, 1000, false, nil)
	if err != nil {
		t.Fatalf("computeIDSweepRange: %v", err)
	}
	if from != 1 || to != 50 {
		t.Errorf("got [%d, %d], want [1, 50]", from, to)
	}
}

func TestComputeIDSweepRange_ExplicitBoth(t *testing.T) {
	from, to, err := computeIDSweepRange(10, 20, 1000, false, nil)
	if err != nil {
		t.Fatalf("computeIDSweepRange: %v", err)
	}
	if from != 10 || to != 20 {
		t.Errorf("got [%d, %d], want [10, 20]", from, to)
	}
}

func TestComputeIDSweepRange_Invalid(t *testing.T) {
	if _, _, err := computeIDSweepRange(20, 10, 1000, false, nil); err == nil {
		t.Fatal("expected error for from > to")
	}

	if _, _, err := computeIDSweepRange(0, 0, 0, false, nil); err == nil {
		t.Fatal("expected error for count = 0")
	}

	errMaxID := errors.New("db error")
	if _, _, err := computeIDSweepRange(0, 0, 100, false, func() (uint32, error) { return 0, errMaxID }); !errors.Is(err, errMaxID) {
		t.Fatalf("expected db error, got %v", err)
	}
}

func TestBuildIDSweepJobs(t *testing.T) {
	jobs := buildIDSweepJobs(1, 250, 100, "tomestone")
	if len(jobs) != 3 {
		t.Fatalf("jobs len = %d, want 3", len(jobs))
	}

	expectedRanges := [][2]uint32{
		{1, 100},
		{101, 200},
		{201, 250},
	}

	for i, j := range jobs {
		if j.Type != handler.EventIDSweep {
			t.Errorf("job[%d] type = %q, want %q", i, j.Type, handler.EventIDSweep)
		}
		var p handler.IDSweepPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			t.Fatalf("job[%d] unmarshal: %v", i, err)
		}
		if p.From != expectedRanges[i][0] || p.To != expectedRanges[i][1] {
			t.Errorf("job[%d] range = [%d, %d], want [%d, %d]", i, p.From, p.To, expectedRanges[i][0], expectedRanges[i][1])
		}
		if p.Source != "tomestone" {
			t.Errorf("job[%d] source = %q, want tomestone", i, p.Source)
		}
		if j.MaxAttempts != -1 {
			t.Errorf("job[%d] MaxAttempts = %d, want -1 (infinite retry)", i, j.MaxAttempts)
		}
	}
}

func TestPublishIDSweepCmd_FlagsRegistered(t *testing.T) {
	flags := publishIDSweepCmd.Flags()
	for _, name := range []string{
		"auto", "batch-size", "from", "to", "count", "chunk-size", "source",
		"fill-gaps", "daemon", "daemon-interval", "min-pending-jobs", "max-gaps",
	} {
		if flags.Lookup(name) == nil {
			t.Errorf("flag --%s not registered on publish id-sweep", name)
		}
	}
}

func TestBuildGapSweepJobs(t *testing.T) {
	gaps := [][2]uint32{
		{1, 50},
		{100, 250},
	}
	jobs := buildGapSweepJobs(gaps, 100, "auto")
	// Gap 1: [1, 50] -> 1 job
	// Gap 2: [100, 250] -> 2 jobs ([100, 199], [200, 250])
	// Total: 3 jobs
	if len(jobs) != 3 {
		t.Fatalf("jobs len = %d, want 3", len(jobs))
	}

	expected := [][2]uint32{
		{1, 50},
		{100, 199},
		{200, 250},
	}
	for i, j := range jobs {
		var p handler.IDSweepPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			t.Fatalf("job[%d] unmarshal: %v", i, err)
		}
		if p.From != expected[i][0] || p.To != expected[i][1] {
			t.Errorf("job[%d] = [%d, %d], want [%d, %d]", i, p.From, p.To, expected[i][0], expected[i][1])
		}
		if p.Source != "auto" {
			t.Errorf("job[%d] source = %q, want auto", i, p.Source)
		}
	}
}

func TestPublishIDSweep_AutoAndGapBatchGeneration(t *testing.T) {
	// Test Auto-Range generation for CronJob
	maxID := uint32(124000)
	maxIDFunc := func() (uint32, error) {
		return maxID, nil
	}

	from, to, err := computeIDSweepRange(0, 0, 1000, true, maxIDFunc)
	if err != nil {
		t.Fatalf("computeIDSweepRange auto failed: %v", err)
	}
	if from != 124001 || to != 125000 {
		t.Fatalf("expected range [124001, 125000], got [%d, %d]", from, to)
	}

	jobs := buildIDSweepJobs(from, to, 100, "auto")
	if len(jobs) != 10 {
		t.Fatalf("expected 10 jobs for 1000 IDs with chunk size 100, got %d", len(jobs))
	}

	for idx, j := range jobs {
		if j.MaxAttempts != -1 {
			t.Errorf("job %d max attempts = %d, want -1", idx, j.MaxAttempts)
		}
		var p handler.IDSweepPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			t.Fatalf("job %d payload unmarshal error: %v", idx, err)
		}
		expectedFrom := 124001 + uint32(idx*100)
		expectedTo := expectedFrom + 99
		if p.From != expectedFrom || p.To != expectedTo {
			t.Errorf("job %d range = [%d, %d], want [%d, %d]", idx, p.From, p.To, expectedFrom, expectedTo)
		}
	}

	// Test Gap-fill generation
	gaps := [][2]uint32{
		{50, 150},  // 101 IDs -> 2 chunks: [50, 149], [150, 150]
		{500, 550}, // 51 IDs -> 1 chunk: [500, 550]
	}
	gapJobs := buildGapSweepJobs(gaps, 100, "lodestone")
	if len(gapJobs) != 3 {
		t.Fatalf("expected 3 gap jobs, got %d", len(gapJobs))
	}
}

func TestPublishFCCensusCmd_FlagsRegistered(t *testing.T) {
	flags := publishFCCensusCmd.Flags()
	if flags.Lookup("fc-id") == nil {
		t.Errorf("flag --fc-id not registered on publish fc-census")
	}
}
