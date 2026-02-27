package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Fake agent store
// ---------------------------------------------------------------------------

type fakeAgentStore struct {
	agents map[string]*agent.Agent
	nextID int
}

func newFakeAgentStore() *fakeAgentStore {
	return &fakeAgentStore{
		agents: make(map[string]*agent.Agent),
	}
}

func (f *fakeAgentStore) Create(_ context.Context, in agent.CreateAgentInput) (*agent.Agent, error) {
	f.nextID++
	a := &agent.Agent{
		ID:           fmt.Sprintf("agent-%d", f.nextID),
		Name:         in.Name,
		APIKeyHash:   in.APIKeyHash,
		APIKeyPrefix: in.APIKeyPrefix,
		Team:         in.Team,
		RateLimit:    in.RateLimit,
		CreatedAt:    time.Now().UTC(),
	}
	f.agents[a.ID] = a
	return a, nil
}

func (f *fakeAgentStore) GetByID(_ context.Context, id string) (*agent.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return a, nil
}

func (f *fakeAgentStore) Update(_ context.Context, id string, in agent.UpdateAgentInput) (*agent.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	if in.Name != nil {
		a.Name = *in.Name
	}
	if in.Team != nil {
		a.Team = *in.Team
	}
	if in.RateLimit != nil {
		a.RateLimit = *in.RateLimit
	}
	if in.AllowlistMode != nil {
		a.AllowlistMode = *in.AllowlistMode
	}
	return a, nil
}

func (f *fakeAgentStore) Archive(_ context.Context, id string) error {
	if _, ok := f.agents[id]; !ok {
		return fmt.Errorf("agent not found")
	}
	delete(f.agents, id)
	return nil
}

func (f *fakeAgentStore) List(_ context.Context, _ agent.AgentListParams) ([]*agent.Agent, string, error) {
	var list []*agent.Agent
	for _, a := range f.agents {
		list = append(list, a)
	}
	return list, "", nil
}

func (f *fakeAgentStore) RegenerateKey(_ context.Context, id, newHash, newPrefix string) (*agent.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	a.APIKeyHash = newHash
	a.APIKeyPrefix = newPrefix
	return a, nil
}

// ---------------------------------------------------------------------------
// Fake budget store
// ---------------------------------------------------------------------------

type fakeBudgetStore struct {
	budgets map[string]*agent.Budget // key: agentID|toolID
	nextID  int
}

func newFakeBudgetStore() *fakeBudgetStore {
	return &fakeBudgetStore{
		budgets: make(map[string]*agent.Budget),
	}
}

func (f *fakeBudgetStore) Set(_ context.Context, in agent.CreateBudgetInput) (*agent.Budget, error) {
	key := in.AgentID + "|" + in.ToolID
	if b, ok := f.budgets[key]; ok {
		b.DailyLimit = in.DailyLimit
		b.MonthlyLimit = in.MonthlyLimit
		return b, nil
	}
	f.nextID++
	b := &agent.Budget{
		ID:           fmt.Sprintf("budget-%d", f.nextID),
		AgentID:      in.AgentID,
		ToolID:       in.ToolID,
		DailyLimit:   in.DailyLimit,
		MonthlyLimit: in.MonthlyLimit,
	}
	f.budgets[key] = b
	return b, nil
}

func (f *fakeBudgetStore) Get(_ context.Context, agentID, toolID string) (*agent.Budget, error) {
	key := agentID + "|" + toolID
	b, ok := f.budgets[key]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return b, nil
}

func (f *fakeBudgetStore) ListByAgent(_ context.Context, agentID string) ([]*agent.Budget, error) {
	var list []*agent.Budget
	for _, b := range f.budgets {
		if b.AgentID == agentID {
			list = append(list, b)
		}
	}
	return list, nil
}

// ---------------------------------------------------------------------------
// Test router setup helpers
// ---------------------------------------------------------------------------

// setupAdminAgentsRouter creates a chi router with admin context injected and
// all agents handler routes mounted.
func setupAdminAgentsRouter(store *fakeAgentStore, budgetStore *fakeBudgetStore) http.Handler {
	h := newAgentsHandler(store, budgetStore)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.ContextWithUser(req.Context(), &auth.User{
				ID:    "admin-1",
				Email: "admin@test.com",
				Role:  "org_admin",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/agents", h.CreateAgent)
	r.Get("/agents", h.ListAgents)
	r.Put("/agents/{id}", h.UpdateAgent)
	r.Delete("/agents/{id}", h.DeleteAgent)
	r.Post("/agents/{id}/regenerate-key", h.RegenerateKey)
	r.Put("/agents/{agentID}/budgets/{toolID}", h.SetBudget)
	r.Get("/agents/{agentID}/budgets/{toolID}", h.GetBudget)
	r.Get("/agents/{agentID}/budgets", h.ListBudgets)
	return r
}

// setupAgentAuthRouter creates a chi router with agent auth context injected
// for agent-authed routes.
func setupAgentAuthRouter(store *fakeAgentStore) http.Handler {
	h := newAgentsHandler(store, nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ag := auth.AgentFromContext(req.Context())
			if ag != nil {
				next.ServeHTTP(w, req)
				return
			}
			// Default test agent context.
			ctx := auth.ContextWithAgent(req.Context(), &auth.Agent{
				ID:   "agent-1",
				Name: "test-agent",
				Team: "eng",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/agents/me", h.GetSelfAgent)
	return r
}

// ---------------------------------------------------------------------------
// CreateAgent tests
// ---------------------------------------------------------------------------

func TestCreateAgent_Success(t *testing.T) {
	store := newFakeAgentStore()
	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	body := `{"name":"test","team":"eng"}`
	req := httptest.NewRequest(http.MethodPost, "/agents", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["name"] != "test" {
		t.Errorf("expected name=test, got %v", resp["name"])
	}
	if resp["team"] != "eng" {
		t.Errorf("expected team=eng, got %v", resp["team"])
	}
	if resp["api_key"] == nil || resp["api_key"] == "" {
		t.Error("expected api_key to be present in response")
	}
	if resp["id"] == nil || resp["id"] == "" {
		t.Error("expected id to be present in response")
	}
	if resp["api_key_prefix"] == nil || resp["api_key_prefix"] == "" {
		t.Error("expected api_key_prefix to be present in response")
	}
}

func TestCreateAgent_MissingName(t *testing.T) {
	store := newFakeAgentStore()
	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	body := `{"team":"eng"}`
	req := httptest.NewRequest(http.MethodPost, "/agents", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "validation_error" {
		t.Errorf("expected code=validation_error, got %q", envelope.Error.Code)
	}
}

func TestCreateAgent_EmptyBody(t *testing.T) {
	store := newFakeAgentStore()
	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	req := httptest.NewRequest(http.MethodPost, "/agents", strings.NewReader(""))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "invalid_body" {
		t.Errorf("expected code=invalid_body, got %q", envelope.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// ListAgents tests
// ---------------------------------------------------------------------------

func TestListAgents_Empty(t *testing.T) {
	store := newFakeAgentStore()
	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	// The handler writes the store result directly. When the store returns
	// a nil slice the JSON encoding produces null, not []. Verify the key
	// exists and is either null or an empty array.
	agentsVal, hasAgents := resp["agents"]
	if !hasAgents {
		t.Fatal("expected agents key in response")
	}
	if agentsVal != nil {
		agents, ok := agentsVal.([]interface{})
		if !ok {
			t.Fatalf("expected agents to be null or an array, got %T", agentsVal)
		}
		if len(agents) != 0 {
			t.Errorf("expected empty agents list, got %d items", len(agents))
		}
	}

	// next_cursor should not be present when there are no results.
	if _, hasCursor := resp["next_cursor"]; hasCursor {
		t.Error("expected no next_cursor key in response for empty list")
	}
}

func TestListAgents_WithAgents(t *testing.T) {
	store := newFakeAgentStore()
	// Pre-populate agents.
	store.agents["a1"] = &agent.Agent{
		ID:           "a1",
		Name:         "agent-alpha",
		APIKeyPrefix: "octroi_abc123",
		Team:         "eng",
		RateLimit:    100,
		CreatedAt:    time.Now().UTC(),
	}
	store.agents["a2"] = &agent.Agent{
		ID:           "a2",
		Name:         "agent-beta",
		APIKeyPrefix: "octroi_def456",
		Team:         "ops",
		RateLimit:    50,
		CreatedAt:    time.Now().UTC(),
	}

	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	agents, ok := resp["agents"].([]interface{})
	if !ok {
		t.Fatalf("expected agents to be an array, got %T", resp["agents"])
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}

	// Verify fields are present on the first agent.
	first := agents[0].(map[string]interface{})
	for _, field := range []string{"id", "name", "team", "rate_limit", "api_key_prefix", "created_at"} {
		if _, ok := first[field]; !ok {
			t.Errorf("expected field %q in agent response", field)
		}
	}

	// api_key_hash should not be serialized (json:"-" tag).
	if _, hasHash := first["api_key_hash"]; hasHash {
		t.Error("api_key_hash should not be exposed in list response")
	}
}

// ---------------------------------------------------------------------------
// UpdateAgent tests
// ---------------------------------------------------------------------------

func TestUpdateAgent_Success(t *testing.T) {
	store := newFakeAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID:        "a1",
		Name:      "old-name",
		Team:      "eng",
		CreatedAt: time.Now().UTC(),
	}

	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	body := `{"name":"new-name"}`
	req := httptest.NewRequest(http.MethodPut, "/agents/a1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp agent.Agent
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Name != "new-name" {
		t.Errorf("expected name=new-name, got %q", resp.Name)
	}
	if resp.Team != "eng" {
		t.Errorf("expected team=eng (unchanged), got %q", resp.Team)
	}
}

func TestUpdateAgent_NotFound(t *testing.T) {
	store := newFakeAgentStore()
	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	body := `{"name":"new-name"}`
	req := httptest.NewRequest(http.MethodPut, "/agents/nonexistent", strings.NewReader(body))
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
// DeleteAgent tests
// ---------------------------------------------------------------------------

func TestDeleteAgent_Success(t *testing.T) {
	store := newFakeAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID:   "a1",
		Name: "to-delete",
	}

	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	req := httptest.NewRequest(http.MethodDelete, "/agents/a1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Agent should be removed from the store.
	if _, ok := store.agents["a1"]; ok {
		t.Error("expected agent to be removed from store after delete")
	}
}

func TestDeleteAgent_NotFound(t *testing.T) {
	store := newFakeAgentStore()
	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	req := httptest.NewRequest(http.MethodDelete, "/agents/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// The handler calls Archive which returns an error for missing agents,
	// but the handler maps it to 500 (not 404) since it does not check for
	// pgx.ErrNoRows in Archive. This matches the current handler behavior.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for not-found delete, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GetSelfAgent tests
// ---------------------------------------------------------------------------

func TestGetSelfAgent_Success(t *testing.T) {
	store := newFakeAgentStore()
	store.agents["agent-1"] = &agent.Agent{
		ID:           "agent-1",
		Name:         "test-agent",
		APIKeyPrefix: "octroi_test12",
		Team:         "eng",
		RateLimit:    100,
		CreatedAt:    time.Now().UTC(),
	}

	router := setupAgentAuthRouter(store)

	req := httptest.NewRequest(http.MethodGet, "/agents/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp agent.Agent
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.ID != "agent-1" {
		t.Errorf("expected id=agent-1, got %q", resp.ID)
	}
	if resp.Name != "test-agent" {
		t.Errorf("expected name=test-agent, got %q", resp.Name)
	}
	if resp.Team != "eng" {
		t.Errorf("expected team=eng, got %q", resp.Team)
	}
}

func TestGetSelfAgent_NoAuthContext(t *testing.T) {
	store := newFakeAgentStore()
	h := newAgentsHandler(store, nil)

	// Set up router without injecting agent context.
	r := chi.NewRouter()
	r.Get("/agents/me", h.GetSelfAgent)

	req := httptest.NewRequest(http.MethodGet, "/agents/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// RegenerateKey tests
// ---------------------------------------------------------------------------

func TestRegenerateKey_Success(t *testing.T) {
	store := newFakeAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID:           "a1",
		Name:         "test-agent",
		APIKeyPrefix: "octroi_old123",
		Team:         "eng",
		RateLimit:    100,
		CreatedAt:    time.Now().UTC(),
	}

	router := setupAdminAgentsRouter(store, newFakeBudgetStore())
	req := httptest.NewRequest(http.MethodPost, "/agents/a1/regenerate-key", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["id"] != "a1" {
		t.Errorf("expected id=a1, got %v", resp["id"])
	}
	if resp["api_key"] == nil || resp["api_key"] == "" {
		t.Error("expected api_key to be present in response")
	}
	if resp["api_key_prefix"] == nil || resp["api_key_prefix"] == "" {
		t.Error("expected api_key_prefix to be present in response")
	}
	// The prefix should have changed from the old one.
	if resp["api_key_prefix"] == "octroi_old123" {
		t.Error("expected api_key_prefix to change after regeneration")
	}
}

func TestRegenerateKey_NotFound(t *testing.T) {
	store := newFakeAgentStore()
	router := setupAdminAgentsRouter(store, newFakeBudgetStore())

	req := httptest.NewRequest(http.MethodPost, "/agents/nonexistent/regenerate-key", nil)
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
// SetBudget tests
// ---------------------------------------------------------------------------

func TestSetBudget_Success(t *testing.T) {
	store := newFakeAgentStore()
	budgetStore := newFakeBudgetStore()
	router := setupAdminAgentsRouter(store, budgetStore)

	body := `{"daily_limit":10.5,"monthly_limit":100.0}`
	req := httptest.NewRequest(http.MethodPut, "/agents/agent-1/budgets/tool-1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp agent.Budget
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.AgentID != "agent-1" {
		t.Errorf("expected agent_id=agent-1, got %q", resp.AgentID)
	}
	if resp.ToolID != "tool-1" {
		t.Errorf("expected tool_id=tool-1, got %q", resp.ToolID)
	}
	if resp.DailyLimit != 10.5 {
		t.Errorf("expected daily_limit=10.5, got %v", resp.DailyLimit)
	}
	if resp.MonthlyLimit != 100.0 {
		t.Errorf("expected monthly_limit=100.0, got %v", resp.MonthlyLimit)
	}
}

func TestSetBudget_InvalidBody(t *testing.T) {
	store := newFakeAgentStore()
	budgetStore := newFakeBudgetStore()
	router := setupAdminAgentsRouter(store, budgetStore)

	req := httptest.NewRequest(http.MethodPut, "/agents/agent-1/budgets/tool-1", strings.NewReader("not-json"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "invalid_body" {
		t.Errorf("expected code=invalid_body, got %q", envelope.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// GetBudget tests
// ---------------------------------------------------------------------------

func TestGetBudget_Success(t *testing.T) {
	store := newFakeAgentStore()
	budgetStore := newFakeBudgetStore()
	budgetStore.budgets["agent-1|tool-1"] = &agent.Budget{
		ID:           "budget-1",
		AgentID:      "agent-1",
		ToolID:       "tool-1",
		DailyLimit:   5.0,
		MonthlyLimit: 50.0,
	}
	router := setupAdminAgentsRouter(store, budgetStore)

	req := httptest.NewRequest(http.MethodGet, "/agents/agent-1/budgets/tool-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp agent.Budget
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.ID != "budget-1" {
		t.Errorf("expected id=budget-1, got %q", resp.ID)
	}
	if resp.DailyLimit != 5.0 {
		t.Errorf("expected daily_limit=5.0, got %v", resp.DailyLimit)
	}
}

func TestGetBudget_NotFound(t *testing.T) {
	store := newFakeAgentStore()
	budgetStore := newFakeBudgetStore()
	router := setupAdminAgentsRouter(store, budgetStore)

	req := httptest.NewRequest(http.MethodGet, "/agents/agent-1/budgets/tool-nonexistent", nil)
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
// ListBudgets tests
// ---------------------------------------------------------------------------

func TestListBudgets_Success(t *testing.T) {
	store := newFakeAgentStore()
	budgetStore := newFakeBudgetStore()
	budgetStore.budgets["agent-1|tool-1"] = &agent.Budget{
		ID:           "budget-1",
		AgentID:      "agent-1",
		ToolID:       "tool-1",
		DailyLimit:   5.0,
		MonthlyLimit: 50.0,
	}
	budgetStore.budgets["agent-1|tool-2"] = &agent.Budget{
		ID:           "budget-2",
		AgentID:      "agent-1",
		ToolID:       "tool-2",
		DailyLimit:   10.0,
		MonthlyLimit: 100.0,
	}
	router := setupAdminAgentsRouter(store, budgetStore)

	req := httptest.NewRequest(http.MethodGet, "/agents/agent-1/budgets", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	budgets, ok := resp["budgets"].([]interface{})
	if !ok {
		t.Fatalf("expected budgets to be an array, got %T", resp["budgets"])
	}
	if len(budgets) != 2 {
		t.Errorf("expected 2 budgets, got %d", len(budgets))
	}
}

func TestListBudgets_Empty(t *testing.T) {
	store := newFakeAgentStore()
	budgetStore := newFakeBudgetStore()
	router := setupAdminAgentsRouter(store, budgetStore)

	req := httptest.NewRequest(http.MethodGet, "/agents/agent-1/budgets", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	budgets, ok := resp["budgets"].([]interface{})
	if !ok {
		t.Fatalf("expected budgets to be an array, got %T", resp["budgets"])
	}
	if len(budgets) != 0 {
		t.Errorf("expected 0 budgets, got %d", len(budgets))
	}
}
