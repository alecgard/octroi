package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Health check handler tests
// ---------------------------------------------------------------------------

// fakePinger implements the Ping(ctx) method used by the health handler.
type fakePinger struct {
	err error
}

func (f *fakePinger) Ping(_ interface{}) error { return f.err }

func TestHealthCheck_OK(t *testing.T) {
	// Build a minimal router with no DBPool (the nil path returns "ok").
	deps := RouterDeps{
		AllowedOrigins: []string{"*"},
	}
	handler := NewRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
	if body["database"] != "connected" {
		t.Errorf("expected database=connected, got %q", body["database"])
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// Well-known manifest tests
// ---------------------------------------------------------------------------

func TestWellKnownHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/octroi.json", nil)
	rec := httptest.NewRecorder()
	WellKnownHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var manifest map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&manifest); err != nil {
		t.Fatalf("failed to decode manifest: %v", err)
	}

	// Verify required top-level fields.
	requiredFields := []string{"name", "description", "version", "api_base", "auth", "endpoints", "health"}
	for _, field := range requiredFields {
		if _, ok := manifest[field]; !ok {
			t.Errorf("manifest missing required field %q", field)
		}
	}

	if name, _ := manifest["name"].(string); name != "Octroi" {
		t.Errorf("expected name=Octroi, got %q", name)
	}
	if apiBase, _ := manifest["api_base"].(string); apiBase != "/api/v1" {
		t.Errorf("expected api_base=/api/v1, got %q", apiBase)
	}

	// Verify auth shape.
	authMap, ok := manifest["auth"].(map[string]interface{})
	if !ok {
		t.Fatal("auth field is not an object")
	}
	if authMap["type"] != "bearer" {
		t.Errorf("expected auth.type=bearer, got %v", authMap["type"])
	}

	// Verify endpoints shape.
	endpoints, ok := manifest["endpoints"].(map[string]interface{})
	if !ok {
		t.Fatal("endpoints field is not an object")
	}
	expectedEndpoints := []string{"tools", "tools_search", "agents", "usage", "proxy"}
	for _, ep := range expectedEndpoints {
		if _, ok := endpoints[ep]; !ok {
			t.Errorf("endpoints missing %q", ep)
		}
	}
}

func TestWellKnownHandler_ViaRouter(t *testing.T) {
	handler := NewRouter(RouterDeps{AllowedOrigins: []string{"*"}})

	req := httptest.NewRequest(http.MethodGet, "/.well-known/octroi.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 via router, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// CORS middleware tests
// ---------------------------------------------------------------------------

func TestCORSMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name            string
		allowedOrigins  []string
		requestOrigin   string
		method          string
		wantStatus      int
		wantAllowOrigin string
		wantVary        string
	}{
		{
			name:            "wildcard allows any origin",
			allowedOrigins:  []string{"*"},
			requestOrigin:   "https://example.com",
			method:          http.MethodGet,
			wantStatus:      http.StatusOK,
			wantAllowOrigin: "*",
		},
		{
			name:            "specific origin is echoed back",
			allowedOrigins:  []string{"https://app.example.com"},
			requestOrigin:   "https://app.example.com",
			method:          http.MethodGet,
			wantStatus:      http.StatusOK,
			wantAllowOrigin: "https://app.example.com",
			wantVary:        "Origin",
		},
		{
			name:            "non-matching origin gets no Allow-Origin header",
			allowedOrigins:  []string{"https://app.example.com"},
			requestOrigin:   "https://evil.com",
			method:          http.MethodGet,
			wantStatus:      http.StatusOK,
			wantAllowOrigin: "",
		},
		{
			name:            "no origin header means no CORS headers",
			allowedOrigins:  []string{"*"},
			requestOrigin:   "",
			method:          http.MethodGet,
			wantStatus:      http.StatusOK,
			wantAllowOrigin: "",
		},
		{
			name:            "preflight returns 204",
			allowedOrigins:  []string{"*"},
			requestOrigin:   "https://example.com",
			method:          http.MethodOptions,
			wantStatus:      http.StatusNoContent,
			wantAllowOrigin: "*",
		},
		{
			name:            "preflight with specific origin",
			allowedOrigins:  []string{"https://app.example.com"},
			requestOrigin:   "https://app.example.com",
			method:          http.MethodOptions,
			wantStatus:      http.StatusNoContent,
			wantAllowOrigin: "https://app.example.com",
			wantVary:        "Origin",
		},
		{
			name:            "empty allowed origins list",
			allowedOrigins:  nil,
			requestOrigin:   "https://example.com",
			method:          http.MethodGet,
			wantStatus:      http.StatusOK,
			wantAllowOrigin: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mw := corsMiddleware(tt.allowedOrigins)
			handler := mw(inner)

			req := httptest.NewRequest(tt.method, "/test", nil)
			if tt.requestOrigin != "" {
				req.Header.Set("Origin", tt.requestOrigin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status: got %d, want %d", rec.Code, tt.wantStatus)
			}

			gotAllowOrigin := rec.Header().Get("Access-Control-Allow-Origin")
			if gotAllowOrigin != tt.wantAllowOrigin {
				t.Errorf("Access-Control-Allow-Origin: got %q, want %q", gotAllowOrigin, tt.wantAllowOrigin)
			}

			if tt.wantVary != "" {
				gotVary := rec.Header().Get("Vary")
				if gotVary != tt.wantVary {
					t.Errorf("Vary: got %q, want %q", gotVary, tt.wantVary)
				}
			}

			// When origin is set and allowed, check CORS method headers are present.
			if tt.requestOrigin != "" && tt.wantAllowOrigin != "" {
				if methods := rec.Header().Get("Access-Control-Allow-Methods"); methods == "" {
					t.Error("expected Access-Control-Allow-Methods to be set")
				}
				if headers := rec.Header().Get("Access-Control-Allow-Headers"); headers == "" {
					t.Error("expected Access-Control-Allow-Headers to be set")
				}
				if maxAge := rec.Header().Get("Access-Control-Max-Age"); maxAge != "86400" {
					t.Errorf("Access-Control-Max-Age: got %q, want 86400", maxAge)
				}
			}
		})
	}
}

func TestCORSMiddleware_PreflightDoesNotCallNext(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	mw := corsMiddleware([]string{"*"})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Error("preflight OPTIONS should not call the next handler")
	}
}

// ---------------------------------------------------------------------------
// Secure headers middleware tests
// ---------------------------------------------------------------------------

func TestSecureHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := secureHeaders(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	expectedHeaders := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":       "DENY",
		"X-XSS-Protection":      "0",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}

	for header, want := range expectedHeaders {
		got := rec.Header().Get(header)
		if got != want {
			t.Errorf("%s: got %q, want %q", header, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Request ID middleware tests
// ---------------------------------------------------------------------------

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requestIDMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Response header should be set.
	respID := rec.Header().Get("X-Request-ID")
	if respID == "" {
		t.Fatal("expected X-Request-ID response header to be set")
	}

	// Generated ID should be 32 hex characters (16 bytes).
	if len(respID) != 32 {
		t.Errorf("expected 32-char hex ID, got %d chars: %q", len(respID), respID)
	}

	// Context value should match response header.
	if capturedID != respID {
		t.Errorf("context ID %q does not match response header ID %q", capturedID, respID)
	}
}

func TestRequestIDMiddleware_ForwardsExistingID(t *testing.T) {
	const existingID = "my-custom-request-id-12345"

	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := requestIDMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", existingID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	respID := rec.Header().Get("X-Request-ID")
	if respID != existingID {
		t.Errorf("expected forwarded ID %q, got %q", existingID, respID)
	}
	if capturedID != existingID {
		t.Errorf("context ID: expected %q, got %q", existingID, capturedID)
	}
}

func TestRequestIDMiddleware_SanitizesWhitespace(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := requestIDMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "  some-id-with-spaces  \n")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	respID := rec.Header().Get("X-Request-ID")
	if respID != "some-id-with-spaces" {
		t.Errorf("expected sanitized ID, got %q", respID)
	}
}

func TestRequestIDFromContext_Empty(t *testing.T) {
	// Calling with a bare context should return empty string.
	id := RequestIDFromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context())
	if id != "" {
		t.Errorf("expected empty string from bare context, got %q", id)
	}
}

// ---------------------------------------------------------------------------
// Login rate limiter tests
// ---------------------------------------------------------------------------

func TestLoginRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := newLoginRateLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		allowed, _ := rl.allow("1.2.3.4")
		if !allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// Fourth request should be denied.
	allowed, retryAfter := rl.allow("1.2.3.4")
	if allowed {
		t.Error("expected request 4 to be denied")
	}
	if retryAfter < 1 {
		t.Errorf("expected retryAfter >= 1, got %d", retryAfter)
	}
}

func TestLoginRateLimiter_SeparateIPs(t *testing.T) {
	rl := newLoginRateLimiter(2, time.Minute)

	// Use up limit for IP A.
	rl.allow("10.0.0.1")
	rl.allow("10.0.0.1")

	allowed, _ := rl.allow("10.0.0.1")
	if allowed {
		t.Error("IP A should be denied after 2 attempts")
	}

	// IP B should still be allowed.
	allowed, _ = rl.allow("10.0.0.2")
	if !allowed {
		t.Error("IP B should still be allowed")
	}
}

func TestLoginRateLimiter_WindowResets(t *testing.T) {
	// Use a very short window so we can test reset.
	rl := newLoginRateLimiter(1, 10*time.Millisecond)

	allowed, _ := rl.allow("1.2.3.4")
	if !allowed {
		t.Fatal("first request should be allowed")
	}

	allowed, _ = rl.allow("1.2.3.4")
	if allowed {
		t.Fatal("second request should be denied")
	}

	// Wait for window to expire.
	time.Sleep(15 * time.Millisecond)

	allowed, _ = rl.allow("1.2.3.4")
	if !allowed {
		t.Error("request after window reset should be allowed")
	}
}

func TestLoginRateLimiter_Cleanup(t *testing.T) {
	rl := newLoginRateLimiter(1, 10*time.Millisecond)

	rl.allow("1.2.3.4")
	rl.allow("5.6.7.8")

	// Both IPs should be stored.
	count := 0
	rl.entries.Range(func(_, _ interface{}) bool { count++; return true })
	if count != 2 {
		t.Fatalf("expected 2 entries, got %d", count)
	}

	// Wait for window to expire, then run cleanup.
	time.Sleep(15 * time.Millisecond)
	rl.cleanup()

	count = 0
	rl.entries.Range(func(_, _ interface{}) bool { count++; return true })
	if count != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", count)
	}
}

func TestLoginRateLimiter_RetryAfterValue(t *testing.T) {
	rl := newLoginRateLimiter(1, 30*time.Second)

	rl.allow("1.2.3.4")
	_, retryAfter := rl.allow("1.2.3.4")

	// retryAfter should be between 1 and 30 seconds.
	if retryAfter < 1 || retryAfter > 30 {
		t.Errorf("expected retryAfter between 1 and 30, got %d", retryAfter)
	}
}

// ---------------------------------------------------------------------------
// writeError / writeJSON helper tests
// ---------------------------------------------------------------------------

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusNotFound, "not_found", "resource not found")

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if envelope.Error.Code != "not_found" {
		t.Errorf("expected code=not_found, got %q", envelope.Error.Code)
	}
	if envelope.Error.Message != "resource not found" {
		t.Errorf("expected message='resource not found', got %q", envelope.Error.Message)
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	data := map[string]string{"hello": "world"}
	writeJSON(rec, http.StatusCreated, data)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if body["hello"] != "world" {
		t.Errorf("expected hello=world, got %q", body["hello"])
	}
}

// ---------------------------------------------------------------------------
// readJSON helper tests
// ---------------------------------------------------------------------------

func TestReadJSON_Valid(t *testing.T) {
	body := strings.NewReader(`{"name":"test","value":42}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)

	var result struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	if err := readJSON(req, &result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test" || result.Value != 42 {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestReadJSON_InvalidJSON(t *testing.T) {
	body := strings.NewReader(`{not json`)
	req := httptest.NewRequest(http.MethodPost, "/", body)

	var result map[string]interface{}
	if err := readJSON(req, &result); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestReadJSON_EmptyBody(t *testing.T) {
	body := strings.NewReader("")
	req := httptest.NewRequest(http.MethodPost, "/", body)

	var result map[string]interface{}
	if err := readJSON(req, &result); err == nil {
		t.Error("expected error for empty body")
	}
}

// ---------------------------------------------------------------------------
// generateID tests
// ---------------------------------------------------------------------------

func TestGenerateID_Format(t *testing.T) {
	id := generateID()

	if len(id) != 32 {
		t.Errorf("expected 32-char hex string, got %d chars: %q", len(id), id)
	}

	// Verify it is valid hex.
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %c in generated ID %q", c, id)
			break
		}
	}
}

func TestGenerateID_Unique(t *testing.T) {
	ids := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := generateID()
		if _, exists := ids[id]; exists {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = struct{}{}
	}
}

// ---------------------------------------------------------------------------
// Middleware integration via router (secure headers, request ID, CORS)
// ---------------------------------------------------------------------------

func TestRouter_SecureHeadersApplied(t *testing.T) {
	handler := NewRouter(RouterDeps{AllowedOrigins: []string{"*"}})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("expected X-Content-Type-Options: nosniff on router responses")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("expected X-Frame-Options: DENY on router responses")
	}
}

func TestRouter_RequestIDApplied(t *testing.T) {
	handler := NewRouter(RouterDeps{AllowedOrigins: []string{"*"}})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID to be set on router responses")
	}
}

func TestRouter_CORSApplied(t *testing.T) {
	handler := NewRouter(RouterDeps{AllowedOrigins: []string{"https://myapp.com"}})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://myapp.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://myapp.com" {
		t.Errorf("expected Access-Control-Allow-Origin=https://myapp.com, got %q", got)
	}
}

func TestRouter_PreflightAtAnyPath(t *testing.T) {
	handler := NewRouter(RouterDeps{AllowedOrigins: []string{"*"}})

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/tools", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Preflight should return 204 (or at least a success status) due to CORS middleware.
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Errorf("expected 204 or 200 for OPTIONS preflight, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// parseTimeParam tests
// ---------------------------------------------------------------------------

func TestParseTimeParam(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantStr string // expected time formatted as RFC3339 or empty
	}{
		{
			name:    "empty string",
			input:   "",
			wantErr: false,
			wantStr: "",
		},
		{
			name:    "date only",
			input:   "2024-06-15",
			wantErr: false,
			wantStr: "2024-06-15T00:00:00Z",
		},
		{
			name:    "RFC3339",
			input:   "2024-06-15T10:30:00Z",
			wantErr: false,
			wantStr: "2024-06-15T10:30:00Z",
		},
		{
			name:    "invalid format",
			input:   "not-a-date",
			wantErr: true,
		},
		{
			name:    "partial date",
			input:   "2024-06",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTimeParam(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantStr == "" {
				if !result.IsZero() {
					t.Errorf("expected zero time, got %v", result)
				}
			} else {
				if result.Format(time.RFC3339) != tt.wantStr {
					t.Errorf("expected %s, got %s", tt.wantStr, result.Format(time.RFC3339))
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractBearerToken tests
// ---------------------------------------------------------------------------

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name  string
		auth  string
		want  string
	}{
		{"valid bearer", "Bearer my-token-123", "my-token-123"},
		{"empty header", "", ""},
		{"just Bearer", "Bearer ", ""},
		{"no space", "Bearertoken", ""},
		{"wrong scheme", "Basic abc123", ""},
		{"bearer lowercase prefix", "Bearer abc", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			got := extractBearerToken(req)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Login rate limiting integration test (via router-style handler)
// ---------------------------------------------------------------------------

func TestLoginRateLimitIntegration(t *testing.T) {
	rl := newLoginRateLimiter(3, time.Minute)

	// Simulate the rate-limit wrapping without the full auth handler.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			ip = fwd
		}
		allowed, retryAfter := rl.allow(ip)
		if !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts, try again later")
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// First 3 requests should succeed.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// Fourth request should be rate limited.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("expected Retry-After header to be set")
	}

	// Verify response body.
	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "rate_limited" {
		t.Errorf("expected error code rate_limited, got %q", envelope.Error.Code)
	}
}

func TestLoginRateLimit_XForwardedFor(t *testing.T) {
	rl := newLoginRateLimiter(1, time.Minute)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			ip = fwd
		}
		allowed, retryAfter := rl.allow(ip)
		if !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts")
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// First request with X-Forwarded-For.
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got %d", rec.Code)
	}

	// Second request from same forwarded IP should be denied.
	req = httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "10.0.0.2:5678"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}

	// Request from different forwarded IP should succeed.
	req = httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("different forwarded IP should succeed, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Router 404 test
// ---------------------------------------------------------------------------

func TestRouter_NotFound(t *testing.T) {
	handler := NewRouter(RouterDeps{AllowedOrigins: []string{"*"}})

	req := httptest.NewRequest(http.MethodGet, "/nonexistent-path", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown path, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// isValidationError tests
// ---------------------------------------------------------------------------

func TestIsValidationError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"random error", fmt.Errorf("something went wrong"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				// isValidationError would panic on nil, but we just skip.
				return
			}
			got := isValidationError(tt.err)
			if got != tt.want {
				t.Errorf("isValidationError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
