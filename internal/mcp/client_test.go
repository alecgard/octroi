package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// newTestMCPServer creates a test MCP server with a simple echo tool.
func newTestMCPServer() *server.MCPServer {
	s := server.NewMCPServer("test-upstream", "1.0.0",
		server.WithToolCapabilities(true),
	)
	s.AddTool(
		mcp.NewTool("echo", mcp.WithDescription("echoes input")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			msg, _ := args["message"].(string)
			if msg == "" {
				msg = "no message"
			}
			return mcp.NewToolResultText(msg), nil
		},
	)
	return s
}

// startTestServer starts an httptest server wrapping the MCP server and returns
// its URL and a cleanup function.
func startTestServer(t *testing.T, mcpSrv *server.MCPServer) string {
	t.Helper()
	ts := server.NewTestStreamableHTTPServer(mcpSrv)
	t.Cleanup(ts.Close)
	return ts.URL
}

// startTestServerWithHeaderCapture starts a test server that records incoming
// request headers for assertion purposes.
func startTestServerWithHeaderCapture(t *testing.T, mcpSrv *server.MCPServer) (string, *headerCapture) {
	t.Helper()
	hc := &headerCapture{}
	sseServer := server.NewStreamableHTTPServer(mcpSrv)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hc.mu.Lock()
		hc.headers = append(hc.headers, r.Header.Clone())
		hc.mu.Unlock()
		sseServer.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL, hc
}

type headerCapture struct {
	mu      sync.Mutex
	headers []http.Header
}

func (hc *headerCapture) lastHeader() http.Header {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if len(hc.headers) == 0 {
		return nil
	}
	return hc.headers[len(hc.headers)-1]
}

func TestNewClient_AuthTypes(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		c, err := NewClient("http://localhost:9999/mcp", "none", nil, 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer c.Close()
	})

	t.Run("bearer", func(t *testing.T) {
		c, err := NewClient("http://localhost:9999/mcp", "bearer",
			map[string]string{"key": "tok123"}, 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer c.Close()
	})

	t.Run("header", func(t *testing.T) {
		c, err := NewClient("http://localhost:9999/mcp", "header",
			map[string]string{"header_name": "X-Api-Key", "key": "secret"}, 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer c.Close()
	})

	t.Run("query", func(t *testing.T) {
		c, err := NewClient("http://localhost:9999/mcp", "query",
			map[string]string{"param_name": "api_key", "key": "qk123"}, 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer c.Close()
		if c.endpoint != "http://localhost:9999/mcp?api_key=qk123" {
			t.Fatalf("expected query param in endpoint, got %s", c.endpoint)
		}
	})

	t.Run("empty_endpoint", func(t *testing.T) {
		_, err := NewClient("", "none", nil, 5*time.Second)
		if err == nil {
			t.Fatal("expected error for empty endpoint")
		}
	})

	t.Run("invalid_auth_type", func(t *testing.T) {
		_, err := NewClient("http://localhost:9999/mcp", "oauth", nil, 5*time.Second)
		if err == nil {
			t.Fatal("expected error for unsupported auth type")
		}
	})

	t.Run("bearer_missing_key", func(t *testing.T) {
		_, err := NewClient("http://localhost:9999/mcp", "bearer", nil, 5*time.Second)
		if err == nil {
			t.Fatal("expected error for missing bearer key")
		}
	})

	t.Run("header_missing_fields", func(t *testing.T) {
		_, err := NewClient("http://localhost:9999/mcp", "header",
			map[string]string{"key": "val"}, 5*time.Second)
		if err == nil {
			t.Fatal("expected error for missing header_name")
		}
	})

	t.Run("query_missing_fields", func(t *testing.T) {
		_, err := NewClient("http://localhost:9999/mcp", "query",
			map[string]string{"key": "val"}, 5*time.Second)
		if err == nil {
			t.Fatal("expected error for missing param_name")
		}
	})
}

func TestClient_Initialize(t *testing.T) {
	url := startTestServer(t, newTestMCPServer())

	c, err := NewClient(url, "none", nil, 10*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func TestClient_ListTools(t *testing.T) {
	url := startTestServer(t, newTestMCPServer())

	c, err := NewClient(url, "none", nil, 10*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Fatalf("expected tool name 'echo', got %q", tools[0].Name)
	}
}

func TestClient_CallTool(t *testing.T) {
	url := startTestServer(t, newTestMCPServer())

	c, err := NewClient(url, "none", nil, 10*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	result, err := c.CallTool(ctx, "echo", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	tc := result.Content[0].(mcp.TextContent)
	if tc.Text != "hello" {
		t.Fatalf("expected 'hello', got %q", tc.Text)
	}
}

func TestClient_Ping(t *testing.T) {
	url := startTestServer(t, newTestMCPServer())

	c, err := NewClient(url, "none", nil, 10*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestClient_BearerAuthHeaders(t *testing.T) {
	mcpSrv := newTestMCPServer()
	url, hc := startTestServerWithHeaderCapture(t, mcpSrv)

	c, err := NewClient(url, "bearer", map[string]string{"key": "my-token"}, 10*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	h := hc.lastHeader()
	if h == nil {
		t.Fatal("no headers captured")
	}
	got := h.Get("Authorization")
	if got != "Bearer my-token" {
		t.Fatalf("expected 'Bearer my-token', got %q", got)
	}
}

func TestClient_CustomHeaderAuth(t *testing.T) {
	mcpSrv := newTestMCPServer()
	url, hc := startTestServerWithHeaderCapture(t, mcpSrv)

	c, err := NewClient(url, "header",
		map[string]string{"header_name": "X-Api-Key", "key": "secret-key"}, 10*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	h := hc.lastHeader()
	if h == nil {
		t.Fatal("no headers captured")
	}
	got := h.Get("X-Api-Key")
	if got != "secret-key" {
		t.Fatalf("expected 'secret-key', got %q", got)
	}
}

func TestClient_QueryAuth(t *testing.T) {
	// For query auth, we verify the query param reaches the server by
	// inspecting the request URL in a custom handler.
	mcpSrv := newTestMCPServer()
	sseServer := server.NewStreamableHTTPServer(mcpSrv)

	var capturedURL string
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedURL = r.URL.String()
		mu.Unlock()
		sseServer.ServeHTTP(w, r)
	}))
	defer ts.Close()

	c, err := NewClient(ts.URL, "query",
		map[string]string{"param_name": "token", "key": "qval"}, 10*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	mu.Lock()
	u := capturedURL
	mu.Unlock()
	if u == "" {
		t.Fatal("no URL captured")
	}
	if !contains(u, "token=qval") {
		t.Fatalf("expected query param token=qval in URL %q", u)
	}
}

// contains checks if s contains substr (simple helper to avoid importing strings).
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Verify Client satisfies the UpstreamClient interface at compile time.
var _ UpstreamClient = (*Client)(nil)
