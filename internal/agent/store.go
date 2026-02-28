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

// agentColumns is the standard column list for agent queries.
const agentColumns = `id, name, api_key_hash, api_key_prefix, api_key_suffix, team, rate_limit, allowlist_mode, created_at, prev_key_hash, prev_key_prefix, prev_key_expires_at`

// scanAgent scans a row into an Agent struct using the standard column order.
func scanAgent(row pgx.Row) (*Agent, error) {
	a := &Agent{}
	err := row.Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.APIKeySuffix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.CreatedAt, &a.PrevKeyHash, &a.PrevKeyPrefix, &a.PrevKeyExpiresAt)
	return a, err
}

// scanAgentRows scans multiple rows into Agent structs.
func scanAgentRows(rows pgx.Rows) ([]*Agent, error) {
	var agents []*Agent
	for rows.Next() {
		a := &Agent{}
		if err := rows.Scan(&a.ID, &a.Name, &a.APIKeyHash, &a.APIKeyPrefix, &a.APIKeySuffix, &a.Team, &a.RateLimit, &a.AllowlistMode, &a.CreatedAt, &a.PrevKeyHash, &a.PrevKeyPrefix, &a.PrevKeyExpiresAt); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

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
	a, err := scanAgent(s.pool.QueryRow(ctx,
		`INSERT INTO agents (name, api_key_hash, api_key_prefix, api_key_suffix, team, rate_limit, allowlist_mode)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+agentColumns,
		in.Name, in.APIKeyHash, in.APIKeyPrefix, in.APIKeySuffix, in.Team, in.RateLimit, false,
	))
	if err != nil {
		return nil, fmt.Errorf("creating agent: %w", err)
	}
	return a, nil
}

// GetByID retrieves an active (non-archived) agent by its primary key.
func (s *Store) GetByID(ctx context.Context, id string) (*Agent, error) {
	a, err := scanAgent(s.pool.QueryRow(ctx,
		`SELECT `+agentColumns+` FROM agents WHERE id = $1 AND archived_at IS NULL`, id,
	))
	if err != nil {
		return nil, fmt.Errorf("getting agent by id: %w", err)
	}
	return a, nil
}

// GetByKeyHash retrieves an agent by its current API key hash, used for authentication.
func (s *Store) GetByKeyHash(ctx context.Context, hash string) (*Agent, error) {
	a, err := scanAgent(s.pool.QueryRow(ctx,
		`SELECT `+agentColumns+` FROM agents WHERE api_key_hash = $1 AND archived_at IS NULL`, hash,
	))
	if err != nil {
		return nil, fmt.Errorf("getting agent by key hash: %w", err)
	}
	return a, nil
}

// GetByPrevKeyHash retrieves an agent by its previous (expiring) key hash.
// Returns the agent only if the previous key has not yet expired.
func (s *Store) GetByPrevKeyHash(ctx context.Context, hash string) (*Agent, error) {
	a, err := scanAgent(s.pool.QueryRow(ctx,
		`SELECT `+agentColumns+`
		 FROM agents
		 WHERE prev_key_hash = $1 AND archived_at IS NULL
		   AND prev_key_expires_at IS NOT NULL AND prev_key_expires_at > now()`,
		hash,
	))
	if err != nil {
		return nil, fmt.Errorf("getting agent by prev key hash: %w", err)
	}
	return a, nil
}

// List returns a page of agents ordered by created_at DESC, id DESC using
// cursor-based pagination. It returns the agents, the next cursor (empty if no
// more results), and any error.
func (s *Store) List(ctx context.Context, params AgentListParams) ([]*Agent, string, error) {
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
			`SELECT `+agentColumns+`
			 FROM agents
			 WHERE archived_at IS NULL AND (created_at, id) < ($1, $2)
			 ORDER BY created_at DESC, id DESC
			 LIMIT $3`,
			cursorTime, cursorID, limit+1,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT `+agentColumns+`
			 FROM agents
			 WHERE archived_at IS NULL
			 ORDER BY created_at DESC, id DESC
			 LIMIT $1`,
			limit+1,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("listing agents: %w", err)
	}
	defer rows.Close()

	agents, err := scanAgentRows(rows)
	if err != nil {
		return nil, "", fmt.Errorf("scanning agent rows: %w", err)
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
func (s *Store) ListByTeams(ctx context.Context, teams []string, params AgentListParams) ([]*Agent, string, error) {
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
			`SELECT `+agentColumns+`
			 FROM agents
			 WHERE team = ANY($1) AND archived_at IS NULL AND (created_at, id) < ($2, $3)
			 ORDER BY created_at DESC, id DESC
			 LIMIT $4`,
			teams, cursorTime, cursorID, limit+1,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT `+agentColumns+`
			 FROM agents
			 WHERE team = ANY($1) AND archived_at IS NULL
			 ORDER BY created_at DESC, id DESC
			 LIMIT $2`,
			teams, limit+1,
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("listing agents by teams: %w", err)
	}
	defer rows.Close()

	agents, err := scanAgentRows(rows)
	if err != nil {
		return nil, "", fmt.Errorf("scanning agent rows: %w", err)
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
func (s *Store) ListIDsByTeam(ctx context.Context, team string) ([]string, error) {
	return s.ListIDsByTeams(ctx, []string{team})
}

// ListIDsByTeams returns all agent IDs for the given teams.
func (s *Store) ListIDsByTeams(ctx context.Context, teams []string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM agents WHERE team = ANY($1) AND archived_at IS NULL`, teams)
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

// RotateKey moves the current key to prev_key with a 24h grace period and sets
// the new key as the active key.
func (s *Store) RotateKey(ctx context.Context, id, newHash, newPrefix, newSuffix string) (*Agent, error) {
	a, err := scanAgent(s.pool.QueryRow(ctx,
		`UPDATE agents
		 SET prev_key_hash = api_key_hash,
		     prev_key_prefix = api_key_prefix,
		     prev_key_expires_at = now() + interval '24 hours',
		     api_key_hash = $1,
		     api_key_prefix = $2,
		     api_key_suffix = $3
		 WHERE id = $4 AND archived_at IS NULL
		 RETURNING `+agentColumns,
		newHash, newPrefix, newSuffix, id,
	))
	if err != nil {
		return nil, fmt.Errorf("rotating agent key: %w", err)
	}
	return a, nil
}

// RevokePrevKey immediately clears the previous key for the given agent.
func (s *Store) RevokePrevKey(ctx context.Context, id string) (*Agent, error) {
	a, err := scanAgent(s.pool.QueryRow(ctx,
		`UPDATE agents
		 SET prev_key_hash = NULL,
		     prev_key_prefix = NULL,
		     prev_key_expires_at = NULL
		 WHERE id = $1 AND archived_at IS NULL
		 RETURNING `+agentColumns,
		id,
	))
	if err != nil {
		return nil, fmt.Errorf("revoking prev key: %w", err)
	}
	return a, nil
}

// CleanupExpiredKeys clears prev_key_hash and prev_key_expires_at for all
// agents whose previous key has expired.
func (s *Store) CleanupExpiredKeys(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE agents
		 SET prev_key_hash = NULL, prev_key_prefix = NULL, prev_key_expires_at = NULL
		 WHERE prev_key_expires_at IS NOT NULL AND prev_key_expires_at <= now()`,
	)
	if err != nil {
		return 0, fmt.Errorf("cleaning up expired keys: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RegenerateKey updates the api_key_hash and api_key_prefix for the given agent.
// Deprecated: Use RotateKey for zero-downtime rotation with a grace period.
func (s *Store) RegenerateKey(ctx context.Context, id, newHash, newPrefix, newSuffix string) (*Agent, error) {
	return s.RotateKey(ctx, id, newHash, newPrefix, newSuffix)
}

// Update performs a partial update on the agent with the given id and returns
// the updated record.
func (s *Store) Update(ctx context.Context, id string, in UpdateAgentInput) (*Agent, error) {
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
		return s.GetByID(ctx, id)
	}

	args = append(args, id)
	query := fmt.Sprintf(
		`UPDATE agents SET %s WHERE id = $%d
		 RETURNING `+agentColumns,
		strings.Join(setClauses, ", "), argIdx,
	)

	a, err := scanAgent(s.pool.QueryRow(ctx, query, args...))
	if err != nil {
		return nil, fmt.Errorf("updating agent: %w", err)
	}
	return a, nil
}

// Archive soft-deletes an agent by setting archived_at.
func (s *Store) Archive(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE agents SET archived_at = now() WHERE id = $1 AND archived_at IS NULL`, id)
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
