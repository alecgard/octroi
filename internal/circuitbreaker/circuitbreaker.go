package circuitbreaker

import (
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = 0
	StateOpen     State = 1
	StateHalfOpen State = 2
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Config holds circuit breaker settings for a tool.
type Config struct {
	Enabled           bool
	ErrorThresholdPct int
	WindowSeconds     int
	CooldownSeconds   int
}

// Stats exposes the current state and counters of a circuit breaker.
type Stats struct {
	State    string `json:"state"`
	Total    int    `json:"total"`
	Errors   int    `json:"errors"`
	ErrorPct int    `json:"error_pct"`
}

// CircuitBreaker tracks error rates and trips to open state.
type CircuitBreaker struct {
	mu          sync.Mutex
	state       State
	total       int
	errors      int
	windowStart time.Time
	openedAt    time.Time
	config      Config
}

// New creates a circuit breaker with the given config.
func New(cfg Config) *CircuitBreaker {
	return &CircuitBreaker{
		state:       StateClosed,
		windowStart: time.Now(),
		config:      cfg,
	}
}

const minSample = 10

// Allow checks whether a request should be allowed through.
// Returns true if the circuit is closed or half-open (one probe).
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		cooldown := time.Duration(cb.config.CooldownSeconds) * time.Second
		if now.Sub(cb.openedAt) >= cooldown {
			cb.state = StateHalfOpen
			return true // allow one probe
		}
		return false
	case StateHalfOpen:
		return false // only one probe at a time
	}
	return true
}

// RecordResult records the outcome of a request.
// Only 5xx status codes count as errors.
func (cb *CircuitBreaker) RecordResult(statusCode int) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	isError := statusCode >= 500

	switch cb.state {
	case StateHalfOpen:
		if isError {
			// Probe failed — reopen.
			cb.state = StateOpen
			cb.openedAt = now
		} else {
			// Probe succeeded — close.
			cb.state = StateClosed
			cb.total = 0
			cb.errors = 0
			cb.windowStart = now
		}
		return

	case StateClosed:
		// Reset window if expired.
		window := time.Duration(cb.config.WindowSeconds) * time.Second
		if now.Sub(cb.windowStart) >= window {
			cb.total = 0
			cb.errors = 0
			cb.windowStart = now
		}

		cb.total++
		if isError {
			cb.errors++
		}

		// Check if we should trip.
		if cb.total >= minSample {
			errorPct := (cb.errors * 100) / cb.total
			if errorPct >= cb.config.ErrorThresholdPct {
				cb.state = StateOpen
				cb.openedAt = now
			}
		}

	case StateOpen:
		// Shouldn't happen (Allow returns false), but record anyway.
	}
}

// GetState returns the current state as a string.
func (cb *CircuitBreaker) GetState() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	// Auto-transition to half-open if cooldown elapsed.
	if cb.state == StateOpen {
		cooldown := time.Duration(cb.config.CooldownSeconds) * time.Second
		if time.Now().Sub(cb.openedAt) >= cooldown {
			cb.state = StateHalfOpen
		}
	}
	return cb.state
}

// GetStats returns current state and error counters.
func (cb *CircuitBreaker) GetStats() Stats {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	errorPct := 0
	if cb.total > 0 {
		errorPct = (cb.errors * 100) / cb.total
	}
	state := cb.state
	if state == StateOpen {
		cooldown := time.Duration(cb.config.CooldownSeconds) * time.Second
		if time.Now().Sub(cb.openedAt) >= cooldown {
			state = StateHalfOpen
		}
	}
	return Stats{
		State:    state.String(),
		Total:    cb.total,
		Errors:   cb.errors,
		ErrorPct: errorPct,
	}
}

// Registry manages circuit breakers for multiple tools.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
}

// NewRegistry creates an empty circuit breaker registry.
func NewRegistry() *Registry {
	return &Registry{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// Get returns the circuit breaker for a tool, creating one if needed.
func (r *Registry) Get(toolID string, cfg Config) *CircuitBreaker {
	r.mu.RLock()
	cb, ok := r.breakers[toolID]
	r.mu.RUnlock()
	if ok {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after write lock.
	if cb, ok = r.breakers[toolID]; ok {
		return cb
	}
	cb = New(cfg)
	r.breakers[toolID] = cb
	return cb
}

// GetStats returns stats for a specific tool, or nil if not tracked.
func (r *Registry) GetStats(toolID string) *Stats {
	r.mu.RLock()
	cb, ok := r.breakers[toolID]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	stats := cb.GetStats()
	return &stats
}
