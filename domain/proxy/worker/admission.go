package worker

// scanQueueCount is the number of scheduling queues the dispatcher
// distinguishes: verification, recovery and background.
const scanQueueCount = 3

// reservationTargets distributes concurrency across the three scan queues.
//
// One slot per queue is reserved first, so a continuously busy background
// queue can never occupy the slot that guarantees verification progress. The
// remaining concurrency-3 slots are split proportionally to the weights with
// integer arithmetic only: each queue receives remaining*weight/100, and the
// at-most-two leftover slots go to the largest fractional remainders
// (remaining*weight)%100, lower index winning ties.
//
// For concurrency below three a per-queue reservation cannot fit; the
// dispatcher owns admission entirely through the persistent small-worker
// round robin and this function returns all zeros.
func reservationTargets(concurrency int, weights [3]int) [3]int {
	if concurrency < scanQueueCount {
		return [3]int{}
	}
	targets := [3]int{1, 1, 1}
	rest := weights
	remaining := concurrency - scanQueueCount
	shared := 0
	for i := range rest {
		share := remaining * rest[i] / 100
		targets[i] += share
		shared += share
	}
	for leftover := remaining - shared; leftover > 0; leftover-- {
		best, bestRemainder := -1, -1
		for i := range rest {
			if r := remaining * rest[i] % 100; r > bestRemainder {
				best, bestRemainder = i, r
			}
		}
		targets[best]++
		rest[best] = 0 // a queue's fractional remainder wins at most one slot
	}
	return targets
}

// admission holds the persistent admission state of one dispatcher. Both
// fields live across admissions for the whole RunScan lifetime: the credits
// carry smooth weighted round-robin entitlement for small concurrency, and
// borrower remembers where the large-concurrency borrowing rotation stopped.
type admission struct {
	credits  [scanQueueCount]int
	borrower int
}

// chooseSmall picks the next queue for the persistent smooth weighted
// round robin used at concurrency one and two. Credits accumulate the queue
// weight on every admission and never reset between admissions, so the
// long-run admission shares converge on the weights; the winner pays the
// total weight of the available queues. A queue without work is unavailable:
// its accumulated credit is discarded rather than banked, and when no queue
// is available chooseSmall reports false.
func (a *admission) chooseSmall(available [scanQueueCount]bool, weights [scanQueueCount]int) (int, bool) {
	total, best := 0, -1
	for i := range weights {
		if !available[i] {
			a.credits[i] = 0
			continue
		}
		total += weights[i]
		a.credits[i] += weights[i]
		if best < 0 || a.credits[i] > a.credits[best] {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	a.credits[best] -= total
	return best, true
}
