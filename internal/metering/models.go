package metering

import "time"

// Transaction represents a single API call record in the metering system.
type Transaction struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	ToolID       string    `json:"tool_id"`
	Timestamp    time.Time `json:"timestamp"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	StatusCode   int       `json:"status_code"`
	LatencyMs    int64     `json:"latency_ms"`
	RequestSize  int64     `json:"request_size"`
	ResponseSize int64     `json:"response_size"`
	Success      bool      `json:"success"`
	Cost         float64   `json:"cost"`
	CostSource   string    `json:"cost_source"`
	Error        string    `json:"error"`
}

// UsageSummary holds aggregate metrics for a set of transactions.
type UsageSummary struct {
	TotalRequests int64   `json:"total_requests"`
	TotalCost     float64 `json:"total_cost"`
	SuccessCount  int64   `json:"success_count"`
	ErrorCount    int64   `json:"error_count"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
}

// UsageQuery defines filters and pagination for querying transactions.
type UsageQuery struct {
	AgentID  string    `json:"agent_id,omitempty"`
	AgentIDs []string  `json:"agent_ids,omitempty"` // for team-scoped queries
	ToolID   string    `json:"tool_id,omitempty"`
	ToolIDs  []string  `json:"tool_ids,omitempty"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Cursor   string    `json:"cursor,omitempty"`
	Limit    int       `json:"limit"`
}
