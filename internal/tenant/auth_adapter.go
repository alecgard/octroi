package tenant

import (
	"context"

	"github.com/alecgard/octroi/internal/auth"
)

// AuthAdapter adapts tenant.Store to the auth.TenantLookup interface.
type AuthAdapter struct {
	store *Store
}

// NewAuthAdapter creates a new AuthAdapter wrapping the given tenant store.
func NewAuthAdapter(store *Store) *AuthAdapter {
	return &AuthAdapter{store: store}
}

// GetBySlug looks up a tenant by slug and returns an auth.Tenant.
func (a *AuthAdapter) GetBySlug(ctx context.Context, slug string) (*auth.Tenant, error) {
	t, err := a.store.GetBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	return &auth.Tenant{
		ID:   t.ID,
		Name: t.Name,
		Slug: t.Slug,
		Plan: t.Plan,
	}, nil
}
