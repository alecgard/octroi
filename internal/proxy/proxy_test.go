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
)

// --- Fakes ---

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
