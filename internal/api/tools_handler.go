package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alecgard/octroi/internal/mcp"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// toolsHandler groups tool-related HTTP handlers.
type toolsHandler struct {
	service *registry.Service
}

func newToolsHandler(svc *registry.Service) *toolsHandler {
	return &toolsHandler{service: svc}
}

// CreateTool handles POST /api/v1/tools (admin).
func (h *toolsHandler) CreateTool(w http.ResponseWriter, r *http.Request) {
	var input registry.CreateToolInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	tool, err := h.service.Create(r.Context(), input)
	if err != nil {
		if isValidationError(err) {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create tool")
		return
	}

	auditLog(r, "create", "tool", tool.ID, "name", tool.Name)

	// Return full tool including endpoint and auth_config for admin.
	writeJSON(w, http.StatusCreated, adminToolView(tool))
}

// UpdateTool handles PUT /api/v1/tools/{id} (admin).
func (h *toolsHandler) UpdateTool(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "tool id is required")
		return
	}

	var input registry.UpdateToolInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	tool, err := h.service.Update(r.Context(), id, input)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "tool not found")
			return
		}
		if isValidationError(err) {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update tool")
		return
	}

	auditLog(r, "update", "tool", id)

	writeJSON(w, http.StatusOK, adminToolView(tool))
}

// DeleteTool handles DELETE /api/v1/tools/{id} (admin).
func (h *toolsHandler) DeleteTool(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "tool id is required")
		return
	}

	err := h.service.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "tool not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete tool")
		return
	}

	auditLog(r, "delete", "tool", id)

	w.WriteHeader(http.StatusNoContent)
}

// ListTools handles GET /api/v1/tools (public).
func (h *toolsHandler) ListTools(w http.ResponseWriter, r *http.Request) {
	params := registry.ToolListParams{
		Cursor: r.URL.Query().Get("cursor"),
		Query:  r.URL.Query().Get("q"),
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil || l < 1 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		params.Limit = l
	}

	tools, nextCursor, err := h.service.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list tools")
		return
	}

	// Public view: Tool struct already omits endpoint and auth_config via json:"-".
	resp := map[string]interface{}{
		"tools": tools,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetTool handles GET /api/v1/tools/{id} (public view, no endpoint/auth_config).
func (h *toolsHandler) GetTool(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "tool id is required")
		return
	}

	tool, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "tool not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get tool")
		return
	}

	// Public view: Tool struct json:"-" tags hide endpoint and auth_config.
	writeJSON(w, http.StatusOK, tool)
}

// AdminListTools handles GET /api/v1/admin/tools (admin view with endpoint/auth_config).
func (h *toolsHandler) AdminListTools(w http.ResponseWriter, r *http.Request) {
	params := registry.ToolListParams{
		Cursor: r.URL.Query().Get("cursor"),
		Query:  r.URL.Query().Get("q"),
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil || l < 1 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		params.Limit = l
	}

	tools, nextCursor, err := h.service.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list tools")
		return
	}

	views := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		views[i] = adminToolView(t)
	}
	resp := map[string]interface{}{
		"tools": views,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	writeJSON(w, http.StatusOK, resp)
}

// RefreshMCPTools returns a handler that triggers re-discovery of sub-tools from an MCP upstream.
func (h *toolsHandler) RefreshMCPTools(agg *mcp.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "invalid_id", "tool id is required")
			return
		}

		tool, err := h.service.GetByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "tool not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to get tool")
			return
		}
		if tool.Mode != "mcp" {
			writeError(w, http.StatusBadRequest, "invalid_mode", "tool is not an MCP tool")
			return
		}

		if err := agg.RefreshUpstream(r.Context(), id); err != nil {
			writeError(w, http.StatusBadGateway, "refresh_failed", "failed to refresh MCP tools: "+err.Error())
			return
		}

		auditLog(r, "refresh_mcp", "tool", id, "name", tool.Name)

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ListMCPTools returns a handler that lists discovered sub-tools from an MCP upstream.
func (h *toolsHandler) ListMCPTools(agg *mcp.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "invalid_id", "tool id is required")
			return
		}

		tool, err := h.service.GetByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "tool not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to get tool")
			return
		}
		if tool.Mode != "mcp" {
			writeError(w, http.StatusBadRequest, "invalid_mode", "tool is not an MCP tool")
			return
		}

		mcpTools, ok := agg.GetUpstreamTools(id)
		if !ok {
			writeJSON(w, http.StatusOK, map[string]interface{}{"tools": []interface{}{}})
			return
		}

		// Convert mcp.Tool objects to simple maps for JSON response.
		toolList := make([]map[string]interface{}, len(mcpTools))
		for i, t := range mcpTools {
			toolList[i] = map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
			}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{"tools": toolList})
	}
}

// adminToolView returns a map that includes endpoint and auth_config for admin responses.
func adminToolView(t *registry.Tool) map[string]interface{} {
	return map[string]interface{}{
		"id":               t.ID,
		"name":             t.Name,
		"description":      t.Description,
		"mode":             t.Mode,
		"endpoint":         t.Endpoint,
		"auth_type":        t.AuthType,
		"auth_config":      t.AuthConfig,
		"variables":        t.Variables,
		"pricing_model":    t.PricingModel,
		"pricing_amount":   t.PricingAmount,
		"pricing_currency": t.PricingCurrency,
		"rate_limit":       t.RateLimit,
		"budget_limit":     t.BudgetLimit,
		"budget_window":    t.BudgetWindow,
		"transport":        t.Transport,
		"enabled":          t.Enabled,
		"created_at":       t.CreatedAt,
		"updated_at":       t.UpdatedAt,
	}
}

// isValidationError checks whether the error is a known validation error from the registry service.
func isValidationError(err error) bool {
	return errors.Is(err, registry.ErrNameRequired) ||
		errors.Is(err, registry.ErrDescriptionRequired) ||
		errors.Is(err, registry.ErrEndpointInvalid) ||
		errors.Is(err, registry.ErrAuthTypeInvalid) ||
		errors.Is(err, registry.ErrModeInvalid) ||
		errors.Is(err, registry.ErrVariablesMissing) ||
		errors.Is(err, registry.ErrTransportInvalid) ||
		errors.Is(err, registry.ErrTransportRequired) ||
		errors.Is(err, registry.ErrTransportForbidden)
}
