package circuitbreaker

import (
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Enabled:           true,
		ErrorThresholdPct: 90,
		WindowSeconds:     120,
		CooldownSeconds:   1, // short for tests
	}
}

func TestClosedState(t *testing.T) {
	cb := New(testConfig())
	if !cb.Allow() {
		t.Fatal("expected closed circuit to allow requests")
	}
	if cb.GetState() != StateClosed {
		t.Fatalf("expected closed state, got %s", cb.GetState())
	}
}

func TestTripsOnHighErrorRate(t *testing.T) {
	cb := New(testConfig())

	// Record 10 errors (100% error rate, above 90% threshold).
	for i := 0; i < 10; i++ {
		cb.Allow()
		cb.RecordResult(500)
	}

	if cb.GetState() != StateOpen {
		t.Fatalf("expected open state after high error rate, got %s", cb.GetState())
	}
	if cb.Allow() {
		t.Error("expected open circuit to reject requests")
	}
}

func TestDoesNotTripBelowMinSample(t *testing.T) {
	cb := New(testConfig())

	// Only 5 errors — below minSample of 10.
	for i := 0; i < 5; i++ {
		cb.Allow()
		cb.RecordResult(500)
	}

	if cb.GetState() != StateClosed {
		t.Fatalf("expected closed state below min sample, got %s", cb.GetState())
	}
}

func TestDoesNotTripBelowThreshold(t *testing.T) {
	cb := New(testConfig())

	// 2 errors + 10 successes = ~15% error rate (well below 90%).
	for i := 0; i < 2; i++ {
		cb.Allow()
		cb.RecordResult(500)
	}
	for i := 0; i < 10; i++ {
		cb.Allow()
		cb.RecordResult(200)
	}

	if cb.GetState() != StateClosed {
		t.Fatalf("expected closed state below threshold, got %s", cb.GetState())
	}
}

func TestDoesNotCountClientErrors(t *testing.T) {
	cb := New(testConfig())

	// 10 requests, all 4xx — should not trip.
	for i := 0; i < 15; i++ {
		cb.Allow()
		cb.RecordResult(400)
	}

	if cb.GetState() != StateClosed {
		t.Fatalf("expected closed state for 4xx errors, got %s", cb.GetState())
	}
}

func TestCooldownTransition(t *testing.T) {
	cb := New(testConfig())

	// Trip the circuit.
	for i := 0; i < 10; i++ {
		cb.Allow()
		cb.RecordResult(500)
	}
	if cb.GetState() != StateOpen {
		t.Fatalf("expected open state, got %s", cb.GetState())
	}

	// Wait for cooldown (1 second).
	time.Sleep(1100 * time.Millisecond)

	// Should transition to half-open and allow one probe.
	if !cb.Allow() {
		t.Fatal("expected half-open to allow probe request")
	}
	if cb.GetState() != StateHalfOpen {
		t.Fatalf("expected half-open state, got %s", cb.GetState())
	}

	// Second request in half-open should be rejected.
	if cb.Allow() {
		t.Error("expected half-open to reject second request")
	}
}

func TestHalfOpenProbeSuccess(t *testing.T) {
	cb := New(testConfig())

	// Trip the circuit.
	for i := 0; i < 10; i++ {
		cb.Allow()
		cb.RecordResult(500)
	}

	// Wait for cooldown.
	time.Sleep(1100 * time.Millisecond)

	cb.Allow() // probe
	cb.RecordResult(200)

	if cb.GetState() != StateClosed {
		t.Fatalf("expected closed state after successful probe, got %s", cb.GetState())
	}
}

func TestHalfOpenProbeFailure(t *testing.T) {
	cb := New(testConfig())

	// Trip the circuit.
	for i := 0; i < 10; i++ {
		cb.Allow()
		cb.RecordResult(500)
	}

	// Wait for cooldown.
	time.Sleep(1100 * time.Millisecond)

	cb.Allow() // probe
	cb.RecordResult(503)

	if cb.GetState() != StateOpen {
		t.Fatalf("expected open state after failed probe, got %s", cb.GetState())
	}
}

func TestWindowReset(t *testing.T) {
	cfg := testConfig()
	cfg.WindowSeconds = 1 // 1 second window for test
	cb := New(cfg)

	// Record some errors.
	for i := 0; i < 5; i++ {
		cb.Allow()
		cb.RecordResult(500)
	}

	// Wait for window to expire.
	time.Sleep(1100 * time.Millisecond)

	// After window reset, counters should be cleared.
	cb.Allow()
	cb.RecordResult(200)

	stats := cb.GetStats()
	if stats.Total != 1 {
		t.Errorf("expected total 1 after window reset, got %d", stats.Total)
	}
	if stats.Errors != 0 {
		t.Errorf("expected 0 errors after window reset, got %d", stats.Errors)
	}
}

func TestRegistryGetOrCreate(t *testing.T) {
	reg := NewRegistry()
	cfg := testConfig()

	cb1 := reg.Get("tool-1", cfg)
	cb2 := reg.Get("tool-1", cfg)

	if cb1 != cb2 {
		t.Error("expected same circuit breaker for same tool ID")
	}

	cb3 := reg.Get("tool-2", cfg)
	if cb1 == cb3 {
		t.Error("expected different circuit breaker for different tool ID")
	}
}

func TestRegistryGetStats(t *testing.T) {
	reg := NewRegistry()

	// Unknown tool should return nil.
	if stats := reg.GetStats("unknown"); stats != nil {
		t.Error("expected nil stats for unknown tool")
	}

	cfg := testConfig()
	cb := reg.Get("tool-1", cfg)
	cb.Allow()
	cb.RecordResult(200)

	stats := reg.GetStats("tool-1")
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if stats.State != "closed" {
		t.Errorf("expected closed state, got %s", stats.State)
	}
	if stats.Total != 1 {
		t.Errorf("expected total 1, got %d", stats.Total)
	}
}
