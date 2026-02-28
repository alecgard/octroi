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
	"github.com/alecgard/octroi/internal/metering"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Fake member agent store
// ---------------------------------------------------------------------------

type fakeMemberAgentStore struct {
	agents map[string]*agent.Agent
	nextID int
}

func newFakeMemberAgentStore() *fakeMemberAgentStore {
	return &fakeMemberAgentStore{agents: make(map[string]*agent.Agent)}
}

func (f *fakeMemberAgentStore) ListByTeams(_ context.Context, teams []string, _ agent.AgentListParams) ([]*agent.Agent, string, error) {
	teamSet := make(map[string]bool, len(teams))
	for _, t := range teams {
		teamSet[t] = true
	}
	var list []*agent.Agent
	for _, a := range f.agents {
		if teamSet[a.Team] {
			list = append(list, a)
		}
	}
	return list, "", nil
}

func (f *fakeMemberAgentStore) GetByID(_ context.Context, id string) (*agent.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return a, nil
}

func (f *fakeMemberAgentStore) Create(_ context.Context, in agent.CreateAgentInput) (*agent.Agent, error) {
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

func (f *fakeMemberAgentStore) Update(_ context.Context, id string, in agent.UpdateAgentInput) (*agent.Agent, error) {
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

func (f *fakeMemberAgentStore) Archive(_ context.Context, id string) error {
	if _, ok := f.agents[id]; !ok {
		return fmt.Errorf("agent not found")
	}
	delete(f.agents, id)
	return nil
}

func (f *fakeMemberAgentStore) RegenerateKey(_ context.Context, id, newHash, newPrefix string) (*agent.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	oldHash := a.APIKeyHash
	a.APIKeyHash = newHash
	a.APIKeyPrefix = newPrefix
	a.PrevKeyHash = &oldHash
	expires := time.Now().Add(24 * time.Hour)
	a.PrevKeyExpiresAt = &expires
	return a, nil
}

func (f *fakeMemberAgentStore) RevokePrevKey(_ context.Context, id string) (*agent.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	a.PrevKeyHash = nil
	a.PrevKeyExpiresAt = nil
	return a, nil
}

func (f *fakeMemberAgentStore) ListIDsByTeams(_ context.Context, teams []string) ([]string, error) {
	teamSet := make(map[string]bool, len(teams))
	for _, t := range teams {
		teamSet[t] = true
	}
	var ids []string
	for _, a := range f.agents {
		if teamSet[a.Team] {
			ids = append(ids, a.ID)
		}
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// Fake member metering store
// ---------------------------------------------------------------------------

type fakeMemberMeterStore struct {
	summary *metering.UsageSummary
	txns    []*metering.Transaction
}

func (f *fakeMemberMeterStore) GetSummary(_ context.Context, _ metering.UsageQuery) (*metering.UsageSummary, error) {
	if f.summary != nil {
		return f.summary, nil
	}
	return &metering.UsageSummary{}, nil
}

func (f *fakeMemberMeterStore) ListTransactions(_ context.Context, _ metering.UsageQuery) ([]*metering.Transaction, string, error) {
	return f.txns, "", nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func memberUser(teams ...string) *auth.User {
	memberships := make([]auth.TeamMembership, len(teams))
	for i, t := range teams {
		memberships[i] = auth.TeamMembership{Team: t, Role: "admin"}
	}
	return &auth.User{
		ID:    "user-1",
		Email: "test@test.com",
		Name:  "Test User",
		Role:  "member",
		Teams: memberships,
	}
}

func setupMemberRouter(agentStore *fakeMemberAgentStore, meterStore *fakeMemberMeterStore, user *auth.User) http.Handler {
	h := newMemberHandler(agentStore, meterStore)
	r := chi.NewRouter()
	if user != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := auth.ContextWithUser(req.Context(), user)
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
	}
	r.Get("/agents", h.ListAgents)
	r.Post("/agents", h.CreateAgent)
	r.Put("/agents/{id}", h.UpdateAgent)
	r.Delete("/agents/{id}", h.DeleteAgent)
	r.Post("/agents/{id}/regenerate-key", h.RegenerateKey)
	r.Get("/usage", h.GetUsage)
	r.Get("/usage/transactions", h.ListTransactions)
	return r
}

// ---------------------------------------------------------------------------
// ListAgents tests
// ---------------------------------------------------------------------------

func TestMemberListAgents_Success(t *testing.T) {
	store := newFakeMemberAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "alpha", Team: "team-a", CreatedAt: time.Now().UTC(),
	}
	store.agents["a2"] = &agent.Agent{
		ID: "a2", Name: "beta", Team: "team-b", CreatedAt: time.Now().UTC(),
	}
	store.agents["a3"] = &agent.Agent{
		ID: "a3", Name: "gamma", Team: "team-c", CreatedAt: time.Now().UTC(),
	}

	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a", "team-b"))

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
		t.Fatalf("expected agents array, got %T", resp["agents"])
	}
	// Should only see agents from team-a and team-b (2 agents), not team-c.
	if len(agents) != 2 {
		t.Errorf("expected 2 agents (team-scoped), got %d", len(agents))
	}
}

func TestMemberListAgents_Unauthenticated(t *testing.T) {
	store := newFakeMemberAgentStore()
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "unauthorized" {
		t.Errorf("expected code=unauthorized, got %q", envelope.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// CreateAgent tests
// ---------------------------------------------------------------------------

func TestMemberCreateAgent_SingleTeam(t *testing.T) {
	store := newFakeMemberAgentStore()
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	body := `{"name":"new-agent"}`
	req := httptest.NewRequest(http.MethodPost, "/agents", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp["name"] != "new-agent" {
		t.Errorf("expected name=new-agent, got %v", resp["name"])
	}
	if resp["team"] != "team-a" {
		t.Errorf("expected team=team-a (auto-assigned), got %v", resp["team"])
	}
	if resp["api_key"] == nil || resp["api_key"] == "" {
		t.Error("expected api_key to be present")
	}
}

func TestMemberCreateAgent_ExplicitTeam(t *testing.T) {
	store := newFakeMemberAgentStore()
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a", "team-b"))

	body := `{"name":"new-agent","team":"team-b"}`
	req := httptest.NewRequest(http.MethodPost, "/agents", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp["team"] != "team-b" {
		t.Errorf("expected team=team-b, got %v", resp["team"])
	}
}

func TestMemberCreateAgent_WrongTeam(t *testing.T) {
	store := newFakeMemberAgentStore()
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	body := `{"name":"new-agent","team":"team-x"}`
	req := httptest.NewRequest(http.MethodPost, "/agents", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "forbidden" {
		t.Errorf("expected code=forbidden, got %q", envelope.Error.Code)
	}
}

func TestMemberCreateAgent_MultiTeamWithoutTeamParam(t *testing.T) {
	store := newFakeMemberAgentStore()
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a", "team-b"))

	body := `{"name":"new-agent"}`
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

// ---------------------------------------------------------------------------
// UpdateAgent tests
// ---------------------------------------------------------------------------

func TestMemberUpdateAgent_Success(t *testing.T) {
	store := newFakeMemberAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "old-name", Team: "team-a", CreatedAt: time.Now().UTC(),
	}
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

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
	// Team should remain unchanged (members cannot change team).
	if resp.Team != "team-a" {
		t.Errorf("expected team=team-a (unchanged), got %q", resp.Team)
	}
}

func TestMemberUpdateAgent_NotFound(t *testing.T) {
	store := newFakeMemberAgentStore()
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	body := `{"name":"new-name"}`
	req := httptest.NewRequest(http.MethodPut, "/agents/nonexistent", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMemberUpdateAgent_WrongTeam(t *testing.T) {
	store := newFakeMemberAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "other-agent", Team: "team-x", CreatedAt: time.Now().UTC(),
	}
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	body := `{"name":"new-name"}`
	req := httptest.NewRequest(http.MethodPut, "/agents/a1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Agent exists but belongs to a different team, so member sees 404.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (wrong team), got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// DeleteAgent tests
// ---------------------------------------------------------------------------

func TestMemberDeleteAgent_Success(t *testing.T) {
	store := newFakeMemberAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "to-delete", Team: "team-a", CreatedAt: time.Now().UTC(),
	}
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodDelete, "/agents/a1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, ok := store.agents["a1"]; ok {
		t.Error("expected agent to be removed from store after delete")
	}
}

func TestMemberDeleteAgent_NotFound(t *testing.T) {
	store := newFakeMemberAgentStore()
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodDelete, "/agents/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMemberDeleteAgent_WrongTeam(t *testing.T) {
	store := newFakeMemberAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "other-agent", Team: "team-x", CreatedAt: time.Now().UTC(),
	}
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodDelete, "/agents/a1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Agent exists but belongs to a different team, so member sees 404.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (wrong team), got %d: %s", rec.Code, rec.Body.String())
	}

	// Agent should still exist in the store (not deleted).
	if _, ok := store.agents["a1"]; !ok {
		t.Error("agent should not have been deleted (wrong team)")
	}
}

// ---------------------------------------------------------------------------
// RegenerateKey tests
// ---------------------------------------------------------------------------

func TestMemberRegenerateKey_Success(t *testing.T) {
	store := newFakeMemberAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "my-agent", Team: "team-a",
		APIKeyPrefix: "octroi_old", CreatedAt: time.Now().UTC(),
	}
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodPost, "/agents/a1/regenerate-key", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp["api_key"] == nil || resp["api_key"] == "" {
		t.Error("expected new api_key in response")
	}
	if resp["id"] != "a1" {
		t.Errorf("expected id=a1, got %v", resp["id"])
	}
	// Prefix should have changed.
	if resp["api_key_prefix"] == "octroi_old" {
		t.Error("expected api_key_prefix to change after regeneration")
	}
}

func TestMemberRegenerateKey_NotFound(t *testing.T) {
	store := newFakeMemberAgentStore()
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodPost, "/agents/nonexistent/regenerate-key", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMemberRegenerateKey_WrongTeam(t *testing.T) {
	store := newFakeMemberAgentStore()
	store.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "other-agent", Team: "team-x", CreatedAt: time.Now().UTC(),
	}
	router := setupMemberRouter(store, &fakeMemberMeterStore{}, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodPost, "/agents/a1/regenerate-key", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (wrong team), got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GetUsage tests
// ---------------------------------------------------------------------------

func TestMemberGetUsage_Success(t *testing.T) {
	agentStore := newFakeMemberAgentStore()
	agentStore.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "alpha", Team: "team-a", CreatedAt: time.Now().UTC(),
	}
	meterStore := &fakeMemberMeterStore{
		summary: &metering.UsageSummary{
			TotalRequests: 42,
			TotalCost:     1.23,
			SuccessCount:  40,
			ErrorCount:    2,
			AvgLatencyMs:  55.5,
		},
	}
	router := setupMemberRouter(agentStore, meterStore, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp metering.UsageSummary
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.TotalRequests != 42 {
		t.Errorf("expected total_requests=42, got %d", resp.TotalRequests)
	}
	if resp.TotalCost != 1.23 {
		t.Errorf("expected total_cost=1.23, got %f", resp.TotalCost)
	}
}

func TestMemberGetUsage_ForbiddenTeamFilter(t *testing.T) {
	agentStore := newFakeMemberAgentStore()
	meterStore := &fakeMemberMeterStore{}
	router := setupMemberRouter(agentStore, meterStore, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodGet, "/usage?team=team-x", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "forbidden" {
		t.Errorf("expected code=forbidden, got %q", envelope.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// ListTransactions tests
// ---------------------------------------------------------------------------

func TestMemberListTransactions_Success(t *testing.T) {
	agentStore := newFakeMemberAgentStore()
	agentStore.agents["a1"] = &agent.Agent{
		ID: "a1", Name: "alpha", Team: "team-a", CreatedAt: time.Now().UTC(),
	}
	meterStore := &fakeMemberMeterStore{
		txns: []*metering.Transaction{
			{
				ID:         "tx-1",
				AgentID:    "a1",
				ToolID:     "tool-1",
				Timestamp:  time.Now().UTC(),
				Method:     "POST",
				Path:       "/chat",
				StatusCode: 200,
				LatencyMs:  50,
				Success:    true,
				Cost:       0.01,
			},
			{
				ID:         "tx-2",
				AgentID:    "a1",
				ToolID:     "tool-2",
				Timestamp:  time.Now().UTC(),
				Method:     "GET",
				Path:       "/search",
				StatusCode: 500,
				LatencyMs:  120,
				Success:    false,
				Cost:       0.0,
				Error:      "upstream error",
			},
		},
	}
	router := setupMemberRouter(agentStore, meterStore, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodGet, "/usage/transactions", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	txns, ok := resp["transactions"].([]interface{})
	if !ok {
		t.Fatalf("expected transactions array, got %T", resp["transactions"])
	}
	if len(txns) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(txns))
	}
}

func TestMemberListTransactions_ForbiddenTeamFilter(t *testing.T) {
	agentStore := newFakeMemberAgentStore()
	meterStore := &fakeMemberMeterStore{}
	router := setupMemberRouter(agentStore, meterStore, memberUser("team-a"))

	req := httptest.NewRequest(http.MethodGet, "/usage/transactions?team=team-x", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
