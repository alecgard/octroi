package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/go-chi/chi/v5"
)

// permissionStoreIface defines the permission store methods used by the handler.
type permissionStoreIface interface {
	ListByAgent(ctx context.Context, tenantID string, agentID string) ([]agent.Permission, error)
	SetWithSubTools(ctx context.Context, tenantID string, agentID, toolID string, allowed bool, subTools []string) error
	SetBulk(ctx context.Context, tenantID string, agentID string, permissions map[string]bool) error
	SetBulkWithSubTools(ctx context.Context, tenantID string, agentID string, permissions map[string]agent.BulkPermission) error
	Delete(ctx context.Context, tenantID string, agentID, toolID string) error
}

// permissionAgentStoreIface defines the agent store methods used by the permissions handler.
type permissionAgentStoreIface interface {
	GetByID(ctx context.Context, tenantID, id string) (*agent.Agent, error)
	Update(ctx context.Context, tenantID, id string, in agent.UpdateAgentInput) (*agent.Agent, error)
}

type permissionsHandler struct {
	store      permissionStoreIface
	agentStore permissionAgentStoreIface
}

func newPermissionsHandler(store permissionStoreIface, agentStore permissionAgentStoreIface) *permissionsHandler {
	return &permissionsHandler{store: store, agentStore: agentStore}
}

// ListPermissions handles GET /api/v1/admin/agents/{agentID}/permissions.
func (h *permissionsHandler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id is required")
		return
	}

	tenant := auth.TenantFromContext(r.Context())

	perms, err := h.store.ListByAgent(r.Context(), tenant.ID, agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list permissions")
		return
	}

	a, err := h.agentStore.GetByID(r.Context(), tenant.ID, agentID)
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

	tenant := auth.TenantFromContext(r.Context())

	var body struct {
		Allowed  bool     `json:"allowed"`
		SubTools []string `json:"sub_tools,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}

	if err := h.store.SetWithSubTools(r.Context(), tenant.ID, agentID, toolID, body.Allowed, body.SubTools); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to set permission")
		return
	}

	auditLog(r, "set_permission", "agent_tool_permission", agentID+"/"+toolID,
		"allowed", body.Allowed)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agent_id":  agentID,
		"tool_id":   toolID,
		"allowed":   body.Allowed,
		"sub_tools": body.SubTools,
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
		Permissions   map[string]json.RawMessage `json:"permissions"`
		AllowlistMode *bool                      `json:"allowlist_mode,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}

	tenant := auth.TenantFromContext(r.Context())

	// Update allowlist_mode if provided.
	if body.AllowlistMode != nil {
		_, err := h.agentStore.Update(r.Context(), tenant.ID, agentID, agent.UpdateAgentInput{
			AllowlistMode: body.AllowlistMode,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to update allowlist mode")
			return
		}
	}

	if len(body.Permissions) > 0 {
		// Parse each permission value: either a plain bool or {allowed, sub_tools}.
		bulkPerms := make(map[string]agent.BulkPermission, len(body.Permissions))
		hasSubTools := false
		for toolID, raw := range body.Permissions {
			// Try bool first (legacy format).
			var boolVal bool
			if err := json.Unmarshal(raw, &boolVal); err == nil {
				bulkPerms[toolID] = agent.BulkPermission{Allowed: boolVal}
				continue
			}
			// Try object format.
			var obj agent.BulkPermission
			if err := json.Unmarshal(raw, &obj); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_body", "invalid permission value for tool "+toolID)
				return
			}
			if len(obj.SubTools) > 0 {
				hasSubTools = true
			}
			bulkPerms[toolID] = obj
		}

		if hasSubTools {
			if err := h.store.SetBulkWithSubTools(r.Context(), tenant.ID, agentID, bulkPerms); err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to set permissions")
				return
			}
		} else {
			// Use the simpler path when no sub-tools are involved.
			simple := make(map[string]bool, len(bulkPerms))
			for toolID, p := range bulkPerms {
				simple[toolID] = p.Allowed
			}
			if err := h.store.SetBulk(r.Context(), tenant.ID, agentID, simple); err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to set permissions")
				return
			}
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

	tenant := auth.TenantFromContext(r.Context())

	if err := h.store.Delete(r.Context(), tenant.ID, agentID, toolID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete permission")
		return
	}

	auditLog(r, "delete_permission", "agent_tool_permission", agentID+"/"+toolID)

	w.WriteHeader(http.StatusNoContent)
}
