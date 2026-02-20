package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides database operations for tool registry management.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// toolColumns is the full list of columns used in SELECT statements.
const toolColumns = `id, name, description, endpoint, auth_type, auth_config,
	pricing_model, pricing_amount, pricing_currency, rate_limit,
	budget_limit, budget_window, created_at, updated_at`

// scanTool scans a single tool row into a Tool struct.
func scanTool(row pgx.Row) (*Tool, error) {
	var t Tool
	var authConfigJSON []byte
	err := row.Scan(
		&t.ID,
		&t.Name,
		&t.Description,
		&t.Endpoint,
		&t.AuthType,
		&authConfigJSON,
		&t.PricingModel,
		&t.PricingAmount,
		&t.PricingCurrency,
		&t.RateLimit,
		&t.BudgetLimit,
		&t.BudgetWindow,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	t.AuthConfig = make(map[string]string)
	if len(authConfigJSON) > 0 {
		if err := json.Unmarshal(authConfigJSON, &t.AuthConfig); err != nil {
			return nil, fmt.Errorf("unmarshalling auth_config: %w", err)
		}
	}
	return &t, nil
}

// Create inserts a new tool and returns the full row.
func (s *Store) Create(ctx context.Context, input CreateToolInput) (*Tool, error) {
	authConfigJSON, err := json.Marshal(input.AuthConfig)
	if err != nil {
		return nil, fmt.Errorf("marshalling auth_config: %w", err)
	}

	query := fmt.Sprintf(`INSERT INTO tools
		(name, description, endpoint, auth_type, auth_config,
		 pricing_model, pricing_amount, pricing_currency, rate_limit,
		 budget_limit, budget_window)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING %s`, toolColumns)

	row := s.pool.QueryRow(ctx, query,
		input.Name,
		input.Description,
		input.Endpoint,
		input.AuthType,
		authConfigJSON,
		input.PricingModel,
		input.PricingAmount,
		input.PricingCurrency,
		input.RateLimit,
		input.BudgetLimit,
		input.BudgetWindow,
	)
	return scanTool(row)
}

// GetByID retrieves a tool by its ID, including endpoint and auth_config.
func (s *Store) GetByID(ctx context.Context, id string) (*Tool, error) {
	query := fmt.Sprintf(`SELECT %s FROM tools WHERE id = $1`, toolColumns)
	row := s.pool.QueryRow(ctx, query, id)
	return scanTool(row)
}

// encodeCursor produces a base64-encoded cursor from a timestamp and ID.
func encodeCursor(createdAt time.Time, id string) string {
	raw := fmt.Sprintf("%s|%s", createdAt.Format(time.RFC3339Nano), id)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a base64-encoded cursor into a timestamp and ID.
func decodeCursor(cursor string) (time.Time, string, error) {
	data, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decoding cursor: %w", err)
	}
	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor format")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parsing cursor timestamp: %w", err)
	}
	return t, parts[1], nil
}

// List returns a page of tools ordered by created_at DESC, id DESC with cursor-based pagination.
func (s *Store) List(ctx context.Context, params ToolListParams) ([]*Tool, string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	args := []interface{}{}
	argIdx := 1
	whereClauses := []string{}

	if params.Cursor != "" {
		cursorTime, cursorID, err := decodeCursor(params.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		whereClauses = append(whereClauses,
			fmt.Sprintf("(created_at, id) < ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, cursorTime, cursorID)
		argIdx += 2
	}

	if params.Query != "" {
		pattern := "%" + params.Query + "%"
		whereClauses = append(whereClauses,
			fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d)", argIdx, argIdx))
		args = append(args, pattern)
		argIdx++
	}

	where := ""
	if len(whereClauses) > 0 {
		where = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`SELECT %s FROM tools %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		toolColumns, where, argIdx)
	args = append(args, limit+1) // fetch one extra to determine next cursor

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("listing tools: %w", err)
	}
	defer rows.Close()

	var tools []*Tool
	for rows.Next() {
		t, err := scanTool(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scanning tool: %w", err)
		}
		tools = append(tools, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterating tools: %w", err)
	}

	var nextCursor string
	if len(tools) > limit {
		last := tools[limit-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
		tools = tools[:limit]
	}

	return tools, nextCursor, nil
}

// Update applies a partial update to a tool and returns the updated row.
func (s *Store) Update(ctx context.Context, id string, input UpdateToolInput) (*Tool, error) {
	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if input.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *input.Name)
		argIdx++
	}
	if input.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, *input.Description)
		argIdx++
	}
	if input.Endpoint != nil {
		setClauses = append(setClauses, fmt.Sprintf("endpoint = $%d", argIdx))
		args = append(args, *input.Endpoint)
		argIdx++
	}
	if input.AuthType != nil {
		setClauses = append(setClauses, fmt.Sprintf("auth_type = $%d", argIdx))
		args = append(args, *input.AuthType)
		argIdx++
	}
	if input.AuthConfig != nil {
		authConfigJSON, err := json.Marshal(*input.AuthConfig)
		if err != nil {
			return nil, fmt.Errorf("marshalling auth_config: %w", err)
		}
		setClauses = append(setClauses, fmt.Sprintf("auth_config = $%d", argIdx))
		args = append(args, authConfigJSON)
		argIdx++
	}
	if input.PricingModel != nil {
		setClauses = append(setClauses, fmt.Sprintf("pricing_model = $%d", argIdx))
		args = append(args, *input.PricingModel)
		argIdx++
	}
	if input.PricingAmount != nil {
		setClauses = append(setClauses, fmt.Sprintf("pricing_amount = $%d", argIdx))
		args = append(args, *input.PricingAmount)
		argIdx++
	}
	if input.PricingCurrency != nil {
		setClauses = append(setClauses, fmt.Sprintf("pricing_currency = $%d", argIdx))
		args = append(args, *input.PricingCurrency)
		argIdx++
	}
	if input.RateLimit != nil {
		setClauses = append(setClauses, fmt.Sprintf("rate_limit = $%d", argIdx))
		args = append(args, *input.RateLimit)
		argIdx++
	}
	if input.BudgetLimit != nil {
		setClauses = append(setClauses, fmt.Sprintf("budget_limit = $%d", argIdx))
		args = append(args, *input.BudgetLimit)
		argIdx++
	}
	if input.BudgetWindow != nil {
		setClauses = append(setClauses, fmt.Sprintf("budget_window = $%d", argIdx))
		args = append(args, *input.BudgetWindow)
		argIdx++
	}

	if len(setClauses) == 0 {
		return s.GetByID(ctx, id)
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argIdx))
	args = append(args, time.Now().UTC())
	argIdx++

	args = append(args, id)

	query := fmt.Sprintf(`UPDATE tools SET %s WHERE id = $%d RETURNING %s`,
		strings.Join(setClauses, ", "), argIdx, toolColumns)

	row := s.pool.QueryRow(ctx, query, args...)
	return scanTool(row)
}

// Delete removes a tool by its ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM tools WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting tool: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Search performs a text search on name and description using ILIKE.
// Results use cursor-based pagination.
func (s *Store) Search(ctx context.Context, query string, limit int, cursor string) ([]*Tool, string, error) {
	if limit <= 0 {
		limit = 20
	}

	args := []interface{}{}
	argIdx := 1
	whereClauses := []string{}

	if cursor != "" {
		cursorTime, cursorID, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		whereClauses = append(whereClauses,
			fmt.Sprintf("(created_at, id) < ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, cursorTime, cursorID)
		argIdx += 2
	}

	if query != "" {
		pattern := "%" + query + "%"
		whereClauses = append(whereClauses,
			fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d)",
				argIdx, argIdx))
		args = append(args, pattern)
		argIdx++
	}

	where := ""
	if len(whereClauses) > 0 {
		where = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	sqlQuery := fmt.Sprintf(`SELECT %s FROM tools %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		toolColumns, where, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, "", fmt.Errorf("searching tools: %w", err)
	}
	defer rows.Close()

	var tools []*Tool
	for rows.Next() {
		t, err := scanTool(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scanning tool: %w", err)
		}
		tools = append(tools, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterating tools: %w", err)
	}

	var nextCursor string
	if len(tools) > limit {
		last := tools[limit-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
		tools = tools[:limit]
	}

	return tools, nextCursor, nil
}
