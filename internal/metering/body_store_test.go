package metering

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupBodyTestDB(t *testing.T) (*pgxpool.Pool, func()) {
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

func TestBodyStore_InsertAndGet(t *testing.T) {
	pool, cleanup := setupBodyTestDB(t)
	defer cleanup()

	ctx := context.Background()
	meterStore := NewStore(pool)
	bodyStore := NewBodyStore(pool)

	// Look up an existing agent and tool for FK constraints.
	var agentID, toolID string
	err := pool.QueryRow(ctx, "SELECT id FROM agents LIMIT 1").Scan(&agentID)
	if err != nil {
		t.Skip("skipping: no agents in DB")
	}
	err = pool.QueryRow(ctx, "SELECT id FROM tools LIMIT 1").Scan(&toolID)
	if err != nil {
		t.Skip("skipping: no tools in DB")
	}

	// Create a transaction first.
	txID := "10000000-0000-0000-0000-b0d7fe510001"
	err = meterStore.BatchInsert(ctx, "00000000-0000-0000-0000-000000000001", []Transaction{{
		ID:        txID,
		AgentID:   agentID,
		ToolID:    toolID,
		Timestamp: time.Now().UTC(),
		Method:    "POST",
		Path:      "/test",
		Success:   true,
	}})
	if err != nil {
		t.Fatalf("creating transaction: %v", err)
	}

	// Clean up after test.
	defer pool.Exec(ctx, "DELETE FROM request_bodies WHERE transaction_id = $1", txID)
	defer pool.Exec(ctx, "DELETE FROM transactions WHERE id = $1", txID)

	reqBody := []byte(`{"query":"test"}`)
	respBody := []byte(`{"result":"ok"}`)

	err = bodyStore.Insert(ctx, "00000000-0000-0000-0000-000000000001", txID, reqBody, respBody)
	if err != nil {
		t.Fatalf("inserting body: %v", err)
	}

	rb, err := bodyStore.GetByTransactionID(ctx, "00000000-0000-0000-0000-000000000001", txID)
	if err != nil {
		t.Fatalf("getting body: %v", err)
	}
	if rb == nil {
		t.Fatal("expected body record, got nil")
	}
	if string(rb.RequestBody) != string(reqBody) {
		t.Errorf("expected request body %s, got %s", reqBody, rb.RequestBody)
	}
	if string(rb.ResponseBody) != string(respBody) {
		t.Errorf("expected response body %s, got %s", respBody, rb.ResponseBody)
	}
}

func TestBodyStore_GetByTransactionID_NotFound(t *testing.T) {
	pool, cleanup := setupBodyTestDB(t)
	defer cleanup()

	bodyStore := NewBodyStore(pool)
	rb, err := bodyStore.GetByTransactionID(context.Background(), "00000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000099")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rb != nil {
		t.Error("expected nil for non-existent transaction")
	}
}

func TestBodyStore_PurgeOlderThan(t *testing.T) {
	pool, cleanup := setupBodyTestDB(t)
	defer cleanup()

	bodyStore := NewBodyStore(pool)
	// Purge with a very short retention (should not fail).
	_, err := bodyStore.PurgeOlderThan(context.Background(), time.Millisecond)
	if err != nil {
		t.Fatalf("purge failed: %v", err)
	}
}
