package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/alecgard/octroi/internal/registry"
	"github.com/mark3labs/mcp-go/mcp"
)

// --- Fakes for aggregator tests ---

type fakeToolRegistryLister struct {
	tools []*registry.Tool
	err   error
}

func (f *fakeToolRegistryLister) List(_ context.Context) ([]*registry.Tool, error) {
	return f.tools, f.err
}

type fakeUpstreamClient struct {
	tools      []mcp.Tool
	listErr    error
	callResult *mcp.CallToolResult
	callErr    error
	closed     bool

	// Track calls for assertions.
	calledTool string
	calledArgs map[string]any
}

func (f *fakeUpstreamClient) Initialize(_ context.Context) error { return nil }

func (f *fakeUpstreamClient) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return f.tools, f.listErr
}

func (f *fakeUpstreamClient) CallTool(_ context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	f.calledTool = name
	f.calledArgs = arguments
	return f.callResult, f.callErr
}

func (f *fakeUpstreamClient) Ping(_ context.Context) error { return nil }

func (f *fakeUpstreamClient) Close() error {
	f.closed = true
	return nil
}

// --- Helper ---

func makeMCPTool(name, description string) mcp.Tool {
	return mcp.NewTool(name, mcp.WithDescription(description))
}

func makeRESTTool(id, name, description string) *registry.Tool {
	return &registry.Tool{
		ID:          id,
		Name:        name,
		Description: description,
		Mode:        "service",
		Endpoint:    "https://example.com",
	}
}

// --- Tests ---

func TestAggregator_ListTools_RESTOnly(t *testing.T) {
	store := &fakeToolRegistryLister{
		tools: []*registry.Tool{
			makeRESTTool("t1", "search", "Search tool"),
			makeRESTTool("t2", "translate", "Translate tool"),
		},
	}
	agg := NewAggregator(store)

	tools, err := agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "search" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "search")
	}
	if tools[1].Name != "translate" {
		t.Errorf("tools[1].Name = %q, want %q", tools[1].Name, "translate")
	}
}

func TestAggregator_ListTools_MCPOnly(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	upstream := &fakeUpstreamClient{
		tools: []mcp.Tool{
			makeMCPTool("search", "MCP search"),
			makeMCPTool("summarize", "MCP summarize"),
		},
	}
	agg.AddUpstream("u1", "github", upstream)

	// Populate cached tools directly (simulating a prior refresh).
	agg.mu.Lock()
	agg.upstreams["u1"].tools = upstream.tools
	agg.mu.Unlock()

	tools, err := agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "github__search" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "github__search")
	}
	if tools[1].Name != "github__summarize" {
		t.Errorf("tools[1].Name = %q, want %q", tools[1].Name, "github__summarize")
	}
}

func TestAggregator_ListTools_Combined(t *testing.T) {
	store := &fakeToolRegistryLister{
		tools: []*registry.Tool{
			makeRESTTool("t1", "search", "REST search"),
		},
	}
	agg := NewAggregator(store)

	upstream := &fakeUpstreamClient{
		tools: []mcp.Tool{makeMCPTool("lookup", "MCP lookup")},
	}
	agg.AddUpstream("u1", "slack", upstream)
	agg.mu.Lock()
	agg.upstreams["u1"].tools = upstream.tools
	agg.mu.Unlock()

	tools, err := agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "search" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "search")
	}
	if tools[1].Name != "slack__lookup" {
		t.Errorf("tools[1].Name = %q, want %q", tools[1].Name, "slack__lookup")
	}
}

func TestAggregator_Route_RESTTool(t *testing.T) {
	store := &fakeToolRegistryLister{
		tools: []*registry.Tool{
			makeRESTTool("t1", "search", "REST search"),
		},
	}
	agg := NewAggregator(store)

	// Must call ListTools first to build routes.
	_, err := agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	route, ok := agg.Route("search")
	if !ok {
		t.Fatal("expected route for 'search', got not found")
	}
	if route.Source != "rest" {
		t.Errorf("route.Source = %q, want %q", route.Source, "rest")
	}
	if route.ToolID != "t1" {
		t.Errorf("route.ToolID = %q, want %q", route.ToolID, "t1")
	}
}

func TestAggregator_Route_MCPTool(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	upstream := &fakeUpstreamClient{
		tools: []mcp.Tool{makeMCPTool("lookup", "MCP lookup")},
	}
	agg.AddUpstream("u1", "github", upstream)
	agg.mu.Lock()
	agg.upstreams["u1"].tools = upstream.tools
	agg.mu.Unlock()

	_, err := agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	route, ok := agg.Route("github__lookup")
	if !ok {
		t.Fatal("expected route for 'github__lookup', got not found")
	}
	if route.Source != "mcp" {
		t.Errorf("route.Source = %q, want %q", route.Source, "mcp")
	}
	if route.ToolID != "u1" {
		t.Errorf("route.ToolID = %q, want %q", route.ToolID, "u1")
	}
	if route.UpstreamID != "u1" {
		t.Errorf("route.UpstreamID = %q, want %q", route.UpstreamID, "u1")
	}
	if route.UpstreamToolName != "lookup" {
		t.Errorf("route.UpstreamToolName = %q, want %q", route.UpstreamToolName, "lookup")
	}
}

func TestAggregator_Route_Unknown(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	_, ok := agg.Route("nonexistent")
	if ok {
		t.Error("expected Route to return false for unknown tool")
	}
}

func TestAggregator_Call_DelegatesToUpstream(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	upstream := &fakeUpstreamClient{
		callResult: mcp.NewToolResultText("hello from upstream"),
	}
	agg.AddUpstream("u1", "github", upstream)

	args := map[string]any{"q": "test"}
	result, err := agg.Call(context.Background(), "u1", "search", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if upstream.calledTool != "search" {
		t.Errorf("calledTool = %q, want %q", upstream.calledTool, "search")
	}
	if upstream.calledArgs["q"] != "test" {
		t.Errorf("calledArgs[q] = %v, want %q", upstream.calledArgs["q"], "test")
	}

	tc := result.Content[0].(mcp.TextContent)
	if tc.Text != "hello from upstream" {
		t.Errorf("result text = %q, want %q", tc.Text, "hello from upstream")
	}
}

func TestAggregator_Call_UnknownUpstream(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	_, err := agg.Call(context.Background(), "nonexistent", "search", nil)
	if err == nil {
		t.Fatal("expected error for unknown upstream")
	}
}

func TestAggregator_AddRemoveUpstream(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	upstream := &fakeUpstreamClient{
		callResult: mcp.NewToolResultText("ok"),
	}
	agg.AddUpstream("u1", "github", upstream)

	// Should be callable.
	_, err := agg.Call(context.Background(), "u1", "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Remove and verify client was closed.
	agg.RemoveUpstream("u1")
	if !upstream.closed {
		t.Error("expected upstream client to be closed after RemoveUpstream")
	}

	// Should no longer be callable.
	_, err = agg.Call(context.Background(), "u1", "test", nil)
	if err == nil {
		t.Error("expected error after removing upstream")
	}
}

func TestAggregator_RemoveUpstream_Nonexistent(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	// Should not panic.
	agg.RemoveUpstream("nonexistent")
}

func TestAggregator_RefreshUpstream(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	upstream := &fakeUpstreamClient{
		tools: []mcp.Tool{makeMCPTool("tool_v1", "Version 1")},
	}
	agg.AddUpstream("u1", "github", upstream)

	// Refresh fetches tools from the upstream client.
	err := agg.RefreshUpstream(context.Background(), "u1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ListTools should now include the upstream tool.
	tools, err := agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "github__tool_v1" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "github__tool_v1")
	}

	// Update upstream tools and refresh again.
	upstream.tools = []mcp.Tool{
		makeMCPTool("tool_v2", "Version 2"),
		makeMCPTool("tool_v3", "Version 3"),
	}
	err = agg.RefreshUpstream(context.Background(), "u1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, err = agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools after refresh, got %d", len(tools))
	}
}

func TestAggregator_RefreshUpstream_Unknown(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	err := agg.RefreshUpstream(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for unknown upstream")
	}
}

func TestAggregator_NamespaceCollision(t *testing.T) {
	// REST tool named "github__search" collides with upstream "github" tool "search".
	store := &fakeToolRegistryLister{
		tools: []*registry.Tool{
			makeRESTTool("t1", "github__search", "REST tool with conflicting name"),
		},
	}
	agg := NewAggregator(store)

	upstream := &fakeUpstreamClient{
		tools: []mcp.Tool{makeMCPTool("search", "MCP search")},
	}
	agg.AddUpstream("u1", "github", upstream)
	agg.mu.Lock()
	agg.upstreams["u1"].tools = upstream.tools
	agg.mu.Unlock()

	tools, err := agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the REST tool should be present; the MCP tool is skipped due to collision.
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool (collision skipped), got %d", len(tools))
	}
	if tools[0].Name != "github__search" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "github__search")
	}

	// Route should point to REST, not MCP.
	route, ok := agg.Route("github__search")
	if !ok {
		t.Fatal("expected route for 'github__search'")
	}
	if route.Source != "rest" {
		t.Errorf("route.Source = %q, want %q (REST takes precedence)", route.Source, "rest")
	}
}

func TestAggregator_ListTools_StoreError(t *testing.T) {
	store := &fakeToolRegistryLister{
		err: fmt.Errorf("db connection failed"),
	}
	agg := NewAggregator(store)

	_, err := agg.ListTools(context.Background())
	if err == nil {
		t.Fatal("expected error when store fails")
	}
}

func TestAggregator_RefreshAllUpstreams(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	u1 := &fakeUpstreamClient{tools: []mcp.Tool{makeMCPTool("a", "A")}}
	u2 := &fakeUpstreamClient{tools: []mcp.Tool{makeMCPTool("b", "B")}}
	agg.AddUpstream("u1", "alpha", u1)
	agg.AddUpstream("u2", "beta", u2)

	err := agg.RefreshAllUpstreams(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, err := agg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
}

func TestAggregator_RefreshAllUpstreams_PartialError(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	u1 := &fakeUpstreamClient{tools: []mcp.Tool{makeMCPTool("a", "A")}}
	u2 := &fakeUpstreamClient{listErr: fmt.Errorf("network error")}
	agg.AddUpstream("u1", "alpha", u1)
	agg.AddUpstream("u2", "beta", u2)

	err := agg.RefreshAllUpstreams(context.Background())
	if err == nil {
		t.Error("expected error when one upstream fails")
	}

	// The successful upstream should still have been refreshed.
	agg.mu.RLock()
	u1Tools := agg.upstreams["u1"].tools
	agg.mu.RUnlock()
	if len(u1Tools) != 1 {
		t.Errorf("expected u1 to have 1 cached tool, got %d", len(u1Tools))
	}
}

func TestAggregator_Call_UpstreamError(t *testing.T) {
	store := &fakeToolRegistryLister{}
	agg := NewAggregator(store)

	upstream := &fakeUpstreamClient{
		callErr: fmt.Errorf("upstream timeout"),
	}
	agg.AddUpstream("u1", "github", upstream)

	_, err := agg.Call(context.Background(), "u1", "search", nil)
	if err == nil {
		t.Fatal("expected error when upstream call fails")
	}
}
