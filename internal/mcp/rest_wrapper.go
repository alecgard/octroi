package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alecgard/octroi/internal/registry"
	"github.com/mark3labs/mcp-go/mcp"
)

// RESTCaller executes a REST tool call from MCP arguments.
type RESTCaller interface {
	Call(ctx context.Context, tool *registry.Tool, arguments map[string]any) (*mcp.CallToolResult, *CallResult, error)
}

// RESTCallerImpl implements RESTCaller using an HTTP client.
type RESTCallerImpl struct {
	client         *http.Client
	maxRequestSize int64
}

// NewRESTCaller creates a new RESTCallerImpl with the given timeout and max request size.
func NewRESTCaller(timeout time.Duration, maxRequestSize int64) *RESTCallerImpl {
	return &RESTCallerImpl{
		client:         &http.Client{Timeout: timeout},
		maxRequestSize: maxRequestSize,
	}
}

// WrapRESTTool converts a registry.Tool into an mcp.Tool definition.
// For "service" mode tools, the schema includes method, path, headers, query, and body.
// For "api" mode tools, template variables become required string properties in addition
// to method, headers, query, and body.
func WrapRESTTool(tool *registry.Tool) mcp.Tool {
	opts := []mcp.ToolOption{
		mcp.WithDescription(tool.Description),
		mcp.WithString("method",
			mcp.Description("HTTP method"),
			mcp.Enum("GET", "POST", "PUT", "PATCH", "DELETE"),
		),
	}

	if tool.Mode == "service" {
		opts = append(opts,
			mcp.WithString("path", mcp.Description("Path appended to tool endpoint")),
		)
	}

	// For API mode, add template variables as required string properties.
	if tool.Mode == "api" {
		vars := registry.ExtractTemplateVars(tool.Endpoint)
		for _, v := range vars {
			opts = append(opts,
				mcp.WithString(v, mcp.Description(fmt.Sprintf("Template variable: %s", v)), mcp.Required()),
			)
		}
	}

	opts = append(opts,
		mcp.WithObject("headers", mcp.Description("HTTP headers to include in the request")),
		mcp.WithObject("query", mcp.Description("Query parameters to include in the request")),
		mcp.WithAny("body", mcp.Description("Request body")),
	)

	return mcp.NewTool(tool.Name, opts...)
}

// WrapAllRESTTools converts a slice of registry tools into MCP tool definitions.
func WrapAllRESTTools(tools []*registry.Tool) []mcp.Tool {
	result := make([]mcp.Tool, len(tools))
	for i, t := range tools {
		result[i] = WrapRESTTool(t)
	}
	return result
}

// Call executes a REST tool call, building an HTTP request from the MCP arguments
// and returning the response as an MCP CallToolResult along with a CallResult for metering.
func (c *RESTCallerImpl) Call(ctx context.Context, tool *registry.Tool, arguments map[string]any) (*mcp.CallToolResult, *CallResult, error) {
	method := "GET"
	if m, ok := arguments["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	path := ""
	if p, ok := arguments["path"].(string); ok {
		path = p
	}

	// Resolve endpoint.
	endpoint := tool.Endpoint
	if tool.Mode == "api" {
		// Merge template variables from tool.Variables with any overrides from arguments.
		vars := make(map[string]string, len(tool.Variables))
		for k, v := range tool.Variables {
			vars[k] = v
		}
		for k, v := range arguments {
			if s, ok := v.(string); ok {
				vars[k] = s
			}
		}
		resolved, err := registry.ResolveTemplate(tool.Endpoint, vars)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve endpoint template: %w", err)
		}
		endpoint = resolved
	}

	// Build target URL.
	targetURL := strings.TrimRight(endpoint, "/")
	if path != "" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		targetURL += path
	}

	// Add query parameters.
	if queryMap, ok := arguments["query"].(map[string]any); ok && len(queryMap) > 0 {
		parsed, err := url.Parse(targetURL)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse target URL: %w", err)
		}
		q := parsed.Query()
		for k, v := range queryMap {
			q.Set(k, fmt.Sprintf("%v", v))
		}
		parsed.RawQuery = q.Encode()
		targetURL = parsed.String()
	}

	// Build request body.
	var bodyReader io.Reader
	var requestSize int64
	if bodyArg, ok := arguments["body"]; ok && bodyArg != nil {
		var bodyBytes []byte
		switch b := bodyArg.(type) {
		case string:
			bodyBytes = []byte(b)
		default:
			var err error
			bodyBytes, err = json.Marshal(b)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
		}
		requestSize = int64(len(bodyBytes))
		bodyReader = io.LimitReader(bytes.NewReader(bodyBytes), c.maxRequestSize)
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build HTTP request: %w", err)
	}

	// Set custom headers.
	if headerMap, ok := arguments["headers"].(map[string]any); ok {
		for k, v := range headerMap {
			req.Header.Set(k, fmt.Sprintf("%v", v))
		}
	}

	// If body is present and no Content-Type was set, default to application/json.
	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// Inject tool auth credentials.
	injectAuth(req, tool)

	// Execute request.
	start := time.Now()
	resp, err := c.client.Do(req)
	latency := time.Since(start)

	if err != nil {
		callResult := &CallResult{
			Success:     false,
			StatusCode:  502,
			Latency:     latency,
			RequestSize: requestSize,
			Error:       err.Error(),
		}
		return nil, callResult, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		callResult := &CallResult{
			Success:     false,
			StatusCode:  resp.StatusCode,
			Latency:     latency,
			RequestSize: requestSize,
			Error:       err.Error(),
		}
		return nil, callResult, fmt.Errorf("failed to read response body: %w", err)
	}

	responseSize := int64(len(respBody))
	success := resp.StatusCode >= 200 && resp.StatusCode < 300

	// Compute cost from pricing model.
	cost := 0.0
	costSource := "none"
	if tool.PricingModel == "per_request" {
		cost = tool.PricingAmount
		costSource = "flat"
	}

	callResult := &CallResult{
		Success:      success,
		StatusCode:   resp.StatusCode,
		Latency:      latency,
		RequestSize:  requestSize,
		ResponseSize: responseSize,
		Cost:         cost,
		CostSource:   costSource,
	}

	mcpResult := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(string(respBody))},
		IsError: !success,
	}

	return mcpResult, callResult, nil
}

// injectAuth applies the tool's auth configuration to the outgoing HTTP request.
func injectAuth(req *http.Request, tool *registry.Tool) {
	switch tool.AuthType {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+tool.AuthConfig["key"])
	case "header":
		headerName := tool.AuthConfig["header_name"]
		if headerName != "" {
			req.Header.Set(headerName, tool.AuthConfig["key"])
		}
	case "query":
		paramName := tool.AuthConfig["param_name"]
		if paramName == "" {
			paramName = "api_key"
		}
		q := req.URL.Query()
		q.Set(paramName, tool.AuthConfig["key"])
		req.URL.RawQuery = q.Encode()
	case "none":
		// No auth injection.
	}
}
