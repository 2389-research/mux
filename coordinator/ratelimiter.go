// ABOUTME: Token bucket rate limiter for API call throttling.
// ABOUTME: Allows bursts up to capacity while maintaining average rate.
package coordinator

import (
	"context"
	"sync"
	"time"
)

// RateLimiter implements token bucket rate limiting.
type RateLimiter struct {
	tokens     float64
	capacity   float64
	refillRate float64
	lastRefill time.Time
	mu         sync.Mutex
}

// NewRateLimiter creates a new token bucket rate limiter.
// capacity: maximum tokens (burst size)
// refillRate: tokens added per second
func NewRateLimiter(capacity, refillRate float64) *RateLimiter {
	return &RateLimiter{
		tokens:     capacity,
		capacity:   capacity,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// Take consumes tokens, blocking until available or context cancelled.
func (r *RateLimiter) Take(ctx context.Context, tokens float64) error {
	for {
		waitTime := r.tryTake(tokens)
		if waitTime == 0 {
			return nil
		}

		// Enforce a minimum wait time of 10ms to prevent CPU-intensive busy loops.
		// This is necessary because:
		// 1. Clock/timer granularity varies across systems (typically 1-15ms)
		// 2. Very short waits (<10ms) can lead to excessive CPU usage from
		//    rapid lock contention and context switching
		// 3. The 10ms minimum ensures predictable behavior across platforms
		//    while still providing responsive rate limiting for most use cases
		if waitTime < 10*time.Millisecond {
			waitTime = 10 * time.Millisecond
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			// Retry after calculated wait time
		}
	}
}

// tryTake attempts to consume tokens. Returns 0 if successful,
// otherwise returns the estimated wait time until tokens are available.
func (r *RateLimiter) tryTake(tokens float64) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.refill()

	if r.tokens >= tokens {
		r.tokens -= tokens
		return 0
	}

	// Calculate wait time for needed tokens
	needed := tokens - r.tokens
	waitSeconds := needed / r.refillRate
	return time.Duration(waitSeconds * float64(time.Second))
}

func (r *RateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(r.lastRefill).Seconds()
	r.lastRefill = now

	r.tokens += elapsed * r.refillRate
	if r.tokens > r.capacity {
		r.tokens = r.capacity
	}
}
