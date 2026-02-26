package mcp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alecgard/octroi/internal/auth"
	mcpkg "github.com/alecgard/octroi/internal/mcp"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// fakeLister implements mcp.ToolLister for testing.
type fakeLister struct {
	tools []mcp.Tool
	err   error
}

func (f *fakeLister) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return f.tools, f.err
}

// fakeCaller implements mcp.ToolCaller for testing.
type fakeCaller struct {
	result *mcp.CallToolResult
	err    error

	// recorded values from last call
	calledName  string
	calledArgs  map[string]any
	calledAgent mcpkg.AgentInfo
}

func (f *fakeCaller) CallTool(_ context.Context, agent mcpkg.AgentInfo, name string, args map[string]any) (*mcp.CallToolResult, error) {
	f.calledName = name
	f.calledArgs = args
	f.calledAgent = agent
	return f.result, f.err
}

// testAgent is the agent injected into request context by the fake middleware.
var testAgent = &auth.Agent{
	ID:   "agent-1",
	Name: "test-agent",
	Team: "test-team",
}

// newTestServer creates an httptest.Server that injects a test agent into
// context (simulating auth middleware) before handing off to the MCP server.
func newTestServer(lister mcpkg.ToolLister, caller mcpkg.ToolCaller) *httptest.Server {
	srv := mcpkg.NewServer(lister, caller)

	// Wrap with fake auth middleware that sets agent in context.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.ContextWithAgent(r.Context(), testAgent)
		srv.Handler().ServeHTTP(w, r.WithContext(ctx))
	})

	return httptest.NewServer(handler)
}

func initRequest() mcp.InitializeRequest {
	return mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "test-client",
				Version: "1.0.0",
			},
		},
	}
}

func TestServer_Initialize(t *testing.T) {
	lister := &fakeLister{}
	caller := &fakeCaller{}
	ts := newTestServer(lister, caller)
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}

	result, err := c.Initialize(ctx, initRequest())
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if result.ServerInfo.Name != "octroi" {
		t.Errorf("server name = %q, want %q", result.ServerInfo.Name, "octroi")
	}
	if result.ServerInfo.Version != "1.0.0" {
		t.Errorf("server version = %q, want %q", result.ServerInfo.Version, "1.0.0")
	}
	if result.Capabilities.Tools == nil {
		t.Error("tools capability should be present")
	}
}

func TestServer_ListTools(t *testing.T) {
	tools := []mcp.Tool{
		mcp.NewTool("search", mcp.WithDescription("Search the web")),
		mcp.NewTool("calculator", mcp.WithDescription("Do math")),
	}
	lister := &fakeLister{tools: tools}
	caller := &fakeCaller{}
	ts := newTestServer(lister, caller)
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}
	if _, err := c.Initialize(ctx, initRequest()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	if len(result.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(result.Tools))
	}

	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	if !names["search"] {
		t.Error("missing tool 'search'")
	}
	if !names["calculator"] {
		t.Error("missing tool 'calculator'")
	}
}

func TestServer_CallTool(t *testing.T) {
	tools := []mcp.Tool{
		mcp.NewTool("search"),
	}
	lister := &fakeLister{tools: tools}
	caller := &fakeCaller{
		result: mcp.NewToolResultText("hello world"),
	}
	ts := newTestServer(lister, caller)
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}
	if _, err := c.Initialize(ctx, initRequest()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "search",
			Arguments: map[string]any{"query": "test"},
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if caller.calledName != "search" {
		t.Errorf("called name = %q, want %q", caller.calledName, "search")
	}
	if q, ok := caller.calledArgs["query"]; !ok || q != "test" {
		t.Errorf("called args = %v, want query=test", caller.calledArgs)
	}
	if caller.calledAgent.ID != "agent-1" {
		t.Errorf("agent ID = %q, want %q", caller.calledAgent.ID, "agent-1")
	}
	if caller.calledAgent.Team != "test-team" {
		t.Errorf("agent team = %q, want %q", caller.calledAgent.Team, "test-team")
	}

	// Check result content
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want TextContent", result.Content[0])
	}
	if tc.Text != "hello world" {
		t.Errorf("result text = %q, want %q", tc.Text, "hello world")
	}
}

func TestServer_CallTool_NotFound(t *testing.T) {
	// Register no tools, then try to call one.
	lister := &fakeLister{tools: nil}
	caller := &fakeCaller{}
	ts := newTestServer(lister, caller)
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}
	if _, err := c.Initialize(ctx, initRequest()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	_, err = c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "nonexistent",
		},
	})
	if err == nil {
		t.Error("expected error calling nonexistent tool, got nil")
	}
}

func TestServer_CallTool_CallerError(t *testing.T) {
	tools := []mcp.Tool{
		mcp.NewTool("failing"),
	}
	lister := &fakeLister{tools: tools}
	caller := &fakeCaller{
		err: fmt.Errorf("upstream unavailable"),
	}
	ts := newTestServer(lister, caller)
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}
	if _, err := c.Initialize(ctx, initRequest()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	_, err = c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "failing",
		},
	})
	if err == nil {
		t.Error("expected error from caller, got nil")
	}
}

func TestServer_ListToolsEmpty(t *testing.T) {
	lister := &fakeLister{tools: nil}
	caller := &fakeCaller{}
	ts := newTestServer(lister, caller)
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}
	if _, err := c.Initialize(ctx, initRequest()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) != 0 {
		t.Errorf("got %d tools, want 0", len(result.Tools))
	}
}
