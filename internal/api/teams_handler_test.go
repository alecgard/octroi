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

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/user"
	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// Fake stores for teams handler tests
// ---------------------------------------------------------------------------

type fakeTeamsAgentStore struct {
	agents []*agent.Agent
	err    error
}

func (f *fakeTeamsAgentStore) List(_ context.Context, _ agent.AgentListParams) ([]*agent.Agent, string, error) {
	return f.agents, "", f.err
}

type fakeTeamsUserStore struct {
	users   map[string]*user.User
	listErr error
}

func newFakeTeamsUserStore() *fakeTeamsUserStore {
	return &fakeTeamsUserStore{users: make(map[string]*user.User)}
}

func (f *fakeTeamsUserStore) List(_ context.Context) ([]*user.User, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var list []*user.User
	for _, u := range f.users {
		list = append(list, u)
	}
	return list, nil
}

func (f *fakeTeamsUserStore) GetByID(_ context.Context, id string) (*user.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, fmt.Errorf("user not found")
	}
	return u, nil
}

func (f *fakeTeamsUserStore) Update(_ context.Context, id string, in user.UpdateUserInput) (*user.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, fmt.Errorf("user not found")
	}
	if in.Teams != nil {
		u.Teams = *in.Teams
	}
	if in.Email != nil {
		u.Email = *in.Email
	}
	if in.Name != nil {
		u.Name = *in.Name
	}
	if in.Role != nil {
		u.Role = *in.Role
	}
	return u, nil
}

// ---------------------------------------------------------------------------
// Router helpers
// ---------------------------------------------------------------------------

func adminUser() *auth.User {
	return &auth.User{
		ID:    "admin-1",
		Email: "admin@test.com",
		Role:  "org_admin",
		Teams: []auth.TeamMembership{{Team: "team-a", Role: "admin"}},
	}
}

func teamsMemberUser() *auth.User {
	return &auth.User{
		ID:    "user-1",
		Email: "user@test.com",
		Role:  "member",
		Teams: []auth.TeamMembership{{Team: "team-a", Role: "admin"}},
	}
}

func setupTeamsRouter(agentStore *fakeTeamsAgentStore, userStore *fakeTeamsUserStore, caller *auth.User) http.Handler {
	h := newTeamsHandler(agentStore, userStore)
	r := chi.NewRouter()

	if caller != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := auth.ContextWithUser(req.Context(), caller)
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
	}

	r.Get("/admin/teams", h.AdminListTeams)
	r.Get("/member/teams", h.MemberListTeams)
	r.Put("/member/teams/{team}/members/{userId}", h.AddTeamMember)
	r.Delete("/member/teams/{team}/members/{userId}", h.RemoveTeamMember)

	return r
}

// ---------------------------------------------------------------------------
// AdminListTeams tests
// ---------------------------------------------------------------------------

func TestAdminListTeams_ReturnsAllTeams(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{
		agents: []*agent.Agent{
			{ID: "a1", Name: "agent-1", APIKeyPrefix: "octroi_abc", Team: "eng"},
			{ID: "a2", Name: "agent-2", APIKeyPrefix: "octroi_def", Team: "ops"},
		},
	}
	userStore := newFakeTeamsUserStore()
	userStore.users["u1"] = &user.User{
		ID: "u1", Email: "alice@test.com", Name: "Alice", Role: "member",
		Teams:     []user.TeamMembership{{Team: "eng", Role: "admin"}},
		CreatedAt: time.Now().UTC(),
	}
	userStore.users["u2"] = &user.User{
		ID: "u2", Email: "bob@test.com", Name: "Bob", Role: "member",
		Teams:     []user.TeamMembership{{Team: "ops", Role: "member"}},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	req := httptest.NewRequest(http.MethodGet, "/admin/teams", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Teams []teamInfo `json:"teams"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(resp.Teams))
	}

	// Teams are sorted alphabetically.
	if resp.Teams[0].Name != "eng" {
		t.Errorf("expected first team=eng, got %q", resp.Teams[0].Name)
	}
	if resp.Teams[1].Name != "ops" {
		t.Errorf("expected second team=ops, got %q", resp.Teams[1].Name)
	}

	// Verify eng team has 1 agent and 1 user.
	if len(resp.Teams[0].Agents) != 1 {
		t.Errorf("expected 1 agent in eng, got %d", len(resp.Teams[0].Agents))
	}
	if len(resp.Teams[0].Users) != 1 {
		t.Errorf("expected 1 user in eng, got %d", len(resp.Teams[0].Users))
	}

	// Verify ops team has 1 agent and 1 user.
	if len(resp.Teams[1].Agents) != 1 {
		t.Errorf("expected 1 agent in ops, got %d", len(resp.Teams[1].Agents))
	}
	if len(resp.Teams[1].Users) != 1 {
		t.Errorf("expected 1 user in ops, got %d", len(resp.Teams[1].Users))
	}
}

func TestAdminListTeams_Empty(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	req := httptest.NewRequest(http.MethodGet, "/admin/teams", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Teams []teamInfo `json:"teams"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Teams == nil {
		t.Fatal("expected teams to be non-nil (empty array)")
	}
	if len(resp.Teams) != 0 {
		t.Errorf("expected 0 teams, got %d", len(resp.Teams))
	}
}

func TestAdminListTeams_AgentStoreError(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{err: fmt.Errorf("db error")}
	userStore := newFakeTeamsUserStore()

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	req := httptest.NewRequest(http.MethodGet, "/admin/teams", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// MemberListTeams tests
// ---------------------------------------------------------------------------

func TestMemberListTeams_ReturnsOnlyUserTeams(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{
		agents: []*agent.Agent{
			{ID: "a1", Name: "agent-1", APIKeyPrefix: "octroi_abc", Team: "team-a"},
			{ID: "a2", Name: "agent-2", APIKeyPrefix: "octroi_def", Team: "team-b"},
		},
	}
	userStore := newFakeTeamsUserStore()
	userStore.users["user-1"] = &user.User{
		ID: "user-1", Email: "user@test.com", Name: "User", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-a", Role: "admin"}},
		CreatedAt: time.Now().UTC(),
	}

	// The caller is a member of team-a only.
	caller := teamsMemberUser()
	router := setupTeamsRouter(agentStore, userStore, caller)

	req := httptest.NewRequest(http.MethodGet, "/member/teams", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Teams []teamInfo `json:"teams"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should only see team-a, not team-b.
	if len(resp.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(resp.Teams))
	}
	if resp.Teams[0].Name != "team-a" {
		t.Errorf("expected team-a, got %q", resp.Teams[0].Name)
	}
}

func TestMemberListTeams_Unauthenticated(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()

	// No caller in context.
	router := setupTeamsRouter(agentStore, userStore, nil)

	req := httptest.NewRequest(http.MethodGet, "/member/teams", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "unauthorized" {
		t.Errorf("expected code=unauthorized, got %q", envelope.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// AddTeamMember tests
// ---------------------------------------------------------------------------

func TestAddTeamMember_Success(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	body := `{"role":"member"}`
	req := httptest.NewRequest(http.MethodPut, "/member/teams/team-a/members/target-1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp user.User
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Teams) != 1 {
		t.Fatalf("expected 1 team membership, got %d", len(resp.Teams))
	}
	if resp.Teams[0].Team != "team-a" {
		t.Errorf("expected team=team-a, got %q", resp.Teams[0].Team)
	}
	if resp.Teams[0].Role != "member" {
		t.Errorf("expected role=member, got %q", resp.Teams[0].Role)
	}
}

func TestAddTeamMember_DefaultRole(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	// Empty role should default to "member".
	body := `{}`
	req := httptest.NewRequest(http.MethodPut, "/member/teams/team-a/members/target-1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp user.User
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Teams) != 1 || resp.Teams[0].Role != "member" {
		t.Errorf("expected default role=member, got teams=%+v", resp.Teams)
	}
}

func TestAddTeamMember_AlreadyMember(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-a", Role: "member"}},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	body := `{"role":"member"}`
	req := httptest.NewRequest(http.MethodPut, "/member/teams/team-a/members/target-1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "conflict" {
		t.Errorf("expected code=conflict, got %q", envelope.Error.Code)
	}
}

func TestAddTeamMember_InvalidRole(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	body := `{"role":"superadmin"}`
	req := httptest.NewRequest(http.MethodPut, "/member/teams/team-a/members/target-1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "validation_error" {
		t.Errorf("expected code=validation_error, got %q", envelope.Error.Code)
	}
}

func TestAddTeamMember_Forbidden(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{},
		CreatedAt: time.Now().UTC(),
	}

	// Caller is a member of team-a but not admin, and is not org_admin.
	caller := &auth.User{
		ID:    "user-2",
		Email: "user2@test.com",
		Role:  "member",
		Teams: []auth.TeamMembership{{Team: "team-a", Role: "member"}},
	}
	router := setupTeamsRouter(agentStore, userStore, caller)

	body := `{"role":"member"}`
	req := httptest.NewRequest(http.MethodPut, "/member/teams/team-a/members/target-1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "forbidden" {
		t.Errorf("expected code=forbidden, got %q", envelope.Error.Code)
	}
}

func TestAddTeamMember_UserNotFound(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	body := `{"role":"member"}`
	req := httptest.NewRequest(http.MethodPut, "/member/teams/team-a/members/nonexistent", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAddTeamMember_Unauthenticated(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()

	router := setupTeamsRouter(agentStore, userStore, nil)

	body := `{"role":"member"}`
	req := httptest.NewRequest(http.MethodPut, "/member/teams/team-a/members/target-1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// RemoveTeamMember tests
// ---------------------------------------------------------------------------

func TestRemoveTeamMember_Success(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	// Target user is a member (not admin) of team-a.
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-a", Role: "member"}, {Team: "team-b", Role: "admin"}},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	req := httptest.NewRequest(http.MethodDelete, "/member/teams/team-a/members/target-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp user.User
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should only have team-b remaining.
	if len(resp.Teams) != 1 {
		t.Fatalf("expected 1 team membership remaining, got %d", len(resp.Teams))
	}
	if resp.Teams[0].Team != "team-b" {
		t.Errorf("expected remaining team=team-b, got %q", resp.Teams[0].Team)
	}
}

func TestRemoveTeamMember_NotInTeam(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-b", Role: "member"}},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	req := httptest.NewRequest(http.MethodDelete, "/member/teams/team-a/members/target-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "not_found" {
		t.Errorf("expected code=not_found, got %q", envelope.Error.Code)
	}
}

func TestRemoveTeamMember_LastAdmin(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	// target-1 is the only admin of team-a.
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-a", Role: "admin"}},
		CreatedAt: time.Now().UTC(),
	}
	// Another user in team-a but as member, not admin.
	userStore.users["other-1"] = &user.User{
		ID: "other-1", Email: "other@test.com", Name: "Other", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-a", Role: "member"}},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	req := httptest.NewRequest(http.MethodDelete, "/member/teams/team-a/members/target-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "constraint_error" {
		t.Errorf("expected code=constraint_error, got %q", envelope.Error.Code)
	}
}

func TestRemoveTeamMember_AdminWithOtherAdmins(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	// Two admins in team-a, so removing one is allowed.
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-a", Role: "admin"}},
		CreatedAt: time.Now().UTC(),
	}
	userStore.users["other-1"] = &user.User{
		ID: "other-1", Email: "other@test.com", Name: "Other", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-a", Role: "admin"}},
		CreatedAt: time.Now().UTC(),
	}

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	req := httptest.NewRequest(http.MethodDelete, "/member/teams/team-a/members/target-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp user.User
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Teams) != 0 {
		t.Errorf("expected 0 teams after removal, got %d", len(resp.Teams))
	}
}

func TestRemoveTeamMember_Forbidden(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()
	userStore.users["target-1"] = &user.User{
		ID: "target-1", Email: "target@test.com", Name: "Target", Role: "member",
		Teams:     []user.TeamMembership{{Team: "team-a", Role: "member"}},
		CreatedAt: time.Now().UTC(),
	}

	// Caller is a regular member of team-a, cannot manage.
	caller := &auth.User{
		ID:    "user-2",
		Email: "user2@test.com",
		Role:  "member",
		Teams: []auth.TeamMembership{{Team: "team-a", Role: "member"}},
	}
	router := setupTeamsRouter(agentStore, userStore, caller)

	req := httptest.NewRequest(http.MethodDelete, "/member/teams/team-a/members/target-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("failed to decode error: %v", err)
	}
	if envelope.Error.Code != "forbidden" {
		t.Errorf("expected code=forbidden, got %q", envelope.Error.Code)
	}
}

func TestRemoveTeamMember_UserNotFound(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()

	router := setupTeamsRouter(agentStore, userStore, adminUser())

	req := httptest.NewRequest(http.MethodDelete, "/member/teams/team-a/members/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveTeamMember_Unauthenticated(t *testing.T) {
	agentStore := &fakeTeamsAgentStore{}
	userStore := newFakeTeamsUserStore()

	router := setupTeamsRouter(agentStore, userStore, nil)

	req := httptest.NewRequest(http.MethodDelete, "/member/teams/team-a/members/target-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}
