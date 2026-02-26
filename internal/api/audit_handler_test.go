package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alecgard/octroi/internal/audit"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// Fake audit store
// ---------------------------------------------------------------------------

type fakeAuditStore struct {
	entries []audit.Entry
}

func newFakeAuditStore() *fakeAuditStore {
	return &fakeAuditStore{}
}

func (f *fakeAuditStore) List(_ context.Context, params audit.ListParams) ([]audit.Entry, string, error) {
	var filtered []audit.Entry
	for _, e := range f.entries {
		if params.ResourceType != "" && e.ResourceType != params.ResourceType {
			continue
		}
		filtered = append(filtered, e)
	}
	if filtered == nil {
		filtered = []audit.Entry{}
	}
	return filtered, "", nil
}

// ---------------------------------------------------------------------------
// Router setup
// ---------------------------------------------------------------------------

func setupAuditRouter(store *fakeAuditStore) http.Handler {
	h := newAuditHandler(store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.ContextWithUser(req.Context(), &auth.User{
				ID:   "admin-1",
				Role: "org_admin",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/audit-log", h.ListAuditLog)
	return r
}

// ---------------------------------------------------------------------------
// ListAuditLog tests
// ---------------------------------------------------------------------------

func TestListAuditLog_Success(t *testing.T) {
	store := newFakeAuditStore()
	userID := "user-1"
	store.entries = []audit.Entry{
		{
			ID:           "entry-1",
			Timestamp:    time.Now().UTC(),
			UserID:       &userID,
			Action:       "create",
			ResourceType: "tool",
			ResourceID:   "tool-1",
			Details:      map[string]any{"name": "test-tool"},
			IP:           "127.0.0.1",
			RequestID:    "req-1",
		},
		{
			ID:           "entry-2",
			Timestamp:    time.Now().UTC(),
			Action:       "delete",
			ResourceType: "agent",
			ResourceID:   "agent-1",
			Details:      map[string]any{},
			IP:           "127.0.0.1",
			RequestID:    "req-2",
		},
	}

	router := setupAuditRouter(store)
	req := httptest.NewRequest(http.MethodGet, "/audit-log", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	entries, ok := resp["entries"].([]interface{})
	if !ok {
		t.Fatalf("expected entries to be an array, got %T", resp["entries"])
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	first := entries[0].(map[string]interface{})
	if first["action"] != "create" {
		t.Errorf("expected action=create, got %v", first["action"])
	}
	if first["resource_type"] != "tool" {
		t.Errorf("expected resource_type=tool, got %v", first["resource_type"])
	}
}

func TestListAuditLog_Empty(t *testing.T) {
	store := newFakeAuditStore()
	router := setupAuditRouter(store)

	req := httptest.NewRequest(http.MethodGet, "/audit-log", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	entries, ok := resp["entries"].([]interface{})
	if !ok {
		t.Fatalf("expected entries to be an array, got %T", resp["entries"])
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}

	if resp["next_cursor"] != "" {
		t.Errorf("expected empty next_cursor, got %v", resp["next_cursor"])
	}
}

func TestListAuditLog_WithResourceTypeFilter(t *testing.T) {
	store := newFakeAuditStore()
	store.entries = []audit.Entry{
		{
			ID:           "entry-1",
			Timestamp:    time.Now().UTC(),
			Action:       "create",
			ResourceType: "tool",
			ResourceID:   "tool-1",
			Details:      map[string]any{},
			IP:           "127.0.0.1",
			RequestID:    "req-1",
		},
		{
			ID:           "entry-2",
			Timestamp:    time.Now().UTC(),
			Action:       "delete",
			ResourceType: "agent",
			ResourceID:   "agent-1",
			Details:      map[string]any{},
			IP:           "127.0.0.1",
			RequestID:    "req-2",
		},
	}

	router := setupAuditRouter(store)
	req := httptest.NewRequest(http.MethodGet, "/audit-log?resource_type=tool", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	entries, ok := resp["entries"].([]interface{})
	if !ok {
		t.Fatalf("expected entries to be an array, got %T", resp["entries"])
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (filtered to tool), got %d", len(entries))
	}

	first := entries[0].(map[string]interface{})
	if first["resource_type"] != "tool" {
		t.Errorf("expected resource_type=tool, got %v", first["resource_type"])
	}
}
