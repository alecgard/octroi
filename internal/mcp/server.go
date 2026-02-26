package mcp

import (
	"context"
	"net/http"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server wraps mcp-go's MCPServer and StreamableHTTPServer, providing a
// dynamic tool registry backed by ToolLister and ToolCaller interfaces.
type Server struct {
	mcp  *server.MCPServer
	http *server.StreamableHTTPServer

	lister ToolLister
	caller ToolCaller
}

// NewServer creates a new MCP server handler. The lister provides dynamic tool
// discovery and the caller handles tool invocations with governance applied.
func NewServer(lister ToolLister, caller ToolCaller) *Server {
	s := &Server{
		lister: lister,
		caller: caller,
	}

	hooks := &server.Hooks{}
	hooks.AddBeforeListTools(func(ctx context.Context, id any, message *mcp.ListToolsRequest) {
		s.refreshTools(ctx)
	})
	hooks.AddBeforeCallTool(func(ctx context.Context, id any, message *mcp.CallToolRequest) {
		s.refreshTools(ctx)
	})

	s.mcp = server.NewMCPServer("octroi", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithHooks(hooks),
	)

	s.http = server.NewStreamableHTTPServer(s.mcp,
		server.WithStateLess(true),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return r.Context()
		}),
	)

	return s
}

// Handler returns an http.Handler suitable for mounting in a Chi router.
func (s *Server) Handler() http.Handler {
	return s.http
}

// refreshTools synchronises the MCPServer's tool registrations with the
// current output of ToolLister. Each tool gets a handler that delegates
// to ToolCaller, extracting agent identity from the request context.
func (s *Server) refreshTools(ctx context.Context) {
	tools, err := s.lister.ListTools(ctx)
	if err != nil {
		// If listing fails, leave the existing registrations in place.
		return
	}

	serverTools := make([]server.ServerTool, len(tools))
	for i, t := range tools {
		tool := t // capture for closure
		serverTools[i] = server.ServerTool{
			Tool: tool,
			Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				agent := agentInfoFromContext(ctx)
				return s.caller.CallTool(ctx, agent, req.Params.Name, req.GetArguments())
			},
		}
	}

	s.mcp.SetTools(serverTools...)
}

// agentInfoFromContext extracts the auth.Agent from context and converts it
// to the mcp package's AgentInfo type. Returns a zero-value AgentInfo if no
// agent is present.
func agentInfoFromContext(ctx context.Context) AgentInfo {
	a := auth.AgentFromContext(ctx)
	if a == nil {
		return AgentInfo{}
	}
	return AgentInfo{
		ID:   a.ID,
		Name: a.Name,
		Team: a.Team,
	}
}
