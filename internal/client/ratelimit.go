package client

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

// CloudRateLimiter enforces the ClickHouse Cloud Management API quota:
// 10 requests per 10 seconds per API key.
//
// Implemented as a token bucket: refill 1 token per 1 second, burst of 10.
// Steady-state 1 req/s with up to 10 in a burst — matches the stated quota.
//
// Per ClickHouse Cloud docs, hitting the quota returns 429 with a Retry-After
// header. Our HTTP client also retries on 429 (see ShouldRetryStatus / AsRetryable),
// so the limiter is preventive — the retry loop is the safety net.
type CloudRateLimiter struct {
	rl *rate.Limiter
}

// NewCloudRateLimiter returns a limiter pre-configured for the Cloud API.
// All callers in chx use the same limit; profiles don't customize it.
func NewCloudRateLimiter() *CloudRateLimiter {
	return &CloudRateLimiter{
		rl: rate.NewLimiter(rate.Every(time.Second), 10),
	}
}

// Wait blocks until a token is available or ctx is cancelled.
func (l *CloudRateLimiter) Wait(ctx context.Context) error {
	return l.rl.Wait(ctx)
}

// Tokens returns the current available token count (for verbose logs / tests).
func (l *CloudRateLimiter) Tokens() float64 {
	return l.rl.Tokens()
}
