package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpkg "github.com/alecgard/octroi/internal/mcp"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/mark3labs/mcp-go/mcp"
)

// --- WrapRESTTool tests ---

func TestWrapRESTTool_ServiceMode(t *testing.T) {
	tool := &registry.Tool{
		ID:          "tool-1",
		Name:        "my-service",
		Description: "A service mode tool",
		Mode:        "service",
		Endpoint:    "https://api.example.com",
	}

	mcpTool := mcpkg.WrapRESTTool(tool)

	if mcpTool.Name != "my-service" {
		t.Errorf("expected name %q, got %q", "my-service", mcpTool.Name)
	}
	if mcpTool.Description != "A service mode tool" {
		t.Errorf("expected description %q, got %q", "A service mode tool", mcpTool.Description)
	}

	props := mcpTool.InputSchema.Properties
	for _, name := range []string{"method", "path", "headers", "query", "body"} {
		if _, ok := props[name]; !ok {
			t.Errorf("expected property %q in schema", name)
		}
	}

	// Verify method has enum values.
	methodProp, ok := props["method"].(map[string]any)
	if !ok {
		t.Fatal("method property is not a map")
	}
	enumVals, ok := methodProp["enum"].([]string)
	if !ok {
		t.Fatal("method enum is not []string")
	}
	if len(enumVals) != 5 {
		t.Errorf("expected 5 enum values, got %d", len(enumVals))
	}
}

func TestWrapRESTTool_APIMode(t *testing.T) {
	tool := &registry.Tool{
		ID:          "tool-2",
		Name:        "weather-api",
		Description: "A weather API tool",
		Mode:        "api",
		Endpoint:    "https://api.weather.com/{city}/{unit}",
		Variables: map[string]string{
			"city": "london",
			"unit": "celsius",
		},
	}

	mcpTool := mcpkg.WrapRESTTool(tool)

	if mcpTool.Name != "weather-api" {
		t.Errorf("expected name %q, got %q", "weather-api", mcpTool.Name)
	}

	props := mcpTool.InputSchema.Properties
	// API mode should NOT have "path" but should have template variables.
	if _, ok := props["path"]; ok {
		t.Error("api mode tool should not have path property")
	}

	for _, varName := range []string{"city", "unit"} {
		if _, ok := props[varName]; !ok {
			t.Errorf("expected template variable %q in schema", varName)
		}
	}

	// Template variables should be required.
	requiredSet := make(map[string]bool)
	for _, r := range mcpTool.InputSchema.Required {
		requiredSet[r] = true
	}
	for _, varName := range []string{"city", "unit"} {
		if !requiredSet[varName] {
			t.Errorf("expected template variable %q to be required", varName)
		}
	}

	// Common properties should still exist.
	for _, name := range []string{"method", "headers", "query", "body"} {
		if _, ok := props[name]; !ok {
			t.Errorf("expected property %q in schema", name)
		}
	}
}

func TestWrapRESTTool_PreservesNameAndDescription(t *testing.T) {
	tool := &registry.Tool{
		Name:        "unique-name",
		Description: "A unique description for this tool",
		Mode:        "service",
		Endpoint:    "https://example.com",
	}

	mcpTool := mcpkg.WrapRESTTool(tool)
	if mcpTool.Name != "unique-name" {
		t.Errorf("name not preserved: got %q", mcpTool.Name)
	}
	if mcpTool.Description != "A unique description for this tool" {
		t.Errorf("description not preserved: got %q", mcpTool.Description)
	}
}

func TestWrapAllRESTTools(t *testing.T) {
	tools := []*registry.Tool{
		{Name: "tool-a", Mode: "service", Endpoint: "https://a.com"},
		{Name: "tool-b", Mode: "api", Endpoint: "https://b.com/{id}", Variables: map[string]string{"id": "1"}},
	}

	mcpTools := mcpkg.WrapAllRESTTools(tools)

	if len(mcpTools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(mcpTools))
	}
	if mcpTools[0].Name != "tool-a" {
		t.Errorf("expected first tool name %q, got %q", "tool-a", mcpTools[0].Name)
	}
	if mcpTools[1].Name != "tool-b" {
		t.Errorf("expected second tool name %q, got %q", "tool-b", mcpTools[1].Name)
	}
}

// --- RESTCallerImpl.Call tests ---

func TestRESTCaller_SuccessfulGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:     "service",
		Endpoint: srv.URL,
		AuthType: "none",
	}

	result, callResult, err := caller.Call(context.Background(), tool, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Error("expected IsError=false")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatal("expected TextContent")
	}
	if tc.Text != `{"result":"ok"}` {
		t.Errorf("unexpected body: %s", tc.Text)
	}

	if !callResult.Success {
		t.Error("expected success=true")
	}
	if callResult.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", callResult.StatusCode)
	}
	if callResult.Latency <= 0 {
		t.Error("expected positive latency")
	}
	if callResult.ResponseSize <= 0 {
		t.Error("expected positive response size")
	}
}

func TestRESTCaller_POSTWithBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to parse body: %v", err)
		}
		if payload["key"] != "value" {
			t.Errorf("expected body key=value, got %v", payload["key"])
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`created`))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:     "service",
		Endpoint: srv.URL,
		AuthType: "none",
	}

	result, callResult, err := caller.Call(context.Background(), tool, map[string]any{
		"method": "POST",
		"body":   map[string]any{"key": "value"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError=false for 201")
	}
	if callResult.StatusCode != 201 {
		t.Errorf("expected status 201, got %d", callResult.StatusCode)
	}
	if callResult.RequestSize <= 0 {
		t.Error("expected positive request size")
	}
}

func TestRESTCaller_AuthBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer my-secret-token" {
			t.Errorf("expected bearer auth, got %q", auth)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:       "service",
		Endpoint:   srv.URL,
		AuthType:   "bearer",
		AuthConfig: map[string]string{"key": "my-secret-token"},
	}

	_, _, err := caller.Call(context.Background(), tool, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRESTCaller_AuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := r.Header.Get("X-Api-Key")
		if val != "header-secret" {
			t.Errorf("expected X-Api-Key header, got %q", val)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:       "service",
		Endpoint:   srv.URL,
		AuthType:   "header",
		AuthConfig: map[string]string{"header_name": "X-Api-Key", "key": "header-secret"},
	}

	_, _, err := caller.Call(context.Background(), tool, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRESTCaller_AuthQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := r.URL.Query().Get("api_key")
		if val != "query-secret" {
			t.Errorf("expected api_key query param, got %q", val)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:       "service",
		Endpoint:   srv.URL,
		AuthType:   "query",
		AuthConfig: map[string]string{"key": "query-secret"},
	}

	_, _, err := caller.Call(context.Background(), tool, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRESTCaller_NonSuccessResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:     "service",
		Endpoint: srv.URL,
		AuthType: "none",
	}

	result, callResult, err := caller.Call(context.Background(), tool, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected IsError=true for 404")
	}
	if callResult.Success {
		t.Error("expected success=false for 404")
	}
	if callResult.StatusCode != 404 {
		t.Errorf("expected status 404, got %d", callResult.StatusCode)
	}
}

func TestRESTCaller_CallResultLatencyAndSizes(t *testing.T) {
	responseBody := "hello world response"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(responseBody))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:     "service",
		Endpoint: srv.URL,
		AuthType: "none",
	}

	_, callResult, err := caller.Call(context.Background(), tool, map[string]any{
		"method": "POST",
		"body":   "request-payload",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callResult.Latency <= 0 {
		t.Error("expected positive latency")
	}
	if callResult.ResponseSize != int64(len(responseBody)) {
		t.Errorf("expected response size %d, got %d", len(responseBody), callResult.ResponseSize)
	}
	if callResult.RequestSize <= 0 {
		t.Error("expected positive request size")
	}
}

func TestRESTCaller_ServiceModeWithPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/users" {
			t.Errorf("expected path /v1/users, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:     "service",
		Endpoint: srv.URL,
		AuthType: "none",
	}

	_, _, err := caller.Call(context.Background(), tool, map[string]any{
		"path": "/v1/users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRESTCaller_QueryParameters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("expected query param page=2, got %q", r.URL.Query().Get("page"))
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("expected query param limit=10, got %q", r.URL.Query().Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:     "service",
		Endpoint: srv.URL,
		AuthType: "none",
	}

	_, _, err := caller.Call(context.Background(), tool, map[string]any{
		"query": map[string]any{"page": "2", "limit": "10"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRESTCaller_APIModeTe_mplateResolution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The endpoint template has {city} which should be resolved.
		if r.URL.Path != "/weather/paris" {
			t.Errorf("expected path /weather/paris, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:     "api",
		Endpoint: srv.URL + "/weather/{city}",
		AuthType: "none",
		Variables: map[string]string{
			"city": "london",
		},
	}

	// Override the city variable via arguments.
	_, _, err := caller.Call(context.Background(), tool, map[string]any{
		"city": "paris",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRESTCaller_CostCalculation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	caller := mcpkg.NewRESTCaller(5*time.Second, 1<<20)
	tool := &registry.Tool{
		Mode:          "service",
		Endpoint:      srv.URL,
		AuthType:      "none",
		PricingModel:  "per_request",
		PricingAmount: 0.05,
	}

	_, callResult, err := caller.Call(context.Background(), tool, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callResult.Cost != 0.05 {
		t.Errorf("expected cost 0.05, got %f", callResult.Cost)
	}
	if callResult.CostSource != "flat" {
		t.Errorf("expected cost source %q, got %q", "flat", callResult.CostSource)
	}
}
