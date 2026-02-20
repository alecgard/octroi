package agent

import "time"

// Agent represents a registered API agent.
type Agent struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	APIKeyHash   string    `json:"-"`
	APIKeyPrefix string    `json:"api_key_prefix"`
	Team         string    `json:"team"`
	RateLimit    int       `json:"rate_limit"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateAgentInput holds the fields required to create a new agent.
type CreateAgentInput struct {
	Name         string `json:"name"`
	APIKeyHash   string `json:"api_key_hash"`
	APIKeyPrefix string `json:"api_key_prefix"`
	Team         string `json:"team"`
	RateLimit    int    `json:"rate_limit"`
}

// UpdateAgentInput holds optional fields for a partial agent update.
type UpdateAgentInput struct {
	Name      *string `json:"name,omitempty"`
	Team      *string `json:"team,omitempty"`
	RateLimit *int    `json:"rate_limit,omitempty"`
}

// AgentListParams controls cursor-based pagination for listing agents.
type AgentListParams struct {
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

// Budget represents a per-agent, per-tool spending limit.
type Budget struct {
	ID           string  `json:"id"`
	AgentID      string  `json:"agent_id"`
	ToolID       string  `json:"tool_id"`
	DailyLimit   float64 `json:"daily_limit"`
	MonthlyLimit float64 `json:"monthly_limit"`
}

// CreateBudgetInput holds the fields required to create or upsert a budget.
type CreateBudgetInput struct {
	AgentID      string  `json:"agent_id"`
	ToolID       string  `json:"tool_id"`
	DailyLimit   float64 `json:"daily_limit"`
	MonthlyLimit float64 `json:"monthly_limit"`
}

// UsageSummary holds aggregated usage data for an agent or tool.
type UsageSummary struct {
	TotalCost     float64 `json:"total_cost"`
	TotalRequests int64   `json:"total_requests"`
	Period        string  `json:"period"`
}
