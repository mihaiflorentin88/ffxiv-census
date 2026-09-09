package worker

import "testing"

// The reservation and small-concurrency admission tests pin the integer-only
// allocation contract of the scan dispatcher: reserved slots guarantee every
// queue progress at concurrency >= 3, and persistent smooth weighted
// round-robin gives the three queues their weighted share at concurrency 1-2
// where a per-queue reservation cannot fit.

func TestReservationTargets(t *testing.T) {
	tests := []struct {
		name        string
		concurrency int
		weights     [3]int
		want        [3]int
	}{
		{
			name:        "minimum concurrency reserves one slot per queue",
			concurrency: 3,
			weights:     [3]int{10, 45, 45},
			want:        [3]int{1, 1, 1},
		},
		{
			name:        "single leftover goes to the largest remainder, lowest index wins ties",
			concurrency: 4,
			weights:     [3]int{10, 45, 45},
			want:        [3]int{1, 2, 1},
		},
		{
			name:        "tied remainders hand the leftover to the lower index",
			concurrency: 6,
			weights:     [3]int{10, 45, 45},
			want:        [3]int{1, 3, 2},
		},
		{
			name:        "ten workers",
			concurrency: 10,
			weights:     [3]int{10, 45, 45},
			want:        [3]int{2, 4, 4},
		},
		{
			name:        "one hundred fifty workers per the approved minimum-one-then-remainder rule",
			concurrency: 150,
			weights:     [3]int{10, 45, 45},
			want:        [3]int{16, 67, 67},
		},
		{
			name:        "three hundred workers distribute two leftovers by remainder",
			concurrency: 300,
			weights:     [3]int{10, 45, 45},
			want:        [3]int{31, 135, 134},
		},
		{
			name:        "remainder ordering follows the fractional share",
			concurrency: 7,
			weights:     [3]int{50, 30, 20},
			want:        [3]int{3, 2, 2},
		},
		{
			name:        "below minimum concurrency reserves nothing; small round-robin owns admission",
			concurrency: 2,
			weights:     [3]int{10, 45, 45},
			want:        [3]int{0, 0, 0},
		},
		{
			name:        "zero concurrency reserves nothing",
			concurrency: 0,
			weights:     [3]int{10, 45, 45},
			want:        [3]int{0, 0, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reservationTargets(tt.concurrency, tt.weights)
			if got != tt.want {
				t.Fatalf("reservationTargets(%d, %v) = %v, want %v", tt.concurrency, tt.weights, got, tt.want)
			}
		})
	}
}

func TestReservationTargetsInvariants(t *testing.T) {
	weightSets := [][3]int{{10, 45, 45}, {33, 33, 34}, {1, 1, 98}, {50, 49, 1}, {34, 33, 33}}
	for concurrency := 3; concurrency <= 120; concurrency++ {
		for _, weights := range weightSets {
			got := reservationTargets(concurrency, weights)
			sum := got[0] + got[1] + got[2]
			if sum != concurrency {
				t.Fatalf("reservationTargets(%d, %v) = %v distributes %d slots, want %d", concurrency, weights, got, sum, concurrency)
			}
			for i, target := range got {
				if target < 1 {
					t.Fatalf("reservationTargets(%d, %v) = %v leaves queue %d without a reserved slot", concurrency, weights, got, i)
				}
			}
		}
	}
}

func TestSmallAdmissionDoesNotStarveVerification(t *testing.T) {
	var a admission
	counts := [3]int{}
	for range 100 {
		q, ok := a.chooseSmall([3]bool{true, true, true}, [3]int{10, 45, 45})
		if !ok {
			t.Fatal("no queue selected despite available work")
		}
		counts[q]++
	}
	if counts != [3]int{10, 45, 45} {
		t.Fatalf("admissions: %v", counts)
	}
}

func TestSmallAdmissionCreditsPersistAcrossAdmissions(t *testing.T) {
	// The exact weighted total above already proves persistence: resetting the
	// credits before every admission would always hand the win to the largest
	// weight (recovery), yielding 100 recovery admissions instead of 10/45/45.
	// This test additionally pins that the same instance keeps behaving across
	// a pause of unavailable queues: entitlement discarded while a queue has
	// no work, rebuilt from zero when it returns.
	var a admission
	for range 20 {
		if _, ok := a.chooseSmall([3]bool{true, true, true}, [3]int{10, 45, 45}); !ok {
			t.Fatal("no queue selected despite available work")
		}
	}
	for i := range 10 {
		q, ok := a.chooseSmall([3]bool{true, false, true}, [3]int{10, 45, 45})
		if !ok {
			t.Fatal("no queue selected despite available work")
		}
		if q == 1 {
			t.Fatalf("admission %d picked unavailable recovery queue", i)
		}
		if a.credits[1] != 0 {
			t.Fatalf("unavailable queue credit = %d, want 0", a.credits[1])
		}
	}
}

func TestSmallAdmissionAllUnavailableReturnsFalse(t *testing.T) {
	var a admission
	q, ok := a.chooseSmall([3]bool{false, false, false}, [3]int{10, 45, 45})
	if ok {
		t.Fatalf("chooseSmall with no available queues selected queue %d", q)
	}
}

func TestSmallAdmissionUniformWeightsRoundRobin(t *testing.T) {
	var a admission
	want := []int{0, 1, 2, 0, 1, 2}
	for i, wq := range want {
		q, ok := a.chooseSmall([3]bool{true, true, true}, [3]int{1, 1, 1})
		if !ok {
			t.Fatalf("admission %d: no queue selected despite available work", i)
		}
		if q != wq {
			t.Fatalf("admission %d picked queue %d, want %d", i, q, wq)
		}
	}
}
