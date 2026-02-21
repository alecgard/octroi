package metering

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides database operations for the metering system.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// BatchInsert writes a slice of transactions to the database in a single
// multi-row INSERT statement. It is a no-op when txns is empty.
func (s *Store) BatchInsert(ctx context.Context, txns []Transaction) error {
	if len(txns) == 0 {
		return nil
	}

	const cols = 13 // number of columns per row (excluding server-generated id)
	args := make([]any, 0, len(txns)*cols)
	rows := make([]string, 0, len(txns))

	for i, tx := range txns {
		base := i * cols
		rows = append(rows, fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5, base+6,
			base+7, base+8, base+9, base+10, base+11, base+12, base+13,
		))
		costSource := tx.CostSource
		if costSource == "" {
			costSource = "flat"
		}
		args = append(args,
			tx.AgentID,
			tx.ToolID,
			tx.Timestamp,
			tx.Method,
			tx.Path,
			tx.StatusCode,
			tx.LatencyMs,
			tx.RequestSize,
			tx.ResponseSize,
			tx.Success,
			tx.Cost,
			tx.Error,
			costSource,
		)
	}

	query := `INSERT INTO transactions
		(agent_id, tool_id, timestamp, method, path, status_code, latency_ms,
		 request_size, response_size, success, cost, error, cost_source)
		VALUES ` + strings.Join(rows, ", ")

	_, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("batch inserting transactions: %w", err)
	}

	return nil
}

// GetSummary returns aggregate usage metrics matching the given query filters.
func (s *Store) GetSummary(ctx context.Context, q UsageQuery) (*UsageSummary, error) {
	where, args := buildWhereClause(q)

	query := `SELECT
		COUNT(*),
		COALESCE(SUM(cost), 0),
		COALESCE(SUM(CASE WHEN success THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN NOT success THEN 1 ELSE 0 END), 0),
		COALESCE(AVG(latency_ms), 0)
	FROM transactions` + where

	var summary UsageSummary
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&summary.TotalRequests,
		&summary.TotalCost,
		&summary.SuccessCount,
		&summary.ErrorCount,
		&summary.AvgLatencyMs,
	)
	if err != nil {
		return nil, fmt.Errorf("querying usage summary: %w", err)
	}

	return &summary, nil
}

// GetToolCallCounts returns the total number of transactions per tool for all tools.
func (s *Store) GetToolCallCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tool_id, COUNT(*) FROM transactions GROUP BY tool_id`)
	if err != nil {
		return nil, fmt.Errorf("querying tool call counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var toolID string
		var count int64
		if err := rows.Scan(&toolID, &count); err != nil {
			return nil, fmt.Errorf("scanning tool call count: %w", err)
		}
		counts[toolID] = count
	}
	return counts, rows.Err()
}

// ListTransactions returns a page of transactions matching the query filters,
// ordered by timestamp DESC, id DESC. It uses cursor-based pagination and
// returns the next cursor (empty string if no more results).
func (s *Store) ListTransactions(ctx context.Context, q UsageQuery) ([]*Transaction, string, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	where, args := buildWhereClause(q)

	// Apply cursor: the cursor encodes "timestamp|id".
	if q.Cursor != "" {
		ts, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		n := len(args)
		if where == "" {
			where = " WHERE"
		} else {
			where += " AND"
		}
		where += fmt.Sprintf(" (timestamp, id) < ($%d, $%d)", n+1, n+2)
		args = append(args, ts, id)
	}

	query := `SELECT id, agent_id, tool_id, timestamp, method, path,
		status_code, latency_ms, request_size, response_size, success, cost, cost_source, error
	FROM transactions` + where +
		` ORDER BY timestamp DESC, id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit+1) // fetch one extra to determine if there's a next page

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("listing transactions: %w", err)
	}
	defer rows.Close()

	var txns []*Transaction
	for rows.Next() {
		var tx Transaction
		if err := rows.Scan(
			&tx.ID, &tx.AgentID, &tx.ToolID, &tx.Timestamp,
			&tx.Method, &tx.Path, &tx.StatusCode, &tx.LatencyMs,
			&tx.RequestSize, &tx.ResponseSize, &tx.Success, &tx.Cost, &tx.CostSource, &tx.Error,
		); err != nil {
			return nil, "", fmt.Errorf("scanning transaction row: %w", err)
		}
		txns = append(txns, &tx)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterating transaction rows: %w", err)
	}

	var nextCursor string
	if len(txns) > limit {
		last := txns[limit-1]
		nextCursor = encodeCursor(last.Timestamp, last.ID)
		txns = txns[:limit]
	}

	return txns, nextCursor, nil
}

// buildWhereClause constructs a WHERE clause and positional arguments from a
// UsageQuery. The returned string starts with " WHERE" or is empty.
func buildWhereClause(q UsageQuery) (string, []any) {
	var conditions []string
	var args []any

	if q.AgentID != "" {
		args = append(args, q.AgentID)
		conditions = append(conditions, fmt.Sprintf("agent_id = $%d", len(args)))
	} else if len(q.AgentIDs) > 0 {
		placeholders := make([]string, len(q.AgentIDs))
		for i, id := range q.AgentIDs {
			args = append(args, id)
			placeholders[i] = fmt.Sprintf("$%d", len(args))
		}
		conditions = append(conditions, "agent_id IN ("+strings.Join(placeholders, ", ")+")")
	}
	if q.ToolID != "" {
		args = append(args, q.ToolID)
		conditions = append(conditions, fmt.Sprintf("tool_id = $%d", len(args)))
	} else if len(q.ToolIDs) > 0 {
		placeholders := make([]string, len(q.ToolIDs))
		for i, id := range q.ToolIDs {
			args = append(args, id)
			placeholders[i] = fmt.Sprintf("$%d", len(args))
		}
		conditions = append(conditions, "tool_id IN ("+strings.Join(placeholders, ", ")+")")
	}
	if !q.From.IsZero() {
		args = append(args, q.From)
		conditions = append(conditions, fmt.Sprintf("timestamp >= $%d", len(args)))
	}
	if !q.To.IsZero() {
		args = append(args, q.To)
		conditions = append(conditions, fmt.Sprintf("timestamp <= $%d", len(args)))
	}

	if len(conditions) == 0 {
		return "", nil
	}

	return " WHERE " + strings.Join(conditions, " AND "), args
}

// encodeCursor encodes a timestamp and id into an opaque cursor string.
func encodeCursor(ts time.Time, id string) string {
	raw := ts.Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor decodes an opaque cursor string into a timestamp and id.
func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decoding cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parsing cursor timestamp: %w", err)
	}
	return ts, parts[1], nil
}
