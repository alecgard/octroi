package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Tenant represents a tenant in the multi-tenant system.
type Tenant struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Slug       string          `json:"slug"`
	Plan       string          `json:"plan"`
	Settings   json.RawMessage `json:"settings"`
	CreatedAt  time.Time       `json:"created_at"`
	ArchivedAt *time.Time      `json:"archived_at,omitempty"`
}

// Store provides database operations for tenants.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new tenant store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new tenant and returns the created record.
func (s *Store) Create(ctx context.Context, name, slug, plan string) (*Tenant, error) {
	if plan == "" {
		plan = "free"
	}
	t := &Tenant{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO tenants (name, slug, plan)
		 VALUES ($1, $2, $3)
		 RETURNING id, name, slug, plan, settings, created_at`,
		name, slug, plan,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Settings, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("creating tenant: %w", err)
	}
	return t, nil
}

// GetByID retrieves an active (non-archived) tenant by its primary key.
func (s *Store) GetByID(ctx context.Context, id string) (*Tenant, error) {
	t := &Tenant{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, slug, plan, settings, created_at
		 FROM tenants WHERE id = $1 AND archived_at IS NULL`, id,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Settings, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting tenant by id: %w", err)
	}
	return t, nil
}

// GetBySlug retrieves an active (non-archived) tenant by its unique slug.
func (s *Store) GetBySlug(ctx context.Context, slug string) (*Tenant, error) {
	t := &Tenant{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, slug, plan, settings, created_at
		 FROM tenants WHERE slug = $1 AND archived_at IS NULL`, slug,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Settings, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting tenant by slug: %w", err)
	}
	return t, nil
}

// List returns all active tenants ordered by created_at DESC.
func (s *Store) List(ctx context.Context) ([]*Tenant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, slug, plan, settings, created_at
		 FROM tenants WHERE archived_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*Tenant
	for rows.Next() {
		t := &Tenant{}
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Settings, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning tenant row: %w", err)
		}
		tenants = append(tenants, t)
	}
	return tenants, rows.Err()
}

// Update performs a partial update on the tenant with the given id.
func (s *Store) Update(ctx context.Context, id, name, plan string, settings json.RawMessage) (*Tenant, error) {
	t := &Tenant{}
	err := s.pool.QueryRow(ctx,
		`UPDATE tenants SET name = $1, plan = $2, settings = $3
		 WHERE id = $4 AND archived_at IS NULL
		 RETURNING id, name, slug, plan, settings, created_at`,
		name, plan, settings, id,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Settings, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("updating tenant: %w", err)
	}
	return t, nil
}

// Archive soft-deletes a tenant by setting archived_at.
func (s *Store) Archive(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants SET archived_at = now() WHERE id = $1 AND archived_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("archiving tenant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}
