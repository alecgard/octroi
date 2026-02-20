package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/user"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// usersHandler groups user management HTTP handlers (admin only).
type usersHandler struct {
	store *user.Store
}

func newUsersHandler(store *user.Store) *usersHandler {
	return &usersHandler{store: store}
}

// checkLastTeamAdmin verifies that removing admin memberships from a user
// would not leave any team without an admin. It compares the user's current
// teams to newTeams and checks affected teams. Returns the team name that
// would be left without an admin, or "" if safe.
func checkLastTeamAdmin(ctx context.Context, store *user.Store, userID string, current, proposed []user.TeamMembership) (string, error) {
	// Find teams where this user is currently admin but either removed or demoted.
	type change struct{ team string }
	var affected []change
	for _, old := range current {
		if old.Role != "admin" {
			continue
		}
		stillAdmin := false
		for _, p := range proposed {
			if p.Team == old.Team && p.Role == "admin" {
				stillAdmin = true
				break
			}
		}
		if !stillAdmin {
			affected = append(affected, change{team: old.Team})
		}
	}

	if len(affected) == 0 {
		return "", nil
	}

	allUsers, err := store.List(ctx)
	if err != nil {
		return "", err
	}

	for _, c := range affected {
		adminCount := 0
		for _, u := range allUsers {
			if u.ID == userID {
				continue // skip the user being modified
			}
			for _, tm := range u.Teams {
				if tm.Team == c.team && tm.Role == "admin" {
					adminCount++
				}
			}
		}
		if adminCount == 0 {
			return c.team, nil
		}
	}
	return "", nil
}

// CreateUser handles POST /api/v1/admin/users.
func (h *usersHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req user.CreateUserInput
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	if req.Email == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "email is required")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "password is required")
		return
	}
	if req.Role != "" && req.Role != "org_admin" && req.Role != "member" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "role must be org_admin or member")
		return
	}

	u, err := h.store.Create(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create user")
		return
	}

	writeJSON(w, http.StatusCreated, u)
}

// ListUsers handles GET /api/v1/admin/users.
func (h *usersHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list users")
		return
	}

	if users == nil {
		users = []*user.User{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users": users,
	})
}

// UpdateUser handles PUT /api/v1/admin/users/{id}.
func (h *usersHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "user id is required")
		return
	}

	var input user.UpdateUserInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	if input.Role != nil && *input.Role != "org_admin" && *input.Role != "member" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "role must be org_admin or member")
		return
	}

	// If teams are being changed, enforce last-admin constraint.
	if input.Teams != nil {
		existing, err := h.store.GetByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "user not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to get user")
			return
		}

		violating, err := checkLastTeamAdmin(r.Context(), h.store, id, existing.Teams, *input.Teams)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to check team constraints")
			return
		}
		if violating != "" {
			writeError(w, http.StatusConflict, "constraint_error", "cannot remove the last admin from team "+violating)
			return
		}
	}

	u, err := h.store.Update(r.Context(), id, input)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update user")
		return
	}

	writeJSON(w, http.StatusOK, u)
}

// MemberListUsers handles GET /api/v1/member/users — read-only user list for any member.
func (h *usersHandler) MemberListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list users")
		return
	}

	if users == nil {
		users = []*user.User{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users": users,
	})
}

// UpdateSelf handles PUT /api/v1/member/users/me — update own profile.
func (h *usersHandler) UpdateSelf(w http.ResponseWriter, r *http.Request) {
	caller := auth.UserFromContext(r.Context())
	if caller == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	var req struct {
		Name     *string `json:"name"`
		Password *string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	input := user.UpdateUserInput{}
	if req.Name != nil {
		input.Name = req.Name
	}
	if req.Password != nil {
		input.Password = req.Password
	}

	u, err := h.store.Update(r.Context(), caller.ID, input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update user")
		return
	}

	writeJSON(w, http.StatusOK, u)
}

// DeleteUser handles DELETE /api/v1/admin/users/{id}.
func (h *usersHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "user id is required")
		return
	}

	// Check if deleting this user would leave a team without an admin.
	existing, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get user")
		return
	}

	// Deleting = removing all team memberships.
	violating, err := checkLastTeamAdmin(r.Context(), h.store, id, existing.Teams, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check team constraints")
		return
	}
	if violating != "" {
		writeError(w, http.StatusConflict, "constraint_error", "cannot delete user: last admin of team "+violating)
		return
	}

	err = h.store.Delete(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete user")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
