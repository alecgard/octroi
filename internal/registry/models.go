package registry

import "time"

// Tool represents a tool registered in the Octroi gateway.
type Tool struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Mode            string            `json:"mode"`
	Endpoint        string            `json:"-"`
	AuthType        string            `json:"auth_type"`
	AuthConfig      map[string]string `json:"-"`
	Variables       map[string]string `json:"-"`
	PricingModel    string            `json:"pricing_model"`
	PricingAmount   float64           `json:"pricing_amount"`
	PricingCurrency string            `json:"pricing_currency"`
	RateLimit       int               `json:"rate_limit"`
	BudgetLimit     float64           `json:"budget_limit"`
	BudgetWindow    string            `json:"budget_window"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// CreateToolInput holds the fields required to create a new tool.
type CreateToolInput struct {
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Mode            string            `json:"mode"`
	Endpoint        string            `json:"endpoint"`
	AuthType        string            `json:"auth_type"`
	AuthConfig      map[string]string `json:"auth_config"`
	Variables       map[string]string `json:"variables"`
	PricingModel    string            `json:"pricing_model"`
	PricingAmount   float64           `json:"pricing_amount"`
	PricingCurrency string            `json:"pricing_currency"`
	RateLimit       int               `json:"rate_limit"`
	BudgetLimit     float64           `json:"budget_limit"`
	BudgetWindow    string            `json:"budget_window"`
}

// UpdateToolInput holds the fields that can be updated on a tool.
// All fields are optional; only non-nil fields are applied.
type UpdateToolInput struct {
	Name            *string            `json:"name"`
	Description     *string            `json:"description"`
	Mode            *string            `json:"mode"`
	Endpoint        *string            `json:"endpoint"`
	AuthType        *string            `json:"auth_type"`
	AuthConfig      *map[string]string `json:"auth_config"`
	Variables       *map[string]string `json:"variables"`
	PricingModel    *string            `json:"pricing_model"`
	PricingAmount   *float64           `json:"pricing_amount"`
	PricingCurrency *string            `json:"pricing_currency"`
	RateLimit       *int               `json:"rate_limit"`
	BudgetLimit     *float64           `json:"budget_limit"`
	BudgetWindow    *string            `json:"budget_window"`
}

// ToolListParams controls listing and pagination of tools.
type ToolListParams struct {
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
	Query  string `json:"query"`
}
