// Package mcp provides the MCP (Model Context Protocol) integration for Octroi.
// It implements both server-side (accepting MCP clients) and client-side
// (connecting to upstream MCP servers) protocol handling, with a governance
// layer that applies rate limits, budgets, and metering uniformly across
// MCP and HTTP tool calls.
package mcp

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolLister returns the aggregated list of MCP tool definitions from all
// sources (MCP upstreams and REST-wrapped tools).
type ToolLister interface {
	ListTools(ctx context.Context) ([]mcp.Tool, error)
}

// ToolCaller executes a tool call, applying governance policy (rate limits,
// budgets) and recording metering. It routes to the correct upstream based
// on tool name.
type ToolCaller interface {
	CallTool(ctx context.Context, agent AgentInfo, name string, arguments map[string]any) (*mcp.CallToolResult, error)
}

// AgentInfo carries the agent identity extracted from auth middleware,
// used by the governance layer for policy evaluation.
type AgentInfo struct {
	ID   string
	Name string
	Team string
}

// UpstreamClient connects to an upstream MCP server and provides tool
// discovery and invocation.
type UpstreamClient interface {
	// Initialize performs the MCP handshake with the upstream server.
	Initialize(ctx context.Context) error

	// ListTools returns the tools available on this upstream.
	ListTools(ctx context.Context) ([]mcp.Tool, error)

	// CallTool invokes a tool on this upstream by its original name.
	CallTool(ctx context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error)

	// Ping checks if the upstream is reachable.
	Ping(ctx context.Context) error

	// Close releases resources associated with this client.
	Close() error
}

// ToolRoute describes where a tool lives and how to reach it.
type ToolRoute struct {
	// Source is "rest" for REST-wrapped tools or "mcp" for upstream MCP tools.
	Source string

	// ToolID is the registry tool ID (for REST tools).
	ToolID string

	// UpstreamID is the MCP upstream ID (for MCP tools).
	UpstreamID string

	// UpstreamToolName is the original tool name on the upstream (for MCP tools,
	// before namespacing).
	UpstreamToolName string
}

// CallResult captures the outcome of a tool call for metering purposes.
type CallResult struct {
	Success      bool
	StatusCode   int
	Latency      time.Duration
	RequestSize  int64
	ResponseSize int64
	Cost         float64
	CostSource   string
	Error        string
}
