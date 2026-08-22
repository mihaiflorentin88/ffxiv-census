package cli

import (
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
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
}

func TestProxyEventsNeedTomestone(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		want   bool
	}{
		{"empty/default needs tomestone", nil, true},
		{"achievement-only does not need tomestone", []string{handler.EventAchievementCensus}, false},
		{"id-sweep needs tomestone", []string{handler.EventIDSweep}, true},
		{"character-census needs tomestone", []string{handler.EventCharacterCensus}, true},
		{"mixed census needs tomestone", []string{handler.EventAchievementCensus, handler.EventIDSweep}, true},
		{"unknown event needs tomestone", []string{"unknown-event"}, true},
		{"multiple achievements still does not need tomestone", []string{handler.EventAchievementCensus, handler.EventAchievementCensus}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proxyEventsNeedTomestone(tt.events)
			if got != tt.want {
				t.Errorf("proxyEventsNeedTomestone(%v) = %v, want %v", tt.events, got, tt.want)
			}
		})
	}
}
