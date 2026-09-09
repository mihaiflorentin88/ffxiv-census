package rabbitmq

import "testing"

func TestFailureAction(t *testing.T) {
	tests := []struct {
		name        string
		attempts    int
		maxAttempts int
		wantBackoff int
		wantPerm    bool
	}{
		{
			name:        "first failure retries after base backoff",
			attempts:    1,
			maxAttempts: 50,
			wantBackoff: 5,
		},
		{
			name:        "second failure doubles backoff",
			attempts:    2,
			maxAttempts: 50,
			wantBackoff: 10,
		},
		{
			name:        "fifth failure reaches the documented 80s step",
			attempts:    5,
			maxAttempts: 50,
			wantBackoff: 80,
		},
		{
			name:        "deep attempts cap at the backoff ceiling",
			attempts:    40,
			maxAttempts: 50,
			wantBackoff: 3600,
		},
		{
			name:        "attempt budget exhausted parks permanently",
			attempts:    50,
			maxAttempts: 50,
			wantBackoff: 0,
			wantPerm:    true,
		},
		{
			name:        "small budget parks at its own ceiling",
			attempts:    5,
			maxAttempts: 5,
			wantBackoff: 0,
			wantPerm:    true,
		},
		{
			name:        "budget below the attempt count parks immediately",
			attempts:    6,
			maxAttempts: 5,
			wantBackoff: 0,
			wantPerm:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backoff, permanent := failureAction(tt.attempts, tt.maxAttempts)
			if backoff != tt.wantBackoff || permanent != tt.wantPerm {
				t.Fatalf("failureAction(%d, %d) = (%d, %v), want (%d, %v)",
					tt.attempts, tt.maxAttempts, backoff, permanent, tt.wantBackoff, tt.wantPerm)
			}
		})
	}
}
