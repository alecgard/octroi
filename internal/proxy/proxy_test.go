package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/go-chi/chi/v5"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// --- Fakes ---

type fakePermissionChecker struct {
	allowed        bool
	subToolAllowed bool
	subToolChecked bool
}

func (f *fakePermissionChecker) IsAllowed(_ context.Context, _, _ string) (bool, error) {
	return f.allowed, nil
}

func (f *fakePermissionChecker) IsSubToolAllowed(_ context.Context, _, _, _ string) (bool, error) {
	f.subToolChecked = true
	return f.subToolAllowed, nil
}

type fakeToolStore struct {
	tools map[string]*registry.Tool
}

func (f *fakeToolStore) GetByID(_ context.Context, id string) (*registry.Tool, error) {
	tool, ok := f.tools[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return tool, nil
}

func (f *fakeToolStore) GetByIDIncludeArchived(_ context.Context, id string) (*registry.Tool, error) {
	tool, ok := f.tools[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return tool, nil
}

type fakeBudgetChecker struct {
	agentAllowed bool
	globalAllowed bool
}

func (f *fakeBudgetChecker) CheckBudget(_ context.Context, _, _ string) (bool, float64, float64, error) {
	return f.agentAllowed, 100, 1000, nil
}

func (f *fakeBudgetChecker) CheckToolGlobalBudget(_ context.Context, _ string) (bool, float64, error) {
	return f.globalAllowed, 500, nil
}

type fakeCollector struct {
	transactions []metering.Transaction
}

func (f *fakeCollector) Record(tx metering.Transaction) {
	f.transactions = append(f.transactions, tx)
}

// --- Helpers ---

func newTestAgent() *auth.Agent {
	return &auth.Agent{
		ID:   "agent-1",
		Name: "test-agent",
	}
}

func newTestTool(endpoint string) *registry.Tool {
	return &registry.Tool{
		ID:            "tool-1",
		Name:          "test-tool",
		Endpoint:      endpoint,
		AuthType:      "none",
		AuthConfig:    map[string]string{},
		PricingModel:  "per_request",
		PricingAmount: 0.01,
	}
}

func setupRouter(handler *Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Handle("/proxy/{toolID}/*", handler)
	r.Handle("/proxy/{toolID}", handler)
	return r
}

func withAgent(r *http.Request, agent *auth.Agent) *http.Request {
	ctx := auth.ContextWithAgent(r.Context(), agent)
	return r.WithContext(ctx)
}

// --- Tests ---

func TestProxyForwarding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/data" {
			t.Errorf("expected upstream path /api/data, got %s", r.URL.Path)
		}
		if r.Header.Get("X-Custom") != "value" {
			t.Errorf("expected X-Custom header to be forwarded")
		}
		// Authorization from the client should be stripped.
		if r.Header.Get("Authorization") != "" {
			t.Errorf("expected Authorization header to be stripped")
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Upstream", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)

	router := setupRouter(handler)

	reqBody := []byte(`{"query":"test"}`)
	req := httptest.NewRequest("POST", "/proxy/tool-1/api/data", bytes.NewReader(reqBody))
	req.Header.Set("X-Custom", "value")
	req.Header.Set("Authorization", "Bearer client-token")
	req = withAgent(req, newTestAgent())

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("X-Upstream") != "true" {
		t.Error("expected upstream response header to be forwarded")
	}
	if rr.Body.String() != `{"query":"test"}` {
		t.Errorf("expected body to be forwarded, got %s", rr.Body.String())
	}
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction recorded, got %d", len(collector.transactions))
	}
	tx := collector.transactions[0]
	if tx.StatusCode != 200 || !tx.Success || tx.AgentID != "agent-1" || tx.ToolID != "tool-1" {
		t.Errorf("unexpected transaction: %+v", tx)
	}
}

func TestToolNotFound(t *testing.T) {
	store := &fakeToolStore{tools: map[string]*registry.Tool{}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/nonexistent/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}

	var errResp proxyError
	if err := json.NewDecoder(rr.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != "not_found" {
		t.Errorf("expected error code not_found, got %s", errResp.Error.Code)
	}
}

func TestBudgetExceeded(t *testing.T) {
	tool := newTestTool("http://localhost")
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	collector := &fakeCollector{}

	t.Run("agent budget exceeded", func(t *testing.T) {
		budgets := &fakeBudgetChecker{agentAllowed: false, globalAllowed: true}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}

		var errResp proxyError
		_ = json.NewDecoder(rr.Body).Decode(&errResp)
		if errResp.Error.Code != "budget_exceeded" {
			t.Errorf("expected error code budget_exceeded, got %s", errResp.Error.Code)
		}
	})

	t.Run("global budget exceeded", func(t *testing.T) {
		budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: false}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})
}

func TestUpstreamError(t *testing.T) {
	// Use an unreachable address to trigger a proxy error.
	tool := newTestTool("http://127.0.0.1:1")
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 1*time.Second, 1<<20)

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rr.Code)
	}

	var errResp proxyError
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "proxy_error" {
		t.Errorf("expected error code proxy_error, got %s", errResp.Error.Code)
	}

	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction recorded on error, got %d", len(collector.transactions))
	}
	if collector.transactions[0].Success {
		t.Error("expected transaction to be marked as failed")
	}
}

func TestAuthInjectionBearer(t *testing.T) {
	var receivedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	tool.AuthType = "bearer"
	tool.AuthConfig = map[string]string{"key": "secret-token-123"}

	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/resource", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	expected := "Bearer secret-token-123"
	if receivedAuth != expected {
		t.Errorf("expected Authorization %q, got %q", expected, receivedAuth)
	}
}

func TestAuthInjectionHeader(t *testing.T) {
	var receivedKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedKey = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	tool.AuthType = "header"
	tool.AuthConfig = map[string]string{
		"header_name": "X-Api-Key",
		"key":         "my-api-key",
	}

	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/resource", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if receivedKey != "my-api-key" {
		t.Errorf("expected X-Api-Key %q, got %q", "my-api-key", receivedKey)
	}
}

func TestAPIMode(t *testing.T) {
	var receivedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Use template endpoint: the upstream URL with a {version} placeholder.
	tool := newTestTool("http://placeholder.invalid")
	tool.Mode = "api"
	// Replace endpoint with the template — use upstream host.
	tool.Endpoint = upstream.URL + "/{version}"
	tool.Variables = map[string]string{"version": "v2"}

	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/data", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if receivedPath != "/v2/data" {
		t.Errorf("expected upstream path /v2/data, got %s", receivedPath)
	}
}

func TestReportedCostHeader(t *testing.T) {
	t.Run("valid cost header", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Octroi-Cost", "0.05")
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		tool := newTestTool(upstream.URL)
		store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
		budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
		collector := &fakeCollector{}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/resource", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if len(collector.transactions) != 1 {
			t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
		}
		tx := collector.transactions[0]
		if tx.Cost != 0.05 {
			t.Errorf("expected cost 0.05, got %f", tx.Cost)
		}
		if tx.CostSource != "reported" {
			t.Errorf("expected cost_source reported, got %s", tx.CostSource)
		}
	})

	t.Run("no cost header falls back to per_request", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		tool := newTestTool(upstream.URL)
		store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
		budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
		collector := &fakeCollector{}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/resource", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		tx := collector.transactions[0]
		if tx.Cost != 0.01 {
			t.Errorf("expected cost 0.01 (per_request fallback), got %f", tx.Cost)
		}
		if tx.CostSource != "flat" {
			t.Errorf("expected cost_source flat, got %s", tx.CostSource)
		}
	})

	t.Run("invalid cost header falls back to per_request", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Octroi-Cost", "not-a-number")
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		tool := newTestTool(upstream.URL)
		store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
		budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
		collector := &fakeCollector{}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/resource", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		tx := collector.transactions[0]
		if tx.Cost != 0.01 {
			t.Errorf("expected cost 0.01 (per_request fallback), got %f", tx.Cost)
		}
		if tx.CostSource != "flat" {
			t.Errorf("expected cost_source flat, got %s", tx.CostSource)
		}
	})

	t.Run("negative cost header falls back to per_request", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Octroi-Cost", "-1.5")
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		tool := newTestTool(upstream.URL)
		store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
		budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
		collector := &fakeCollector{}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/resource", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		tx := collector.transactions[0]
		if tx.Cost != 0.01 {
			t.Errorf("expected cost 0.01 (per_request fallback), got %f", tx.Cost)
		}
		if tx.CostSource != "flat" {
			t.Errorf("expected cost_source flat, got %s", tx.CostSource)
		}
	})
}

func TestQueryAuth(t *testing.T) {
	var receivedQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	t.Run("default param name", func(t *testing.T) {
		tool := newTestTool(upstream.URL)
		tool.AuthType = "query"
		tool.AuthConfig = map[string]string{"key": "secret123"}

		store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
		budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
		collector := &fakeCollector{}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/resource", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if receivedQuery != "api_key=secret123" {
			t.Errorf("expected query api_key=secret123, got %s", receivedQuery)
		}
	})

	t.Run("custom param name", func(t *testing.T) {
		tool := newTestTool(upstream.URL)
		tool.AuthType = "query"
		tool.AuthConfig = map[string]string{"key": "mykey", "param_name": "token"}

		store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
		budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
		collector := &fakeCollector{}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/resource", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if receivedQuery != "token=mykey" {
			t.Errorf("expected query token=mykey, got %s", receivedQuery)
		}
	})

	t.Run("with existing query params", func(t *testing.T) {
		tool := newTestTool(upstream.URL)
		tool.AuthType = "query"
		tool.AuthConfig = map[string]string{"key": "secret123"}

		store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
		budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
		collector := &fakeCollector{}
		handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
		router := setupRouter(handler)

		req := httptest.NewRequest("GET", "/proxy/tool-1/resource?foo=bar", nil)
		req = withAgent(req, newTestAgent())
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		// Should contain both the original param and the auth param.
		if receivedQuery != "api_key=secret123&foo=bar" && receivedQuery != "foo=bar&api_key=secret123" {
			t.Errorf("expected query to contain both foo=bar and api_key=secret123, got %s", receivedQuery)
		}
	})
}

func TestPermissionDenied(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetPermissionChecker(&fakePermissionChecker{allowed: false})

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}

	var errResp proxyError
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "permission_denied" {
		t.Errorf("expected error code permission_denied, got %s", errResp.Error.Code)
	}
}

func TestRetryOn5xx(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	tool.MaxRetries = 2
	tool.RetryBackoffMs = 10 // fast for tests
	tool.TimeoutMs = 5000
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if attempts != 3 {
		t.Errorf("expected 3 upstream attempts, got %d", attempts)
	}
	// Should have 3 transactions: 2 failed + 1 success.
	if len(collector.transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(collector.transactions))
	}
	if collector.transactions[0].StatusCode != 503 || collector.transactions[1].StatusCode != 503 {
		t.Errorf("expected first two transactions to be 503")
	}
	if collector.transactions[2].StatusCode != 200 {
		t.Errorf("expected last transaction to be 200, got %d", collector.transactions[2].StatusCode)
	}
}

func TestRetryExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	tool.MaxRetries = 2
	tool.RetryBackoffMs = 10
	tool.TimeoutMs = 5000
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rr.Code)
	}
	// 3 attempts total (1 + 2 retries), all recorded as transactions.
	if len(collector.transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(collector.transactions))
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	tool.MaxRetries = 2
	tool.RetryBackoffMs = 10
	tool.TimeoutMs = 5000
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	// 4xx should not be retried.
	if attempts != 1 {
		t.Errorf("expected 1 attempt for 4xx, got %d", attempts)
	}
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
	}
}

func TestPerToolTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	tool.TimeoutMs = 50 // 50ms timeout, upstream takes 500ms
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 30*time.Second, 1<<20)
	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 (timeout), got %d", rr.Code)
	}
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
	}
}

func TestPermissionAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetPermissionChecker(&fakePermissionChecker{allowed: true})

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

// --- Circuit Breaker Fakes ---

type fakeCircuitBreaker struct {
	allowResult bool
	results     []int
}

func (f *fakeCircuitBreaker) Allow(_ string, _ CircuitBreakerConfig) bool {
	return f.allowResult
}

func (f *fakeCircuitBreaker) RecordResult(_ string, _ CircuitBreakerConfig, statusCode int) {
	f.results = append(f.results, statusCode)
}

func TestCircuitBreakerOpen(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	tool.CBEnabled = true
	tool.CBErrorThresholdPct = 90
	tool.CBWindowSeconds = 120
	tool.CBCooldownSeconds = 60
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetCircuitBreaker(&fakeCircuitBreaker{allowResult: false})

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}

	var errResp proxyError
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "circuit_open" {
		t.Errorf("expected error code circuit_open, got %s", errResp.Error.Code)
	}
}

func TestCircuitBreakerRecordsResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	tool.CBEnabled = true
	tool.CBErrorThresholdPct = 90
	tool.CBWindowSeconds = 120
	tool.CBCooldownSeconds = 60
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	cb := &fakeCircuitBreaker{allowResult: true}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetCircuitBreaker(cb)

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1/test", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if len(cb.results) != 1 || cb.results[0] != 200 {
		t.Errorf("expected CB result [200], got %v", cb.results)
	}
}

// --- MCP sub-tool permission tests ---

type fakeMCPCaller struct {
	called  bool
	err     error
	isError bool // if true, returns result with IsError=true
}

func (f *fakeMCPCaller) Call(_ context.Context, _ string, _ string, _ map[string]any) (*mcpgo.CallToolResult, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	if f.isError {
		r := mcpgo.NewToolResultError("upstream error")
		return r, nil
	}
	return mcpgo.NewToolResultText("ok"), nil
}

// --- Webhook Dispatcher Fake ---

type fakeWebhookDispatcher struct {
	dispatched bool
	toolID     string
	toolName   string
}

func (f *fakeWebhookDispatcher) MaybeDispatch(toolID, toolName, webhookURL string, thresholdPct int, budgetLimit, currentUsage float64) {
	f.dispatched = true
	f.toolID = toolID
	f.toolName = toolName
}

func TestMCPSubToolPermissionDenied(t *testing.T) {
	tool := newTestTool("http://localhost")
	tool.Mode = "mcp"
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetMCPCaller(&fakeMCPCaller{})
	perm := &fakePermissionChecker{allowed: true, subToolAllowed: false}
	handler.SetPermissionChecker(perm)

	router := setupRouter(handler)

	req := httptest.NewRequest("POST", "/proxy/tool-1/search_code", bytes.NewReader([]byte(`{}`)))
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp proxyError
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "permission_denied" {
		t.Errorf("expected error code permission_denied, got %s", errResp.Error.Code)
	}
	if !perm.subToolChecked {
		t.Error("expected sub-tool permission check to be called")
	}
}

func TestMCPSubToolPermissionAllowed(t *testing.T) {
	tool := newTestTool("http://localhost")
	tool.Mode = "mcp"
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	mcpUp := &fakeMCPCaller{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetMCPCaller(mcpUp)
	perm := &fakePermissionChecker{allowed: true, subToolAllowed: true}
	handler.SetPermissionChecker(perm)

	router := setupRouter(handler)

	req := httptest.NewRequest("POST", "/proxy/tool-1/search_code", bytes.NewReader([]byte(`{}`)))
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code == http.StatusForbidden {
		t.Fatal("expected to pass permission check, but got 403")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !perm.subToolChecked {
		t.Error("expected sub-tool permission check to be called")
	}
	if !mcpUp.called {
		t.Error("expected MCP caller to be invoked")
	}
}

// --- MCP sub-tool listing fakes ---

type fakeMCPToolLister struct {
	tools map[string][]mcpgo.Tool
}

func (f *fakeMCPToolLister) GetUpstreamTools(id string) ([]mcpgo.Tool, bool) {
	tools, ok := f.tools[id]
	return tools, ok
}

// --- MCP sub-tool listing tests ---

func TestListMCPSubTools(t *testing.T) {
	tool := newTestTool("http://localhost")
	tool.Mode = "mcp"
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetMCPCaller(&fakeMCPCaller{})
	handler.SetMCPToolLister(&fakeMCPToolLister{
		tools: map[string][]mcpgo.Tool{
			"tool-1": {
				{Name: "search", Description: "Search the web"},
				{Name: "fetch", Description: "Fetch a URL"},
			},
		},
	})

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["tool_id"] != "tool-1" {
		t.Errorf("expected tool_id tool-1, got %v", resp["tool_id"])
	}
	if resp["tool_name"] != "test-tool" {
		t.Errorf("expected tool_name test-tool, got %v", resp["tool_name"])
	}
	subTools, ok := resp["sub_tools"].([]any)
	if !ok {
		t.Fatalf("expected sub_tools to be an array, got %T", resp["sub_tools"])
	}
	if len(subTools) != 2 {
		t.Fatalf("expected 2 sub-tools, got %d", len(subTools))
	}
}

func TestListMCPSubToolsNotConfigured(t *testing.T) {
	tool := newTestTool("http://localhost")
	tool.Mode = "mcp"
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	// Do NOT set mcpToolLister — leave it nil.

	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/proxy/tool-1", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}

	var errResp proxyError
	_ = json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp.Error.Code != "proxy_error" {
		t.Errorf("expected error code proxy_error, got %s", errResp.Error.Code)
	}
}

func TestListMCPSubToolsNotMCP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	// Mode is empty (default HTTP service mode), not "mcp".
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetMCPToolLister(&fakeMCPToolLister{
		tools: map[string][]mcpgo.Tool{
			"tool-1": {{Name: "search"}},
		},
	})

	router := setupRouter(handler)

	// GET /proxy/tool-1 for a non-MCP tool should fall through to normal HTTP proxy.
	req := httptest.NewRequest("GET", "/proxy/tool-1", nil)
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// Should proxy to upstream successfully, not list sub-tools.
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{"ok":true}` {
		t.Errorf("expected upstream response body, got %s", rr.Body.String())
	}
}

// --- MCP circuit breaker and webhook tests ---

func TestMCPCircuitBreakerRecordsSuccess(t *testing.T) {
	tool := newTestTool("http://localhost")
	tool.Mode = "mcp"
	tool.CBEnabled = true
	tool.CBErrorThresholdPct = 90
	tool.CBWindowSeconds = 120
	tool.CBCooldownSeconds = 60
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	cb := &fakeCircuitBreaker{allowResult: true}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetCircuitBreaker(cb)
	handler.SetMCPCaller(&fakeMCPCaller{})

	router := setupRouter(handler)

	req := httptest.NewRequest("POST", "/proxy/tool-1/search_code", bytes.NewReader([]byte(`{}`)))
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(cb.results) != 1 || cb.results[0] != 200 {
		t.Errorf("expected CB result [200], got %v", cb.results)
	}
}

func TestMCPCircuitBreakerRecordsError(t *testing.T) {
	tool := newTestTool("http://localhost")
	tool.Mode = "mcp"
	tool.CBEnabled = true
	tool.CBErrorThresholdPct = 90
	tool.CBWindowSeconds = 120
	tool.CBCooldownSeconds = 60
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	cb := &fakeCircuitBreaker{allowResult: true}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetCircuitBreaker(cb)
	handler.SetMCPCaller(&fakeMCPCaller{err: fmt.Errorf("upstream unavailable")})

	router := setupRouter(handler)

	req := httptest.NewRequest("POST", "/proxy/tool-1/search_code", bytes.NewReader([]byte(`{}`)))
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(cb.results) != 1 || cb.results[0] != 502 {
		t.Errorf("expected CB result [502], got %v", cb.results)
	}
}

func TestMCPWebhookDispatched(t *testing.T) {
	tool := newTestTool("http://localhost")
	tool.Mode = "mcp"
	tool.BudgetLimit = 1000
	tool.WebhookURL = "https://example.com/webhook"
	tool.WebhookThresholdPct = 80
	store := &fakeToolStore{tools: map[string]*registry.Tool{"tool-1": tool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	collector := &fakeCollector{}
	wh := &fakeWebhookDispatcher{}
	handler := NewHandler(store, budgets, collector, 5*time.Second, 1<<20)
	handler.SetMCPCaller(&fakeMCPCaller{})
	handler.SetWebhookDispatcher(wh)

	router := setupRouter(handler)

	req := httptest.NewRequest("POST", "/proxy/tool-1/search_code", bytes.NewReader([]byte(`{}`)))
	req = withAgent(req, newTestAgent())
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !wh.dispatched {
		t.Error("expected webhook dispatcher to be called")
	}
	if wh.toolID != "tool-1" {
		t.Errorf("expected webhook toolID tool-1, got %s", wh.toolID)
	}
}
