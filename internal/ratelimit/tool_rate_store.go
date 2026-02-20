package ratelimit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ToolRateOverride represents a team- or agent-scoped rate limit override for a tool.
type ToolRateOverride struct {
	ID        string `json:"id"`
	ToolID    string `json:"tool_id"`
	Scope     string `json:"scope"`
	ScopeID   string `json:"scope_id"`
	RateLimit int    `json:"rate_limit"`
}

// ToolRateLimitStore provides CRUD for tool_rate_limits and resolution of effective rates.
type ToolRateLimitStore struct {
	pool *pgxpool.Pool
}

// NewToolRateLimitStore creates a new ToolRateLimitStore.
func NewToolRateLimitStore(pool *pgxpool.Pool) *ToolRateLimitStore {
	return &ToolRateLimitStore{pool: pool}
}

// ListByTool returns all rate limit overrides for the given tool.
func (s *ToolRateLimitStore) ListByTool(ctx context.Context, toolID string) ([]ToolRateOverride, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tool_id, scope, scope_id, rate_limit
		 FROM tool_rate_limits WHERE tool_id = $1 ORDER BY scope, scope_id`, toolID)
	if err != nil {
		return nil, fmt.Errorf("listing tool rate limits: %w", err)
	}
	defer rows.Close()

	var overrides []ToolRateOverride
	for rows.Next() {
		var o ToolRateOverride
		if err := rows.Scan(&o.ID, &o.ToolID, &o.Scope, &o.ScopeID, &o.RateLimit); err != nil {
			return nil, fmt.Errorf("scanning tool rate limit: %w", err)
		}
		overrides = append(overrides, o)
	}
	return overrides, rows.Err()
}

// Set upserts a rate limit override for a tool+scope+scopeID combination.
func (s *ToolRateLimitStore) Set(ctx context.Context, toolID, scope, scopeID string, rate int) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tool_rate_limits (tool_id, scope, scope_id, rate_limit)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tool_id, scope, scope_id) DO UPDATE SET rate_limit = EXCLUDED.rate_limit`,
		toolID, scope, scopeID, rate)
	if err != nil {
		return fmt.Errorf("upserting tool rate limit: %w", err)
	}
	return nil
}

// Delete removes a rate limit override for a tool+scope+scopeID combination.
func (s *ToolRateLimitStore) Delete(ctx context.Context, toolID, scope, scopeID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tool_rate_limits WHERE tool_id = $1 AND scope = $2 AND scope_id = $3`,
		toolID, scope, scopeID)
	if err != nil {
		return fmt.Errorf("deleting tool rate limit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Resolve returns the effective rate limits for a tool across all three scopes.
// globalRate comes from tools.rate_limit, teamRate and agentRate from tool_rate_limits.
// A zero value means no limit is configured for that scope.
func (s *ToolRateLimitStore) Resolve(ctx context.Context, toolID, team, agentID string) (globalRate, teamRate, agentRate int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(t.rate_limit, 0),
			COALESCE((SELECT trl.rate_limit FROM tool_rate_limits trl
			          WHERE trl.tool_id = t.id AND trl.scope = 'team' AND trl.scope_id = $2), 0),
			COALESCE((SELECT trl.rate_limit FROM tool_rate_limits trl
			          WHERE trl.tool_id = t.id AND trl.scope = 'agent' AND trl.scope_id = $3), 0)
		FROM tools t
		WHERE t.id = $1`,
		toolID, team, agentID,
	).Scan(&globalRate, &teamRate, &agentRate)
	if err != nil {
		err = fmt.Errorf("resolving tool rate limits: %w", err)
	}
	return
}
