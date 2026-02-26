package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDispatcher_FiresWebhook(t *testing.T) {
	var mu sync.Mutex
	var received *Payload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p Payload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("failed to decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = &p
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher(1 * time.Hour)
	d.MaybeDispatch("tool-1", "Test Tool", srv.URL, 80, 100, 85)

	// Wait for async delivery.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if received == nil {
		t.Fatal("expected webhook to be delivered")
	}
	if received.ToolID != "tool-1" {
		t.Errorf("expected tool_id tool-1, got %s", received.ToolID)
	}
	if received.ToolName != "Test Tool" {
		t.Errorf("expected tool_name Test Tool, got %s", received.ToolName)
	}
	if received.ThresholdPercent != 80 {
		t.Errorf("expected threshold 80, got %d", received.ThresholdPercent)
	}
	if received.CurrentUsage != 85 {
		t.Errorf("expected current_usage 85, got %f", received.CurrentUsage)
	}
	if received.BudgetLimit != 100 {
		t.Errorf("expected budget_limit 100, got %f", received.BudgetLimit)
	}
}

func TestDispatcher_Cooldown(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher(1 * time.Hour)

	// First call should fire.
	d.MaybeDispatch("tool-1", "Tool", srv.URL, 80, 100, 90)
	time.Sleep(100 * time.Millisecond)

	// Second call within cooldown should be suppressed.
	d.MaybeDispatch("tool-1", "Tool", srv.URL, 80, 100, 95)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("expected 1 webhook call (cooldown), got %d", callCount)
	}
}

func TestDispatcher_BelowThreshold(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher(1 * time.Hour)
	// Usage 70% is below 80% threshold.
	d.MaybeDispatch("tool-1", "Tool", srv.URL, 80, 100, 70)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 0 {
		t.Errorf("expected no webhook call below threshold, got %d", callCount)
	}
}

func TestDispatcher_NoURL(t *testing.T) {
	d := NewDispatcher(1 * time.Hour)
	// Should not panic with empty URL.
	d.MaybeDispatch("tool-1", "Tool", "", 80, 100, 90)
}

func TestDispatcher_ZeroBudget(t *testing.T) {
	d := NewDispatcher(1 * time.Hour)
	// Should not fire with zero budget (avoids division by zero).
	d.MaybeDispatch("tool-1", "Tool", "http://example.com", 80, 0, 0)
}
