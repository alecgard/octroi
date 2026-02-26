package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/alecgard/octroi/internal/audit"
)

// auditLogStore defines the audit store methods used by the handler.
type auditLogStore interface {
	List(ctx context.Context, params audit.ListParams) ([]audit.Entry, string, error)
}

type auditHandler struct {
	store auditLogStore
}

func newAuditHandler(store auditLogStore) *auditHandler {
	return &auditHandler{store: store}
}

// ListAuditLog handles GET /api/v1/admin/audit-log.
func (h *auditHandler) ListAuditLog(w http.ResponseWriter, r *http.Request) {
	params := audit.ListParams{
		ResourceType: r.URL.Query().Get("resource_type"),
		Cursor:       r.URL.Query().Get("cursor"),
	}

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			params.Limit = n
		}
	}

	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			params.From = &t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			params.To = &t
		}
	}

	entries, nextCursor, err := h.store.List(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list audit log")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"entries":     entries,
		"next_cursor": nextCursor,
	})
}
