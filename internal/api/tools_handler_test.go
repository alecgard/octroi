package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// fakeToolService implements toolServicer for handler-level tests.
// ---------------------------------------------------------------------------

type fakeToolService struct {
	tools      map[string]*registry.Tool
	nextID     int
	createHook func(input registry.CreateToolInput) error // optional error injection
}

func newFakeToolService() *fakeToolService {
	return &fakeToolService{
		tools: make(map[string]*registry.Tool),
	}
}

func (f *fakeToolService) seed(tools ...*registry.Tool) {
	for _, t := range tools {
		f.tools[t.ID] = t
	}
}

func (f *fakeToolService) Create(_ context.Context, input registry.CreateToolInput) (*registry.Tool, error) {
	if f.createHook != nil {
		if err := f.createHook(input); err != nil {
			return nil, err
		}
	}
	// Validate like the real service does.
	if strings.TrimSpace(input.Name) == "" {
		return nil, registry.ErrNameRequired
	}
	if strings.TrimSpace(input.Description) == "" {
		return nil, registry.ErrDescriptionRequired
	}
	if input.Mode != "" && input.Mode != "service" && input.Mode != "api" && input.Mode != "mcp" {
		return nil, registry.ErrModeInvalid
	}

	f.nextID++
	id := strings.Replace("00000000-0000-0000-0000-000000000000", "000000000000", strings.Repeat("0", 12-len(string(rune(f.nextID))))+string(rune('0'+f.nextID)), 1)
	// Use a simpler ID.
	id = "tool-" + strings.Repeat("0", 3) + itoa(f.nextID)

	now := time.Now().UTC()
	mode := input.Mode
	if mode == "" {
		mode = "service"
	}
	authType := input.AuthType
	if authType == "" {
		authType = "none"
	}
	authConfig := input.AuthConfig
	if authConfig == nil {
		authConfig = map[string]string{}
	}
	variables := input.Variables
	if variables == nil {
		variables = map[string]string{}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	tool := &registry.Tool{
		ID:          id,
		Name:        input.Name,
		Description: input.Description,
		Mode:        mode,
		Endpoint:    input.Endpoint,
		AuthType:    authType,
		AuthConfig:  authConfig,
		Variables:   variables,
		Transport:   input.Transport,
		Enabled:     enabled,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	f.tools[id] = tool
	return tool, nil
}

func (f *fakeToolService) GetByID(_ context.Context, id string) (*registry.Tool, error) {
	t, ok := f.tools[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return t, nil
}

func (f *fakeToolService) List(_ context.Context, params registry.ToolListParams) ([]*registry.Tool, string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	var result []*registry.Tool
	for _, t := range f.tools {
		if params.Query != "" {
			q := strings.ToLower(params.Query)
			if !strings.Contains(strings.ToLower(t.Name), q) && !strings.Contains(strings.ToLower(t.Description), q) {
				continue
			}
		}
		result = append(result, t)
	}

	// Sort by created_at DESC for deterministic pagination (simple approach).
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].CreatedAt.After(result[i].CreatedAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	var nextCursor string
	if len(result) > limit {
		nextCursor = "next-page-cursor"
		result = result[:limit]
	}
	return result, nextCursor, nil
}

func (f *fakeToolService) Update(_ context.Context, id string, input registry.UpdateToolInput) (*registry.Tool, error) {
	t, ok := f.tools[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			return nil, registry.ErrNameRequired
		}
		t.Name = *input.Name
	}
	if input.Description != nil {
		t.Description = *input.Description
	}
	if input.Mode != nil {
		t.Mode = *input.Mode
	}
	if input.Endpoint != nil {
		t.Endpoint = *input.Endpoint
	}
	if input.Enabled != nil {
		t.Enabled = *input.Enabled
	}
	t.UpdatedAt = time.Now().UTC()
	return t, nil
}

func (f *fakeToolService) Delete(_ context.Context, id string) error {
	if _, ok := f.tools[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(f.tools, id)
	return nil
}

func itoa(n int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if s == "" {
		return "0"
	}
	return s
}

// ---------------------------------------------------------------------------
// Helper: build a chi router with the tools handler + optional admin middleware bypass.
// ---------------------------------------------------------------------------

func toolsRouter(svc toolServicer) chi.Router {
	r := chi.NewRouter()
	h := newToolsHandler(svc)

	// Public routes.
	r.Get("/api/v1/tools", h.ListTools)
	r.Get("/api/v1/tools/{id}", h.GetTool)

	// Admin routes (bypass auth by injecting a fake admin user).
	r.Route("/api/v1/admin", func(ar chi.Router) {
		ar.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := auth.ContextWithUser(req.Context(), &auth.User{
					ID:    "admin-1",
					Email: "admin@test.com",
					Role:  "org_admin",
				})
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
		ar.Get("/tools", h.AdminListTools)
		ar.Post("/tools", h.CreateTool)
		ar.Put("/tools/{id}", h.UpdateTool)
		ar.Delete("/tools/{id}", h.DeleteTool)
	})

	return r
}

// ---------------------------------------------------------------------------
// Helper: decode JSON response body.
// ---------------------------------------------------------------------------

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ListTools tests
// ---------------------------------------------------------------------------

func TestListTools_Empty(t *testing.T) {
	svc := newFakeToolService()
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]json.RawMessage
	decodeJSON(t, rec, &body)

	var tools []interface{}
	if err := json.Unmarshal(body["tools"], &tools); err != nil {
		t.Fatalf("failed to unmarshal tools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
	// When no next_cursor, the key should be absent (handler only sets it when non-empty).
	if _, ok := body["next_cursor"]; ok {
		t.Error("expected next_cursor to be absent for empty list")
	}
}

func TestListTools_WithTools(t *testing.T) {
	svc := newFakeToolService()
	svc.seed(
		&registry.Tool{
			ID:          "t1",
			Name:        "Tool One",
			Description: "First tool",
			Mode:        "service",
			Endpoint:    "http://internal:8080",
			AuthType:    "bearer",
			AuthConfig:  map[string]string{"token": "secret"},
			Variables:   map[string]string{},
			Enabled:     true,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		},
	)
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	decodeJSON(t, rec, &body)

	if len(body.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(body.Tools))
	}

	tool := body.Tools[0]
	if tool["name"] != "Tool One" {
		t.Errorf("expected name 'Tool One', got %v", tool["name"])
	}
	// Public view: endpoint and auth_config should NOT be present (json:"-" tags).
	if _, ok := tool["endpoint"]; ok {
		t.Error("public list should not expose endpoint")
	}
	if _, ok := tool["auth_config"]; ok {
		t.Error("public list should not expose auth_config")
	}
}

func TestAdminListTools_ExposesAllFields(t *testing.T) {
	svc := newFakeToolService()
	svc.seed(
		&registry.Tool{
			ID:          "t1",
			Name:        "Admin Tool",
			Description: "Secret tool",
			Mode:        "service",
			Endpoint:    "http://backend:9090/api",
			AuthType:    "bearer",
			AuthConfig:  map[string]string{"token": "s3cret"},
			Variables:   map[string]string{"key": "val"},
			Enabled:     true,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		},
	)
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tools", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	decodeJSON(t, rec, &body)

	if len(body.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(body.Tools))
	}

	tool := body.Tools[0]
	if tool["endpoint"] != "http://backend:9090/api" {
		t.Errorf("admin list should expose endpoint, got %v", tool["endpoint"])
	}
	if tool["auth_config"] == nil {
		t.Error("admin list should expose auth_config")
	}
	authConfig, ok := tool["auth_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected auth_config to be a map, got %T", tool["auth_config"])
	}
	if authConfig["token"] != "s3cret" {
		t.Errorf("expected auth_config.token='s3cret', got %v", authConfig["token"])
	}
}

func TestListTools_QueryFilter(t *testing.T) {
	svc := newFakeToolService()
	svc.seed(
		&registry.Tool{
			ID:          "t1",
			Name:        "Weather API",
			Description: "Get weather data",
			Mode:        "service",
			Enabled:     true,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		},
		&registry.Tool{
			ID:          "t2",
			Name:        "Stock Tracker",
			Description: "Track stock prices",
			Mode:        "api",
			Enabled:     true,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		},
	)
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools?q=weather", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	decodeJSON(t, rec, &body)

	if len(body.Tools) != 1 {
		t.Fatalf("expected 1 filtered tool, got %d", len(body.Tools))
	}
	if body.Tools[0]["name"] != "Weather API" {
		t.Errorf("expected 'Weather API', got %v", body.Tools[0]["name"])
	}
}

func TestListTools_Pagination(t *testing.T) {
	svc := newFakeToolService()
	svc.seed(
		&registry.Tool{
			ID:          "t1",
			Name:        "Tool A",
			Description: "First",
			Mode:        "service",
			Enabled:     true,
			CreatedAt:   time.Now().Add(-2 * time.Second).UTC(),
			UpdatedAt:   time.Now().UTC(),
		},
		&registry.Tool{
			ID:          "t2",
			Name:        "Tool B",
			Description: "Second",
			Mode:        "service",
			Enabled:     true,
			CreatedAt:   time.Now().Add(-1 * time.Second).UTC(),
			UpdatedAt:   time.Now().UTC(),
		},
	)
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools?limit=1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Tools      []map[string]interface{} `json:"tools"`
		NextCursor string                   `json:"next_cursor"`
	}
	decodeJSON(t, rec, &body)

	if len(body.Tools) != 1 {
		t.Fatalf("expected 1 tool with limit=1, got %d", len(body.Tools))
	}
	if body.NextCursor == "" {
		t.Error("expected next_cursor to be set when more results exist")
	}
}

// ---------------------------------------------------------------------------
// GetTool tests
// ---------------------------------------------------------------------------

func TestGetTool_Found(t *testing.T) {
	svc := newFakeToolService()
	svc.seed(&registry.Tool{
		ID:          "t1",
		Name:        "My Tool",
		Description: "A tool",
		Mode:        "service",
		Endpoint:    "http://internal:8080",
		AuthType:    "none",
		AuthConfig:  map[string]string{},
		Variables:   map[string]string{},
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	})
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools/t1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var tool map[string]interface{}
	decodeJSON(t, rec, &tool)

	if tool["id"] != "t1" {
		t.Errorf("expected id=t1, got %v", tool["id"])
	}
	if tool["name"] != "My Tool" {
		t.Errorf("expected name='My Tool', got %v", tool["name"])
	}
	// Public view: endpoint hidden.
	if _, ok := tool["endpoint"]; ok {
		t.Error("public GetTool should not expose endpoint")
	}
}

func TestGetTool_NotFound(t *testing.T) {
	svc := newFakeToolService()
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools/nonexistent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	var body errorEnvelope
	decodeJSON(t, rec, &body)
	if body.Error.Code != "not_found" {
		t.Errorf("expected error code 'not_found', got %q", body.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// CreateTool tests
// ---------------------------------------------------------------------------

func TestCreateTool_Success(t *testing.T) {
	svc := newFakeToolService()
	r := toolsRouter(svc)

	payload := `{
		"name": "New Tool",
		"description": "A brand new tool",
		"mode": "service",
		"endpoint": "http://backend:8080"
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var tool map[string]interface{}
	decodeJSON(t, rec, &tool)

	if tool["name"] != "New Tool" {
		t.Errorf("expected name='New Tool', got %v", tool["name"])
	}
	if tool["description"] != "A brand new tool" {
		t.Errorf("expected description='A brand new tool', got %v", tool["description"])
	}
	// Admin create response should include endpoint.
	if tool["endpoint"] != "http://backend:8080" {
		t.Errorf("expected endpoint in admin response, got %v", tool["endpoint"])
	}
	if tool["id"] == nil || tool["id"] == "" {
		t.Error("expected id to be set")
	}
}

func TestCreateTool_MissingName(t *testing.T) {
	svc := newFakeToolService()
	r := toolsRouter(svc)

	payload := `{
		"description": "No name provided",
		"endpoint": "http://backend:8080"
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	var body errorEnvelope
	decodeJSON(t, rec, &body)
	if body.Error.Code != "validation_error" {
		t.Errorf("expected error code 'validation_error', got %q", body.Error.Code)
	}
}

func TestCreateTool_InvalidMode(t *testing.T) {
	svc := newFakeToolService()
	r := toolsRouter(svc)

	payload := `{
		"name": "Bad Mode Tool",
		"description": "Has invalid mode",
		"mode": "invalid_mode",
		"endpoint": "http://backend:8080"
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	var body errorEnvelope
	decodeJSON(t, rec, &body)
	if body.Error.Code != "validation_error" {
		t.Errorf("expected error code 'validation_error', got %q", body.Error.Code)
	}
}

func TestCreateTool_InvalidBody(t *testing.T) {
	svc := newFakeToolService()
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tools", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// UpdateTool tests
// ---------------------------------------------------------------------------

func TestUpdateTool_Success(t *testing.T) {
	svc := newFakeToolService()
	svc.seed(&registry.Tool{
		ID:          "t1",
		Name:        "Old Name",
		Description: "Old desc",
		Mode:        "service",
		Endpoint:    "http://backend:8080",
		AuthType:    "none",
		AuthConfig:  map[string]string{},
		Variables:   map[string]string{},
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	})
	r := toolsRouter(svc)

	payload := `{"name": "Updated Name"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/tools/t1", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var tool map[string]interface{}
	decodeJSON(t, rec, &tool)

	if tool["name"] != "Updated Name" {
		t.Errorf("expected name='Updated Name', got %v", tool["name"])
	}
	// Admin update response includes endpoint.
	if tool["endpoint"] != "http://backend:8080" {
		t.Errorf("expected endpoint in admin response, got %v", tool["endpoint"])
	}
}

func TestUpdateTool_NotFound(t *testing.T) {
	svc := newFakeToolService()
	r := toolsRouter(svc)

	payload := `{"name": "Updated"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/tools/nonexistent", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var body errorEnvelope
	decodeJSON(t, rec, &body)
	if body.Error.Code != "not_found" {
		t.Errorf("expected error code 'not_found', got %q", body.Error.Code)
	}
}

func TestUpdateTool_InvalidBody(t *testing.T) {
	svc := newFakeToolService()
	svc.seed(&registry.Tool{
		ID:        "t1",
		Name:      "Tool",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/tools/t1", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// DeleteTool tests
// ---------------------------------------------------------------------------

func TestDeleteTool_Success(t *testing.T) {
	svc := newFakeToolService()
	svc.seed(&registry.Tool{
		ID:          "t1",
		Name:        "Doomed Tool",
		Description: "Will be deleted",
		Mode:        "service",
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	})
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/tools/t1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify tool is actually removed.
	if _, ok := svc.tools["t1"]; ok {
		t.Error("expected tool t1 to be deleted from store")
	}
}

func TestDeleteTool_NotFound(t *testing.T) {
	svc := newFakeToolService()
	r := toolsRouter(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/tools/nonexistent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var body errorEnvelope
	decodeJSON(t, rec, &body)
	if body.Error.Code != "not_found" {
		t.Errorf("expected error code 'not_found', got %q", body.Error.Code)
	}
}
