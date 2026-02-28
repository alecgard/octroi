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
// It checks the active key first, then falls back to the previous (expiring) key.
func (a *AuthAdapter) GetByKeyHash(ctx context.Context, hash string) (*auth.Agent, error) {
	ag, err := a.store.GetByKeyHash(ctx, hash)
	if err != nil {
		// Primary key not found; try the previous (expiring) key.
		ag, prevErr := a.store.GetByPrevKeyHash(ctx, hash)
		if prevErr != nil {
			return nil, err // return original error
		}
		return agentToAuth(ag), nil
	}
	return agentToAuth(ag), nil
}

func agentToAuth(ag *Agent) *auth.Agent {
	return &auth.Agent{
		ID:        ag.ID,
		Name:      ag.Name,
		Team:      ag.Team,
		RateLimit: ag.RateLimit,
	}
}
