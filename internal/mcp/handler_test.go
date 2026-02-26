package mcp

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
	"github.com/alecgard/octroi/internal/ratelimit"
	"github.com/alecgard/octroi/internal/registry"
)

// --- Fakes ---

type fakeToolStore struct {
	tools []*registry.Tool
}

func (f *fakeToolStore) List(_ context.Context, _ registry.ToolListParams) ([]*registry.Tool, string, error) {
	return f.tools, "", nil
}

func (f *fakeToolStore) GetByID(_ context.Context, id string) (*registry.Tool, error) {
	for _, t := range f.tools {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

type fakeBudgetChecker struct {
	agentAllowed  bool
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

type fakeToolRateLimiter struct {
	allowed bool
}

func (f *fakeToolRateLimiter) CheckToolRateLimit(_ context.Context, _, _, _ string) (bool, int, int, time.Time, error) {
	return f.allowed, 100, 50, time.Now().Add(time.Minute), nil
}

type fakeAgentLookup struct {
	agent *auth.Agent
}

func (f *fakeAgentLookup) GetByKeyHash(_ context.Context, hash string) (*auth.Agent, error) {
	if f.agent != nil {
		return f.agent, nil
	}
	return nil, fmt.Errorf("not found")
}

type fakeMetricsRecorder struct{}

func (f *fakeMetricsRecorder) IncProxyRequests(_, _, _, _ string, _ int)   {}
func (f *fakeMetricsRecorder) ObserveUpstreamDuration(_, _ string, _ float64) {}
func (f *fakeMetricsRecorder) IncActiveRequests(_ string)                     {}
func (f *fakeMetricsRecorder) DecActiveRequests(_ string)                     {}
func (f *fakeMetricsRecorder) IncBudgetRejection(_ string)                    {}
func (f *fakeMetricsRecorder) IncToolRateLimitRejection()                     {}
func (f *fakeMetricsRecorder) IncUpstreamError(_, _, _ string)                {}

// --- Helpers ---

func newTestAgent() *auth.Agent {
	return &auth.Agent{
		ID:        "agent-1",
		Name:      "test-agent",
		Team:      "default",
		RateLimit: 100,
	}
}

func newTestTool(endpoint string) *registry.Tool {
	return &registry.Tool{
		ID:            "tool-1",
		Name:          "test-tool",
		Description:   "A test tool",
		Endpoint:      endpoint,
		AuthType:      "none",
		AuthConfig:    map[string]string{},
		PricingModel:  "per_request",
		PricingAmount: 0.01,
	}
}

// jsonRPCRequest builds a JSON-RPC 2.0 request body.
func jsonRPCRequest(id int, method string, params any) []byte {
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      id,
	}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	return b
}

func setupMCPHandler(tools []*registry.Tool, agnt *auth.Agent, budgets *fakeBudgetChecker, collector *fakeCollector, rateLimiter *fakeToolRateLimiter, limiter *ratelimit.Limiter) *Handler {
	store := &fakeToolStore{tools: tools}

	h := NewHandler(HandlerDeps{
		Tools:          store,
		Budgets:        budgets,
		Collector:      collector,
		ToolRateLimits: rateLimiter,
		Limiter:        limiter,
		Metrics:        &fakeMetricsRecorder{},
		AgentLookup:    &fakeAgentLookup{agent: agnt},
		ProxyTimeout:   5 * time.Second,
		MaxRequestSize: 1 << 20,
	})

	_ = h.RefreshTools(context.Background())
	return h
}

// mcpRequest sends a JSON-RPC request to the MCP handler and returns the response body.
func mcpRequest(t *testing.T, handler *Handler, apiKey string, body []byte) (int, map[string]any) {
	t.Helper()

	// First initialize the session.
	initBody := jsonRPCRequest(0, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	initReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		initReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	initRR := httptest.NewRecorder()
	handler.ServeHTTP(initRR, initReq)

	// Now send the actual request.
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var result map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&result)
	return rr.Code, result
}

// --- Tests ---

func TestToolsList(t *testing.T) {
	tool := newTestTool("http://localhost")
	handler := setupMCPHandler(
		[]*registry.Tool{tool},
		newTestAgent(),
		&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
		&fakeCollector{},
		&fakeToolRateLimiter{allowed: true},
		nil,
	)

	body := jsonRPCRequest(1, "tools/list", map[string]any{})
	_, result := mcpRequest(t, handler, "test-key", body)

	// Check result has tools.
	resultObj, ok := result["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", result)
	}
	tools, ok := resultObj["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %v", resultObj)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	toolObj := tools[0].(map[string]any)
	if toolObj["name"] != "test-tool" {
		t.Errorf("expected tool name test-tool, got %v", toolObj["name"])
	}
	if toolObj["description"] != "A test tool" {
		t.Errorf("expected tool description, got %v", toolObj["description"])
	}
}

func TestToolCallSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/data" {
			t.Errorf("expected path /data, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	collector := &fakeCollector{}
	handler := setupMCPHandler(
		[]*registry.Tool{tool},
		newTestAgent(),
		&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
		collector,
		&fakeToolRateLimiter{allowed: true},
		nil,
	)

	body := jsonRPCRequest(2, "tools/call", map[string]any{
		"name":      "test-tool",
		"arguments": map[string]any{"method": "GET", "path": "/data"},
	})
	_, result := mcpRequest(t, handler, "test-key", body)

	resultObj, ok := result["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", result)
	}

	content, ok := resultObj["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content array, got %v", resultObj)
	}

	textContent := content[0].(map[string]any)
	if textContent["text"] != `{"status":"ok"}` {
		t.Errorf("expected response body in text content, got %v", textContent["text"])
	}

	// Verify transaction was recorded.
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
	}
	tx := collector.transactions[0]
	if tx.AgentID != "agent-1" || tx.ToolID != "tool-1" || !tx.Success {
		t.Errorf("unexpected transaction: %+v", tx)
	}
}

func TestToolCallWithBody(t *testing.T) {
	var receivedBody string
	var receivedMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("created"))
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	handler := setupMCPHandler(
		[]*registry.Tool{tool},
		newTestAgent(),
		&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
		&fakeCollector{},
		&fakeToolRateLimiter{allowed: true},
		nil,
	)

	body := jsonRPCRequest(3, "tools/call", map[string]any{
		"name": "test-tool",
		"arguments": map[string]any{
			"method": "POST",
			"path":   "/items",
			"body":   `{"name":"widget"}`,
		},
	})
	_, _ = mcpRequest(t, handler, "test-key", body)

	if receivedMethod != "POST" {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedBody != `{"name":"widget"}` {
		t.Errorf("expected body, got %s", receivedBody)
	}
}

func TestToolCallNoAuth(t *testing.T) {
	tool := newTestTool("http://localhost")
	handler := setupMCPHandler(
		[]*registry.Tool{tool},
		nil, // No agent for lookup
		&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
		&fakeCollector{},
		&fakeToolRateLimiter{allowed: true},
		nil,
	)

	body := jsonRPCRequest(4, "tools/call", map[string]any{
		"name":      "test-tool",
		"arguments": map[string]any{"method": "GET", "path": "/"},
	})
	_, result := mcpRequest(t, handler, "invalid-key", body)

	resultObj, ok := result["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", result)
	}

	isError, _ := resultObj["isError"].(bool)
	if !isError {
		t.Error("expected isError to be true for unauthenticated request")
	}

	content, ok := resultObj["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected error content, got %v", resultObj)
	}
	text := content[0].(map[string]any)["text"].(string)
	if text != "unauthorized: missing or invalid agent credentials" {
		t.Errorf("expected unauthorized error, got %s", text)
	}
}

func TestToolCallBudgetExceeded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called when budget is exceeded")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	t.Run("agent budget exceeded", func(t *testing.T) {
		tool := newTestTool(upstream.URL)
		handler := setupMCPHandler(
			[]*registry.Tool{tool},
			newTestAgent(),
			&fakeBudgetChecker{agentAllowed: false, globalAllowed: true},
			&fakeCollector{},
			&fakeToolRateLimiter{allowed: true},
			nil,
		)

		body := jsonRPCRequest(5, "tools/call", map[string]any{
			"name":      "test-tool",
			"arguments": map[string]any{"method": "GET", "path": "/"},
		})
		_, result := mcpRequest(t, handler, "test-key", body)

		resultObj := result["result"].(map[string]any)
		if !resultObj["isError"].(bool) {
			t.Error("expected isError to be true")
		}
		content := resultObj["content"].([]any)
		text := content[0].(map[string]any)["text"].(string)
		if text != "budget_exceeded: agent budget exceeded for this tool" {
			t.Errorf("expected budget_exceeded error, got %s", text)
		}
	})

	t.Run("global budget exceeded", func(t *testing.T) {
		tool := newTestTool(upstream.URL)
		handler := setupMCPHandler(
			[]*registry.Tool{tool},
			newTestAgent(),
			&fakeBudgetChecker{agentAllowed: true, globalAllowed: false},
			&fakeCollector{},
			&fakeToolRateLimiter{allowed: true},
			nil,
		)

		body := jsonRPCRequest(6, "tools/call", map[string]any{
			"name":      "test-tool",
			"arguments": map[string]any{"method": "GET", "path": "/"},
		})
		_, result := mcpRequest(t, handler, "test-key", body)

		resultObj := result["result"].(map[string]any)
		if !resultObj["isError"].(bool) {
			t.Error("expected isError to be true")
		}
		content := resultObj["content"].([]any)
		text := content[0].(map[string]any)["text"].(string)
		if text != "budget_exceeded: global tool budget exceeded" {
			t.Errorf("expected global budget_exceeded error, got %s", text)
		}
	})
}

func TestToolCallRateLimitExceeded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called when rate limited")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tool := newTestTool(upstream.URL)
	handler := setupMCPHandler(
		[]*registry.Tool{tool},
		newTestAgent(),
		&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
		&fakeCollector{},
		&fakeToolRateLimiter{allowed: false},
		nil,
	)

	body := jsonRPCRequest(7, "tools/call", map[string]any{
		"name":      "test-tool",
		"arguments": map[string]any{"method": "GET", "path": "/"},
	})
	_, result := mcpRequest(t, handler, "test-key", body)

	resultObj := result["result"].(map[string]any)
	if !resultObj["isError"].(bool) {
		t.Error("expected isError to be true")
	}
	content := resultObj["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if text != "rate_limited: tool rate limit exceeded" {
		t.Errorf("expected rate_limited error, got %s", text)
	}
}

func TestToolCallCredentialInjection(t *testing.T) {
	t.Run("bearer auth", func(t *testing.T) {
		var receivedAuth string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer upstream.Close()

		tool := newTestTool(upstream.URL)
		tool.AuthType = "bearer"
		tool.AuthConfig = map[string]string{"key": "secret-token-123"}

		handler := setupMCPHandler(
			[]*registry.Tool{tool},
			newTestAgent(),
			&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
			&fakeCollector{},
			&fakeToolRateLimiter{allowed: true},
			nil,
		)

		body := jsonRPCRequest(8, "tools/call", map[string]any{
			"name":      "test-tool",
			"arguments": map[string]any{"method": "GET", "path": "/resource"},
		})
		_, _ = mcpRequest(t, handler, "test-key", body)

		expected := "Bearer secret-token-123"
		if receivedAuth != expected {
			t.Errorf("expected Authorization %q, got %q", expected, receivedAuth)
		}
	})

	t.Run("header auth", func(t *testing.T) {
		var receivedKey string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedKey = r.Header.Get("X-Api-Key")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer upstream.Close()

		tool := newTestTool(upstream.URL)
		tool.AuthType = "header"
		tool.AuthConfig = map[string]string{"header_name": "X-Api-Key", "key": "my-api-key"}

		handler := setupMCPHandler(
			[]*registry.Tool{tool},
			newTestAgent(),
			&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
			&fakeCollector{},
			&fakeToolRateLimiter{allowed: true},
			nil,
		)

		body := jsonRPCRequest(9, "tools/call", map[string]any{
			"name":      "test-tool",
			"arguments": map[string]any{"method": "GET", "path": "/resource"},
		})
		_, _ = mcpRequest(t, handler, "test-key", body)

		if receivedKey != "my-api-key" {
			t.Errorf("expected X-Api-Key %q, got %q", "my-api-key", receivedKey)
		}
	})

	t.Run("query auth", func(t *testing.T) {
		var receivedQuery string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer upstream.Close()

		tool := newTestTool(upstream.URL)
		tool.AuthType = "query"
		tool.AuthConfig = map[string]string{"key": "secret123"}

		handler := setupMCPHandler(
			[]*registry.Tool{tool},
			newTestAgent(),
			&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
			&fakeCollector{},
			&fakeToolRateLimiter{allowed: true},
			nil,
		)

		body := jsonRPCRequest(10, "tools/call", map[string]any{
			"name":      "test-tool",
			"arguments": map[string]any{"method": "GET", "path": "/resource"},
		})
		_, _ = mcpRequest(t, handler, "test-key", body)

		if receivedQuery != "api_key=secret123" {
			t.Errorf("expected query api_key=secret123, got %s", receivedQuery)
		}
	})
}

func TestToolNameDeduplication(t *testing.T) {
	tool1 := &registry.Tool{
		ID:          "tool-1",
		Name:        "weather",
		Description: "First weather tool",
		Endpoint:    "http://localhost",
		AuthType:    "none",
		AuthConfig:  map[string]string{},
	}
	tool2 := &registry.Tool{
		ID:          "tool-2",
		Name:        "weather",
		Description: "Second weather tool",
		Endpoint:    "http://localhost",
		AuthType:    "none",
		AuthConfig:  map[string]string{},
	}

	handler := setupMCPHandler(
		[]*registry.Tool{tool1, tool2},
		newTestAgent(),
		&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
		&fakeCollector{},
		&fakeToolRateLimiter{allowed: true},
		nil,
	)

	body := jsonRPCRequest(11, "tools/list", map[string]any{})
	_, result := mcpRequest(t, handler, "test-key", body)

	resultObj := result["result"].(map[string]any)
	tools := resultObj["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		name := tool.(map[string]any)["name"].(string)
		names[name] = true
	}

	if !names["weather"] {
		t.Error("expected first tool to be named 'weather'")
	}
	if !names["weather-2"] {
		t.Error("expected second tool to be named 'weather-2'")
	}
}

func TestUpstreamError(t *testing.T) {
	// Use an unreachable address.
	tool := newTestTool("http://127.0.0.1:1")
	collector := &fakeCollector{}
	handler := setupMCPHandler(
		[]*registry.Tool{tool},
		newTestAgent(),
		&fakeBudgetChecker{agentAllowed: true, globalAllowed: true},
		collector,
		&fakeToolRateLimiter{allowed: true},
		nil,
	)

	body := jsonRPCRequest(12, "tools/call", map[string]any{
		"name":      "test-tool",
		"arguments": map[string]any{"method": "GET", "path": "/test"},
	})
	_, result := mcpRequest(t, handler, "test-key", body)

	resultObj := result["result"].(map[string]any)
	if !resultObj["isError"].(bool) {
		t.Error("expected isError to be true for upstream error")
	}

	// Verify transaction was recorded for the error.
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
	}
	if collector.transactions[0].Success {
		t.Error("expected transaction to be marked as failed")
	}
}
