package cli

import (
	"testing"
)

func TestConsumeCmd_FlagsAndArgs(t *testing.T) {
	cmd := consumeCmd

	if cmd.Use != "consume [event]" {
		t.Fatalf("unexpected Use: %s", cmd.Use)
	}

	eventsFlag := cmd.Flags().Lookup("events")
	if eventsFlag == nil {
		t.Fatal("expected --events flag to be present")
	}
	if eventsFlag.DefValue != "all" {
		t.Fatalf("expected --events default to be 'all', got %s", eventsFlag.DefValue)
	}

	concurrencyFlag := cmd.Flags().Lookup("concurrency")
	if concurrencyFlag == nil {
		t.Fatal("expected --concurrency flag to be present")
	}
	if concurrencyFlag.DefValue != "4" {
		t.Fatalf("expected --concurrency default to be '4', got %s", concurrencyFlag.DefValue)
	}

	pollIntervalFlag := cmd.Flags().Lookup("poll-interval")
	if pollIntervalFlag == nil {
		t.Fatal("expected --poll-interval flag to be present")
	}
	if pollIntervalFlag.DefValue != "500ms" {
		t.Fatalf("expected --poll-interval default to be '500ms', got %s", pollIntervalFlag.DefValue)
	}
}
