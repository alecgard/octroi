package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/go-chi/chi/v5"
)

// usageHandler groups usage and transaction HTTP handlers.
type usageHandler struct {
	store      *metering.Store
	agentStore *agent.Store
}

func newUsageHandler(store *metering.Store, agentStore *agent.Store) *usageHandler {
	return &usageHandler{store: store, agentStore: agentStore}
}

// parseTimeParam parses a date query param in YYYY-MM-DD or RFC3339 format.
func parseTimeParam(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	// Try RFC3339 first.
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t, nil
	}
	// Fall back to date-only.
	t, err = time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// buildUsageQuery constructs a UsageQuery from query params, respecting agent auth scope.
func buildUsageQuery(r *http.Request, isAdmin bool) (*metering.UsageQuery, error) {
	q := &metering.UsageQuery{}

	if isAdmin {
		if agentParam := r.URL.Query().Get("agent_id"); agentParam != "" {
			if strings.Contains(agentParam, ",") {
				q.AgentIDs = strings.Split(agentParam, ",")
			} else {
				q.AgentID = agentParam
			}
		}
		if toolParam := r.URL.Query().Get("tool_id"); toolParam != "" {
			if strings.Contains(toolParam, ",") {
				q.ToolIDs = strings.Split(toolParam, ",")
			} else {
				q.ToolID = toolParam
			}
		}
	} else {
		agent := auth.AgentFromContext(r.Context())
		if agent != nil {
			q.AgentID = agent.ID
		}
		if toolParam := r.URL.Query().Get("tool_id"); toolParam != "" {
			if strings.Contains(toolParam, ",") {
				q.ToolIDs = strings.Split(toolParam, ",")
			} else {
				q.ToolID = toolParam
			}
		}
	}

	from, err := parseTimeParam(r.URL.Query().Get("from"))
	if err != nil {
		return nil, err
	}
	q.From = from

	to, err := parseTimeParam(r.URL.Query().Get("to"))
	if err != nil {
		return nil, err
	}
	q.To = to

	q.Cursor = r.URL.Query().Get("cursor")

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		l, lErr := strconv.Atoi(limitStr)
		if lErr != nil || l < 1 {
			return nil, lErr
		}
		q.Limit = l
	}

	return q, nil
}

// GetUsage handles GET /api/v1/usage (agent-authed; agent can only see own usage).
func (h *usageHandler) GetUsage(w http.ResponseWriter, r *http.Request) {
	q, err := buildUsageQuery(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid query parameters: "+err.Error())
		return
	}

	summary, err := h.store.GetSummary(r.Context(), *q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get usage summary")
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// GetUsageAdmin handles GET /api/v1/admin/usage (admin can query any agent/tool).
func (h *usageHandler) GetUsageAdmin(w http.ResponseWriter, r *http.Request) {
	q, err := buildUsageQuery(r, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid query parameters: "+err.Error())
		return
	}

	// Team filter: if ?team=X (or comma-separated), resolve to agent IDs.
	if teamFilter := r.URL.Query().Get("team"); teamFilter != "" && q.AgentID == "" {
		teams := strings.Split(teamFilter, ",")
		var allAgentIDs []string
		for _, t := range teams {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			ids, tErr := h.agentStore.ListIDsByTeam(r.Context(), t)
			if tErr != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "failed to list team agents")
				return
			}
			allAgentIDs = append(allAgentIDs, ids...)
		}
		// Merge with any existing AgentIDs from agent_id param.
		if len(q.AgentIDs) > 0 {
			// Intersect: only keep agent IDs that are in both sets.
			teamSet := make(map[string]bool, len(allAgentIDs))
			for _, id := range allAgentIDs {
				teamSet[id] = true
			}
			var intersected []string
			for _, id := range q.AgentIDs {
				if teamSet[id] {
					intersected = append(intersected, id)
				}
			}
			q.AgentIDs = intersected
		} else {
			q.AgentIDs = allAgentIDs
		}
	}

	summary, err := h.store.GetSummary(r.Context(), *q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get usage summary")
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// GetUsageByAgent handles GET /api/v1/admin/usage/agents/{agentID} (admin).
func (h *usageHandler) GetUsageByAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id is required")
		return
	}

	from, err := parseTimeParam(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid 'from' parameter")
		return
	}
	to, err := parseTimeParam(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid 'to' parameter")
		return
	}

	q := metering.UsageQuery{
		AgentID: agentID,
		From:    from,
		To:      to,
	}

	summary, err := h.store.GetSummary(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get usage summary")
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// GetUsageByTool handles GET /api/v1/admin/usage/tools/{toolID} (admin).
func (h *usageHandler) GetUsageByTool(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "toolID")
	if toolID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "tool_id is required")
		return
	}

	from, err := parseTimeParam(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid 'from' parameter")
		return
	}
	to, err := parseTimeParam(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid 'to' parameter")
		return
	}

	q := metering.UsageQuery{
		ToolID: toolID,
		From:   from,
		To:     to,
	}

	summary, err := h.store.GetSummary(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get usage summary")
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// GetUsageByAgentTool handles GET /api/v1/admin/usage/agents/{agentID}/tools/{toolID} (admin).
func (h *usageHandler) GetUsageByAgentTool(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	toolID := chi.URLParam(r, "toolID")
	if agentID == "" || toolID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id and tool_id are required")
		return
	}

	from, err := parseTimeParam(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid 'from' parameter")
		return
	}
	to, err := parseTimeParam(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid 'to' parameter")
		return
	}

	q := metering.UsageQuery{
		AgentID: agentID,
		ToolID:  toolID,
		From:    from,
		To:      to,
	}

	summary, err := h.store.GetSummary(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get usage summary")
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// ListTransactions handles GET /api/v1/usage/transactions (agent-authed) or
// GET /api/v1/admin/usage/transactions (admin).
func (h *usageHandler) ListTransactions(w http.ResponseWriter, r *http.Request, isAdmin bool) {
	q, err := buildUsageQuery(r, isAdmin)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid query parameters: "+err.Error())
		return
	}

	// Team filter for admin.
	if isAdmin {
		if teamFilter := r.URL.Query().Get("team"); teamFilter != "" && q.AgentID == "" {
			teams := strings.Split(teamFilter, ",")
			var allAgentIDs []string
			for _, t := range teams {
				t = strings.TrimSpace(t)
				if t == "" {
					continue
				}
				ids, tErr := h.agentStore.ListIDsByTeam(r.Context(), t)
				if tErr != nil {
					writeError(w, http.StatusInternalServerError, "internal_error", "failed to list team agents")
					return
				}
				allAgentIDs = append(allAgentIDs, ids...)
			}
			if len(q.AgentIDs) > 0 {
				teamSet := make(map[string]bool, len(allAgentIDs))
				for _, id := range allAgentIDs {
					teamSet[id] = true
				}
				var intersected []string
				for _, id := range q.AgentIDs {
					if teamSet[id] {
						intersected = append(intersected, id)
					}
				}
				q.AgentIDs = intersected
			} else {
				q.AgentIDs = allAgentIDs
			}
		}
	}

	txns, nextCursor, err := h.store.ListTransactions(r.Context(), *q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list transactions")
		return
	}

	resp := map[string]interface{}{
		"transactions": txns,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetToolCallCounts handles GET /api/v1/admin/usage/tools/calls (admin).
func (h *usageHandler) GetToolCallCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := h.store.GetToolCallCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get tool call counts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"counts": counts})
}
