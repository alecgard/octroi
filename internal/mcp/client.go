package mcp

import (
	"context"
	"fmt"
	"net/url"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// Client wraps an mcp-go streamable HTTP client to implement the
// UpstreamClient interface. It connects to a single upstream MCP server
// and provides tool discovery and invocation.
type Client struct {
	inner    *mcpclient.Client
	endpoint string
}

// NewClient creates a new MCP client that connects to the given endpoint.
// authType controls how credentials are injected into requests:
//   - "bearer": sets Authorization: Bearer <key> header (authConfig key: "key")
//   - "header": sets a custom header (authConfig keys: "header_name", "key")
//   - "query": appends a query parameter to the URL (authConfig keys: "param_name", "key")
//   - "none": no authentication
//
// timeout sets the HTTP request timeout for all calls.
func NewClient(endpoint string, authType string, authConfig map[string]string, timeout time.Duration) (*Client, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("mcp client: endpoint must not be empty")
	}

	var opts []transport.StreamableHTTPCOption

	switch authType {
	case "bearer":
		key := authConfig["key"]
		if key == "" {
			return nil, fmt.Errorf("mcp client: bearer auth requires 'key' in authConfig")
		}
		opts = append(opts, transport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + key,
		}))

	case "header":
		headerName := authConfig["header_name"]
		key := authConfig["key"]
		if headerName == "" || key == "" {
			return nil, fmt.Errorf("mcp client: header auth requires 'header_name' and 'key' in authConfig")
		}
		opts = append(opts, transport.WithHTTPHeaders(map[string]string{
			headerName: key,
		}))

	case "query":
		paramName := authConfig["param_name"]
		key := authConfig["key"]
		if paramName == "" || key == "" {
			return nil, fmt.Errorf("mcp client: query auth requires 'param_name' and 'key' in authConfig")
		}
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("mcp client: invalid endpoint URL: %w", err)
		}
		q := u.Query()
		q.Set(paramName, key)
		u.RawQuery = q.Encode()
		endpoint = u.String()

	case "none", "":
		// No authentication.

	default:
		return nil, fmt.Errorf("mcp client: unsupported auth type %q", authType)
	}

	if timeout > 0 {
		opts = append(opts, transport.WithHTTPTimeout(timeout))
	}

	inner, err := mcpclient.NewStreamableHttpClient(endpoint, opts...)
	if err != nil {
		return nil, fmt.Errorf("mcp client: failed to create client: %w", err)
	}

	return &Client{
		inner:    inner,
		endpoint: endpoint,
	}, nil
}

// Initialize performs the MCP handshake with the upstream server.
func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.inner.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "octroi",
				Version: "1.0.0",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("mcp client initialize: %w", err)
	}
	return nil
}

// ListTools returns the tools available on the upstream MCP server.
func (c *Client) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	result, err := c.inner.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("mcp client list tools: %w", err)
	}
	return result.Tools, nil
}

// CallTool invokes a tool on the upstream MCP server by name.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	result, err := c.inner.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: arguments,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("mcp client call tool: %w", err)
	}
	return result, nil
}

// Ping checks if the upstream MCP server is reachable.
func (c *Client) Ping(ctx context.Context) error {
	if err := c.inner.Ping(ctx); err != nil {
		return fmt.Errorf("mcp client ping: %w", err)
	}
	return nil
}

// Close releases resources associated with this client.
func (c *Client) Close() error {
	if err := c.inner.Close(); err != nil {
		return fmt.Errorf("mcp client close: %w", err)
	}
	return nil
}
