package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/alecgard/octroi/internal/config"
	"github.com/alecgard/octroi/internal/tenant"
	"github.com/alecgard/octroi/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var ensureAdminCmd = &cobra.Command{
	Use:   "ensure-admin",
	Short: "Ensure the default admin account exists",
	RunE:  runEnsureAdmin,
}

func init() {
	rootCmd.AddCommand(ensureAdminCmd)
}

func runEnsureAdmin(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Ensure a tenant exists.
	tenantName := os.Getenv("OCTROI_TENANT_NAME")
	if tenantName == "" {
		tenantName = "Local"
	}
	tenantSlug := os.Getenv("OCTROI_TENANT_SLUG")
	if tenantSlug == "" {
		tenantSlug = "local"
	}

	tenantStore := tenant.NewStore(pool)
	t, err := tenantStore.GetBySlug(ctx, tenantSlug)
	if err != nil {
		t, err = tenantStore.Create(ctx, tenantName, tenantSlug, "free")
		if err != nil {
			return fmt.Errorf("creating tenant: %w", err)
		}
		slog.Info("created tenant", "name", t.Name, "slug", t.Slug, "id", t.ID)
	} else {
		slog.Info("tenant already exists", "name", t.Name, "slug", t.Slug, "id", t.ID)
	}

	userStore := user.NewStore(pool)
	input := user.CreateUserInput{
		Email:    "admin@octroi.dev",
		Password: "octroi",
		Name:     "Admin",
		Teams:    []user.TeamMembership{},
		Role:     "org_admin",
		TenantID: t.ID,
	}
	_, err = userStore.GetByEmail(ctx, t.ID, input.Email)
	if err == nil {
		slog.Info("admin already exists, skipping")
		return nil
	}
	u, err := userStore.Create(ctx, input)
	if err != nil {
		return fmt.Errorf("creating admin: %w", err)
	}
	slog.Info("created admin", "email", u.Email, "tenant_id", t.ID)
	return nil
}
