package tomestone

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// RequestRateController owns the token bucket, configured/clamped rate, and
// global consecutive-429 count for a set of Tomestone clients sharing one
// rate limiter. It replaces per-client adaptive state so that backoff and
// recovery are coordinated across all clients using the same controller.
type RequestRateController struct {
	mu              sync.Mutex
	limiter         *rate.Limiter
	configuredRate  float64
	consecutive429s int
}

// NewRequestRateController creates a controller that applies the default rate
// and maxSafeRate clamp once. The returned controller is safe for concurrent
// use by multiple clients.
func NewRequestRateController(configured float64) *RequestRateController {
	r := configured
	if r <= 0 {
		r = 10.0
	}
	if r > maxSafeRate {
		r = maxSafeRate
	}
	return &RequestRateController{
		limiter:        rate.NewLimiter(rate.Limit(r), 1),
		configuredRate: r,
	}
}

// Wait blocks until the next token is available or ctx is cancelled.
func (c *RequestRateController) Wait(ctx context.Context) error {
	return c.limiter.Wait(ctx)
}

// RecordRateLimit records a429 response and reduces the shared rate.
// Returns the new consecutive429 count.
func (c *RequestRateController) RecordRateLimit(logger contract.Logger, ctx context.Context) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consecutive429s++
	shift := uint(c.consecutive429s)
	if shift > 30 {
		shift = 30
	}
	newRate := c.configuredRate / float64(uint(1)<<shift)
	if newRate < 0.5 {
		newRate = 0.5
	}
	c.limiter.SetLimit(rate.Limit(newRate))
	logger.InfoContext(
		ctx, "tomestone.rate_limit",
		slog.Int("consecutive_429s", c.consecutive429s),
		slog.Float64("new_rate", newRate),
	)
	return c.consecutive429s
}

// RecordSuccess records a successful response and recovers the rate if backed
// off. Cannot exceed the shared global backoff state.
func (c *RequestRateController) RecordSuccess(logger contract.Logger, ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.consecutive429s > 0 {
		c.consecutive429s--
		newRate := c.configuredRate
		if c.consecutive429s > 0 {
			shift := uint(c.consecutive429s)
			if shift > 30 {
				shift = 30
			}
			newRate = c.configuredRate / float64(uint(1)<<shift)
			if newRate < 0.5 {
				newRate = 0.5
			}
		}
		c.limiter.SetLimit(rate.Limit(newRate))
		logger.InfoContext(
			ctx, "tomestone.rate_recovery",
			slog.Int("consecutive_429s", c.consecutive429s),
			slog.Float64("recovered_rate", newRate),
		)
	}
}

// Consecutive429s returns the current consecutive429 count (for testing).
func (c *RequestRateController) Consecutive429s() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.consecutive429s
}

// Rate returns the current effective rate limit (for testing).
func (c *RequestRateController) Rate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return float64(c.limiter.Limit())
}

// WaitWithRetryAfter applies a retry-after pause in addition to the token
// bucket wait. Used for429 responses with Retry-After headers.
func (c *RequestRateController) WaitWithRetryAfter(ctx context.Context, retryAfter time.Duration) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	if retryAfter > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryAfter):
		}
	}
	return nil
}
