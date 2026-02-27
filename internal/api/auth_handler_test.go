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

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/user"
)

// ---------------------------------------------------------------------------
// Fake authUserStore
// ---------------------------------------------------------------------------

type fakeAuthUserStore struct {
	users    map[string]*user.User // keyed by email
	sessions map[string]string     // token -> userID
	nextTok  string
}

func newFakeAuthUserStore() *fakeAuthUserStore {
	return &fakeAuthUserStore{
		users:    make(map[string]*user.User),
		sessions: make(map[string]string),
		nextTok:  "fake-session-token",
	}
}

func (f *fakeAuthUserStore) addUser(email, password, name, role string) *user.User {
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	u := &user.User{
		ID:           fmt.Sprintf("user-%s", email),
		Email:        email,
		PasswordHash: string(hash),
		Name:         name,
		Teams:        []user.TeamMembership{{Team: "eng", Role: "member"}},
		Role:         role,
		CreatedAt:    time.Now(),
	}
	f.users[email] = u
	return u
}

func (f *fakeAuthUserStore) GetByEmail(_ context.Context, _, email string) (*user.User, error) {
	u, ok := f.users[email]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return u, nil
}

func (f *fakeAuthUserStore) CreateSession(_ context.Context, userID string) (string, *user.Session, error) {
	tok := f.nextTok
	f.sessions[tok] = userID
	sess := &user.Session{
		UserID:    userID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}
	return tok, sess, nil
}

func (f *fakeAuthUserStore) DeleteSession(_ context.Context, token string) error {
	delete(f.sessions, token)
	return nil
}

// ---------------------------------------------------------------------------
// Helper to build a chi router wired to the authHandler
// ---------------------------------------------------------------------------

func authTestRouter(store authUserStore) http.Handler {
	h := newAuthHandler(store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.ContextWithTenant(req.Context(), &auth.Tenant{ID: "t1"})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/api/v1/auth/login", h.Login)
	r.Get("/api/v1/auth/me", h.Me)
	r.Post("/api/v1/auth/logout", h.Logout)
	return r
}

// ---------------------------------------------------------------------------
// Login tests
// ---------------------------------------------------------------------------

func TestLogin_Success(t *testing.T) {
	store := newFakeAuthUserStore()
	store.addUser("alice@example.com", "s3cret", "Alice", "org_admin")
	router := authTestRouter(store)

	body := `{"email":"alice@example.com","password":"s3cret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["token"] != "fake-session-token" {
		t.Errorf("expected token=fake-session-token, got %v", resp["token"])
	}

	u, ok := resp["user"].(map[string]interface{})
	if !ok {
		t.Fatal("expected user object in response")
	}
	if u["email"] != "alice@example.com" {
		t.Errorf("expected email=alice@example.com, got %v", u["email"])
	}
	if u["name"] != "Alice" {
		t.Errorf("expected name=Alice, got %v", u["name"])
	}
	if u["role"] != "org_admin" {
		t.Errorf("expected role=org_admin, got %v", u["role"])
	}
}

func TestLogin_MissingEmail(t *testing.T) {
	store := newFakeAuthUserStore()
	router := authTestRouter(store)

	body := `{"password":"s3cret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}

	var resp errorEnvelope
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error.Code != "validation_error" {
		t.Errorf("expected code=validation_error, got %q", resp.Error.Code)
	}
}

func TestLogin_MissingPassword(t *testing.T) {
	store := newFakeAuthUserStore()
	router := authTestRouter(store)

	body := `{"email":"alice@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestLogin_WrongEmail(t *testing.T) {
	store := newFakeAuthUserStore()
	store.addUser("alice@example.com", "s3cret", "Alice", "member")
	router := authTestRouter(store)

	body := `{"email":"nobody@example.com","password":"s3cret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	var resp errorEnvelope
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error.Code != "unauthorized" {
		t.Errorf("expected code=unauthorized, got %q", resp.Error.Code)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	store := newFakeAuthUserStore()
	store.addUser("alice@example.com", "s3cret", "Alice", "member")
	router := authTestRouter(store)

	body := `{"email":"alice@example.com","password":"wrong"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	var resp errorEnvelope
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error.Code != "unauthorized" {
		t.Errorf("expected code=unauthorized, got %q", resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// Me tests
// ---------------------------------------------------------------------------

func TestMe_Success(t *testing.T) {
	store := newFakeAuthUserStore()
	router := authTestRouter(store)

	// Inject user into context via middleware simulation.
	u := &auth.User{
		ID:    "user-1",
		Email: "alice@example.com",
		Name:  "Alice",
		Teams: []auth.TeamMembership{{Team: "eng", Role: "admin"}},
		Role:  "org_admin",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["id"] != "user-1" {
		t.Errorf("expected id=user-1, got %v", resp["id"])
	}
	if resp["email"] != "alice@example.com" {
		t.Errorf("expected email=alice@example.com, got %v", resp["email"])
	}
	if resp["name"] != "Alice" {
		t.Errorf("expected name=Alice, got %v", resp["name"])
	}
	if resp["role"] != "org_admin" {
		t.Errorf("expected role=org_admin, got %v", resp["role"])
	}
}

func TestMe_Unauthenticated(t *testing.T) {
	store := newFakeAuthUserStore()
	router := authTestRouter(store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	var resp errorEnvelope
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error.Code != "unauthorized" {
		t.Errorf("expected code=unauthorized, got %q", resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// Logout tests
// ---------------------------------------------------------------------------

func TestLogout_Success(t *testing.T) {
	store := newFakeAuthUserStore()
	store.sessions["my-token"] = "user-1"
	router := authTestRouter(store)

	u := &auth.User{ID: "user-1", Email: "alice@example.com", Name: "Alice", Role: "member"}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer my-token")
	req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify session was deleted from store.
	if _, exists := store.sessions["my-token"]; exists {
		t.Error("expected session to be deleted from store")
	}
}

func TestLogout_NoToken(t *testing.T) {
	store := newFakeAuthUserStore()
	router := authTestRouter(store)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}
