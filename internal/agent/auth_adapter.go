package agent

import (
	"context"

	"github.com/alecgard/octroi/internal/auth"
)

// AuthAdapter wraps an agent Store to satisfy auth.AgentLookup.
type AuthAdapter struct {
	store *Store
}

// NewAuthAdapter creates an adapter that bridges agent.Store to auth.AgentLookup.
func NewAuthAdapter(store *Store) *AuthAdapter {
	return &AuthAdapter{store: store}
}

// GetByKeyHash looks up an agent by API key hash and converts to auth.Agent.
func (a *AuthAdapter) GetByKeyHash(ctx context.Context, hash string) (*auth.Agent, error) {
	ag, err := a.store.GetByKeyHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	return &auth.Agent{
		ID:        ag.ID,
		Name:      ag.Name,
		Team:      ag.Team,
		RateLimit: ag.RateLimit,
	}, nil
}
