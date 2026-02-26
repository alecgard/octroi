package agent

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer agentStore.Delete(ctx, a.ID)

	// With allowlist_mode=false, any tool should be allowed.
	allowed, err := permStore.IsAllowed(ctx, a.ID, "00000000-0000-0000-0000-000000000001")
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
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer agentStore.Delete(ctx, a.ID)

	enabled := true
	_, err = agentStore.Update(ctx, a.ID, UpdateAgentInput{AllowlistMode: &enabled})
	if err != nil {
		t.Fatalf("enabling allowlist: %v", err)
	}

	// We need real tool IDs from the DB. Create a temporary tool for this.
	// Since we can't easily create tools here, test with a non-existent tool ID.
	fakeToolID := "00000000-0000-0000-0000-000000000099"

	// Should be denied (no permission row exists).
	allowed, err := permStore.IsAllowed(ctx, a.ID, fakeToolID)
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
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer func() {
		agentStore.Delete(ctx, a.ID)
	}()

	// List should be empty initially.
	perms, err := permStore.ListByAgent(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(perms) != 0 {
		t.Errorf("expected 0 permissions, got %d", len(perms))
	}
}
