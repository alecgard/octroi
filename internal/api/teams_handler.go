package api

import (
	"net/http"
	"sort"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/user"
	"github.com/go-chi/chi/v5"
)

// teamsHandler groups team-related HTTP handlers.
type teamsHandler struct {
	agentStore *agent.Store
	userStore  *user.Store
}

func newTeamsHandler(agentStore *agent.Store, userStore *user.Store) *teamsHandler {
	return &teamsHandler{
		agentStore: agentStore,
		userStore:  userStore,
	}
}

type teamInfo struct {
	Name   string       `json:"name"`
	Agents []agentBrief `json:"agents"`
	Users  []userBrief  `json:"users"`
}

type agentBrief struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	APIKeyPrefix string `json:"api_key_prefix"`
}

type userBrief struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	TeamRole string `json:"team_role"`
}

// AdminListTeams handles GET /api/v1/admin/teams — all teams.
func (h *teamsHandler) AdminListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.buildTeams(r, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list teams")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"teams": teams})
}

// MemberListTeams handles GET /api/v1/member/teams — user's teams only.
func (h *teamsHandler) MemberListTeams(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	teams, err := h.buildTeams(r, u.TeamNames())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list teams")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"teams": teams})
}

// buildTeams fetches all agents and users and groups them by team.
// If filterTeams is non-nil, only those teams are included.
func (h *teamsHandler) buildTeams(r *http.Request, filterTeams []string) ([]teamInfo, error) {
	ctx := r.Context()

	// Fetch all agents (up to a reasonable limit).
	agents, _, err := h.agentStore.List(ctx, agent.AgentListParams{Limit: 1000})
	if err != nil {
		return nil, err
	}

	// Fetch all users.
	users, err := h.userStore.List(ctx)
	if err != nil {
		return nil, err
	}

	// Collect distinct team names.
	teamSet := map[string]bool{}
	for _, a := range agents {
		if a.Team != "" {
			teamSet[a.Team] = true
		}
	}
	for _, u := range users {
		for _, tm := range u.Teams {
			teamSet[tm.Team] = true
		}
	}

	// Build sorted team list.
	filterSet := map[string]bool{}
	if filterTeams != nil {
		for _, t := range filterTeams {
			filterSet[t] = true
		}
	}

	var teamNames []string
	for t := range teamSet {
		if filterTeams != nil && !filterSet[t] {
			continue
		}
		teamNames = append(teamNames, t)
	}
	sort.Strings(teamNames)

	// Group agents and users by team.
	result := make([]teamInfo, 0, len(teamNames))
	for _, name := range teamNames {
		info := teamInfo{Name: name}
		for _, a := range agents {
			if a.Team == name {
				info.Agents = append(info.Agents, agentBrief{
					ID:           a.ID,
					Name:         a.Name,
					APIKeyPrefix: a.APIKeyPrefix,
				})
			}
		}
		for _, u := range users {
			for _, tm := range u.Teams {
				if tm.Team == name {
					info.Users = append(info.Users, userBrief{
						ID:       u.ID,
						Email:    u.Email,
						Name:     u.Name,
						Role:     u.Role,
						TeamRole: tm.Role,
					})
					break
				}
			}
		}
		if info.Agents == nil {
			info.Agents = []agentBrief{}
		}
		if info.Users == nil {
			info.Users = []userBrief{}
		}
		result = append(result, info)
	}

	return result, nil
}

// AddTeamMember handles PUT /api/v1/member/teams/{team}/members/{userId}.
func (h *teamsHandler) AddTeamMember(w http.ResponseWriter, r *http.Request) {
	caller := auth.UserFromContext(r.Context())
	if caller == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	team := chi.URLParam(r, "team")
	userID := chi.URLParam(r, "userId")

	if !caller.CanManageTeam(team) {
		writeError(w, http.StatusForbidden, "forbidden", "you cannot manage team "+team)
		return
	}

	var req struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if req.Role != "admin" && req.Role != "member" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "team role must be admin or member")
		return
	}

	// Load target user.
	target, err := h.userStore.GetByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}

	// Org admins already have access to every team.
	if target.Role == "org_admin" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "org admins already have access to all teams")
		return
	}

	// Reject if user is already in the team.
	for _, tm := range target.Teams {
		if tm.Team == team {
			writeError(w, http.StatusConflict, "conflict", "user is already a member of team "+team)
			return
		}
	}

	newTeams := make([]user.TeamMembership, len(target.Teams), len(target.Teams)+1)
	copy(newTeams, target.Teams)
	newTeams = append(newTeams, user.TeamMembership{Team: team, Role: req.Role})

	updated, err := h.userStore.Update(r.Context(), userID, user.UpdateUserInput{
		Teams: &newTeams,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update user teams")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// RemoveTeamMember handles DELETE /api/v1/member/teams/{team}/members/{userId}.
func (h *teamsHandler) RemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	caller := auth.UserFromContext(r.Context())
	if caller == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	team := chi.URLParam(r, "team")
	userID := chi.URLParam(r, "userId")

	if !caller.CanManageTeam(team) {
		writeError(w, http.StatusForbidden, "forbidden", "you cannot manage team "+team)
		return
	}

	// Load target user.
	target, err := h.userStore.GetByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}

	// Check if user is in the team.
	var removingRole string
	inTeam := false
	for _, tm := range target.Teams {
		if tm.Team == team {
			inTeam = true
			removingRole = tm.Role
			break
		}
	}
	if !inTeam {
		writeError(w, http.StatusNotFound, "not_found", "user is not in team "+team)
		return
	}

	// Enforce at least one team admin constraint.
	if removingRole == "admin" {
		// Count admins for this team across all users.
		allUsers, err := h.userStore.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to list users")
			return
		}
		adminCount := 0
		for _, u := range allUsers {
			for _, tm := range u.Teams {
				if tm.Team == team && tm.Role == "admin" {
					adminCount++
				}
			}
		}
		if adminCount <= 1 {
			writeError(w, http.StatusConflict, "constraint_error", "cannot remove the last team admin")
			return
		}
	}

	// Remove team from user's teams.
	newTeams := make([]user.TeamMembership, 0, len(target.Teams))
	for _, tm := range target.Teams {
		if tm.Team != team {
			newTeams = append(newTeams, tm)
		}
	}

	updated, err := h.userStore.Update(r.Context(), userID, user.UpdateUserInput{
		Teams: &newTeams,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update user teams")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}
