package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// Fake metering store
// ---------------------------------------------------------------------------

type fakeUsageMeteringStore struct {
	summary          *metering.UsageSummary
	summaryErr       error
	lastSummaryQuery metering.UsageQuery

	transactions     []*metering.Transaction
	nextCursor       string
	listErr          error
	lastListQuery    metering.UsageQuery

	toolCallCounts    map[string]int64
	toolCallCountsErr error

	subToolCallCounts    map[string]int64
	subToolCallCountsErr error
	lastSubToolID        string

	transaction   *metering.Transaction
	transactionErr error
	lastGetByID   string
}

func (f *fakeUsageMeteringStore) GetSummary(_ context.Context, _ string, q metering.UsageQuery) (*metering.UsageSummary, error) {
	f.lastSummaryQuery = q
	return f.summary, f.summaryErr
}

func (f *fakeUsageMeteringStore) ListTransactions(_ context.Context, _ string, q metering.UsageQuery) ([]*metering.Transaction, string, error) {
	f.lastListQuery = q
	return f.transactions, f.nextCursor, f.listErr
}

func (f *fakeUsageMeteringStore) GetToolCallCounts(_ context.Context, _ string) (map[string]int64, error) {
	return f.toolCallCounts, f.toolCallCountsErr
}

func (f *fakeUsageMeteringStore) GetSubToolCallCounts(_ context.Context, _ string, toolID string) (map[string]int64, error) {
	f.lastSubToolID = toolID
	return f.subToolCallCounts, f.subToolCallCountsErr
}

func (f *fakeUsageMeteringStore) GetByID(_ context.Context, _, id string) (*metering.Transaction, error) {
	f.lastGetByID = id
	return f.transaction, f.transactionErr
}

// ---------------------------------------------------------------------------
// Fake agent store (for team filter resolution)
// ---------------------------------------------------------------------------

type fakeUsageAgentStore struct {
	teamAgents map[string][]string
}

func (f *fakeUsageAgentStore) ListIDsByTeam(_ context.Context, _, team string) ([]string, error) {
	ids, ok := f.teamAgents[team]
	if !ok {
		return nil, nil
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// Fake body store
// ---------------------------------------------------------------------------

type fakeUsageBodyStore struct {
	body *metering.RequestBody
	err  error
}

func (f *fakeUsageBodyStore) GetByTransactionID(_ context.Context, _, _ string) (*metering.RequestBody, error) {
	return f.body, f.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestUsageHandler(ms *fakeUsageMeteringStore, as *fakeUsageAgentStore, bs *fakeUsageBodyStore) *usageHandler {
	h := &usageHandler{
		store:      ms,
		agentStore: as,
	}
	if bs != nil {
		h.bodyStore = bs
	}
	return h
}

// ---------------------------------------------------------------------------
// GetUsage tests
// ---------------------------------------------------------------------------

func TestGetUsage_Success(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		summary: &metering.UsageSummary{
			TotalRequests: 42,
			TotalCost:     1.25,
			SuccessCount:  40,
			ErrorCount:    2,
			AvgLatencyMs:  150.5,
		},
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage", nil)
	ctx := auth.ContextWithAgent(req.Context(), &auth.Agent{ID: "agent-1", Name: "test", Team: "team-a", TenantID: "t1"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetUsage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var got metering.UsageSummary
	decodeJSON(t, rec, &got)

	if got.TotalRequests != 42 {
		t.Errorf("expected TotalRequests=42, got %d", got.TotalRequests)
	}
	if got.TotalCost != 1.25 {
		t.Errorf("expected TotalCost=1.25, got %f", got.TotalCost)
	}

	// Verify that the query was scoped to the agent.
	if ms.lastSummaryQuery.AgentID != "agent-1" {
		t.Errorf("expected query scoped to agent-1, got %q", ms.lastSummaryQuery.AgentID)
	}
}

func TestGetUsage_FilterByToolID(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		summary: &metering.UsageSummary{TotalRequests: 10},
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage?tool_id=tool-abc", nil)
	ctx := auth.ContextWithAgent(req.Context(), &auth.Agent{ID: "agent-1", Name: "test", Team: "team-a", TenantID: "t1"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetUsage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ms.lastSummaryQuery.ToolID != "tool-abc" {
		t.Errorf("expected tool_id filter tool-abc, got %q", ms.lastSummaryQuery.ToolID)
	}
	if ms.lastSummaryQuery.AgentID != "agent-1" {
		t.Errorf("expected agent scoped to agent-1, got %q", ms.lastSummaryQuery.AgentID)
	}
}

func TestGetUsage_StoreError(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		summaryErr: errors.New("db down"),
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage", nil)
	ctx := auth.ContextWithAgent(req.Context(), &auth.Agent{ID: "agent-1", TenantID: "t1"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetUsage(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// GetUsageAdmin tests
// ---------------------------------------------------------------------------

func TestGetUsageAdmin_Success(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		summary: &metering.UsageSummary{
			TotalRequests: 100,
			TotalCost:     5.50,
		},
	}
	as := &fakeUsageAgentStore{}
	h := newTestUsageHandler(ms, as, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{
		ID:   "user-1",
		Role: "admin",
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetUsageAdmin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var got metering.UsageSummary
	decodeJSON(t, rec, &got)
	if got.TotalRequests != 100 {
		t.Errorf("expected TotalRequests=100, got %d", got.TotalRequests)
	}
}

func TestGetUsageAdmin_WithTeamFilter(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		summary: &metering.UsageSummary{TotalRequests: 30},
	}
	as := &fakeUsageAgentStore{
		teamAgents: map[string][]string{
			"team-a": {"agent-1", "agent-2"},
		},
	}
	h := newTestUsageHandler(ms, as, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage?team=team-a", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{
		ID:   "user-1",
		Role: "admin",
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetUsageAdmin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify the query was scoped to the team's agents.
	if len(ms.lastSummaryQuery.AgentIDs) != 2 {
		t.Fatalf("expected 2 agent IDs from team filter, got %d", len(ms.lastSummaryQuery.AgentIDs))
	}
}

func TestGetUsageAdmin_StoreError(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		summaryErr: errors.New("db down"),
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{ID: "user-1", Role: "admin"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetUsageAdmin(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// ListTransactions tests
// ---------------------------------------------------------------------------

func TestListTransactions_AgentScoped(t *testing.T) {
	now := time.Now().UTC()
	ms := &fakeUsageMeteringStore{
		transactions: []*metering.Transaction{
			{ID: "tx-1", AgentID: "agent-1", ToolID: "tool-1", Timestamp: now, StatusCode: 200, Success: true},
			{ID: "tx-2", AgentID: "agent-1", ToolID: "tool-2", Timestamp: now, StatusCode: 500, Success: false},
		},
		nextCursor: "cursor-abc",
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/transactions", nil)
	ctx := auth.ContextWithAgent(req.Context(), &auth.Agent{ID: "agent-1", Name: "test", Team: "team-a", TenantID: "t1"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ListTransactions(rec, req, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]json.RawMessage
	decodeJSON(t, rec, &resp)

	var txns []*metering.Transaction
	if err := json.Unmarshal(resp["transactions"], &txns); err != nil {
		t.Fatalf("failed to unmarshal transactions: %v", err)
	}
	if len(txns) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(txns))
	}

	var cursor string
	if err := json.Unmarshal(resp["next_cursor"], &cursor); err != nil {
		t.Fatalf("failed to unmarshal next_cursor: %v", err)
	}
	if cursor != "cursor-abc" {
		t.Errorf("expected next_cursor=cursor-abc, got %q", cursor)
	}

	// Verify agent scoping.
	if ms.lastListQuery.AgentID != "agent-1" {
		t.Errorf("expected query scoped to agent-1, got %q", ms.lastListQuery.AgentID)
	}
}

func TestListTransactions_AdminWithFilters(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		transactions: []*metering.Transaction{},
	}
	as := &fakeUsageAgentStore{
		teamAgents: map[string][]string{
			"team-b": {"agent-5", "agent-6"},
		},
	}
	h := newTestUsageHandler(ms, as, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/usage/transactions?agent_id=agent-5&team=team-b&tool_id=tool-x&limit=10", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{
		ID:   "user-1",
		Role: "admin",
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ListTransactions(rec, req, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// agent_id is set, so team filter is a no-op (resolveTeamFilter skips when AgentID is set).
	if ms.lastListQuery.AgentID != "agent-5" {
		t.Errorf("expected AgentID=agent-5, got %q", ms.lastListQuery.AgentID)
	}
	if ms.lastListQuery.ToolID != "tool-x" {
		t.Errorf("expected ToolID=tool-x, got %q", ms.lastListQuery.ToolID)
	}
	if ms.lastListQuery.Limit != 10 {
		t.Errorf("expected Limit=10, got %d", ms.lastListQuery.Limit)
	}
}

func TestListTransactions_NoCursor(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		transactions: []*metering.Transaction{
			{ID: "tx-1"},
		},
		nextCursor: "", // no more pages
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/transactions", nil)
	ctx := auth.ContextWithAgent(req.Context(), &auth.Agent{ID: "agent-1", TenantID: "t1"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ListTransactions(rec, req, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]json.RawMessage
	decodeJSON(t, rec, &resp)

	if _, ok := resp["next_cursor"]; ok {
		t.Error("expected no next_cursor key when cursor is empty")
	}
}

func TestListTransactions_StoreError(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		listErr: errors.New("db down"),
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/transactions", nil)
	ctx := auth.ContextWithAgent(req.Context(), &auth.Agent{ID: "agent-1", TenantID: "t1"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ListTransactions(rec, req, false)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// GetToolCallCounts tests
// ---------------------------------------------------------------------------

func TestGetToolCallCounts_Success(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		toolCallCounts: map[string]int64{
			"tool-1": 100,
			"tool-2": 50,
		},
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/tools/calls", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{ID: "user-1", Role: "admin"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetToolCallCounts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Counts map[string]int64 `json:"counts"`
	}
	decodeJSON(t, rec, &resp)

	if resp.Counts["tool-1"] != 100 {
		t.Errorf("expected tool-1 count=100, got %d", resp.Counts["tool-1"])
	}
	if resp.Counts["tool-2"] != 50 {
		t.Errorf("expected tool-2 count=50, got %d", resp.Counts["tool-2"])
	}
}

func TestGetToolCallCounts_StoreError(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		toolCallCountsErr: errors.New("db down"),
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/tools/calls", nil)
	req = req.WithContext(auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"}))
	rec := httptest.NewRecorder()
	h.GetToolCallCounts(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// GetSubToolCallCounts tests
// ---------------------------------------------------------------------------

func TestGetSubToolCallCounts_Success(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		subToolCallCounts: map[string]int64{
			"/api/v1/search": 80,
			"/api/v1/query":  20,
		},
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	// Mount on a chi router to get URL params.
	r := chi.NewRouter()
	r.Get("/api/v1/admin/usage/tools/{id}/calls", h.GetSubToolCallCounts)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/tools/tool-abc/calls", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{ID: "user-1", Role: "admin"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if ms.lastSubToolID != "tool-abc" {
		t.Errorf("expected tool ID tool-abc, got %q", ms.lastSubToolID)
	}

	var resp struct {
		Counts map[string]int64 `json:"counts"`
	}
	decodeJSON(t, rec, &resp)

	if resp.Counts["/api/v1/search"] != 80 {
		t.Errorf("expected /api/v1/search count=80, got %d", resp.Counts["/api/v1/search"])
	}
}

func TestGetSubToolCallCounts_MissingID(t *testing.T) {
	ms := &fakeUsageMeteringStore{}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	// Call directly without chi routing so id param is empty.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/tools//calls", nil)
	req = req.WithContext(auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"}))
	rec := httptest.NewRecorder()
	h.GetSubToolCallCounts(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// GetTransactionDetail tests
// ---------------------------------------------------------------------------

func TestGetTransactionDetail_Found(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ms := &fakeUsageMeteringStore{
		transaction: &metering.Transaction{
			ID:         "tx-123",
			AgentID:    "agent-1",
			ToolID:     "tool-1",
			Timestamp:  now,
			Method:     "POST",
			Path:       "/api/search",
			StatusCode: 200,
			LatencyMs:  45,
			Success:    true,
			Cost:       0.002,
		},
	}
	bs := &fakeUsageBodyStore{
		body: &metering.RequestBody{
			ID:            "body-1",
			TransactionID: "tx-123",
			RequestBody:   []byte(`{"query":"hello"}`),
			ResponseBody:  []byte(`{"result":"world"}`),
		},
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, bs)

	r := chi.NewRouter()
	r.Get("/api/v1/admin/usage/transactions/{id}", h.GetTransactionDetail)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/transactions/tx-123", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{ID: "user-1", Role: "admin"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if ms.lastGetByID != "tx-123" {
		t.Errorf("expected GetByID called with tx-123, got %q", ms.lastGetByID)
	}

	var resp map[string]json.RawMessage
	decodeJSON(t, rec, &resp)

	if _, ok := resp["transaction"]; !ok {
		t.Fatal("expected 'transaction' key in response")
	}
	if _, ok := resp["body"]; !ok {
		t.Fatal("expected 'body' key in response when body store is present")
	}

	// Verify body content.
	var body map[string]interface{}
	if err := json.Unmarshal(resp["body"], &body); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}
	reqBody, ok := body["request_body"].(map[string]interface{})
	if !ok {
		t.Fatal("expected request_body to be a parsed JSON object")
	}
	if reqBody["query"] != "hello" {
		t.Errorf("expected request_body.query=hello, got %v", reqBody["query"])
	}
}

func TestGetTransactionDetail_NotFound(t *testing.T) {
	ms := &fakeUsageMeteringStore{
		transactionErr: errors.New("not found"),
	}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	r := chi.NewRouter()
	r.Get("/api/v1/admin/usage/transactions/{id}", h.GetTransactionDetail)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/transactions/tx-999", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{ID: "user-1", Role: "admin"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGetTransactionDetail_NoBodyStore(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ms := &fakeUsageMeteringStore{
		transaction: &metering.Transaction{
			ID:        "tx-456",
			AgentID:   "agent-1",
			Timestamp: now,
		},
	}
	// No body store provided.
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	r := chi.NewRouter()
	r.Get("/api/v1/admin/usage/transactions/{id}", h.GetTransactionDetail)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/transactions/tx-456", nil)
	ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
	ctx = auth.ContextWithUser(ctx, &auth.User{ID: "user-1", Role: "admin"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]json.RawMessage
	decodeJSON(t, rec, &resp)

	if _, ok := resp["body"]; ok {
		t.Error("expected no 'body' key when body store is nil")
	}
}

func TestGetTransactionDetail_MissingID(t *testing.T) {
	ms := &fakeUsageMeteringStore{}
	h := newTestUsageHandler(ms, &fakeUsageAgentStore{}, nil)

	// Call directly without chi routing so id param is empty.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/transactions/", nil)
	rec := httptest.NewRecorder()
	h.GetTransactionDetail(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
