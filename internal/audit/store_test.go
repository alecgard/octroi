package audit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTestDB(t *testing.T) (*pgxpool.Pool, func()) {
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

func TestStore_InsertAndList(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(pool)
	ctx := context.Background()

	// Insert a test entry.
	entry := Entry{
		Action:       "test_action",
		ResourceType: "test_resource",
		ResourceID:   "test-id-123",
		Details:      map[string]any{"key": "value"},
		IP:           "127.0.0.1",
		RequestID:    "req-test-001",
	}
	if err := store.Insert(ctx, entry); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Clean up.
	defer pool.Exec(ctx, "DELETE FROM audit_log WHERE resource_id = 'test-id-123'")

	// List and verify.
	entries, _, err := store.List(ctx, ListParams{ResourceType: "test_resource", Limit: 10})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.ResourceID == "test-id-123" {
			found = true
			if e.Action != "test_action" {
				t.Errorf("expected action test_action, got %s", e.Action)
			}
			if e.Details["key"] != "value" {
				t.Errorf("expected details key=value, got %v", e.Details)
			}
		}
	}
	if !found {
		t.Error("expected to find inserted audit log entry")
	}
}

func TestStore_ListWithTimeFilter(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	store := NewStore(pool)
	ctx := context.Background()

	entry := Entry{
		Action:       "time_test",
		ResourceType: "test_time",
		ResourceID:   "time-test-001",
		Details:      map[string]any{},
	}
	if err := store.Insert(ctx, entry); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM audit_log WHERE resource_id = 'time-test-001'")

	// Query with future "from" should return nothing.
	future := time.Now().Add(1 * time.Hour)
	entries, _, err := store.List(ctx, ListParams{From: &future, ResourceType: "test_time"})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries with future from filter, got %d", len(entries))
	}
}
