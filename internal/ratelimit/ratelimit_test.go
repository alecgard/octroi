package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a controllable time source for deterministic tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestLimiter creates a Limiter wired to the given fake clock.
func newTestLimiter(rate int, window time.Duration, clock *fakeClock) *Limiter {
	l := New(rate, window)
	l.now = clock.Now
	return l
}

func TestAllowBasic(t *testing.T) {
	clock := newFakeClock(time.Now())
	l := newTestLimiter(3, time.Minute, clock)

	for i := 0; i < 3; i++ {
		if !l.Allow("agent-1", 0) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	if l.Allow("agent-1", 0) {
		t.Fatal("4th request should be denied")
	}
}

func TestAllowDifferentKeys(t *testing.T) {
	clock := newFakeClock(time.Now())
	l := newTestLimiter(1, time.Minute, clock)

	if !l.Allow("a", 0) {
		t.Fatal("first request for key 'a' should be allowed")
	}
	if l.Allow("a", 0) {
		t.Fatal("second request for key 'a' should be denied")
	}
	// Different key should have its own bucket.
	if !l.Allow("b", 0) {
		t.Fatal("first request for key 'b' should be allowed")
	}
}

func TestTokenRefill(t *testing.T) {
	clock := newFakeClock(time.Now())
	// 60 tokens per minute = 1 token per second.
	l := newTestLimiter(60, time.Minute, clock)

	// Exhaust all tokens.
	for i := 0; i < 60; i++ {
		l.Allow("k", 0)
	}
	if l.Allow("k", 0) {
		t.Fatal("should be denied after exhausting tokens")
	}

	// Advance 1 second -> 1 token refilled.
	clock.Advance(1 * time.Second)
	if !l.Allow("k", 0) {
		t.Fatal("should be allowed after 1 second refill")
	}
	if l.Allow("k", 0) {
		t.Fatal("should be denied again after consuming refilled token")
	}

	// Advance 5 seconds -> 5 tokens.
	clock.Advance(5 * time.Second)
	for i := 0; i < 5; i++ {
		if !l.Allow("k", 0) {
			t.Fatalf("request %d should be allowed after 5s refill", i+1)
		}
	}
	if l.Allow("k", 0) {
		t.Fatal("should be denied after consuming 5 refilled tokens")
	}
}

func TestTokenRefillCap(t *testing.T) {
	clock := newFakeClock(time.Now())
	l := newTestLimiter(5, time.Minute, clock)

	// Use 2 tokens.
	l.Allow("k", 0)
	l.Allow("k", 0)

	// Advance a very long time; tokens should cap at rate.
	clock.Advance(10 * time.Minute)

	_, remaining, _ := l.Status("k", 0)
	if remaining != 5 {
		t.Fatalf("remaining should cap at 5, got %d", remaining)
	}
}

func TestCustomRateOverride(t *testing.T) {
	tests := []struct {
		name       string
		defaultR   int
		customR    int
		wantAllow  int // how many requests should be allowed
	}{
		{"custom higher than default", 2, 5, 5},
		{"custom lower than default", 10, 3, 3},
		{"zero custom uses default", 5, 0, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newFakeClock(time.Now())
			l := newTestLimiter(tt.defaultR, time.Minute, clock)

			allowed := 0
			for i := 0; i < tt.wantAllow+2; i++ {
				if l.Allow("key", tt.customR) {
					allowed++
				}
			}
			if allowed != tt.wantAllow {
				t.Fatalf("expected %d allowed, got %d", tt.wantAllow, allowed)
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	clock := newFakeClock(time.Now())
	l := newTestLimiter(100, time.Minute, clock)

	var wg sync.WaitGroup
	allowed := make(chan bool, 200)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed <- l.Allow("concurrent", 0)
		}()
	}

	wg.Wait()
	close(allowed)

	count := 0
	for ok := range allowed {
		if ok {
			count++
		}
	}

	if count != 100 {
		t.Fatalf("expected exactly 100 allowed, got %d", count)
	}
}

func TestStatus(t *testing.T) {
	clock := newFakeClock(time.Now())
	l := newTestLimiter(10, time.Minute, clock)

	// Fresh bucket.
	limit, remaining, _ := l.Status("s", 0)
	if limit != 10 {
		t.Fatalf("expected limit 10, got %d", limit)
	}
	if remaining != 10 {
		t.Fatalf("expected remaining 10, got %d", remaining)
	}

	// Consume 3 tokens.
	l.Allow("s", 0)
	l.Allow("s", 0)
	l.Allow("s", 0)

	limit, remaining, resetAt := l.Status("s", 0)
	if limit != 10 {
		t.Fatalf("expected limit 10, got %d", limit)
	}
	if remaining != 7 {
		t.Fatalf("expected remaining 7, got %d", remaining)
	}

	// Reset time should be in the future (about 18 seconds for 3 tokens at
	// 10/min = 1 token per 6 seconds).
	now := clock.Now()
	if !resetAt.After(now) {
		t.Fatalf("resetAt %v should be after now %v", resetAt, now)
	}
}

func TestStatusCustomRate(t *testing.T) {
	clock := newFakeClock(time.Now())
	l := newTestLimiter(10, time.Minute, clock)

	limit, remaining, _ := l.Status("s", 20)
	if limit != 20 {
		t.Fatalf("expected limit 20, got %d", limit)
	}
	if remaining != 20 {
		t.Fatalf("expected remaining 20, got %d", remaining)
	}
}

func TestStatusFullBucketResetIsNow(t *testing.T) {
	clock := newFakeClock(time.Now())
	l := newTestLimiter(5, time.Minute, clock)

	_, _, resetAt := l.Status("full", 0)
	now := clock.Now()

	if resetAt != now {
		t.Fatalf("full bucket resetAt should equal now, got diff %v", resetAt.Sub(now))
	}
}
