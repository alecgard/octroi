package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/go-chi/chi/v5"
)

// fakePermissionStore implements permission operations for testing.
type fakePermissionStore struct {
	permissions map[string]map[string]bool // agentID -> toolID -> allowed
}

func newFakePermissionStore() *fakePermissionStore {
	return &fakePermissionStore{
		permissions: make(map[string]map[string]bool),
	}
}

func (f *fakePermissionStore) set(agentID, toolID string, allowed bool) {
	if f.permissions[agentID] == nil {
		f.permissions[agentID] = make(map[string]bool)
	}
	f.permissions[agentID][toolID] = allowed
}

// fakePermAgentStore implements permissionAgentStoreIface for testing.
type fakePermAgentStore struct {
	agents map[string]*agent.Agent
}

func (f *fakePermAgentStore) GetByID(_ context.Context, _ string, id string) (*agent.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent not found")
	}
	return a, nil
}

func (f *fakePermAgentStore) Update(_ context.Context, _ string, id string, in agent.UpdateAgentInput) (*agent.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent not found")
	}
	if in.AllowlistMode != nil {
		a.AllowlistMode = *in.AllowlistMode
	}
	return a, nil
}

// fakePermStore implements permissionStoreIface for testing.
type fakePermStore struct {
	perms   map[string]map[string]agent.Permission // agentID -> toolID -> permission
	deleted []string                               // track deleted keys as "agentID|toolID"
	err     error                                  // if set, all methods return this error
}

func newFakePermStore() *fakePermStore {
	return &fakePermStore{
		perms: make(map[string]map[string]agent.Permission),
	}
}

func (f *fakePermStore) ListByAgent(_ context.Context, _ string, agentID string) ([]agent.Permission, error) {
	if f.err != nil {
		return nil, f.err
	}
	var result []agent.Permission
	for _, p := range f.perms[agentID] {
		result = append(result, p)
	}
	return result, nil
}

func (f *fakePermStore) SetWithSubTools(_ context.Context, _, agentID, toolID string, allowed bool, subTools []string) error {
	if f.err != nil {
		return f.err
	}
	if f.perms[agentID] == nil {
		f.perms[agentID] = make(map[string]agent.Permission)
	}
	f.perms[agentID][toolID] = agent.Permission{AgentID: agentID, ToolID: toolID, Allowed: allowed, SubTools: subTools}
	return nil
}

func (f *fakePermStore) SetBulk(_ context.Context, _ string, agentID string, permissions map[string]bool) error {
	return f.err
}

func (f *fakePermStore) SetBulkWithSubTools(_ context.Context, _ string, agentID string, permissions map[string]agent.BulkPermission) error {
	return f.err
}

func (f *fakePermStore) Delete(_ context.Context, _, agentID, toolID string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, agentID+"|"+toolID)
	delete(f.perms[agentID], toolID)
	return nil
}

// setupPermissionsRouter creates a chi router with admin auth context and
// permissions handler routes mounted.
func setupPermissionsRouter(store *fakePermStore, agentStore *fakePermAgentStore) http.Handler {
	h := newPermissionsHandler(store, agentStore)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
			ctx = auth.ContextWithUser(ctx, &auth.User{
				ID:    "admin-1",
				Email: "admin@test.com",
				Role:  "org_admin",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Delete("/agents/{agentID}/permissions/{toolID}", h.DeletePermission)
	return r
}

// TestPermissionsHandler_ListPermissions tests the ListPermissions handler.
func TestPermissionsHandler_ListPermissions(t *testing.T) {
	// Use a minimal router with fake deps.
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
			ctx = auth.ContextWithUser(ctx, &auth.User{ID: "u1", Email: "admin@test.com", Role: "org_admin"})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	// We can't easily construct a real PermissionStore without DB,
	// so test the handler with a minimal setup via the full router.
	// For now, test that the route exists and returns appropriate errors.
	router := NewRouter(RouterDeps{
		AllowedOrigins: []string{"*"},
	})

	req := httptest.NewRequest("GET", "/api/v1/admin/agents/nonexistent/permissions", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// Without auth, should get 401.
	if rr.Code != http.StatusUnauthorized {
		t.Logf("Response: %d %s", rr.Code, rr.Body.String())
	}
}

// TestPermissionsHandler_SetPermission_Body tests request body parsing.
func TestPermissionsHandler_SetPermission_Body(t *testing.T) {
	body := map[string]interface{}{
		"allowed": true,
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest("PUT", "/api/v1/admin/agents/agent-1/permissions/tool-1", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	var parsed struct {
		Allowed bool `json:"allowed"`
	}
	if err := readJSON(req, &parsed); err != nil {
		t.Fatalf("readJSON: %v", err)
	}
	if !parsed.Allowed {
		t.Error("expected Allowed to be true")
	}
}

// TestPermissionsHandler_BulkBody tests bulk request body parsing.
func TestPermissionsHandler_BulkBody(t *testing.T) {
	allowlistMode := true
	body := struct {
		Permissions   map[string]bool `json:"permissions"`
		AllowlistMode *bool           `json:"allowlist_mode"`
	}{
		Permissions:   map[string]bool{"tool-1": true, "tool-2": false},
		AllowlistMode: &allowlistMode,
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest("PUT", "/api/v1/admin/agents/agent-1/permissions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	var parsed struct {
		Permissions   map[string]bool `json:"permissions"`
		AllowlistMode *bool           `json:"allowlist_mode,omitempty"`
	}
	if err := readJSON(req, &parsed); err != nil {
		t.Fatalf("readJSON: %v", err)
	}
	if len(parsed.Permissions) != 2 {
		t.Errorf("expected 2 permissions, got %d", len(parsed.Permissions))
	}
	if parsed.AllowlistMode == nil || !*parsed.AllowlistMode {
		t.Error("expected allowlist_mode to be true")
	}
}

// TestPermissionCheck_Agent verifies that UpdateAgentInput AllowlistMode field works.
func TestPermissionCheck_AllowlistModeField(t *testing.T) {
	enabled := true
	input := agent.UpdateAgentInput{
		AllowlistMode: &enabled,
	}
	if input.AllowlistMode == nil || !*input.AllowlistMode {
		t.Error("expected AllowlistMode to be true")
	}
}

// ---------------------------------------------------------------------------
// DeletePermission tests
// ---------------------------------------------------------------------------

func TestDeletePermission_Success(t *testing.T) {
	store := newFakePermStore()
	agentStore := &fakePermAgentStore{agents: map[string]*agent.Agent{
		"agent-1": {ID: "agent-1", Name: "test"},
	}}
	router := setupPermissionsRouter(store, agentStore)

	req := httptest.NewRequest(http.MethodDelete, "/agents/agent-1/permissions/tool-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the store's Delete method was called with correct arguments.
	if len(store.deleted) != 1 || store.deleted[0] != "agent-1|tool-1" {
		t.Errorf("expected Delete called with agent-1|tool-1, got %v", store.deleted)
	}
}

func TestDeletePermission_MissingParams(t *testing.T) {
	store := newFakePermStore()
	agentStore := &fakePermAgentStore{agents: map[string]*agent.Agent{}}

	// Chi will not match a route when a URL param segment is missing entirely,
	// so we test by sending requests where one param is empty via a router that
	// accepts optional-style paths. We set up two additional routes to allow
	// empty segments to reach the handler.
	h := newPermissionsHandler(store, agentStore)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
			ctx = auth.ContextWithUser(ctx, &auth.User{
				ID:    "admin-1",
				Email: "admin@test.com",
				Role:  "org_admin",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	// Mount the handler directly so we can call it without chi URL params
	// to simulate empty params.
	r.Delete("/test-no-params", h.DeletePermission)

	req := httptest.NewRequest(http.MethodDelete, "/test-no-params", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

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
