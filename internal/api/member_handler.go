package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// memberHandler groups member (team-scoped) HTTP handlers.
type memberHandler struct {
	agentStore *agent.Store
	meterStore *metering.Store
}

func newMemberHandler(agentStore *agent.Store, meterStore *metering.Store) *memberHandler {
	return &memberHandler{
		agentStore: agentStore,
		meterStore: meterStore,
	}
}


// ListAgents handles GET /api/v1/member/agents — agents in user's teams.
func (h *memberHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	params := agent.AgentListParams{
		Cursor: r.URL.Query().Get("cursor"),
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil || l < 1 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		params.Limit = l
	}

	agents, nextCursor, err := h.agentStore.ListByTeams(r.Context(), u.TeamNames(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list agents")
		return
	}

	resp := map[string]interface{}{
		"agents": agents,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateAgent handles POST /api/v1/member/agents — auto-assigns user's team.
func (h *memberHandler) CreateAgent(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	var req struct {
		Name      string `json:"name"`
		Team      string `json:"team"`
		RateLimit int    `json:"rate_limit"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "name is required")
		return
	}

	teamNames := u.TeamNames()

	// Determine which team to assign the agent to.
	team := req.Team
	if team == "" {
		if len(teamNames) == 1 {
			team = teamNames[0]
		} else if len(teamNames) == 0 {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "you are not in any team")
			return
		} else {
			writeError(w, http.StatusUnprocessableEntity, "validation_error", "team is required when you belong to multiple teams")
			return
		}
	} else {
		if !u.InTeam(team) {
			writeError(w, http.StatusForbidden, "forbidden", "you are not a member of team "+team)
			return
		}
	}

	apiKey, plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate api key")
		return
	}

	input := agent.CreateAgentInput{
		Name:         req.Name,
		APIKeyHash:   apiKey.Hash,
		APIKeyPrefix: apiKey.Prefix,
		Team:         team,
		RateLimit:    req.RateLimit,
	}

	ag, err := h.agentStore.Create(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create agent")
		return
	}

	auditLog(r, "create", "agent", ag.ID, "name", ag.Name)

	resp := map[string]interface{}{
		"id":             ag.ID,
		"name":           ag.Name,
		"api_key_prefix": ag.APIKeyPrefix,
		"api_key":        plaintext,
		"team":           ag.Team,
		"rate_limit":     ag.RateLimit,
		"created_at":     ag.CreatedAt,
	}
	writeJSON(w, http.StatusCreated, resp)
}

// UpdateAgent handles PUT /api/v1/member/agents/{id} — team-scoped.
func (h *memberHandler) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "agent id is required")
		return
	}

	// Verify ownership.
	existing, err := h.agentStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get agent")
		return
	}
	if !u.InTeam(existing.Team) {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}

	var input agent.UpdateAgentInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}
	// Members cannot change team.
	input.Team = nil

	ag, err := h.agentStore.Update(r.Context(), id, input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update agent")
		return
	}

	auditLog(r, "update", "agent", id, "name", ag.Name)

	writeJSON(w, http.StatusOK, ag)
}

// DeleteAgent handles DELETE /api/v1/member/agents/{id} — team-scoped.
func (h *memberHandler) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "agent id is required")
		return
	}

	// Verify ownership.
	existing, err := h.agentStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get agent")
		return
	}
	if !u.InTeam(existing.Team) {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}

	if err := h.agentStore.Archive(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete agent")
		return
	}

	auditLog(r, "delete", "agent", id, "name", existing.Name)

	w.WriteHeader(http.StatusNoContent)
}

// RegenerateKey handles POST /api/v1/member/agents/{id}/regenerate-key.
func (h *memberHandler) RegenerateKey(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "agent id is required")
		return
	}

	// Verify ownership.
	existing, err := h.agentStore.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get agent")
		return
	}
	if !u.InTeam(existing.Team) {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}

	apiKey, plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate api key")
		return
	}

	ag, err := h.agentStore.RegenerateKey(r.Context(), id, apiKey.Hash, apiKey.Prefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to regenerate key")
		return
	}

	auditLog(r, "regenerate_key", "agent", id, "name", ag.Name)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":             ag.ID,
		"name":           ag.Name,
		"api_key_prefix": ag.APIKeyPrefix,
		"api_key":        plaintext,
		"team":           ag.Team,
		"rate_limit":     ag.RateLimit,
		"created_at":     ag.CreatedAt,
	})
}

// GetUsage handles GET /api/v1/member/usage — team-scoped usage.
func (h *memberHandler) GetUsage(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	// Optional team filter — must be one of user's teams (supports comma-separated).
	teamNames := u.TeamNames()
	teams := teamNames
	if teamFilter := r.URL.Query().Get("team"); teamFilter != "" {
		filterTeams := strings.Split(teamFilter, ",")
		var validTeams []string
		for _, t := range filterTeams {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if !u.InTeam(t) {
				writeError(w, http.StatusForbidden, "forbidden", "you are not a member of team "+t)
				return
			}
			validTeams = append(validTeams, t)
		}
		if len(validTeams) > 0 {
			teams = validTeams
		}
	}

	agentIDs, err := h.agentStore.ListIDsByTeams(r.Context(), teams)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list team agents")
		return
	}

	q, err := parseUsageFilters(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid query parameters: "+err.Error())
		return
	}
	q.AgentIDs = agentIDs

	summary, err := h.meterStore.GetSummary(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get usage summary")
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// ListTransactions handles GET /api/v1/member/usage/transactions — team-scoped.
func (h *memberHandler) ListTransactions(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	// Optional team filter — must be one of user's teams (supports comma-separated).
	teamNames := u.TeamNames()
	teams := teamNames
	if teamFilter := r.URL.Query().Get("team"); teamFilter != "" {
		filterTeams := strings.Split(teamFilter, ",")
		var validTeams []string
		for _, t := range filterTeams {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if !u.InTeam(t) {
				writeError(w, http.StatusForbidden, "forbidden", "you are not a member of team "+t)
				return
			}
			validTeams = append(validTeams, t)
		}
		if len(validTeams) > 0 {
			teams = validTeams
		}
	}

	agentIDs, err := h.agentStore.ListIDsByTeams(r.Context(), teams)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list team agents")
		return
	}

	q, err := parseUsageFilters(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_params", "invalid query parameters: "+err.Error())
		return
	}
	q.AgentIDs = agentIDs

	txns, nextCursor, err := h.meterStore.ListTransactions(r.Context(), q)
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
