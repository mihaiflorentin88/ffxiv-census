package k8s

import (
	"os"
	"strings"
	"testing"
)

func TestUIStatsRefreshRunsHourly(t *testing.T) {
	values, err := os.ReadFile("values.yaml")
	if err != nil {
		t.Fatalf("read Helm values: %v", err)
	}

	const want = "- name: refresh-ui-stats\n      schedule: \"17 * * * *\""
	if !strings.Contains(string(values), want) {
		t.Fatalf("refresh-ui-stats CronJob is not scheduled hourly at minute 17")
	}
}
