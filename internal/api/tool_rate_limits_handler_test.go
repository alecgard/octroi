package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/ratelimit"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Fake stores
// ---------------------------------------------------------------------------

type fakeRateLimitStore struct {
	overrides map[string][]ratelimit.ToolRateOverride // keyed by toolID
}

func newFakeRateLimitStore() *fakeRateLimitStore {
	return &fakeRateLimitStore{overrides: make(map[string][]ratelimit.ToolRateOverride)}
}

func (f *fakeRateLimitStore) ListByTool(_ context.Context, toolID string) ([]ratelimit.ToolRateOverride, error) {
	return f.overrides[toolID], nil
}

func (f *fakeRateLimitStore) Set(_ context.Context, toolID, scope, scopeID string, rate int) error {
	f.overrides[toolID] = append(f.overrides[toolID], ratelimit.ToolRateOverride{
		ID:        "override-1",
		ToolID:    toolID,
		Scope:     scope,
		ScopeID:   scopeID,
		RateLimit: rate,
	})
	return nil
}

func (f *fakeRateLimitStore) Delete(_ context.Context, toolID, scope, scopeID string) error {
	overrides, ok := f.overrides[toolID]
	if !ok {
		return pgx.ErrNoRows
	}
	for i, o := range overrides {
		if o.Scope == scope && o.ScopeID == scopeID {
			f.overrides[toolID] = append(overrides[:i], overrides[i+1:]...)
			return nil
		}
	}
	return pgx.ErrNoRows
}

type fakeRateLimitToolStore struct {
	tools map[string]*registry.Tool
}

func newFakeRateLimitToolStore() *fakeRateLimitToolStore {
	return &fakeRateLimitToolStore{tools: make(map[string]*registry.Tool)}
}

func (f *fakeRateLimitToolStore) GetByID(_ context.Context, id string) (*registry.Tool, error) {
	t, ok := f.tools[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// Router setup
// ---------------------------------------------------------------------------

func setupRateLimitsRouter(rlStore *fakeRateLimitStore, toolStore *fakeRateLimitToolStore) http.Handler {
	h := newToolRateLimitsHandler(rlStore, toolStore)
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
	r.Get("/tools/{toolID}/rate-limits", h.ListToolRateLimits)
	r.Put("/tools/{toolID}/rate-limits", h.SetToolRateLimit)
	r.Delete("/tools/{toolID}/rate-limits/{scope}/{scopeID}", h.DeleteToolRateLimit)
	return r
}

// ---------------------------------------------------------------------------
// ListToolRateLimits tests
// ---------------------------------------------------------------------------

func TestListToolRateLimits_Success(t *testing.T) {
	rlStore := newFakeRateLimitStore()
	toolStore := newFakeRateLimitToolStore()
	toolStore.tools["tool-1"] = &registry.Tool{
		ID:        "tool-1",
		Name:      "test-tool",
		RateLimit: 100,
		CreatedAt: time.Now().UTC(),
	}
	rlStore.overrides["tool-1"] = []ratelimit.ToolRateOverride{
		{ID: "o1", ToolID: "tool-1", Scope: "team", ScopeID: "eng", RateLimit: 200},
	}

	router := setupRateLimitsRouter(rlStore, toolStore)
	req := httptest.NewRequest(http.MethodGet, "/tools/tool-1/rate-limits", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["global_rate_limit"] != float64(100) {
		t.Errorf("expected global_rate_limit=100, got %v", resp["global_rate_limit"])
	}
	overrides, ok := resp["overrides"].([]interface{})
	if !ok {
		t.Fatalf("expected overrides to be an array, got %T", resp["overrides"])
	}
	if len(overrides) != 1 {
		t.Errorf("expected 1 override, got %d", len(overrides))
	}
}

func TestListToolRateLimits_ToolNotFound(t *testing.T) {
	rlStore := newFakeRateLimitStore()
	toolStore := newFakeRateLimitToolStore()

	router := setupRateLimitsRouter(rlStore, toolStore)
	req := httptest.NewRequest(http.MethodGet, "/tools/nonexistent/rate-limits", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "not_found" {
		t.Errorf("expected code=not_found, got %q", envelope.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// SetToolRateLimit tests
// ---------------------------------------------------------------------------

func TestSetToolRateLimit_Success(t *testing.T) {
	rlStore := newFakeRateLimitStore()
	toolStore := newFakeRateLimitToolStore()
	toolStore.tools["tool-1"] = &registry.Tool{ID: "tool-1", Name: "test-tool"}

	router := setupRateLimitsRouter(rlStore, toolStore)
	body := `{"scope":"team","scope_id":"eng","rate_limit":50}`
	req := httptest.NewRequest(http.MethodPut, "/tools/tool-1/rate-limits", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	if len(rlStore.overrides["tool-1"]) != 1 {
		t.Fatalf("expected 1 override stored, got %d", len(rlStore.overrides["tool-1"]))
	}
	o := rlStore.overrides["tool-1"][0]
	if o.Scope != "team" || o.ScopeID != "eng" || o.RateLimit != 50 {
		t.Errorf("unexpected override: %+v", o)
	}
}

func TestSetToolRateLimit_InvalidScope(t *testing.T) {
	rlStore := newFakeRateLimitStore()
	toolStore := newFakeRateLimitToolStore()
	toolStore.tools["tool-1"] = &registry.Tool{ID: "tool-1", Name: "test-tool"}

	router := setupRateLimitsRouter(rlStore, toolStore)
	body := `{"scope":"global","scope_id":"x","rate_limit":50}`
	req := httptest.NewRequest(http.MethodPut, "/tools/tool-1/rate-limits", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "invalid_params" {
		t.Errorf("expected code=invalid_params, got %q", envelope.Error.Code)
	}
}

func TestSetToolRateLimit_ToolNotFound(t *testing.T) {
	rlStore := newFakeRateLimitStore()
	toolStore := newFakeRateLimitToolStore()

	router := setupRateLimitsRouter(rlStore, toolStore)
	body := `{"scope":"team","scope_id":"eng","rate_limit":50}`
	req := httptest.NewRequest(http.MethodPut, "/tools/nonexistent/rate-limits", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "not_found" {
		t.Errorf("expected code=not_found, got %q", envelope.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// DeleteToolRateLimit tests
// ---------------------------------------------------------------------------

func TestDeleteToolRateLimit_Success(t *testing.T) {
	rlStore := newFakeRateLimitStore()
	toolStore := newFakeRateLimitToolStore()
	rlStore.overrides["tool-1"] = []ratelimit.ToolRateOverride{
		{ID: "o1", ToolID: "tool-1", Scope: "team", ScopeID: "eng", RateLimit: 200},
	}

	router := setupRateLimitsRouter(rlStore, toolStore)
	req := httptest.NewRequest(http.MethodDelete, "/tools/tool-1/rate-limits/team/eng", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	if len(rlStore.overrides["tool-1"]) != 0 {
		t.Errorf("expected override to be removed, got %d", len(rlStore.overrides["tool-1"]))
	}
}

func TestDeleteToolRateLimit_NotFound(t *testing.T) {
	rlStore := newFakeRateLimitStore()
	toolStore := newFakeRateLimitToolStore()

	router := setupRateLimitsRouter(rlStore, toolStore)
	req := httptest.NewRequest(http.MethodDelete, "/tools/tool-1/rate-limits/team/eng", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "not_found" {
		t.Errorf("expected code=not_found, got %q", envelope.Error.Code)
	}
}

func TestDeleteToolRateLimit_InvalidScope(t *testing.T) {
	rlStore := newFakeRateLimitStore()
	toolStore := newFakeRateLimitToolStore()

	router := setupRateLimitsRouter(rlStore, toolStore)
	req := httptest.NewRequest(http.MethodDelete, "/tools/tool-1/rate-limits/global/x", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "invalid_params" {
		t.Errorf("expected code=invalid_params, got %q", envelope.Error.Code)
	}
}
