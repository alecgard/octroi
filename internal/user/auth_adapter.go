package user

import (
	"context"

	"github.com/alecgard/octroi/internal/auth"
)

// AuthAdapter adapts user.Store to the auth.SessionLookup interface.
type AuthAdapter struct {
	store *Store
}

// NewAuthAdapter creates a new AuthAdapter wrapping the given user store.
func NewAuthAdapter(store *Store) *AuthAdapter {
	return &AuthAdapter{store: store}
}

// LookupSession looks up a session token and returns the associated auth.User.
func (a *AuthAdapter) LookupSession(ctx context.Context, token string) (*auth.User, error) {
	u, err := a.store.GetSessionUser(ctx, token)
	if err != nil {
		return nil, err
	}
	teams := make([]auth.TeamMembership, len(u.Teams))
	for i, tm := range u.Teams {
		teams[i] = auth.TeamMembership{
			Team: tm.Team,
			Role: tm.Role,
		}
	}
	return &auth.User{
		ID:    u.ID,
		Email: u.Email,
		Name:  u.Name,
		Teams: teams,
		Role:  u.Role,
	}, nil
}
