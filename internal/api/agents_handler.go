package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// agentsHandler groups agent-related HTTP handlers.
type agentsHandler struct {
	store       *agent.Store
	budgetStore *agent.BudgetStore
}

func newAgentsHandler(store *agent.Store, budgetStore *agent.BudgetStore) *agentsHandler {
	return &agentsHandler{
		store:       store,
		budgetStore: budgetStore,
	}
}

// createAgentRequest is the JSON body for creating an agent.
type createAgentRequest struct {
	Name      string `json:"name"`
	Team      string `json:"team"`
	RateLimit int    `json:"rate_limit"`
}

// CreateAgent handles POST /api/v1/agents (admin).
// Generates an API key and returns the plaintext key in the response (only time it is shown).
func (h *agentsHandler) CreateAgent(w http.ResponseWriter, r *http.Request) {
	var req createAgentRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "name is required")
		return
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
		Team:         req.Team,
		RateLimit:    req.RateLimit,
	}

	ag, err := h.store.Create(r.Context(), input)
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

// UpdateAgent handles PUT /api/v1/agents/{id} (admin).
func (h *agentsHandler) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "agent id is required")
		return
	}

	var input agent.UpdateAgentInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	ag, err := h.store.Update(r.Context(), id, input)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update agent")
		return
	}

	auditLog(r, "update", "agent", id)

	writeJSON(w, http.StatusOK, ag)
}

// DeleteAgent handles DELETE /api/v1/agents/{id} (admin).
func (h *agentsHandler) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "agent id is required")
		return
	}

	err := h.store.Delete(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete agent")
		return
	}

	auditLog(r, "delete", "agent", id)

	w.WriteHeader(http.StatusNoContent)
}

// ListAgents handles GET /api/v1/agents (admin).
func (h *agentsHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
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

	agents, nextCursor, err := h.store.List(r.Context(), params)
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

// GetSelfAgent handles GET /api/v1/agents/me (agent-authed).
// Returns the agent from the auth context.
func (h *agentsHandler) GetSelfAgent(w http.ResponseWriter, r *http.Request) {
	authAgent := auth.AgentFromContext(r.Context())
	if authAgent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no authenticated agent")
		return
	}

	// Fetch the full agent record from the store.
	ag, err := h.store.GetByID(r.Context(), authAgent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get agent")
		return
	}

	writeJSON(w, http.StatusOK, ag)
}

// RegenerateKey handles POST /api/v1/admin/agents/{id}/regenerate-key (admin).
func (h *agentsHandler) RegenerateKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "agent id is required")
		return
	}

	apiKey, plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to generate api key")
		return
	}

	ag, err := h.store.RegenerateKey(r.Context(), id, apiKey.Hash, apiKey.Prefix)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to regenerate key")
		return
	}

	auditLog(r, "regenerate_key", "agent", id)

	resp := map[string]interface{}{
		"id":             ag.ID,
		"name":           ag.Name,
		"api_key_prefix": ag.APIKeyPrefix,
		"api_key":        plaintext,
		"team":           ag.Team,
		"rate_limit":     ag.RateLimit,
		"created_at":     ag.CreatedAt,
	}
	writeJSON(w, http.StatusOK, resp)
}

// SetBudget handles PUT /api/v1/agents/{agentID}/budgets/{toolID} (admin).
func (h *agentsHandler) SetBudget(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	toolID := chi.URLParam(r, "toolID")
	if agentID == "" || toolID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id and tool_id are required")
		return
	}

	var input struct {
		DailyLimit   float64 `json:"daily_limit"`
		MonthlyLimit float64 `json:"monthly_limit"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	budget, err := h.budgetStore.Set(r.Context(), agent.CreateBudgetInput{
		AgentID:      agentID,
		ToolID:       toolID,
		DailyLimit:   input.DailyLimit,
		MonthlyLimit: input.MonthlyLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to set budget")
		return
	}

	auditLog(r, "set_budget", "agent_budget", agentID, "tool_id", toolID)

	writeJSON(w, http.StatusOK, budget)
}

// GetBudget handles GET /api/v1/agents/{agentID}/budgets/{toolID} (admin).
func (h *agentsHandler) GetBudget(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	toolID := chi.URLParam(r, "toolID")
	if agentID == "" || toolID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id and tool_id are required")
		return
	}

	budget, err := h.budgetStore.Get(r.Context(), agentID, toolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "budget not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get budget")
		return
	}

	writeJSON(w, http.StatusOK, budget)
}

// ListBudgets handles GET /api/v1/agents/{agentID}/budgets (admin).
func (h *agentsHandler) ListBudgets(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "invalid_params", "agent_id is required")
		return
	}

	budgets, err := h.budgetStore.ListByAgent(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list budgets")
		return
	}

	if budgets == nil {
		budgets = []*agent.Budget{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"budgets": budgets,
	})
}
