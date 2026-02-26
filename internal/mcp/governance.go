package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/proxy"
	"github.com/mark3labs/mcp-go/mcp"
)

// ToolRouter resolves a tool name to its route info.
type ToolRouter interface {
	Route(name string) (ToolRoute, bool)
}

// MCPUpstreamCaller forwards a tool call to an upstream MCP server.
type MCPUpstreamCaller interface {
	Call(ctx context.Context, upstreamID string, toolName string, arguments map[string]any) (*mcp.CallToolResult, error)
}

// GovernedCaller implements ToolCaller by applying governance policy
// (rate limits, budgets, metering) before delegating to the actual upstream.
type GovernedCaller struct {
	router     ToolRouter
	toolStore  proxy.ToolStore
	budgets    proxy.BudgetChecker
	rateLimits proxy.ToolRateLimitChecker
	collector  proxy.MeteringRecorder
	restCaller RESTCaller
	mcpCaller  MCPUpstreamCaller
}

// NewGovernedCaller creates a GovernedCaller with all required dependencies.
func NewGovernedCaller(
	router ToolRouter,
	toolStore proxy.ToolStore,
	budgets proxy.BudgetChecker,
	rateLimits proxy.ToolRateLimitChecker,
	collector proxy.MeteringRecorder,
	restCaller RESTCaller,
	mcpCaller MCPUpstreamCaller,
) *GovernedCaller {
	return &GovernedCaller{
		router:     router,
		toolStore:  toolStore,
		budgets:    budgets,
		rateLimits: rateLimits,
		collector:  collector,
		restCaller: restCaller,
		mcpCaller:  mcpCaller,
	}
}

// CallTool routes and executes a tool call with governance checks.
func (g *GovernedCaller) CallTool(ctx context.Context, agent AgentInfo, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	route, ok := g.router.Route(name)
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("unknown tool: %s", name)), nil
	}

	switch route.Source {
	case "rest":
		return g.callREST(ctx, agent, name, route, arguments)
	case "mcp":
		return g.callMCP(ctx, agent, name, route, arguments)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unsupported tool source: %s", route.Source)), nil
	}
}

func (g *GovernedCaller) callREST(ctx context.Context, agent AgentInfo, name string, route ToolRoute, arguments map[string]any) (*mcp.CallToolResult, error) {
	tool, err := g.toolStore.GetByID(ctx, route.ToolID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("tool not found: %s", route.ToolID)), nil
	}

	// Check per-tool rate limits.
	if g.rateLimits != nil {
		allowed, _, _, _, rlErr := g.rateLimits.CheckToolRateLimit(ctx, tool.ID, agent.Team, agent.ID)
		if rlErr == nil && !allowed {
			g.recordTransaction(agent, tool.ID, name, 429, 0, false, 0, "flat")
			return mcp.NewToolResultError(fmt.Sprintf("rate limit exceeded for tool %s", tool.Name)), nil
		}
	}

	// Check per-agent budget.
	allowed, _, _, err := g.budgets.CheckBudget(ctx, agent.ID, tool.ID)
	if err == nil && !allowed {
		g.recordTransaction(agent, tool.ID, name, 403, 0, false, 0, "flat")
		return mcp.NewToolResultError(fmt.Sprintf("agent budget exceeded for tool %s", tool.Name)), nil
	}

	// Check global tool budget.
	globalAllowed, _, err := g.budgets.CheckToolGlobalBudget(ctx, tool.ID)
	if err == nil && !globalAllowed {
		g.recordTransaction(agent, tool.ID, name, 403, 0, false, 0, "flat")
		return mcp.NewToolResultError(fmt.Sprintf("global budget exceeded for tool %s", tool.Name)), nil
	}

	// Execute the REST call.
	start := time.Now()
	result, callResult, err := g.restCaller.Call(ctx, tool, arguments)
	latency := time.Since(start)

	if err != nil {
		g.recordTransaction(agent, tool.ID, name, 502, latency, false, 0, "flat")
		return nil, fmt.Errorf("rest call failed: %w", err)
	}

	statusCode := 200
	cost := 0.0
	costSource := "flat"
	success := true
	if callResult != nil {
		statusCode = callResult.StatusCode
		cost = callResult.Cost
		costSource = callResult.CostSource
		success = callResult.Success
		latency = callResult.Latency
	}

	g.recordTransaction(agent, tool.ID, name, statusCode, latency, success, cost, costSource)
	return result, nil
}

func (g *GovernedCaller) callMCP(ctx context.Context, agent AgentInfo, name string, route ToolRoute, arguments map[string]any) (*mcp.CallToolResult, error) {
	// Use the parent tool ID for governance checks (rate limits, budgets, metering).
	toolID := route.ToolID

	// Look up the parent tool for governance.
	tool, err := g.toolStore.GetByID(ctx, toolID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("tool not found: %s", toolID)), nil
	}

	// Check per-tool rate limits.
	if g.rateLimits != nil {
		allowed, _, _, _, rlErr := g.rateLimits.CheckToolRateLimit(ctx, tool.ID, agent.Team, agent.ID)
		if rlErr == nil && !allowed {
			g.recordTransaction(agent, toolID, name, 429, 0, false, 0, "flat")
			return mcp.NewToolResultError(fmt.Sprintf("rate limit exceeded for tool %s", tool.Name)), nil
		}
	}

	// Check per-agent budget.
	allowed, _, _, err := g.budgets.CheckBudget(ctx, agent.ID, tool.ID)
	if err == nil && !allowed {
		g.recordTransaction(agent, toolID, name, 403, 0, false, 0, "flat")
		return mcp.NewToolResultError(fmt.Sprintf("agent budget exceeded for tool %s", tool.Name)), nil
	}

	// Check global tool budget.
	globalAllowed, _, err := g.budgets.CheckToolGlobalBudget(ctx, tool.ID)
	if err == nil && !globalAllowed {
		g.recordTransaction(agent, toolID, name, 403, 0, false, 0, "flat")
		return mcp.NewToolResultError(fmt.Sprintf("global budget exceeded for tool %s", tool.Name)), nil
	}

	start := time.Now()
	result, err := g.mcpCaller.Call(ctx, route.UpstreamID, route.UpstreamToolName, arguments)
	latency := time.Since(start)

	if err != nil {
		g.recordTransaction(agent, toolID, name, 502, latency, false, 0, "flat")
		return nil, fmt.Errorf("mcp upstream call failed: %w", err)
	}

	success := result == nil || !result.IsError
	statusCode := 200
	if !success {
		statusCode = 500
	}

	// Use parent tool's pricing for cost.
	cost := 0.0
	costSource := "flat"
	if tool.PricingModel == "per_request" {
		cost = tool.PricingAmount
	}

	g.recordTransaction(agent, toolID, name, statusCode, latency, success, cost, costSource)
	return result, nil
}

func (g *GovernedCaller) recordTransaction(agent AgentInfo, toolID, name string, statusCode int, latency time.Duration, success bool, cost float64, costSource string) {
	g.collector.Record(metering.Transaction{
		AgentID:    agent.ID,
		ToolID:     toolID,
		Timestamp:  time.Now().UTC(),
		Method:     "tools/call",
		Path:       name,
		StatusCode: statusCode,
		LatencyMs:  latency.Milliseconds(),
		Success:    success,
		Cost:       cost,
		CostSource: costSource,
	})
}
