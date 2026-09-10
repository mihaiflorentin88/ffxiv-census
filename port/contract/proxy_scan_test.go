package contract

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsChallenge(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "202 challenge",
			err:  &ProxyCheckError{Kind: CheckTarget, Reason: "status 202", Challenge: true},
			want: true,
		},
		{
			name: "403 challenge block",
			err:  &ProxyCheckError{Kind: CheckTarget, Reason: "status 403 challenge", Challenge: true},
			want: true,
		},
		{
			name: "challenge wrapped by handler layers",
			err:  fmt.Errorf("id-sweep lodestone fetch 13291950: fetch character 13291950: request %s: %w", "url", &ProxyCheckError{Kind: CheckTarget, Reason: "status 202", Challenge: true}),
			want: true,
		},
		{
			name: "429 rate limit is not a challenge",
			err:  &ProxyCheckError{Kind: CheckTarget, Reason: "status 429"},
			want: false,
		},
		{
			name: "proxy dial failure is not a challenge",
			err:  &ProxyCheckError{Kind: CheckProxy, Reason: "dial refused"},
			want: false,
		},
		{
			name: "plain error is not a challenge",
			err:  errors.New("boom"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsChallenge(tt.err); got != tt.want {
				t.Fatalf("IsChallenge(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
