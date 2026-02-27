package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Permission represents a single agent-tool permission record.
type Permission struct {
	ID       string   `json:"id"`
	AgentID  string   `json:"agent_id"`
	ToolID   string   `json:"tool_id"`
	Allowed  bool     `json:"allowed"`
	SubTools []string `json:"sub_tools,omitempty"`
}

// BulkPermission represents a permission entry for bulk set operations.
type BulkPermission struct {
	Allowed  bool     `json:"allowed"`
	SubTools []string `json:"sub_tools,omitempty"`
}

// PermissionStore provides database operations for agent tool permissions.
type PermissionStore struct {
	pool       *pgxpool.Pool
	agentStore *Store
}

// NewPermissionStore creates a new PermissionStore.
func NewPermissionStore(pool *pgxpool.Pool, agentStore *Store) *PermissionStore {
	return &PermissionStore{pool: pool, agentStore: agentStore}
}

// IsAllowed checks if an agent is permitted to use a tool.
// If the agent's allowlist_mode is false, all tools are allowed.
// If true, only tools with an explicit allowed=true row are permitted.
func (s *PermissionStore) IsAllowed(ctx context.Context, tenantID string, agentID, toolID string) (bool, error) {
	agent, err := s.agentStore.GetByID(ctx, tenantID, agentID)
	if err != nil {
		return false, fmt.Errorf("looking up agent: %w", err)
	}
	if !agent.AllowlistMode {
		return true, nil
	}

	var allowed bool
	err = s.pool.QueryRow(ctx,
		`SELECT allowed FROM agent_tool_permissions WHERE agent_id = $1 AND tool_id = $2 AND tenant_id = $3`,
		agentID, toolID, tenantID,
	).Scan(&allowed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("checking permission: %w", err)
	}
	return allowed, nil
}

// IsSubToolAllowed checks if an agent is permitted to use a specific sub-tool of a tool.
// Returns true if:
//   - The agent's allowlist_mode is false (all tools allowed)
//   - The tool has an allowed=true row AND (sub_tools is empty OR subTool is in sub_tools OR subTool is "")
func (s *PermissionStore) IsSubToolAllowed(ctx context.Context, tenantID string, agentID, toolID, subTool string) (bool, error) {
	agent, err := s.agentStore.GetByID(ctx, tenantID, agentID)
	if err != nil {
		return false, fmt.Errorf("looking up agent: %w", err)
	}
	if !agent.AllowlistMode {
		return true, nil
	}

	var allowed bool
	var subTools []string
	err = s.pool.QueryRow(ctx,
		`SELECT allowed, sub_tools FROM agent_tool_permissions WHERE agent_id = $1 AND tool_id = $2 AND tenant_id = $3`,
		agentID, toolID, tenantID,
	).Scan(&allowed, &subTools)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("checking permission: %w", err)
	}
	if !allowed {
		return false, nil
	}
	// Empty sub_tools or empty subTool name means all sub-tools allowed.
	if len(subTools) == 0 || subTool == "" {
		return true, nil
	}
	for _, st := range subTools {
		if st == subTool {
			return true, nil
		}
	}
	return false, nil
}

// SetWithSubTools upserts a permission for an agent-tool pair including sub-tools.
func (s *PermissionStore) SetWithSubTools(ctx context.Context, tenantID string, agentID, toolID string, allowed bool, subTools []string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_tool_permissions (agent_id, tool_id, allowed, sub_tools, tenant_id)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (agent_id, tool_id) DO UPDATE SET allowed = $3, sub_tools = $4`,
		agentID, toolID, allowed, subTools, tenantID,
	)
	if err != nil {
		return fmt.Errorf("setting permission: %w", err)
	}
	return nil
}

// Delete removes a permission for an agent-tool pair.
func (s *PermissionStore) Delete(ctx context.Context, tenantID string, agentID, toolID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM agent_tool_permissions WHERE agent_id = $1 AND tool_id = $2 AND tenant_id = $3`,
		agentID, toolID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("deleting permission: %w", err)
	}
	return nil
}

// ListByAgent returns all permissions for the given agent.
func (s *PermissionStore) ListByAgent(ctx context.Context, tenantID string, agentID string) ([]Permission, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, agent_id, tool_id, allowed, sub_tools FROM agent_tool_permissions WHERE agent_id = $1 AND tenant_id = $2 ORDER BY created_at DESC`,
		agentID, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing permissions: %w", err)
	}
	defer rows.Close()

	var perms []Permission
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.ID, &p.AgentID, &p.ToolID, &p.Allowed, &p.SubTools); err != nil {
			return nil, fmt.Errorf("scanning permission: %w", err)
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

// SetBulk sets permissions for an agent in bulk, upserting each entry.
func (s *PermissionStore) SetBulk(ctx context.Context, tenantID string, agentID string, permissions map[string]bool) error {
	if len(permissions) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for toolID, allowed := range permissions {
		_, err := tx.Exec(ctx,
			`INSERT INTO agent_tool_permissions (agent_id, tool_id, allowed, tenant_id)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (agent_id, tool_id) DO UPDATE SET allowed = $3`,
			agentID, toolID, allowed, tenantID,
		)
		if err != nil {
			return fmt.Errorf("setting permission for tool %s: %w", toolID, err)
		}
	}

	return tx.Commit(ctx)
}

// SetBulkWithSubTools sets permissions with sub-tools for an agent in bulk.
func (s *PermissionStore) SetBulkWithSubTools(ctx context.Context, tenantID string, agentID string, permissions map[string]BulkPermission) error {
	if len(permissions) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for toolID, perm := range permissions {
		_, err := tx.Exec(ctx,
			`INSERT INTO agent_tool_permissions (agent_id, tool_id, allowed, sub_tools, tenant_id)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (agent_id, tool_id) DO UPDATE SET allowed = $3, sub_tools = $4`,
			agentID, toolID, perm.Allowed, perm.SubTools, tenantID,
		)
		if err != nil {
			return fmt.Errorf("setting permission for tool %s: %w", toolID, err)
		}
	}

	return tx.Commit(ctx)
}
