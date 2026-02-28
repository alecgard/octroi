package api

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// mcpServerDef describes a mock MCP server with its tool list.
type mcpServerDef struct {
	Name  string
	Tools []mcpToolDef
}

type mcpToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var mcpServers = map[string]mcpServerDef{
	"/brave": {
		Name: "BraveSearch",
		Tools: []mcpToolDef{
			{Name: "brave_web_search", Description: "Search the web using Brave Search", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "count": map[string]any{"type": "integer"}}}},
			{Name: "brave_local_search", Description: "Search for local businesses and places", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "count": map[string]any{"type": "integer"}}}},
			{Name: "brave_news_search", Description: "Search for recent news articles", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "freshness": map[string]any{"type": "string"}}}},
		},
	},
	"/deepwiki": {
		Name: "DeepWiki",
		Tools: []mcpToolDef{
			{Name: "read_wiki_structure", Description: "Get documentation topics for a GitHub repo", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"repoName": map[string]any{"type": "string"}}}},
			{Name: "read_wiki_contents", Description: "View full documentation page for a repo topic", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"repoName": map[string]any{"type": "string"}, "topic": map[string]any{"type": "string"}}}},
			{Name: "ask_question", Description: "Ask a natural-language question about a repository", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"repoName": map[string]any{"type": "string"}, "question": map[string]any{"type": "string"}}}},
		},
	},
}

// mockHandler is a catch-all mock upstream that the proxy can forward to.
// It replaces the Python mock servers used by the seed script.
func mockHandler(w http.ResponseWriter, r *http.Request) {
	// Strip the /mock prefix to get the sub-path for MCP routing.
	subPath := strings.TrimPrefix(r.URL.Path, "/mock")

	// Check if this is an MCP JSON-RPC request.
	if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			JSONRPC string         `json:"jsonrpc"`
			ID      any            `json:"id"`
			Method  string         `json:"method"`
			Params  map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Method != "" {
			handleMCPRequest(w, subPath, body.Method, body.ID, body.Params)
			return
		}
	}

	// REST mock: small random delay for realism.
	//nolint:gosec // weak rand is fine for mock delays
	delay := time.Duration(10+rand.Intn(491)) * time.Millisecond
	time.Sleep(delay)

	w.Header().Set("Content-Type", "application/json")

	// 50% chance of including a cost header with lognormal value.
	//nolint:gosec
	if rand.Float64() < 0.5 {
		// lognormvariate(-4.0, 1.5) capped at 2.0
		cost := math.Exp(-4.0 + 1.5*rand.NormFloat64())
		if cost > 2.0 {
			cost = 2.0
		}
		w.Header().Set("X-Octroi-Cost", fmt.Sprintf("%.6f", cost))
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func handleMCPRequest(w http.ResponseWriter, subPath, method string, reqID any, params map[string]any) {
	// Find the MCP server by path prefix.
	var server *mcpServerDef
	for prefix, s := range mcpServers {
		if strings.HasPrefix(subPath, prefix) {
			s := s // capture loop variable
			server = &s
			break
		}
	}

	// notifications/initialized gets a plain 200 with no body.
	if method == "notifications/initialized" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var result any
	switch method {
	case "initialize":
		name := "MockServer"
		if server != nil {
			name = server.Name
		}
		result = map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":   map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":     map[string]any{"name": name, "version": "1.0.0"},
		}

	case "tools/list":
		tools := []mcpToolDef{}
		if server != nil {
			tools = server.Tools
		}
		result = map[string]any{"tools": tools}

	case "tools/call":
		// Small delay for realism.
		//nolint:gosec
		delay := time.Duration(10+rand.Intn(491)) * time.Millisecond
		time.Sleep(delay)
		toolName := "?"
		if params != nil {
			if n, ok := params["name"].(string); ok {
				toolName = n
			}
		}
		callResult, _ := json.Marshal(map[string]any{"ok": true, "tool": toolName})
		result = map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(callResult)},
			},
		}

	default:
		// Unknown method: plain 200.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Build JSON-RPC response.
	resp, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"result":  result,
	})

	// Streamable HTTP (SSE) format.
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
}
