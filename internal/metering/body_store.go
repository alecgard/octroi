package metering

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequestBody holds captured request and response bodies for a transaction.
type RequestBody struct {
	ID            string    `json:"id"`
	TransactionID string    `json:"transaction_id"`
	RequestBody   []byte    `json:"request_body,omitempty"`
	ResponseBody  []byte    `json:"response_body,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// BodyStore provides database operations for request body storage.
type BodyStore struct {
	pool *pgxpool.Pool
}

// NewBodyStore creates a new BodyStore.
func NewBodyStore(pool *pgxpool.Pool) *BodyStore {
	return &BodyStore{pool: pool}
}

// Insert stores request and response bodies for a transaction.
func (s *BodyStore) Insert(ctx context.Context, tenantID string, transactionID string, reqBody, respBody []byte) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO request_bodies (transaction_id, request_body, response_body, tenant_id)
		 VALUES ($1, $2, $3, $4)`,
		transactionID, reqBody, respBody, tenantID,
	)
	if err != nil {
		return fmt.Errorf("inserting request body: %w", err)
	}
	return nil
}

// GetByTransactionID retrieves the request body record for a transaction.
func (s *BodyStore) GetByTransactionID(ctx context.Context, tenantID string, transactionID string) (*RequestBody, error) {
	var rb RequestBody
	err := s.pool.QueryRow(ctx,
		`SELECT id, transaction_id, request_body, response_body, created_at
		 FROM request_bodies WHERE transaction_id = $1 AND tenant_id = $2`,
		transactionID, tenantID,
	).Scan(&rb.ID, &rb.TransactionID, &rb.RequestBody, &rb.ResponseBody, &rb.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("getting request body: %w", err)
	}
	return &rb, nil
}

// PurgeOlderThan deletes request body records older than the given retention duration.
func (s *BodyStore) PurgeOlderThan(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention)
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM request_bodies WHERE created_at < $1`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("purging request bodies: %w", err)
	}
	return tag.RowsAffected(), nil
}
