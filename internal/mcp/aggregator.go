package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/alecgard/octroi/internal/registry"
	"github.com/mark3labs/mcp-go/mcp"
)

// ToolRegistryLister is a subset of registry.Store that lists tools for aggregation.
type ToolRegistryLister interface {
	List(ctx context.Context) ([]*registry.Tool, error)
}

// upstreamEntry holds a connected upstream MCP client and its cached tools.
type upstreamEntry struct {
	client UpstreamClient
	name   string     // human-readable upstream name (used for namespacing)
	toolID string     // registry tool ID for this upstream (for governance)
	tools  []mcp.Tool // cached tools from last refresh
}

// Aggregator aggregates tools from the REST registry and upstream MCP servers
// into a unified list. It implements ToolLister, ToolRouter, and MCPUpstreamCaller.
type Aggregator struct {
	toolStore  ToolRegistryLister
	upstreams  map[string]*upstreamEntry // keyed by tool ID
	toolRoutes map[string]ToolRoute
	mu         sync.RWMutex
}

// NewAggregator creates an Aggregator that lists REST tools from the given store.
func NewAggregator(toolStore ToolRegistryLister) *Aggregator {
	return &Aggregator{
		toolStore:  toolStore,
		upstreams:  make(map[string]*upstreamEntry),
		toolRoutes: make(map[string]ToolRoute),
	}
}

// AddUpstream registers an upstream MCP client with the given id (which is the
// registry tool ID) and human-readable name for tool namespacing.
func (a *Aggregator) AddUpstream(id, name string, client UpstreamClient) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.upstreams[id] = &upstreamEntry{
		client: client,
		name:   name,
		toolID: id,
	}
}

// RemoveUpstream removes an upstream by id and closes its client.
func (a *Aggregator) RemoveUpstream(id string) {
	a.mu.Lock()
	entry, ok := a.upstreams[id]
	if ok {
		delete(a.upstreams, id)
	}
	a.mu.Unlock()

	if ok && entry.client != nil {
		_ = entry.client.Close()
	}
}

// ListTools returns the aggregated list of tools from the REST registry and
// all upstream MCP servers. Upstream tool names are prefixed with
// "{upstream_name}__" to avoid collisions. MCP mode tools are excluded from
// REST wrapping (they appear via their upstream clients instead).
func (a *Aggregator) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	// Fetch all tools from the registry.
	allTools, err := a.toolStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("aggregator: list REST tools: %w", err)
	}

	// Separate REST tools from MCP tools.
	var restTools []*registry.Tool
	for _, t := range allTools {
		if t.Mode != "mcp" {
			restTools = append(restTools, t)
		}
	}

	mcpTools := WrapAllRESTTools(restTools)

	a.mu.Lock()
	defer a.mu.Unlock()

	// Build fresh route map.
	routes := make(map[string]ToolRoute, len(mcpTools))

	// Add REST tools to the route map.
	for i, t := range restTools {
		name := mcpTools[i].Name
		routes[name] = ToolRoute{
			Source: "rest",
			ToolID: t.ID,
		}
	}

	// Add upstream MCP tools (namespaced).
	var upstreamTools []mcp.Tool
	for _, entry := range a.upstreams {
		for _, tool := range entry.tools {
			namespacedName := entry.name + "__" + tool.Name

			if _, exists := routes[namespacedName]; exists {
				slog.Warn("aggregator: tool name collision, skipping duplicate",
					"name", namespacedName,
					"tool_id", entry.toolID,
				)
				continue
			}

			routes[namespacedName] = ToolRoute{
				Source:           "mcp",
				ToolID:           entry.toolID, // parent tool ID for governance
				UpstreamID:       entry.toolID,
				UpstreamToolName: tool.Name,
			}

			// Clone the tool with the namespaced name.
			nsTool := tool
			nsTool.Name = namespacedName
			upstreamTools = append(upstreamTools, nsTool)
		}
	}

	a.toolRoutes = routes

	combined := make([]mcp.Tool, 0, len(mcpTools)+len(upstreamTools))
	combined = append(combined, mcpTools...)
	combined = append(combined, upstreamTools...)
	return combined, nil
}

// Route resolves a tool name to its ToolRoute. It returns false if the tool
// is not known.
func (a *Aggregator) Route(name string) (ToolRoute, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	r, ok := a.toolRoutes[name]
	return r, ok
}

// Call forwards a tool call to the specified upstream MCP server using the
// original (un-namespaced) tool name.
func (a *Aggregator) Call(ctx context.Context, upstreamID string, toolName string, arguments map[string]any) (*mcp.CallToolResult, error) {
	a.mu.RLock()
	entry, ok := a.upstreams[upstreamID]
	a.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("aggregator: unknown upstream %q", upstreamID)
	}
	return entry.client.CallTool(ctx, toolName, arguments)
}

// RefreshUpstream re-fetches tools from a specific upstream and rebuilds
// the cached tool list for that upstream.
func (a *Aggregator) RefreshUpstream(ctx context.Context, id string) error {
	a.mu.RLock()
	entry, ok := a.upstreams[id]
	a.mu.RUnlock()

	if !ok {
		return fmt.Errorf("aggregator: unknown upstream %q", id)
	}

	tools, err := entry.client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("aggregator: refresh upstream %q: %w", id, err)
	}

	a.mu.Lock()
	// Re-check entry still exists after re-acquiring write lock.
	if e, ok := a.upstreams[id]; ok {
		e.tools = tools
	}
	a.mu.Unlock()
	return nil
}

// RefreshAllUpstreams re-fetches tools from every registered upstream.
func (a *Aggregator) RefreshAllUpstreams(ctx context.Context) error {
	a.mu.RLock()
	ids := make([]string, 0, len(a.upstreams))
	for id := range a.upstreams {
		ids = append(ids, id)
	}
	a.mu.RUnlock()

	var firstErr error
	for _, id := range ids {
		if err := a.RefreshUpstream(ctx, id); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			slog.Error("aggregator: failed to refresh upstream", "upstream_id", id, "error", err)
		}
	}
	return firstErr
}

// GetUpstreamTools returns the cached sub-tools for a specific upstream.
func (a *Aggregator) GetUpstreamTools(id string) ([]mcp.Tool, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	entry, ok := a.upstreams[id]
	if !ok {
		return nil, false
	}
	return entry.tools, true
}
