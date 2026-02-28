package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides database operations for agents.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new agent store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new agent and returns the created record.
func (s *Store) Create(ctx context.Context, in CreateAgentInput) (*Agent, error) {
	a := &Agent{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO agents (name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at`,
		in.Name, in.APIKeyHash, in.APIKeyPrefix, in.Team, in.RateLimit, false, in.TenantID,
	).Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.TenantID, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("creating agent: %w", err)
	}
	return a, nil
}

// GetByID retrieves an active (non-archived) agent by its primary key.
func (s *Store) GetByID(ctx context.Context, tenantID, id string) (*Agent, error) {
	a := &Agent{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at
		 FROM agents WHERE id = $1 AND tenant_id = $2 AND archived_at IS NULL`,
		id, tenantID,
	).Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.TenantID, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting agent by id: %w", err)
	}
	return a, nil
}


// GetByKeyHash retrieves an agent by its API key hash, used for authentication.
// Intentionally cross-tenant: the API key hash is globally unique and the tenant
// is unknown at lookup time. The auth middleware verifies tenant match after lookup.
func (s *Store) GetByKeyHash(ctx context.Context, hash string) (*Agent, error) {
	a := &Agent{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at
		 FROM agents WHERE api_key_hash = $1 AND archived_at IS NULL`,
		hash,
	).Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.TenantID, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting agent by key hash: %w", err)
	}
	return a, nil
}

// List returns a page of agents ordered by created_at DESC, id DESC using
// cursor-based pagination. It returns the agents, the next cursor (empty if no
// more results), and any error.
func (s *Store) List(ctx context.Context, tenantID string, params AgentListParams) ([]*Agent, string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	var rows pgx.Rows
	var err error

	if params.Cursor != "" {
		cursorTime, cursorID, cerr := decodeCursor(params.Cursor)
		if cerr != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", cerr)
		}
		rows, err = s.pool.Query(ctx,
			`SELECT id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at
			 FROM agents
			 WHERE archived_at IS NULL AND tenant_id = $1 AND (created_at, id) < ($2, $3)
			 ORDER BY created_at DESC, id DESC
			 LIMIT $4`,
			tenantID, cursorTime, cursorID, limit+1,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at
			 FROM agents
			 WHERE archived_at IS NULL AND tenant_id = $1
			 ORDER BY created_at DESC, id DESC
			 LIMIT $2`,
			tenantID, limit+1,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("listing agents: %w", err)
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		a := &Agent{}
		if err := rows.Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.TenantID, &a.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("scanning agent row: %w", err)
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterating agent rows: %w", err)
	}

	var nextCursor string
	if len(agents) > limit {
		last := agents[limit-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
		agents = agents[:limit]
	}

	return agents, nextCursor, nil
}

// ListByTeams returns a page of agents filtered by any of the given teams,
// ordered by created_at DESC, id DESC using cursor-based pagination.
func (s *Store) ListByTeams(ctx context.Context, tenantID string, teams []string, params AgentListParams) ([]*Agent, string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	var rows pgx.Rows
	var err error

	if params.Cursor != "" {
		cursorTime, cursorID, cerr := decodeCursor(params.Cursor)
		if cerr != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", cerr)
		}
		rows, err = s.pool.Query(ctx,
			`SELECT id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at
			 FROM agents
			 WHERE team = ANY($1) AND archived_at IS NULL AND tenant_id = $2 AND (created_at, id) < ($3, $4)
			 ORDER BY created_at DESC, id DESC
			 LIMIT $5`,
			teams, tenantID, cursorTime, cursorID, limit+1,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at
			 FROM agents
			 WHERE team = ANY($1) AND archived_at IS NULL AND tenant_id = $2
			 ORDER BY created_at DESC, id DESC
			 LIMIT $3`,
			teams, tenantID, limit+1,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("listing agents by teams: %w", err)
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		a := &Agent{}
		if err := rows.Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.TenantID, &a.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("scanning agent row: %w", err)
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterating agent rows: %w", err)
	}

	var nextCursor string
	if len(agents) > limit {
		last := agents[limit-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
		agents = agents[:limit]
	}

	return agents, nextCursor, nil
}

// ListIDsByTeam returns all agent IDs for the given team.
func (s *Store) ListIDsByTeam(ctx context.Context, tenantID string, team string) ([]string, error) {
	return s.ListIDsByTeams(ctx, tenantID, []string{team})
}

// ListIDsByTeams returns all agent IDs for the given teams.
func (s *Store) ListIDsByTeams(ctx context.Context, tenantID string, teams []string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM agents WHERE team = ANY($1) AND archived_at IS NULL AND tenant_id = $2`, teams, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing agent ids by teams: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning agent id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RegenerateKey updates the api_key_hash and api_key_prefix for the given agent.
func (s *Store) RegenerateKey(ctx context.Context, tenantID string, id, newHash, newPrefix string) (*Agent, error) {
	a := &Agent{}
	err := s.pool.QueryRow(ctx,
		`UPDATE agents SET api_key_hash = $1, api_key_prefix = $2 WHERE id = $3 AND tenant_id = $4
		 RETURNING id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at`,
		newHash, newPrefix, id, tenantID,
	).Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.TenantID, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("regenerating agent key: %w", err)
	}
	return a, nil
}

// Update performs a partial update on the agent with the given id and returns
// the updated record.
func (s *Store) Update(ctx context.Context, tenantID, id string, in UpdateAgentInput) (*Agent, error) {
	var setClauses []string
	var args []any
	argIdx := 1

	if in.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *in.Name)
		argIdx++
	}
	if in.Team != nil {
		setClauses = append(setClauses, fmt.Sprintf("team = $%d", argIdx))
		args = append(args, *in.Team)
		argIdx++
	}
	if in.RateLimit != nil {
		setClauses = append(setClauses, fmt.Sprintf("rate_limit = $%d", argIdx))
		args = append(args, *in.RateLimit)
		argIdx++
	}
	if in.AllowlistMode != nil {
		setClauses = append(setClauses, fmt.Sprintf("allowlist_mode = $%d", argIdx))
		args = append(args, *in.AllowlistMode)
		argIdx++
	}

	if len(setClauses) == 0 {
		return s.GetByID(ctx, tenantID, id)
	}

	args = append(args, id)
	idIdx := argIdx
	argIdx++
	args = append(args, tenantID)
	query := fmt.Sprintf(
		`UPDATE agents SET %s WHERE id = $%d AND tenant_id = $%d
		 RETURNING id, name, api_key_hash, api_key_prefix, team, rate_limit, allowlist_mode, tenant_id, created_at`,
		strings.Join(setClauses, ", "), idIdx, argIdx,
	)

	a := &Agent{}
	err := s.pool.QueryRow(ctx, query, args...).
		Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.TenantID, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("updating agent: %w", err)
	}
	return a, nil
}

// Archive soft-deletes an agent by setting archived_at.
func (s *Store) Archive(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE agents SET archived_at = now() WHERE id = $1 AND tenant_id = $2 AND archived_at IS NULL`, id, tenantID)
	if err != nil {
		return fmt.Errorf("archiving agent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent not found")
	}
	return nil
}

// encodeCursor produces a base64 string from a created_at timestamp and id.
func encodeCursor(createdAt time.Time, id string) string {
	raw := createdAt.Format(time.RFC3339Nano) + "|" + id
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a base64 cursor back into its created_at and id parts.
func decodeCursor(cursor string) (time.Time, string, error) {
	data, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decoding cursor base64: %w", err)
	}

	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor format")
	}

	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parsing cursor time: %w", err)
	}

	return t, parts[1], nil
}
