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

// userStorer is the subset of *user.Store used by usersHandler.
type userStorer interface {
	Create(ctx context.Context, in user.CreateUserInput) (*user.User, error)
	List(ctx context.Context, tenantID string) ([]*user.User, error)
	GetByID(ctx context.Context, tenantID, id string) (*user.User, error)
	Update(ctx context.Context, tenantID, id string, in user.UpdateUserInput) (*user.User, error)
	Archive(ctx context.Context, tenantID, id string) error
}

// usersHandler groups user management HTTP handlers (admin only).
type usersHandler struct {
	store userStorer
}

func newUsersHandler(store *user.Store) *usersHandler {
	return &usersHandler{store: store}
}

// checkLastTeamAdmin verifies that removing admin memberships from a user
// would not leave any team without an admin. It compares the user's current
// teams to newTeams and checks affected teams. Returns the team name that
// would be left without an admin, or "" if safe.
func checkLastTeamAdmin(ctx context.Context, store userStorer, tenantID, userID string, current, proposed []user.TeamMembership) (string, error) {
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

	allUsers, err := store.List(ctx, tenantID)
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

	tenant := mustTenant(w, r)
	if tenant == nil {
		return
	}
	req.TenantID = tenant.ID

	u, err := h.store.Create(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create user")
		return
	}

	auditLog(r, "create", "user", u.ID, "email", u.Email)

	writeJSON(w, http.StatusCreated, u)
}

// ListUsers handles GET /api/v1/admin/users.
func (h *usersHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	tenant := mustTenant(w, r)
	if tenant == nil {
		return
	}
	users, err := h.store.List(r.Context(), tenant.ID)
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

	tenant := mustTenant(w, r)
	if tenant == nil {
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
		existing, err := h.store.GetByID(r.Context(), tenant.ID, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "user not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to get user")
			return
		}

		violating, err := checkLastTeamAdmin(r.Context(), h.store, tenant.ID, id, existing.Teams, *input.Teams)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to check team constraints")
			return
		}
		if violating != "" {
			writeError(w, http.StatusConflict, "constraint_error", "cannot remove the last admin from team "+violating)
			return
		}
	}

	u, err := h.store.Update(r.Context(), tenant.ID, id, input)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update user")
		return
	}

	auditLog(r, "update", "user", id, "email", u.Email)

	writeJSON(w, http.StatusOK, u)
}

// UpdateSelf handles PUT /api/v1/member/users/me — update own profile (name only).
func (h *usersHandler) UpdateSelf(w http.ResponseWriter, r *http.Request) {
	caller := auth.UserFromContext(r.Context())
	if caller == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	var req struct {
		Name *string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	input := user.UpdateUserInput{}
	if req.Name != nil {
		input.Name = req.Name
	}

	u, err := h.store.Update(r.Context(), caller.TenantID, caller.ID, input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update user")
		return
	}

	writeJSON(w, http.StatusOK, u)
}

// ChangePassword handles PUT /api/v1/member/users/me/password.
func (h *usersHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	caller := auth.UserFromContext(r.Context())
	if caller == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "current_password and new_password are required")
		return
	}

	if len(req.NewPassword) < 6 {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "new password must be at least 6 characters")
		return
	}

	// Fetch user to verify current password.
	u, err := h.store.GetByID(r.Context(), caller.TenantID, caller.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get user")
		return
	}

	if !user.CheckPassword(u, req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "current password is incorrect")
		return
	}

	input := user.UpdateUserInput{Password: &req.NewPassword}
	if _, err := h.store.Update(r.Context(), caller.TenantID, caller.ID, input); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update password")
		return
	}

	auditLog(r, "change_password", "user", caller.ID)

	writeJSON(w, http.StatusOK, map[string]string{"status": "password updated"})
}

// DeleteUser handles DELETE /api/v1/admin/users/{id}.
func (h *usersHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "user id is required")
		return
	}

	tenant := mustTenant(w, r)
	if tenant == nil {
		return
	}

	// Check if deleting this user would leave a team without an admin.
	existing, err := h.store.GetByID(r.Context(), tenant.ID, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get user")
		return
	}

	// Deleting = removing all team memberships.
	violating, err := checkLastTeamAdmin(r.Context(), h.store, tenant.ID, id, existing.Teams, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check team constraints")
		return
	}
	if violating != "" {
		writeError(w, http.StatusConflict, "constraint_error", "cannot delete user: last admin of team "+violating)
		return
	}

	err = h.store.Archive(r.Context(), tenant.ID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete user")
		return
	}

	auditLog(r, "delete", "user", id, "email", existing.Email)

	w.WriteHeader(http.StatusNoContent)
}
