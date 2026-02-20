package api

import (
	"net/http"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/user"
)

// authHandler groups authentication HTTP handlers.
type authHandler struct {
	store *user.Store
}

func newAuthHandler(store *user.Store) *authHandler {
	return &authHandler{store: store}
}

// Login handles POST /api/v1/auth/login.
func (h *authHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body")
		return
	}

	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "email and password are required")
		return
	}

	u, err := h.store.GetByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}

	if !user.CheckPassword(u, req.Password) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}

	token, _, err := h.store.CreateSession(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user": map[string]interface{}{
			"id":    u.ID,
			"email": u.Email,
			"name":  u.Name,
			"teams": u.Teams,
			"role":  u.Role,
		},
	})
}

// Me handles GET /api/v1/auth/me.
func (h *authHandler) Me(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":    u.ID,
		"email": u.Email,
		"name":  u.Name,
		"teams": u.Teams,
		"role":  u.Role,
	})
}

// Logout handles POST /api/v1/auth/logout.
func (h *authHandler) Logout(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	_ = h.store.DeleteSession(r.Context(), token)
	w.WriteHeader(http.StatusNoContent)
}

// extractBearerToken extracts the bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	return ""
}
