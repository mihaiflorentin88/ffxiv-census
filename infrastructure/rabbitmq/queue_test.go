package rabbitmq

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestRetryBackoffSec(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		want     int
	}{
		{name: "first failure retries after the base backoff", attempts: 1, want: 5},
		{name: "second failure doubles backoff", attempts: 2, want: 10},
		{name: "fifth failure reaches the documented 80s step", attempts: 5, want: 80},
		{name: "eleventh failure saturates at the cap", attempts: 11, want: 3600},
		{name: "deep attempts stay capped at one hour", attempts: 40, want: 3600},
		{name: "non-positive attempts fall back to the base backoff", attempts: 0, want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryBackoffSec(tt.attempts); got != tt.want {
				t.Fatalf("retryBackoffSec(%d) = %d, want %d", tt.attempts, got, tt.want)
			}
		})
	}
}

func TestCanonicalEventType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "id-sweep", want: "id-sweep"},
		{in: "id-sweep.failed", want: "id-sweep"},
		{in: "character-census.failed", want: "character-census"},
		{in: "achievement-census", want: "achievement-census"},
		{in: "new-proxy.failed", want: "new-proxy"},
	}
	for _, tt := range tests {
		if got := canonicalEventType(tt.in); got != tt.want {
			t.Fatalf("canonicalEventType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFailureDecision(t *testing.T) {
	challengeErr := fmt.Errorf("id-sweep lodestone fetch 1: fetch character 1: request %s: %w",
		"url", &contract.ProxyCheckError{Kind: contract.CheckTarget, Reason: "status 202", Challenge: true})
	fourTwentyNine := fmt.Errorf("request %s: %w", "url",
		&contract.ProxyCheckError{Kind: contract.CheckTarget, Reason: "status 429"})
	poisonDeep := fmt.Errorf("handler: %w", errors.Join(contract.ErrPoisonPayload, errors.New("invalid json")))

	tests := []struct {
		name        string
		err         error
		attempts    int
		wantDead    bool
		wantBackoff int
	}{
		{
			name:     "poison payload dead-parks",
			err:      errors.Join(contract.ErrPoisonPayload, errors.New("invalid json")),
			attempts: 1,
			wantDead: true,
		},
		{
			name:     "poison wrapped by handler layers still dead-parks",
			err:      poisonDeep,
			attempts: 7,
			wantDead: true,
		},
		{
			name:     "no handler dead-parks",
			err:      fmt.Errorf("%w: no handler registered for event id-sweep", contract.ErrNoHandler),
			attempts: 1,
			wantDead: true,
		},
		{
			name:        "challenge retries on the ladder",
			err:         challengeErr,
			attempts:    1,
			wantBackoff: 5,
		},
		{
			name:        "timeout retries",
			err:         errors.New("context deadline exceeded"),
			attempts:    2,
			wantBackoff: 10,
		},
		{
			name:        "429 retries",
			err:         fourTwentyNine,
			attempts:    3,
			wantBackoff: 20,
		},
		{
			name:        "5xx retries with capped backoff",
			err:         errors.New("HTTP 503 from lodestone"),
			attempts:    12,
			wantBackoff: 3600,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := failureDecision(tt.err, tt.attempts)
			if decision.dead != tt.wantDead {
				t.Fatalf("failureDecision(%v, %d).dead = %v, want %v", tt.err, tt.attempts, decision.dead, tt.wantDead)
			}
			if !tt.wantDead && decision.backoffSec != tt.wantBackoff {
				t.Fatalf("failureDecision(%v, %d).backoffSec = %d, want %d", tt.err, tt.attempts, decision.backoffSec, tt.wantBackoff)
			}
		})
	}
}

func TestKnownEventType(t *testing.T) {
	for _, et := range eventTypes() {
		if !knownEventType(et) {
			t.Fatalf("knownEventType(%q) = false, want true", et)
		}
	}
	if knownEventType("census.id-sweep") {
		t.Fatal("knownEventType(\"census.id-sweep\") = true, want false")
	}
	if knownEventType("no-such-event") {
		t.Fatal("knownEventType(\"no-such-event\") = true, want false")
	}
}
