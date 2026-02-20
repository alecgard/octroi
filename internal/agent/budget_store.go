package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BudgetStore provides database operations for agent-tool budgets.
type BudgetStore struct {
	pool *pgxpool.Pool
}

// NewBudgetStore creates a new budget store backed by the given connection pool.
func NewBudgetStore(pool *pgxpool.Pool) *BudgetStore {
	return &BudgetStore{pool: pool}
}

// Set upserts a budget for the given agent/tool combination.
func (s *BudgetStore) Set(ctx context.Context, in CreateBudgetInput) (*Budget, error) {
	b := &Budget{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO agent_tool_budgets (agent_id, tool_id, daily_limit, monthly_limit)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (agent_id, tool_id)
		 DO UPDATE SET daily_limit = EXCLUDED.daily_limit, monthly_limit = EXCLUDED.monthly_limit
		 RETURNING id, agent_id, tool_id, daily_limit, monthly_limit`,
		in.AgentID, in.ToolID, in.DailyLimit, in.MonthlyLimit,
	).Scan(&b.ID, &b.AgentID, &b.ToolID, &b.DailyLimit, &b.MonthlyLimit)
	if err != nil {
		return nil, fmt.Errorf("upserting budget: %w", err)
	}
	return b, nil
}

// Get retrieves a budget for the given agent and tool.
func (s *BudgetStore) Get(ctx context.Context, agentID, toolID string) (*Budget, error) {
	b := &Budget{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, agent_id, tool_id, daily_limit, monthly_limit
		 FROM agent_tool_budgets
		 WHERE agent_id = $1 AND tool_id = $2`,
		agentID, toolID,
	).Scan(&b.ID, &b.AgentID, &b.ToolID, &b.DailyLimit, &b.MonthlyLimit)
	if err != nil {
		return nil, fmt.Errorf("getting budget: %w", err)
	}
	return b, nil
}

// ListByAgent returns all budgets for the given agent.
func (s *BudgetStore) ListByAgent(ctx context.Context, agentID string) ([]*Budget, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, agent_id, tool_id, daily_limit, monthly_limit
		 FROM agent_tool_budgets
		 WHERE agent_id = $1
		 ORDER BY tool_id`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing budgets: %w", err)
	}
	defer rows.Close()

	var budgets []*Budget
	for rows.Next() {
		b := &Budget{}
		if err := rows.Scan(&b.ID, &b.AgentID, &b.ToolID, &b.DailyLimit, &b.MonthlyLimit); err != nil {
			return nil, fmt.Errorf("scanning budget row: %w", err)
		}
		budgets = append(budgets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating budget rows: %w", err)
	}
	return budgets, nil
}

// Delete removes a budget for the given agent and tool.
func (s *BudgetStore) Delete(ctx context.Context, agentID, toolID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM agent_tool_budgets WHERE agent_id = $1 AND tool_id = $2`,
		agentID, toolID,
	)
	if err != nil {
		return fmt.Errorf("deleting budget: %w", err)
	}
	return nil
}

// CheckBudget verifies whether the agent is within its daily and monthly budget
// for the given tool. A limit of 0 means unlimited. It returns whether the
// request is allowed, plus the remaining daily and monthly amounts.
func (s *BudgetStore) CheckBudget(ctx context.Context, agentID, toolID string) (allowed bool, remainingDaily float64, remainingMonthly float64, err error) {
	budget, err := s.Get(ctx, agentID, toolID)
	if err != nil {
		return false, 0, 0, fmt.Errorf("checking budget: %w", err)
	}

	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var dailySpend, monthlySpend float64

	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost), 0)
		 FROM transactions
		 WHERE agent_id = $1 AND tool_id = $2 AND timestamp >= $3`,
		agentID, toolID, startOfDay,
	).Scan(&dailySpend)
	if err != nil {
		return false, 0, 0, fmt.Errorf("summing daily spend: %w", err)
	}

	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost), 0)
		 FROM transactions
		 WHERE agent_id = $1 AND tool_id = $2 AND timestamp >= $3`,
		agentID, toolID, startOfMonth,
	).Scan(&monthlySpend)
	if err != nil {
		return false, 0, 0, fmt.Errorf("summing monthly spend: %w", err)
	}

	allowed = true

	if budget.DailyLimit > 0 {
		remainingDaily = budget.DailyLimit - dailySpend
		if remainingDaily < 0 {
			remainingDaily = 0
		}
		if dailySpend >= budget.DailyLimit {
			allowed = false
		}
	}

	if budget.MonthlyLimit > 0 {
		remainingMonthly = budget.MonthlyLimit - monthlySpend
		if remainingMonthly < 0 {
			remainingMonthly = 0
		}
		if monthlySpend >= budget.MonthlyLimit {
			allowed = false
		}
	}

	return allowed, remainingDaily, remainingMonthly, nil
}

// CheckToolGlobalBudget checks whether the total spend for a tool across all
// agents is within the tool's configured budget_limit and budget_window.
func (s *BudgetStore) CheckToolGlobalBudget(ctx context.Context, toolID string) (allowed bool, remaining float64, err error) {
	var budgetLimit float64
	var budgetWindow string

	err = s.pool.QueryRow(ctx,
		`SELECT budget_limit, budget_window FROM tools WHERE id = $1`,
		toolID,
	).Scan(&budgetLimit, &budgetWindow)
	if err != nil {
		return false, 0, fmt.Errorf("getting tool budget config: %w", err)
	}

	// A limit of 0 means unlimited.
	if budgetLimit == 0 {
		return true, 0, nil
	}

	now := time.Now().UTC()
	var windowStart time.Time
	switch budgetWindow {
	case "daily":
		windowStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "monthly":
		windowStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		// Default to monthly if window is unrecognized.
		windowStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}

	var totalSpend float64
	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost), 0)
		 FROM transactions
		 WHERE tool_id = $1 AND timestamp >= $2`,
		toolID, windowStart,
	).Scan(&totalSpend)
	if err != nil {
		return false, 0, fmt.Errorf("summing tool global spend: %w", err)
	}

	remaining = budgetLimit - totalSpend
	if remaining < 0 {
		remaining = 0
	}

	allowed = totalSpend < budgetLimit
	return allowed, remaining, nil
}
