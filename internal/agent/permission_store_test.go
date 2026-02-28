package agent

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testTenantID = "00000000-0000-0000-0000-000000000001"

func setupPermissionTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url := os.Getenv("OCTROI_DATABASE_URL")
	if url == "" {
		url = "postgres://octroi:octroi@localhost:5433/octroi?sslmode=disable"
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("skipping DB test: %v", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("skipping DB test: %v", err)
	}

	return pool, func() { pool.Close() }
}

// createTestTool inserts a minimal tool row and returns its ID.
func createTestTool(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO tools (name, description, endpoint, tenant_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		name, "test tool", "http://localhost:9999", testTenantID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("creating test tool: %v", err)
	}
	return id
}

// deleteTestTool removes a test tool by ID.
func deleteTestTool(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	_, _ = pool.Exec(context.Background(), `DELETE FROM tools WHERE id = $1`, id)
}

func TestPermissionStore_IsAllowed_NoAllowlist(t *testing.T) {
	pool, cleanup := setupPermissionTestDB(t)
	defer cleanup()

	ctx := context.Background()
	agentStore := NewStore(pool)
	permStore := NewPermissionStore(pool, agentStore)

	// Create a test agent with allowlist_mode=false.
	a, err := agentStore.Create(ctx, CreateAgentInput{
		Name:         "perm-test-agent",
		APIKeyHash:   "permhash123",
		APIKeyPrefix: "octroi_permte",
		Team:         "test",
		RateLimit:    60,
		TenantID:     testTenantID,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", a.ID)

	// With allowlist_mode=false, any tool should be allowed.
	allowed, err := permStore.IsAllowed(ctx, testTenantID, a.ID, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatalf("IsAllowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true when allowlist_mode is false")
	}
}

func TestPermissionStore_IsAllowed_WithAllowlist(t *testing.T) {
	pool, cleanup := setupPermissionTestDB(t)
	defer cleanup()

	ctx := context.Background()
	agentStore := NewStore(pool)
	permStore := NewPermissionStore(pool, agentStore)

	// Create a test agent and enable allowlist mode.
	a, err := agentStore.Create(ctx, CreateAgentInput{
		Name:         "perm-allowlist-agent",
		APIKeyHash:   "permhash456",
		APIKeyPrefix: "octroi_permal",
		Team:         "test",
		RateLimit:    60,
		TenantID:     testTenantID,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", a.ID)

	enabled := true
	_, err = agentStore.Update(ctx, testTenantID, a.ID, UpdateAgentInput{AllowlistMode: &enabled})
	if err != nil {
		t.Fatalf("enabling allowlist: %v", err)
	}

	// We need real tool IDs from the DB. Create a temporary tool for this.
	// Since we can't easily create tools here, test with a non-existent tool ID.
	fakeToolID := "00000000-0000-0000-0000-000000000099"

	// Should be denied (no permission row exists).
	allowed, err := permStore.IsAllowed(ctx, testTenantID, a.ID, fakeToolID)
	if err != nil {
		t.Fatalf("IsAllowed: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false when no permission row exists")
	}
}

func TestPermissionStore_SetAndList(t *testing.T) {
	pool, cleanup := setupPermissionTestDB(t)
	defer cleanup()

	ctx := context.Background()
	agentStore := NewStore(pool)
	permStore := NewPermissionStore(pool, agentStore)

	a, err := agentStore.Create(ctx, CreateAgentInput{
		Name:         "perm-setlist-agent",
		APIKeyHash:   "permhash789",
		APIKeyPrefix: "octroi_permsl",
		Team:         "test",
		RateLimit:    60,
		TenantID:     testTenantID,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer func() {
		pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", a.ID)
	}()

	// List should be empty initially.
	perms, err := permStore.ListByAgent(ctx, testTenantID, a.ID)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(perms) != 0 {
		t.Errorf("expected 0 permissions, got %d", len(perms))
	}
}

func TestPermissionStore_IsSubToolAllowed_NoAllowlist(t *testing.T) {
	pool, cleanup := setupPermissionTestDB(t)
	defer cleanup()

	ctx := context.Background()
	agentStore := NewStore(pool)
	permStore := NewPermissionStore(pool, agentStore)

	a, err := agentStore.Create(ctx, CreateAgentInput{
		Name:         "subtool-noallow",
		APIKeyHash:   "subnoallow001",
		APIKeyPrefix: "octroi_subno1",
		Team:         "test",
		RateLimit:    60,
		TenantID:     testTenantID,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", a.ID)

	// Without allowlist mode, any sub-tool should be allowed.
	allowed, err := permStore.IsSubToolAllowed(ctx, testTenantID, a.ID, "00000000-0000-0000-0000-000000000001", "search_code")
	if err != nil {
		t.Fatalf("IsSubToolAllowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true when allowlist_mode is false")
	}
}

func TestPermissionStore_IsSubToolAllowed_ParentDenied(t *testing.T) {
	pool, cleanup := setupPermissionTestDB(t)
	defer cleanup()

	ctx := context.Background()
	agentStore := NewStore(pool)
	permStore := NewPermissionStore(pool, agentStore)

	a, err := agentStore.Create(ctx, CreateAgentInput{
		Name:         "subtool-denied",
		APIKeyHash:   "subdenied001",
		APIKeyPrefix: "octroi_subde1",
		Team:         "test",
		RateLimit:    60,
		TenantID:     testTenantID,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", a.ID)

	enabled := true
	_, err = agentStore.Update(ctx, testTenantID, a.ID, UpdateAgentInput{AllowlistMode: &enabled})
	if err != nil {
		t.Fatalf("enabling allowlist: %v", err)
	}

	toolID := createTestTool(t, pool, "subtool-denied-tool")
	defer deleteTestTool(t, pool, toolID)

	// No permission row → denied.
	allowed, err := permStore.IsSubToolAllowed(ctx, testTenantID, a.ID, toolID, "search_code")
	if err != nil {
		t.Fatalf("IsSubToolAllowed: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false when no permission row exists")
	}

	// Set parent allowed=false.
	if err := permStore.SetWithSubTools(ctx, testTenantID, a.ID, toolID, false, nil); err != nil {
		t.Fatalf("Set: %v", err)
	}
	defer permStore.Delete(ctx, testTenantID, a.ID, toolID)

	allowed, err = permStore.IsSubToolAllowed(ctx, testTenantID, a.ID, toolID, "search_code")
	if err != nil {
		t.Fatalf("IsSubToolAllowed: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false when parent is denied")
	}
}

func TestPermissionStore_IsSubToolAllowed_EmptySubTools(t *testing.T) {
	pool, cleanup := setupPermissionTestDB(t)
	defer cleanup()

	ctx := context.Background()
	agentStore := NewStore(pool)
	permStore := NewPermissionStore(pool, agentStore)

	a, err := agentStore.Create(ctx, CreateAgentInput{
		Name:         "subtool-empty",
		APIKeyHash:   "subempty001",
		APIKeyPrefix: "octroi_subem1",
		Team:         "test",
		RateLimit:    60,
		TenantID:     testTenantID,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", a.ID)

	enabled := true
	_, err = agentStore.Update(ctx, testTenantID, a.ID, UpdateAgentInput{AllowlistMode: &enabled})
	if err != nil {
		t.Fatalf("enabling allowlist: %v", err)
	}

	toolID := createTestTool(t, pool, "subtool-empty-tool")
	defer deleteTestTool(t, pool, toolID)

	// Set allowed=true with empty sub_tools → all sub-tools allowed.
	if err := permStore.SetWithSubTools(ctx, testTenantID, a.ID, toolID, true, nil); err != nil {
		t.Fatalf("SetWithSubTools: %v", err)
	}
	defer permStore.Delete(ctx, testTenantID, a.ID, toolID)

	allowed, err := permStore.IsSubToolAllowed(ctx, testTenantID, a.ID, toolID, "any_sub_tool")
	if err != nil {
		t.Fatalf("IsSubToolAllowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true when sub_tools is empty (all allowed)")
	}
}

func TestPermissionStore_IsSubToolAllowed_Membership(t *testing.T) {
	pool, cleanup := setupPermissionTestDB(t)
	defer cleanup()

	ctx := context.Background()
	agentStore := NewStore(pool)
	permStore := NewPermissionStore(pool, agentStore)

	a, err := agentStore.Create(ctx, CreateAgentInput{
		Name:         "subtool-member",
		APIKeyHash:   "submember001",
		APIKeyPrefix: "octroi_subme1",
		Team:         "test",
		RateLimit:    60,
		TenantID:     testTenantID,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", a.ID)

	enabled := true
	_, err = agentStore.Update(ctx, testTenantID, a.ID, UpdateAgentInput{AllowlistMode: &enabled})
	if err != nil {
		t.Fatalf("enabling allowlist: %v", err)
	}

	toolID := createTestTool(t, pool, "subtool-member-tool")
	defer deleteTestTool(t, pool, toolID)

	// Set allowed=true with specific sub_tools.
	if err := permStore.SetWithSubTools(ctx, testTenantID, a.ID, toolID, true, []string{"search_code", "read_file"}); err != nil {
		t.Fatalf("SetWithSubTools: %v", err)
	}
	defer permStore.Delete(ctx, testTenantID, a.ID, toolID)

	// Allowed sub-tool.
	allowed, err := permStore.IsSubToolAllowed(ctx, testTenantID, a.ID, toolID, "search_code")
	if err != nil {
		t.Fatalf("IsSubToolAllowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true for search_code")
	}

	// Denied sub-tool.
	allowed, err = permStore.IsSubToolAllowed(ctx, testTenantID, a.ID, toolID, "push_commits")
	if err != nil {
		t.Fatalf("IsSubToolAllowed: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false for push_commits")
	}

	// Empty subTool name → allowed (means no sub-tool filtering).
	allowed, err = permStore.IsSubToolAllowed(ctx, testTenantID, a.ID, toolID, "")
	if err != nil {
		t.Fatalf("IsSubToolAllowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true for empty sub-tool name")
	}
}

func TestPermissionStore_SetWithSubTools_RoundTrip(t *testing.T) {
	pool, cleanup := setupPermissionTestDB(t)
	defer cleanup()

	ctx := context.Background()
	agentStore := NewStore(pool)
	permStore := NewPermissionStore(pool, agentStore)

	a, err := agentStore.Create(ctx, CreateAgentInput{
		Name:         "subtool-roundtrip",
		APIKeyHash:   "subrt001",
		APIKeyPrefix: "octroi_subrt1",
		Team:         "test",
		RateLimit:    60,
		TenantID:     testTenantID,
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM agents WHERE id = $1", a.ID)

	toolID := createTestTool(t, pool, "subtool-roundtrip-tool")
	defer deleteTestTool(t, pool, toolID)

	subTools := []string{"tool_a", "tool_b", "tool_c"}
	if err := permStore.SetWithSubTools(ctx, testTenantID, a.ID, toolID, true, subTools); err != nil {
		t.Fatalf("SetWithSubTools: %v", err)
	}
	defer permStore.Delete(ctx, testTenantID, a.ID, toolID)

	// Verify via ListByAgent.
	perms, err := permStore.ListByAgent(ctx, testTenantID, a.ID)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(perms) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(perms))
	}
	p := perms[0]
	if !p.Allowed {
		t.Error("expected allowed=true")
	}
	if len(p.SubTools) != 3 {
		t.Fatalf("expected 3 sub_tools, got %d", len(p.SubTools))
	}
	expected := map[string]bool{"tool_a": true, "tool_b": true, "tool_c": true}
	for _, st := range p.SubTools {
		if !expected[st] {
			t.Errorf("unexpected sub_tool: %s", st)
		}
	}

	// Update to fewer sub-tools.
	if err := permStore.SetWithSubTools(ctx, testTenantID, a.ID, toolID, true, []string{"tool_a"}); err != nil {
		t.Fatalf("SetWithSubTools update: %v", err)
	}
	perms, err = permStore.ListByAgent(ctx, testTenantID, a.ID)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(perms[0].SubTools) != 1 || perms[0].SubTools[0] != "tool_a" {
		t.Errorf("expected sub_tools=[tool_a], got %v", perms[0].SubTools)
	}
}
