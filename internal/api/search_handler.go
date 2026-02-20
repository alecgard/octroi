package api

import (
	"net/http"
	"strconv"

	"github.com/alecgard/octroi/internal/registry"
)

// searchHandler groups search-related HTTP handlers.
type searchHandler struct {
	service *registry.Service
}

func newSearchHandler(svc *registry.Service) *searchHandler {
	return &searchHandler{service: svc}
}

// SearchTools handles GET /api/v1/tools/search?q=...&limit=...&cursor=...
// This is unauthenticated. Returns tools without endpoint or auth_config
// (endpoint and auth_config have json:"-" on the Tool struct).
func (h *searchHandler) SearchTools(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	cursor := r.URL.Query().Get("cursor")

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil || l < 1 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		limit = l
	}

	tools, nextCursor, err := h.service.Search(r.Context(), q, limit, cursor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to search tools")
		return
	}

	resp := map[string]interface{}{
		"tools": tools,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	writeJSON(w, http.StatusOK, resp)
}
