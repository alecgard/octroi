package mcp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/mark3labs/mcp-go/mcp"
)

// --- Fakes ---

type fakeRouter struct {
	routes map[string]ToolRoute
}

func (f *fakeRouter) Route(name string) (ToolRoute, bool) {
	r, ok := f.routes[name]
	return r, ok
}

type fakeToolStore struct {
	tools map[string]*registry.Tool
}

func (f *fakeToolStore) GetByID(_ context.Context, id string) (*registry.Tool, error) {
	t, ok := f.tools[id]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", id)
	}
	return t, nil
}

func (f *fakeToolStore) GetByIDIncludeArchived(_ context.Context, id string) (*registry.Tool, error) {
	t, ok := f.tools[id]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", id)
	}
	return t, nil
}

type fakeBudgetChecker struct {
	agentAllowed  bool
	globalAllowed bool
}

func (f *fakeBudgetChecker) CheckBudget(_ context.Context, _, _ string) (bool, float64, float64, error) {
	return f.agentAllowed, 100, 1000, nil
}

func (f *fakeBudgetChecker) CheckToolGlobalBudget(_ context.Context, _ string) (bool, float64, error) {
	return f.globalAllowed, 5000, nil
}

type fakeRateLimiter struct {
	allowed bool
}

func (f *fakeRateLimiter) CheckToolRateLimit(_ context.Context, _, _, _ string) (bool, int, int, time.Time, error) {
	return f.allowed, 100, 50, time.Now().Add(time.Minute), nil
}

type fakeRESTCaller struct {
	result     *mcp.CallToolResult
	callResult *CallResult
	err        error
	called     bool
}

func (f *fakeRESTCaller) Call(_ context.Context, _ *registry.Tool, _ map[string]any) (*mcp.CallToolResult, *CallResult, error) {
	f.called = true
	return f.result, f.callResult, f.err
}

type fakeMCPCaller struct {
	result *mcp.CallToolResult
	err    error
	called bool
}

func (f *fakeMCPCaller) Call(_ context.Context, _ string, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
	f.called = true
	return f.result, f.err
}

type fakeCollector struct {
	transactions []metering.Transaction
}

func (f *fakeCollector) Record(tx metering.Transaction) {
	f.transactions = append(f.transactions, tx)
}

type fakePermissionChecker struct {
	allowed        bool
	subToolAllowed bool
}

func (f *fakePermissionChecker) IsAllowed(_ context.Context, _, _ string) (bool, error) {
	return f.allowed, nil
}

func (f *fakePermissionChecker) IsSubToolAllowed(_ context.Context, _, _, _ string) (bool, error) {
	return f.subToolAllowed, nil
}

// --- Helpers ---

var testToolID = "tool-1"

func defaultTool() *registry.Tool {
	return &registry.Tool{
		ID:            testToolID,
		Name:          "search",
		PricingModel:  "per_request",
		PricingAmount: 0.01,
	}
}

func defaultAgent() AgentInfo {
	return AgentInfo{ID: "agent-1", Name: "test-agent", Team: "team-1"}
}

func newGovernedCaller(
	router ToolRouter,
	store *fakeToolStore,
	budgets *fakeBudgetChecker,
	rl *fakeRateLimiter,
	collector *fakeCollector,
	rest *fakeRESTCaller,
	mcpUp *fakeMCPCaller,
) *GovernedCaller {
	return NewGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
}

// --- Tests ---

func TestGovernedCaller_RESTSuccess(t *testing.T) {
	router := &fakeRouter{routes: map[string]ToolRoute{
		"search": {Source: "rest", ToolID: testToolID},
	}}
	store := &fakeToolStore{tools: map[string]*registry.Tool{testToolID: defaultTool()}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	rl := &fakeRateLimiter{allowed: true}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{
		result: mcp.NewToolResultText("result data"),
		callResult: &CallResult{
			Success:    true,
			StatusCode: 200,
			Cost:       0.01,
			CostSource: "flat",
			Latency:    50 * time.Millisecond,
		},
	}
	mcpUp := &fakeMCPCaller{}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	result, err := gc.CallTool(context.Background(), defaultAgent(), "search", map[string]any{"q": "test"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result, got error")
	}
	if !rest.called {
		t.Error("REST caller was not invoked")
	}
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
	}

	tx := collector.transactions[0]
	if tx.AgentID != "agent-1" {
		t.Errorf("tx.AgentID = %q, want %q", tx.AgentID, "agent-1")
	}
	if tx.ToolID != testToolID {
		t.Errorf("tx.ToolID = %q, want %q", tx.ToolID, testToolID)
	}
	if tx.Method != "tools/call" {
		t.Errorf("tx.Method = %q, want %q", tx.Method, "tools/call")
	}
	if tx.Path != "search" {
		t.Errorf("tx.Path = %q, want %q", tx.Path, "search")
	}
	if tx.StatusCode != 200 {
		t.Errorf("tx.StatusCode = %d, want %d", tx.StatusCode, 200)
	}
	if !tx.Success {
		t.Error("tx.Success = false, want true")
	}
	if tx.Cost != 0.01 {
		t.Errorf("tx.Cost = %f, want %f", tx.Cost, 0.01)
	}
}

func TestGovernedCaller_RateLimitRejection(t *testing.T) {
	router := &fakeRouter{routes: map[string]ToolRoute{
		"search": {Source: "rest", ToolID: testToolID},
	}}
	store := &fakeToolStore{tools: map[string]*registry.Tool{testToolID: defaultTool()}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	rl := &fakeRateLimiter{allowed: false}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{}
	mcpUp := &fakeMCPCaller{}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	result, err := gc.CallTool(context.Background(), defaultAgent(), "search", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for rate limit rejection")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want TextContent", result.Content[0])
	}
	if tc.Text != "rate limit exceeded for tool search" {
		t.Errorf("error text = %q", tc.Text)
	}
	if rest.called {
		t.Error("REST caller should not have been invoked")
	}
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 metering transaction, got %d", len(collector.transactions))
	}
	if collector.transactions[0].StatusCode != 429 {
		t.Errorf("tx.StatusCode = %d, want 429", collector.transactions[0].StatusCode)
	}
}

func TestGovernedCaller_AgentBudgetRejection(t *testing.T) {
	router := &fakeRouter{routes: map[string]ToolRoute{
		"search": {Source: "rest", ToolID: testToolID},
	}}
	store := &fakeToolStore{tools: map[string]*registry.Tool{testToolID: defaultTool()}}
	budgets := &fakeBudgetChecker{agentAllowed: false, globalAllowed: true}
	rl := &fakeRateLimiter{allowed: true}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{}
	mcpUp := &fakeMCPCaller{}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	result, err := gc.CallTool(context.Background(), defaultAgent(), "search", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for budget rejection")
	}
	tc := result.Content[0].(mcp.TextContent)
	if tc.Text != "agent budget exceeded for tool search" {
		t.Errorf("error text = %q", tc.Text)
	}
	if rest.called {
		t.Error("REST caller should not have been invoked")
	}
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 metering transaction, got %d", len(collector.transactions))
	}
	if collector.transactions[0].StatusCode != 403 {
		t.Errorf("tx.StatusCode = %d, want 403", collector.transactions[0].StatusCode)
	}
}

func TestGovernedCaller_GlobalBudgetRejection(t *testing.T) {
	router := &fakeRouter{routes: map[string]ToolRoute{
		"search": {Source: "rest", ToolID: testToolID},
	}}
	store := &fakeToolStore{tools: map[string]*registry.Tool{testToolID: defaultTool()}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: false}
	rl := &fakeRateLimiter{allowed: true}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{}
	mcpUp := &fakeMCPCaller{}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	result, err := gc.CallTool(context.Background(), defaultAgent(), "search", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for global budget rejection")
	}
	tc := result.Content[0].(mcp.TextContent)
	if tc.Text != "global budget exceeded for tool search" {
		t.Errorf("error text = %q", tc.Text)
	}
	if rest.called {
		t.Error("REST caller should not have been invoked")
	}
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 metering transaction, got %d", len(collector.transactions))
	}
	if collector.transactions[0].StatusCode != 403 {
		t.Errorf("tx.StatusCode = %d, want 403", collector.transactions[0].StatusCode)
	}
}

func TestGovernedCaller_MCPUpstreamSuccess(t *testing.T) {
	mcpToolID := "mcp-tool-1"
	mcpTool := &registry.Tool{
		ID:   mcpToolID,
		Name: "upstream",
		Mode: "mcp",
	}
	router := &fakeRouter{routes: map[string]ToolRoute{
		"upstream__search": {Source: "mcp", ToolID: mcpToolID, UpstreamID: mcpToolID, UpstreamToolName: "search"},
	}}
	store := &fakeToolStore{tools: map[string]*registry.Tool{mcpToolID: mcpTool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	rl := &fakeRateLimiter{allowed: true}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{}
	mcpUp := &fakeMCPCaller{
		result: mcp.NewToolResultText("upstream result"),
	}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	result, err := gc.CallTool(context.Background(), defaultAgent(), "upstream__search", map[string]any{"q": "test"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result, got error")
	}
	if !mcpUp.called {
		t.Error("MCP upstream caller was not invoked")
	}
	if rest.called {
		t.Error("REST caller should not have been invoked for MCP tool")
	}

	tc := result.Content[0].(mcp.TextContent)
	if tc.Text != "upstream result" {
		t.Errorf("result text = %q, want %q", tc.Text, "upstream result")
	}

	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
	}
	tx := collector.transactions[0]
	if tx.ToolID != mcpToolID {
		t.Errorf("tx.ToolID = %q, want %q", tx.ToolID, mcpToolID)
	}
	if tx.Path != "upstream__search" {
		t.Errorf("tx.Path = %q, want %q", tx.Path, "upstream__search")
	}
	if tx.StatusCode != 200 {
		t.Errorf("tx.StatusCode = %d, want 200", tx.StatusCode)
	}
}

func TestGovernedCaller_UnknownTool(t *testing.T) {
	router := &fakeRouter{routes: map[string]ToolRoute{}}
	store := &fakeToolStore{}
	budgets := &fakeBudgetChecker{}
	rl := &fakeRateLimiter{}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{}
	mcpUp := &fakeMCPCaller{}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	result, err := gc.CallTool(context.Background(), defaultAgent(), "nonexistent", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for unknown tool")
	}
	tc := result.Content[0].(mcp.TextContent)
	if tc.Text != "unknown tool: nonexistent" {
		t.Errorf("error text = %q", tc.Text)
	}
	if len(collector.transactions) != 0 {
		t.Errorf("expected 0 transactions for unknown tool, got %d", len(collector.transactions))
	}
}

func TestGovernedCaller_MeteringRecorded(t *testing.T) {
	router := &fakeRouter{routes: map[string]ToolRoute{
		"search": {Source: "rest", ToolID: testToolID},
	}}
	store := &fakeToolStore{tools: map[string]*registry.Tool{testToolID: defaultTool()}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	rl := &fakeRateLimiter{allowed: true}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{
		result: mcp.NewToolResultText("ok"),
		callResult: &CallResult{
			Success:    true,
			StatusCode: 200,
			Cost:       0.05,
			CostSource: "reported",
			Latency:    120 * time.Millisecond,
		},
	}
	mcpUp := &fakeMCPCaller{}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	_, err := gc.CallTool(context.Background(), defaultAgent(), "search", map[string]any{"q": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
	}

	tx := collector.transactions[0]
	if tx.AgentID != "agent-1" {
		t.Errorf("tx.AgentID = %q, want %q", tx.AgentID, "agent-1")
	}
	if tx.ToolID != testToolID {
		t.Errorf("tx.ToolID = %q, want %q", tx.ToolID, testToolID)
	}
	if tx.Method != "tools/call" {
		t.Errorf("tx.Method = %q, want %q", tx.Method, "tools/call")
	}
	if tx.Path != "search" {
		t.Errorf("tx.Path = %q, want %q", tx.Path, "search")
	}
	if tx.StatusCode != 200 {
		t.Errorf("tx.StatusCode = %d, want 200", tx.StatusCode)
	}
	if !tx.Success {
		t.Error("tx.Success = false, want true")
	}
	if tx.Cost != 0.05 {
		t.Errorf("tx.Cost = %f, want 0.05", tx.Cost)
	}
	if tx.CostSource != "reported" {
		t.Errorf("tx.CostSource = %q, want %q", tx.CostSource, "reported")
	}
	if tx.Timestamp.IsZero() {
		t.Error("tx.Timestamp should not be zero")
	}
}

func TestGovernedCaller_MCPSubToolDenied(t *testing.T) {
	mcpToolID := "mcp-tool-1"
	mcpTool := &registry.Tool{
		ID:   mcpToolID,
		Name: "upstream",
		Mode: "mcp",
	}
	router := &fakeRouter{routes: map[string]ToolRoute{
		"upstream__search": {Source: "mcp", ToolID: mcpToolID, UpstreamID: mcpToolID, UpstreamToolName: "search"},
	}}
	store := &fakeToolStore{tools: map[string]*registry.Tool{mcpToolID: mcpTool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	rl := &fakeRateLimiter{allowed: true}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{}
	mcpUp := &fakeMCPCaller{result: mcp.NewToolResultText("should not reach")}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	gc.SetPermissionChecker(&fakePermissionChecker{allowed: true, subToolAllowed: false})

	result, err := gc.CallTool(context.Background(), defaultAgent(), "upstream__search", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for sub-tool denial")
	}
	tc := result.Content[0].(mcp.TextContent)
	if tc.Text != "sub-tool search not permitted for tool upstream" {
		t.Errorf("error text = %q", tc.Text)
	}
	if mcpUp.called {
		t.Error("MCP upstream should not have been called")
	}
	if len(collector.transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(collector.transactions))
	}
	if collector.transactions[0].StatusCode != 403 {
		t.Errorf("tx.StatusCode = %d, want 403", collector.transactions[0].StatusCode)
	}
}

func TestGovernedCaller_MCPSubToolAllowed(t *testing.T) {
	mcpToolID := "mcp-tool-1"
	mcpTool := &registry.Tool{
		ID:   mcpToolID,
		Name: "upstream",
		Mode: "mcp",
	}
	router := &fakeRouter{routes: map[string]ToolRoute{
		"upstream__search": {Source: "mcp", ToolID: mcpToolID, UpstreamID: mcpToolID, UpstreamToolName: "search"},
	}}
	store := &fakeToolStore{tools: map[string]*registry.Tool{mcpToolID: mcpTool}}
	budgets := &fakeBudgetChecker{agentAllowed: true, globalAllowed: true}
	rl := &fakeRateLimiter{allowed: true}
	collector := &fakeCollector{}
	rest := &fakeRESTCaller{}
	mcpUp := &fakeMCPCaller{result: mcp.NewToolResultText("allowed result")}

	gc := newGovernedCaller(router, store, budgets, rl, collector, rest, mcpUp)
	gc.SetPermissionChecker(&fakePermissionChecker{allowed: true, subToolAllowed: true})

	result, err := gc.CallTool(context.Background(), defaultAgent(), "upstream__search", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}
	if !mcpUp.called {
		t.Error("MCP upstream should have been called")
	}
	tc := result.Content[0].(mcp.TextContent)
	if tc.Text != "allowed result" {
		t.Errorf("result text = %q, want %q", tc.Text, "allowed result")
	}
}
