package api

import (
	"net/http"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/go-chi/chi/v5"
)

type permissionsHandler struct {
	store      *agent.PermissionStore
	agentStore *agent.Store
}

func newPermissionsHandler(store *agent.PermissionStore, agentStore *agent.Store) *permissionsHandler {
	return &permissionsHandler{store: store, agentStore: agentStore}
}

// ListPermissions handles GET /api/v1/admin/agents/{agentID}/permissions.
func (h *permissionsHandler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id is required")
		return
	}

	perms, err := h.store.ListByAgent(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list permissions")
		return
	}

	a, err := h.agentStore.GetByID(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"allowlist_mode": a.AllowlistMode,
		"permissions":    perms,
	})
}

// SetPermission handles PUT /api/v1/admin/agents/{agentID}/permissions/{toolID}.
func (h *permissionsHandler) SetPermission(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	toolID := chi.URLParam(r, "toolID")
	if agentID == "" || toolID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id and tool_id are required")
		return
	}

	var body struct {
		Allowed bool `json:"allowed"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}

	if err := h.store.Set(r.Context(), agentID, toolID, body.Allowed); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to set permission")
		return
	}

	auditLog(r, "set_permission", "agent_tool_permission", agentID+"/"+toolID,
		"allowed", body.Allowed)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agent_id": agentID,
		"tool_id":  toolID,
		"allowed":  body.Allowed,
	})
}

// BulkSetPermissions handles PUT /api/v1/admin/agents/{agentID}/permissions.
func (h *permissionsHandler) BulkSetPermissions(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id is required")
		return
	}

	var body struct {
		Permissions   map[string]bool `json:"permissions"`
		AllowlistMode *bool           `json:"allowlist_mode,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}

	// Update allowlist_mode if provided.
	if body.AllowlistMode != nil {
		_, err := h.agentStore.Update(r.Context(), agentID, agent.UpdateAgentInput{
			AllowlistMode: body.AllowlistMode,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to update allowlist mode")
			return
		}
	}

	if len(body.Permissions) > 0 {
		if err := h.store.SetBulk(r.Context(), agentID, body.Permissions); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to set permissions")
			return
		}
	}

	auditLog(r, "bulk_set_permissions", "agent_tool_permission", agentID,
		"permission_count", len(body.Permissions))

	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// DeletePermission handles DELETE /api/v1/admin/agents/{agentID}/permissions/{toolID}.
func (h *permissionsHandler) DeletePermission(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	toolID := chi.URLParam(r, "toolID")
	if agentID == "" || toolID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id and tool_id are required")
		return
	}

	if err := h.store.Delete(r.Context(), agentID, toolID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete permission")
		return
	}

	auditLog(r, "delete_permission", "agent_tool_permission", agentID+"/"+toolID)

	w.WriteHeader(http.StatusNoContent)
}
