package api

import (
	"errors"
	"net/http"

	"github.com/alecgard/octroi/internal/ratelimit"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// toolRateLimitsHandler groups handlers for tool rate limit overrides.
type toolRateLimitsHandler struct {
	store     *ratelimit.ToolRateLimitStore
	toolStore *registry.Store
}

func newToolRateLimitsHandler(store *ratelimit.ToolRateLimitStore, toolStore *registry.Store) *toolRateLimitsHandler {
	return &toolRateLimitsHandler{store: store, toolStore: toolStore}
}

// ListToolRateLimits handles GET /api/v1/admin/tools/{toolID}/rate-limits.
func (h *toolRateLimitsHandler) ListToolRateLimits(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "toolID")
	if toolID == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "tool id is required")
		return
	}

	tool, err := h.toolStore.GetByID(r.Context(), toolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "tool not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get tool")
		return
	}

	overrides, err := h.store.ListByTool(r.Context(), toolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list rate limit overrides")
		return
	}
	if overrides == nil {
		overrides = []ratelimit.ToolRateOverride{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"global_rate_limit": tool.RateLimit,
		"overrides":         overrides,
	})
}

// SetToolRateLimit handles PUT /api/v1/admin/tools/{toolID}/rate-limits.
func (h *toolRateLimitsHandler) SetToolRateLimit(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "toolID")
	if toolID == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "tool id is required")
		return
	}

	var input struct {
		Scope     string `json:"scope"`
		ScopeID   string `json:"scope_id"`
		RateLimit int    `json:"rate_limit"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	if input.Scope != "team" && input.Scope != "agent" {
		writeError(w, http.StatusBadRequest, "invalid_params", "scope must be 'team' or 'agent'")
		return
	}
	if input.ScopeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "scope_id is required")
		return
	}
	if input.RateLimit <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_params", "rate_limit must be a positive integer")
		return
	}

	// Verify tool exists.
	if _, err := h.toolStore.GetByID(r.Context(), toolID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "tool not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to verify tool")
		return
	}

	if err := h.store.Set(r.Context(), toolID, input.Scope, input.ScopeID, input.RateLimit); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to set rate limit override")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteToolRateLimit handles DELETE /api/v1/admin/tools/{toolID}/rate-limits/{scope}/{scopeID}.
func (h *toolRateLimitsHandler) DeleteToolRateLimit(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "toolID")
	scope := chi.URLParam(r, "scope")
	scopeID := chi.URLParam(r, "scopeID")

	if toolID == "" || scope == "" || scopeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "toolID, scope, and scopeID are required")
		return
	}

	if scope != "team" && scope != "agent" {
		writeError(w, http.StatusBadRequest, "invalid_params", "scope must be 'team' or 'agent'")
		return
	}

	err := h.store.Delete(r.Context(), toolID, scope, scopeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "rate limit override not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete rate limit override")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
