package repository

import (
	"strings"
	"testing"
)

func TestSummaryCountsQueryDoesNotReadCharacterJobs(t *testing.T) {
	lower := strings.ToLower(summaryCountsQuery)
	if strings.Contains(lower, "character_jobs") {
		t.Fatalf("summary query still reads character_jobs: %s", summaryCountsQuery)
	}
	if !strings.Contains(lower, "max_job_level") {
		t.Fatalf("summary query does not use max_job_level: %s", summaryCountsQuery)
	}
}
