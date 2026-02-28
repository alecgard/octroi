package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry represents a single audit log entry.
type Entry struct {
	ID           string         `json:"id"`
	Timestamp    time.Time      `json:"timestamp"`
	UserID       *string        `json:"user_id,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Details      map[string]any `json:"details"`
	IP           string         `json:"ip"`
	RequestID    string         `json:"request_id"`
	TenantID     string         `json:"tenant_id"`
}

// ListParams controls listing and pagination of audit log entries.
type ListParams struct {
	From         *time.Time `json:"from"`
	To           *time.Time `json:"to"`
	ResourceType string     `json:"resource_type"`
	Cursor       string     `json:"cursor"`
	Limit        int        `json:"limit"`
}

// Store provides database operations for the audit log.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new audit log store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Insert writes a single audit log entry.
func (s *Store) Insert(ctx context.Context, e Entry) error {
	detailsJSON, err := json.Marshal(e.Details)
	if err != nil {
		detailsJSON = []byte("{}")
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO audit_log (user_id, action, resource_type, resource_id, details, ip, request_id, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.UserID, e.Action, e.ResourceType, e.ResourceID, detailsJSON, e.IP, e.RequestID, e.TenantID,
	)
	return err
}

// List returns audit log entries with cursor pagination, newest first.
func (s *Store) List(ctx context.Context, tenantID string, params ListParams) ([]Entry, string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	args := []interface{}{tenantID}
	argIdx := 2
	whereClauses := []string{fmt.Sprintf("tenant_id = $1")}

	if params.Cursor != "" {
		cursorTime, cursorID, err := decodeCursor(params.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		whereClauses = append(whereClauses,
			fmt.Sprintf("(timestamp, id) < ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, cursorTime, cursorID)
		argIdx += 2
	}

	if params.From != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("timestamp >= $%d", argIdx))
		args = append(args, *params.From)
		argIdx++
	}
	if params.To != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("timestamp <= $%d", argIdx))
		args = append(args, *params.To)
		argIdx++
	}
	if params.ResourceType != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("resource_type = $%d", argIdx))
		args = append(args, params.ResourceType)
		argIdx++
	}

	where := ""
	if len(whereClauses) > 0 {
		where = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT id, timestamp, user_id, action, resource_type, resource_id, details, ip, request_id, tenant_id
		 FROM audit_log %s
		 ORDER BY timestamp DESC, id DESC
		 LIMIT $%d`, where, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("listing audit log: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		var detailsJSON []byte
		err := rows.Scan(&e.ID, &e.Timestamp, &e.UserID, &e.Action, &e.ResourceType, &e.ResourceID, &detailsJSON, &e.IP, &e.RequestID, &e.TenantID)
		if err != nil {
			return nil, "", fmt.Errorf("scanning audit log row: %w", err)
		}
		e.Details = make(map[string]any)
		if len(detailsJSON) > 0 {
			_ = json.Unmarshal(detailsJSON, &e.Details)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterating audit log: %w", err)
	}

	var nextCursor string
	if len(entries) > limit {
		last := entries[limit-1]
		nextCursor = encodeCursor(last.Timestamp, last.ID)
		entries = entries[:limit]
	}

	return entries, nextCursor, nil
}

func encodeCursor(ts time.Time, id string) string {
	raw := fmt.Sprintf("%s|%s", ts.Format(time.RFC3339Nano), id)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	data, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor format")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return t, parts[1], nil
}
