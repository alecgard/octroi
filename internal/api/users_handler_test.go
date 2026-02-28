package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/user"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// Fake user store
// ---------------------------------------------------------------------------

type fakeUserStore struct {
	users  map[string]*user.User
	nextID int
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{users: make(map[string]*user.User)}
}

func (f *fakeUserStore) Create(_ context.Context, in user.CreateUserInput) (*user.User, error) {
	f.nextID++
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.MinCost)
	if err != nil {
		return nil, err
	}
	role := in.Role
	if role == "" {
		role = "member"
	}
	teams := in.Teams
	if teams == nil {
		teams = []user.TeamMembership{}
	}
	u := &user.User{
		ID:           fmt.Sprintf("user-%d", f.nextID),
		Email:        in.Email,
		PasswordHash: string(hash),
		Name:         in.Name,
		Teams:        teams,
		Role:         role,
		CreatedAt:    time.Now().UTC(),
	}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeUserStore) List(_ context.Context, _ string) ([]*user.User, error) {
	var out []*user.User
	for _, u := range f.users {
		if u.ArchivedAt == nil {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeUserStore) GetByID(_ context.Context, _, id string) (*user.User, error) {
	u, ok := f.users[id]
	if !ok || u.ArchivedAt != nil {
		return nil, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeUserStore) Update(_ context.Context, _, id string, in user.UpdateUserInput) (*user.User, error) {
	u, ok := f.users[id]
	if !ok || u.ArchivedAt != nil {
		return nil, pgx.ErrNoRows
	}
	if in.Email != nil {
		u.Email = *in.Email
	}
	if in.Name != nil {
		u.Name = *in.Name
	}
	if in.Password != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.MinCost)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = string(hash)
	}
	if in.Teams != nil {
		u.Teams = *in.Teams
	}
	if in.Role != nil {
		u.Role = *in.Role
	}
	return u, nil
}

func (f *fakeUserStore) Archive(_ context.Context, _, id string) error {
	u, ok := f.users[id]
	if !ok || u.ArchivedAt != nil {
		return fmt.Errorf("user not found")
	}
	now := time.Now().UTC()
	u.ArchivedAt = &now
	return nil
}

// seed adds a user directly into the fake store (useful for pre-populating).
func (f *fakeUserStore) seed(u *user.User) {
	f.users[u.ID] = u
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func usersRouter(h *usersHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/api/v1/admin/users", h.CreateUser)
	r.Get("/api/v1/admin/users", h.ListUsers)
	r.Put("/api/v1/admin/users/{id}", h.UpdateUser)
	r.Put("/api/v1/member/users/me", h.UpdateSelf)
	r.Put("/api/v1/member/users/me/password", h.ChangePassword)
	r.Delete("/api/v1/admin/users/{id}", h.DeleteUser)
	return r
}

func adminCtx() context.Context {
	ctx := auth.ContextWithTenant(context.Background(), &auth.Tenant{ID: "t1"})
	return auth.ContextWithUser(ctx, &auth.User{
		ID:    "admin-1",
		Email: "admin@test.com",
		Role:  "admin",
		Teams: []auth.TeamMembership{{Team: "team-a", Role: "admin"}},
	})
}

func memberCtx(id string) context.Context {
	ctx := auth.ContextWithTenant(context.Background(), &auth.Tenant{ID: "t1"})
	return auth.ContextWithUser(ctx, &auth.User{
		ID:    id,
		Email: "member@test.com",
		Role:  "member",
		Teams: []auth.TeamMembership{{Team: "team-a", Role: "member"}},
	})
}

func hashPassword(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	return string(h)
}

func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("failed to decode error body: %v", err)
	}
	return env
}

// ---------------------------------------------------------------------------
// CreateUser tests
// ---------------------------------------------------------------------------

func TestCreateUser_Success(t *testing.T) {
	store := newFakeUserStore()
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"email":"new@example.com","password":"secret123","name":"New User","role":"member"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var u user.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if u.Email != "new@example.com" {
		t.Errorf("expected email new@example.com, got %q", u.Email)
	}
	if u.Name != "New User" {
		t.Errorf("expected name 'New User', got %q", u.Name)
	}
	if u.Role != "member" {
		t.Errorf("expected role member, got %q", u.Role)
	}
}

func TestCreateUser_MissingEmail(t *testing.T) {
	store := newFakeUserStore()
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"password":"secret123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	env := decodeErrorBody(t, rec)
	if env.Error.Code != "validation_error" {
		t.Errorf("expected code validation_error, got %q", env.Error.Code)
	}
}

func TestCreateUser_MissingPassword(t *testing.T) {
	store := newFakeUserStore()
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"email":"test@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	env := decodeErrorBody(t, rec)
	if env.Error.Code != "validation_error" {
		t.Errorf("expected code validation_error, got %q", env.Error.Code)
	}
}

func TestCreateUser_InvalidRole(t *testing.T) {
	store := newFakeUserStore()
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"email":"test@example.com","password":"secret123","role":"superadmin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	env := decodeErrorBody(t, rec)
	if env.Error.Code != "validation_error" {
		t.Errorf("expected code validation_error, got %q", env.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// ListUsers tests
// ---------------------------------------------------------------------------

func TestListUsers_Empty(t *testing.T) {
	store := newFakeUserStore()
	h := &usersHandler{store: store}
	router := usersRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	var users []user.User
	if err := json.Unmarshal(body["users"], &users); err != nil {
		t.Fatalf("failed to unmarshal users: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}
}

func TestListUsers_WithUsers(t *testing.T) {
	store := newFakeUserStore()
	store.seed(&user.User{
		ID:    "u1",
		Email: "alice@example.com",
		Name:  "Alice",
		Role:  "admin",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "admin"}},
	})
	store.seed(&user.User{
		ID:    "u2",
		Email: "bob@example.com",
		Name:  "Bob",
		Role:  "member",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "member"}},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	var users []user.User
	if err := json.Unmarshal(body["users"], &users); err != nil {
		t.Fatalf("failed to unmarshal users: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

// ---------------------------------------------------------------------------
// UpdateUser tests
// ---------------------------------------------------------------------------

func TestUpdateUser_Success(t *testing.T) {
	store := newFakeUserStore()
	store.seed(&user.User{
		ID:    "u1",
		Email: "old@example.com",
		Name:  "Old Name",
		Role:  "member",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "member"}},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	newName := "New Name"
	body := fmt.Sprintf(`{"name":%q}`, newName)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/u1", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var u user.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if u.Name != newName {
		t.Errorf("expected name %q, got %q", newName, u.Name)
	}
}

func TestUpdateUser_NotFound(t *testing.T) {
	store := newFakeUserStore()
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"name":"whatever"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/nonexistent", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestUpdateUser_InvalidRole(t *testing.T) {
	store := newFakeUserStore()
	store.seed(&user.User{
		ID:    "u1",
		Email: "test@example.com",
		Role:  "member",
		Teams: []user.TeamMembership{},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"role":"superadmin"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/u1", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	env := decodeErrorBody(t, rec)
	if env.Error.Code != "validation_error" {
		t.Errorf("expected code validation_error, got %q", env.Error.Code)
	}
}

func TestUpdateUser_LastAdminConstraint(t *testing.T) {
	store := newFakeUserStore()
	// u1 is the only admin of team-a.
	store.seed(&user.User{
		ID:    "u1",
		Email: "admin@example.com",
		Role:  "admin",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "admin"}},
	})
	// u2 is a member of team-a (not admin).
	store.seed(&user.User{
		ID:    "u2",
		Email: "member@example.com",
		Role:  "member",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "member"}},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	// Try to demote u1 from admin to member on team-a.
	body := `{"teams":[{"team":"team-a","role":"member"}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/u1", strings.NewReader(body))
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	env := decodeErrorBody(t, rec)
	if env.Error.Code != "constraint_error" {
		t.Errorf("expected code constraint_error, got %q", env.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// UpdateSelf tests
// ---------------------------------------------------------------------------

func TestUpdateSelf_Success(t *testing.T) {
	store := newFakeUserStore()
	store.seed(&user.User{
		ID:    "user-1",
		Email: "me@example.com",
		Name:  "Old Name",
		Role:  "member",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "member"}},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"name":"New Name"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/member/users/me", strings.NewReader(body))
	req = req.WithContext(memberCtx("user-1"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var u user.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if u.Name != "New Name" {
		t.Errorf("expected name 'New Name', got %q", u.Name)
	}
}

func TestUpdateSelf_Unauthenticated(t *testing.T) {
	store := newFakeUserStore()
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"name":"Hacker"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/member/users/me", strings.NewReader(body))
	// No auth context set.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// ChangePassword tests
// ---------------------------------------------------------------------------

func TestChangePassword_Success(t *testing.T) {
	store := newFakeUserStore()
	store.seed(&user.User{
		ID:           "user-1",
		Email:        "me@example.com",
		PasswordHash: hashPassword(t, "oldpassword"),
		Role:         "member",
		Teams:        []user.TeamMembership{},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"current_password":"oldpassword","new_password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/member/users/me/password", strings.NewReader(body))
	req = req.WithContext(memberCtx("user-1"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the password was actually changed.
	u := store.users["user-1"]
	if !user.CheckPassword(u, "newpassword123") {
		t.Error("new password should be valid after change")
	}
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	store := newFakeUserStore()
	store.seed(&user.User{
		ID:           "user-1",
		Email:        "me@example.com",
		PasswordHash: hashPassword(t, "correctpassword"),
		Role:         "member",
		Teams:        []user.TeamMembership{},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"current_password":"wrongpassword","new_password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/member/users/me/password", strings.NewReader(body))
	req = req.WithContext(memberCtx("user-1"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	env := decodeErrorBody(t, rec)
	if env.Error.Code != "unauthorized" {
		t.Errorf("expected code unauthorized, got %q", env.Error.Code)
	}
}

func TestChangePassword_ShortNewPassword(t *testing.T) {
	store := newFakeUserStore()
	store.seed(&user.User{
		ID:           "user-1",
		Email:        "me@example.com",
		PasswordHash: hashPassword(t, "oldpassword"),
		Role:         "member",
		Teams:        []user.TeamMembership{},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	body := `{"current_password":"oldpassword","new_password":"short"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/member/users/me/password", strings.NewReader(body))
	req = req.WithContext(memberCtx("user-1"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	env := decodeErrorBody(t, rec)
	if env.Error.Code != "validation_error" {
		t.Errorf("expected code validation_error, got %q", env.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// DeleteUser tests
// ---------------------------------------------------------------------------

func TestDeleteUser_Success(t *testing.T) {
	store := newFakeUserStore()
	// Two admins on team-a so deleting one is safe.
	store.seed(&user.User{
		ID:    "u1",
		Email: "target@example.com",
		Role:  "admin",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "admin"}},
	})
	store.seed(&user.User{
		ID:    "u2",
		Email: "other-admin@example.com",
		Role:  "admin",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "admin"}},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/u1", nil)
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the user was archived.
	if store.users["u1"].ArchivedAt == nil {
		t.Error("expected user to be archived")
	}
}

func TestDeleteUser_NotFound(t *testing.T) {
	store := newFakeUserStore()
	h := &usersHandler{store: store}
	router := usersRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/nonexistent", nil)
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestDeleteUser_LastAdminConstraint(t *testing.T) {
	store := newFakeUserStore()
	// u1 is the sole admin of team-a.
	store.seed(&user.User{
		ID:    "u1",
		Email: "sole-admin@example.com",
		Role:  "admin",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "admin"}},
	})
	store.seed(&user.User{
		ID:    "u2",
		Email: "member@example.com",
		Role:  "member",
		Teams: []user.TeamMembership{{Team: "team-a", Role: "member"}},
	})
	h := &usersHandler{store: store}
	router := usersRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/u1", nil)
	req = req.WithContext(adminCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	env := decodeErrorBody(t, rec)
	if env.Error.Code != "constraint_error" {
		t.Errorf("expected code constraint_error, got %q", env.Error.Code)
	}
}
