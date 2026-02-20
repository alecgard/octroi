package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- mock store ---

type mockAgentLookup struct {
	agents map[string]*Agent
}

func (m *mockAgentLookup) GetByKeyHash(ctx context.Context, hash string) (*Agent, error) {
	agent, ok := m.agents[hash]
	if !ok {
		return nil, errors.New("not found")
	}
	return agent, nil
}

// --- GenerateAPIKey tests ---

func TestGenerateAPIKey_PrefixAndLength(t *testing.T) {
	key, plaintext, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error: %v", err)
	}

	if !strings.HasPrefix(plaintext, "octroi_") {
		t.Errorf("plaintext key should start with 'octroi_', got %q", plaintext)
	}

	// "octroi_" (7) + 32 random chars = 39
	if len(plaintext) != 39 {
		t.Errorf("expected plaintext length 39, got %d", len(plaintext))
	}

	if key.Prefix != plaintext[:14] {
		t.Errorf("expected prefix %q, got %q", plaintext[:14], key.Prefix)
	}

	if key.Hash == "" {
		t.Error("expected non-empty hash")
	}
}

func TestGenerateAPIKey_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		_, plaintext, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey() error: %v", err)
		}
		if seen[plaintext] {
			t.Fatalf("duplicate key generated: %s", plaintext)
		}
		seen[plaintext] = true
	}
}

// --- HashKey tests ---

func TestHashKey_Deterministic(t *testing.T) {
	key := "octroi_testkey1234567890abcdefghij"
	h1 := HashKey(key)
	h2 := HashKey(key)
	if h1 != h2 {
		t.Errorf("HashKey should be deterministic: %q != %q", h1, h2)
	}
}

func TestHashKey_DifferentInputs(t *testing.T) {
	h1 := HashKey("octroi_key_aaa")
	h2 := HashKey("octroi_key_bbb")
	if h1 == h2 {
		t.Error("different keys should produce different hashes")
	}
}

func TestHashKey_Length(t *testing.T) {
	h := HashKey("anything")
	// SHA-256 produces 64 hex characters
	if len(h) != 64 {
		t.Errorf("expected hash length 64, got %d", len(h))
	}
}

// --- Context helpers tests ---

func TestAgentContext_RoundTrip(t *testing.T) {
	agent := &Agent{ID: "a1", Name: "test-agent", Team: "backend", RateLimit: 100}
	ctx := ContextWithAgent(context.Background(), agent)
	got := AgentFromContext(ctx)
	if got == nil {
		t.Fatal("expected agent from context, got nil")
	}
	if got.ID != agent.ID {
		t.Errorf("expected ID %q, got %q", agent.ID, got.ID)
	}
}

func TestAgentFromContext_Empty(t *testing.T) {
	got := AgentFromContext(context.Background())
	if got != nil {
		t.Errorf("expected nil from empty context, got %+v", got)
	}
}

// --- AgentAuthMiddleware tests ---

func TestAgentAuthMiddleware(t *testing.T) {
	plaintext := "octroi_validkey1234567890abcdefgh"
	hash := HashKey(plaintext)

	store := &mockAgentLookup{
		agents: map[string]*Agent{
			hash: {ID: "agent-1", Name: "TestAgent", Team: "platform", RateLimit: 60},
		},
	}
	svc := NewService(store)

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent := AgentFromContext(r.Context())
		if agent == nil {
			t.Error("expected agent in context inside handler")
		}
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
		wantError  bool
	}{
		{
			name:       "valid key",
			authHeader: "Bearer " + plaintext,
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "invalid key",
			authHeader: "Bearer octroi_wrongkey000000000000000000",
			wantStatus: http.StatusUnauthorized,
			wantError:  true,
		},
		{
			name:       "missing header",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
			wantError:  true,
		},
		{
			name:       "malformed header no bearer",
			authHeader: "Token " + plaintext,
			wantStatus: http.StatusUnauthorized,
			wantError:  true,
		},
		{
			name:       "bearer only no token",
			authHeader: "Bearer",
			wantStatus: http.StatusUnauthorized,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()

			handler := AgentAuthMiddleware(svc)(okHandler)
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, rr.Code)
			}

			if tt.wantError {
				assertJSONError(t, rr)
			}
		})
	}
}

// --- AdminAuthMiddleware tests ---

func TestAdminAuthMiddleware(t *testing.T) {
	adminKey := "super-secret-admin-key"

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
		wantError  bool
	}{
		{
			name:       "valid admin key",
			authHeader: "Bearer " + adminKey,
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "wrong admin key",
			authHeader: "Bearer wrong-key",
			wantStatus: http.StatusUnauthorized,
			wantError:  true,
		},
		{
			name:       "missing header",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
			wantError:  true,
		},
		{
			name:       "malformed header",
			authHeader: "Basic " + adminKey,
			wantStatus: http.StatusUnauthorized,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()

			handler := AdminAuthMiddleware(adminKey)(okHandler)
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, rr.Code)
			}

			if tt.wantError {
				assertJSONError(t, rr)
			}
		})
	}
}

// assertJSONError checks that the response body contains the expected error JSON structure.
func assertJSONError(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var resp errorResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if resp.Error.Code != "unauthorized" {
		t.Errorf("expected error code 'unauthorized', got %q", resp.Error.Code)
	}
	if resp.Error.Message == "" {
		t.Error("expected non-empty error message")
	}
}
