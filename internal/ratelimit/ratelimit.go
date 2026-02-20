package ratelimit

import (
	"sync"
	"time"
)

// bucket tracks the token state for a single key.
type bucket struct {
	tokens     float64
	lastRefill time.Time
	rate       int
}

// Limiter implements a token-bucket rate limiter keyed by arbitrary string
// identifiers (e.g. agent ID, tool ID).
type Limiter struct {
	mu          sync.Mutex
	buckets     map[string]*bucket
	defaultRate int
	window      time.Duration
	now         func() time.Time // injectable clock for testing
}

// New creates a Limiter that allows defaultRate requests per window.
func New(defaultRate int, window time.Duration) *Limiter {
	return &Limiter{
		buckets:     make(map[string]*bucket),
		defaultRate: defaultRate,
		window:      window,
		now:         time.Now,
	}
}

// effectiveRate returns customRate if positive, otherwise the default rate.
func (l *Limiter) effectiveRate(customRate int) int {
	if customRate > 0 {
		return customRate
	}
	return l.defaultRate
}

// getBucket returns the bucket for key, creating one if it doesn't exist.
// Must be called with l.mu held.
func (l *Limiter) getBucket(key string, rate int) *bucket {
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{
			tokens:     float64(rate),
			lastRefill: l.now(),
			rate:       rate,
		}
		l.buckets[key] = b
	}
	// Update the rate if it changed (e.g. agent config updated).
	b.rate = rate
	return b
}

// refill adds tokens to the bucket based on elapsed time since the last refill.
// Must be called with l.mu held.
func (l *Limiter) refill(b *bucket) {
	now := l.now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}

	// Tokens accumulate at rate/window per second.
	refillRate := float64(b.rate) / l.window.Seconds()
	b.tokens += elapsed * refillRate
	if b.tokens > float64(b.rate) {
		b.tokens = float64(b.rate)
	}
	b.lastRefill = now
}

// Allow checks whether a request identified by key is permitted. If customRate
// is positive it overrides the default rate for this key. Returns true and
// consumes one token when allowed, false when the limit is exceeded.
func (l *Limiter) Allow(key string, customRate int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	rate := l.effectiveRate(customRate)
	b := l.getBucket(key, rate)
	l.refill(b)

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Status returns the current rate-limit state for key. limit is the maximum
// number of tokens, remaining is the number of tokens left (floored to int),
// and resetAt is the time at which the bucket will be fully replenished.
func (l *Limiter) Status(key string, customRate int) (limit int, remaining int, resetAt time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rate := l.effectiveRate(customRate)
	b := l.getBucket(key, rate)
	l.refill(b)

	limit = rate
	remaining = int(b.tokens)
	if remaining < 0 {
		remaining = 0
	}

	// Time until full replenishment from current level.
	deficit := float64(rate) - b.tokens
	if deficit <= 0 {
		resetAt = l.now()
	} else {
		refillRate := float64(rate) / l.window.Seconds()
		resetAt = l.now().Add(time.Duration(deficit/refillRate*1e9) * time.Nanosecond)
	}
	return
}
