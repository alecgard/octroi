// Package mcp implements an MCP (Model Context Protocol) server endpoint for
// Octroi, allowing MCP clients to discover and invoke tools through Octroi's
// governance layer (rate limiting, budget enforcement, credential injection,
// metering).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/proxy"
	"github.com/alecgard/octroi/internal/ratelimit"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ToolLister provides read access to the tool registry.
type ToolLister interface {
	List(ctx context.Context, params registry.ToolListParams) ([]*registry.Tool, string, error)
	GetByID(ctx context.Context, id string) (*registry.Tool, error)
}

// MCPMetrics records MCP-specific Prometheus metrics.
type MCPMetrics interface {
	IncMCPRequests(method string)
	ObserveMCPToolCallDuration(toolName string, seconds float64)
}

// HandlerDeps holds all dependencies for the MCP handler.
type HandlerDeps struct {
	Tools          ToolLister
	Budgets        proxy.BudgetChecker
	Collector      proxy.MeteringRecorder
	ToolRateLimits proxy.ToolRateLimitChecker
	Limiter        *ratelimit.Limiter
	Metrics        proxy.MetricsRecorder
	MCPMetrics     MCPMetrics
	AgentLookup    auth.AgentLookup
	ProxyTimeout   time.Duration
	MaxRequestSize int64
}

// Handler implements an MCP server endpoint backed by Octroi's governance layer.
type Handler struct {
	tools          ToolLister
	budgets        proxy.BudgetChecker
	collector      proxy.MeteringRecorder
	toolRateLimits proxy.ToolRateLimitChecker
	limiter        *ratelimit.Limiter
	metrics        proxy.MetricsRecorder
	mcpMetrics     MCPMetrics
	agentLookup    auth.AgentLookup
	proxyTimeout   time.Duration
	maxRequestSize int64

	mcpServer  *server.MCPServer
	httpServer *server.StreamableHTTPServer

	// toolMap maps MCP tool name -> registry tool ID for routing.
	toolMap   map[string]string
	toolMapMu sync.RWMutex
}

// inputSchema is the generic HTTP call schema for REST-wrapped MCP tools.
var inputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"method": {
			"type": "string",
			"enum": ["GET", "POST", "PUT", "PATCH", "DELETE"],
			"default": "GET",
			"description": "HTTP method"
		},
		"path": {
			"type": "string",
			"default": "/",
			"description": "URL path appended to the tool endpoint"
		},
		"headers": {
			"type": "object",
			"additionalProperties": {"type": "string"},
			"description": "Additional HTTP headers"
		},
		"query": {
			"type": "object",
			"additionalProperties": {"type": "string"},
			"description": "Query string parameters"
		},
		"body": {
			"description": "Request body (string or JSON object)"
		}
	}
}`)

// NewHandler creates a new MCP handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler {
	h := &Handler{
		tools:          deps.Tools,
		budgets:        deps.Budgets,
		collector:      deps.Collector,
		toolRateLimits: deps.ToolRateLimits,
		limiter:        deps.Limiter,
		metrics:        deps.Metrics,
		mcpMetrics:     deps.MCPMetrics,
		agentLookup:    deps.AgentLookup,
		proxyTimeout:   deps.ProxyTimeout,
		maxRequestSize: deps.MaxRequestSize,
		toolMap:        make(map[string]string),
	}

	h.mcpServer = server.NewMCPServer(
		"octroi",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	h.httpServer = server.NewStreamableHTTPServer(
		h.mcpServer,
		server.WithStateLess(true),
		server.WithHTTPContextFunc(h.authContextFunc),
	)

	return h
}

// ServeHTTP delegates to the mcp-go StreamableHTTPServer.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.httpServer.ServeHTTP(w, r)
}

// authContextFunc extracts the Bearer token from the HTTP request,
// authenticates the agent, and injects it into the context.
// This mirrors auth.AgentAuthMiddleware but works as a context function
// for the mcp-go StreamableHTTPServer.
func (h *Handler) authContextFunc(ctx context.Context, r *http.Request) context.Context {
	token := extractBearerToken(r)
	if token == "" {
		return ctx
	}

	hash := auth.HashKey(token)
	agent, err := h.agentLookup.GetByKeyHash(ctx, hash)
	if err != nil || agent == nil {
		return ctx
	}

	return auth.ContextWithAgent(ctx, agent)
}

// RefreshTools loads all tools from the registry and registers them as MCP tools.
func (h *Handler) RefreshTools(ctx context.Context) error {
	var allTools []*registry.Tool
	cursor := ""

	for {
		page, nextCursor, err := h.tools.List(ctx, registry.ToolListParams{
			Limit:  100,
			Cursor: cursor,
		})
		if err != nil {
			return fmt.Errorf("listing tools: %w", err)
		}
		allTools = append(allTools, page...)
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	// Build name deduplication map.
	nameCount := make(map[string]int)
	for _, t := range allTools {
		nameCount[t.Name]++
	}

	// Build MCP tools and the name->ID map.
	newToolMap := make(map[string]string, len(allTools))
	mcpTools := make([]server.ServerTool, 0, len(allTools))
	nameSeen := make(map[string]int)

	for _, t := range allTools {
		mcpName := t.Name
		if nameCount[t.Name] > 1 {
			nameSeen[t.Name]++
			if nameSeen[t.Name] > 1 {
				mcpName = fmt.Sprintf("%s-%d", t.Name, nameSeen[t.Name])
			}
		}

		newToolMap[mcpName] = t.ID

		mcpTool := mcp.NewToolWithRawSchema(mcpName, t.Description, inputSchema)

		// Capture tool ID for the handler closure.
		toolID := t.ID
		handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.handleToolCall(ctx, toolID, request)
		}

		mcpTools = append(mcpTools, server.ServerTool{
			Tool:    mcpTool,
			Handler: handler,
		})
	}

	h.toolMapMu.Lock()
	h.toolMap = newToolMap
	h.toolMapMu.Unlock()

	h.mcpServer.SetTools(mcpTools...)

	slog.Info("mcp tools refreshed", "count", len(mcpTools))
	return nil
}

// handleToolCall processes an MCP tool call through the governance pipeline.
func (h *Handler) handleToolCall(ctx context.Context, toolID string, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	callStart := time.Now()
	if h.mcpMetrics != nil {
		h.mcpMetrics.IncMCPRequests("tools/call")
	}

	// 1. Extract agent from context.
	agent := auth.AgentFromContext(ctx)
	if agent == nil {
		return mcp.NewToolResultError("unauthorized: missing or invalid agent credentials"), nil
	}

	// 2. Look up tool.
	tool, err := h.tools.GetByID(ctx, toolID)
	if err != nil {
		return mcp.NewToolResultError("tool not found"), nil
	}

	defer func() {
		if h.mcpMetrics != nil {
			h.mcpMetrics.ObserveMCPToolCallDuration(tool.Name, time.Since(callStart).Seconds())
		}
	}()

	// 3. Global rate limit check.
	if h.limiter != nil {
		if !h.limiter.Allow(agent.ID, agent.RateLimit) {
			return mcp.NewToolResultError("rate_limited: agent rate limit exceeded"), nil
		}
	}

	// 4. Per-tool rate limit check.
	if h.toolRateLimits != nil {
		allowed, _, _, _, tlErr := h.toolRateLimits.CheckToolRateLimit(ctx, tool.ID, agent.Team, agent.ID)
		if tlErr == nil && !allowed {
			if h.metrics != nil {
				h.metrics.IncToolRateLimitRejection()
			}
			return mcp.NewToolResultError("rate_limited: tool rate limit exceeded"), nil
		}
	}

	// 5. Agent budget check.
	if h.budgets != nil {
		allowed, _, _, err := h.budgets.CheckBudget(ctx, agent.ID, tool.ID)
		if err == nil && !allowed {
			if h.metrics != nil {
				h.metrics.IncBudgetRejection("agent")
			}
			return mcp.NewToolResultError("budget_exceeded: agent budget exceeded for this tool"), nil
		}
	}

	// 6. Global tool budget check.
	if h.budgets != nil {
		allowed, _, err := h.budgets.CheckToolGlobalBudget(ctx, tool.ID)
		if err == nil && !allowed {
			if h.metrics != nil {
				h.metrics.IncBudgetRejection("global")
			}
			return mcp.NewToolResultError("budget_exceeded: global tool budget exceeded"), nil
		}
	}

	// 7. Resolve endpoint.
	endpoint := tool.Endpoint
	if tool.Mode == "api" {
		resolved, err := registry.ResolveTemplate(tool.Endpoint, tool.Variables)
		if err != nil {
			return mcp.NewToolResultError("failed to resolve endpoint template"), nil
		}
		endpoint = resolved
	}

	// 8. Parse tool call arguments.
	args := request.GetArguments()
	method := "GET"
	path := "/"
	if m, ok := args["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}
	if p, ok := args["path"].(string); ok && p != "" {
		path = p
	}

	// Build target URL.
	targetURL := strings.TrimRight(endpoint, "/") + path

	// Apply query parameters.
	if q, ok := args["query"].(map[string]any); ok && len(q) > 0 {
		sep := "?"
		if strings.Contains(targetURL, "?") {
			sep = "&"
		}
		for k, v := range q {
			targetURL += sep + k + "=" + fmt.Sprintf("%v", v)
			sep = "&"
		}
	}

	// Build request body.
	var bodyReader io.Reader
	if body, ok := args["body"]; ok && body != nil {
		switch b := body.(type) {
		case string:
			bodyReader = strings.NewReader(b)
		default:
			bodyJSON, err := json.Marshal(b)
			if err != nil {
				return mcp.NewToolResultError("failed to marshal request body"), nil
			}
			bodyReader = strings.NewReader(string(bodyJSON))
		}
	}

	// 9. Create HTTP request.
	outReq, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return mcp.NewToolResultError("failed to build upstream request"), nil
	}

	// Apply custom headers from arguments.
	if hdrs, ok := args["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			outReq.Header.Set(k, fmt.Sprintf("%v", v))
		}
	}

	// Set Content-Type for requests with body.
	if bodyReader != nil && outReq.Header.Get("Content-Type") == "" {
		outReq.Header.Set("Content-Type", "application/json")
	}

	// 10. Inject credentials.
	switch tool.AuthType {
	case "bearer":
		outReq.Header.Set("Authorization", "Bearer "+tool.AuthConfig["key"])
	case "header":
		headerName := tool.AuthConfig["header_name"]
		if headerName != "" {
			outReq.Header.Set(headerName, tool.AuthConfig["key"])
		}
	case "query":
		paramName := tool.AuthConfig["param_name"]
		if paramName == "" {
			paramName = "api_key"
		}
		q := outReq.URL.Query()
		q.Set(paramName, tool.AuthConfig["key"])
		outReq.URL.RawQuery = q.Encode()
	case "none":
		// No auth injection.
	}

	// 11. Execute upstream request.
	client := &http.Client{Timeout: h.proxyTimeout}
	start := time.Now()
	resp, err := client.Do(outReq)
	latency := time.Since(start)

	if h.metrics != nil {
		h.metrics.ObserveUpstreamDuration(tool.ID, tool.Name, latency.Seconds())
	}

	if err != nil {
		if h.metrics != nil {
			h.metrics.IncProxyRequests(tool.ID, tool.Name, agent.ID, method, 502)
		}
		h.recordTransaction(agent.ID, tool, method, path, 502, latency, 0, 0, false, "")
		return mcp.NewToolResultError("upstream request failed: " + err.Error()), nil
	}
	defer resp.Body.Close()

	if h.metrics != nil {
		h.metrics.IncProxyRequests(tool.ID, tool.Name, agent.ID, method, resp.StatusCode)
	}

	// Read response body.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, h.maxRequestSize))
	if err != nil {
		h.recordTransaction(agent.ID, tool, method, path, resp.StatusCode, latency, 0, 0, false, "")
		return mcp.NewToolResultError("failed to read upstream response"), nil
	}

	// Capture cost header.
	reportedCostHeader := resp.Header.Get("X-Octroi-Cost")

	// 12. Record transaction.
	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	h.recordTransaction(agent.ID, tool, method, path, resp.StatusCode, latency, 0, int64(len(respBody)), success, reportedCostHeader)

	// 13. Return result.
	if !success {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody))),
			},
			IsError: true,
		}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(respBody)),
		},
	}, nil
}

// recordTransaction records a metering transaction for an MCP tool call.
func (h *Handler) recordTransaction(agentID string, tool *registry.Tool, method, path string, statusCode int, latency time.Duration, requestSize, responseSize int64, success bool, reportedCostHeader string) {
	if h.collector == nil {
		return
	}

	cost := 0.0
	costSource := "flat"

	if reportedCostHeader != "" {
		if parsed, err := strconv.ParseFloat(reportedCostHeader, 64); err == nil && parsed >= 0 {
			cost = parsed
			costSource = "reported"
		} else if tool.PricingModel == "per_request" {
			cost = tool.PricingAmount
		}
	} else if tool.PricingModel == "per_request" {
		cost = tool.PricingAmount
	}

	h.collector.Record(metering.Transaction{
		AgentID:      agentID,
		ToolID:       tool.ID,
		Timestamp:    time.Now().UTC(),
		Method:       method,
		Path:         path,
		StatusCode:   statusCode,
		LatencyMs:    latency.Milliseconds(),
		RequestSize:  requestSize,
		ResponseSize: responseSize,
		Success:      success,
		Cost:         cost,
		CostSource:   costSource,
	})
}

// extractBearerToken extracts the Bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
