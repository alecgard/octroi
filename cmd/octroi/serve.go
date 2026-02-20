package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/api"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/config"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/proxy"
	"github.com/alecgard/octroi/internal/ratelimit"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/alecgard/octroi/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Octroi gateway server",
	RunE:  runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return err
	}
	slog.Info("connected to database")

	toolStore := registry.NewStore(pool)
	toolService := registry.NewService(toolStore)
	agentStore := agent.NewStore(pool)
	budgetStore := agent.NewBudgetStore(pool)
	meterStore := metering.NewStore(pool)
	collector := metering.NewCollector(meterStore, cfg.Metering.BatchSize, cfg.Metering.FlushInterval)
	go collector.Start(ctx)

	userStore := user.NewStore(pool)

	limiter := ratelimit.New(cfg.RateLimit.Default, cfg.RateLimit.Window)
	authService := auth.NewService(agent.NewAuthAdapter(agentStore))

	proxyHandler := proxy.NewHandler(toolStore, budgetStore, collector, cfg.Proxy.Timeout, cfg.Proxy.MaxRequestSize)

	router := api.NewRouter(api.RouterDeps{
		ToolService: toolService,
		ToolStore:   toolStore,
		AgentStore:  agentStore,
		BudgetStore: budgetStore,
		MeterStore:  meterStore,
		Collector:   collector,
		Auth:        authService,
		Limiter:     limiter,
		Proxy:       proxyHandler,
		AdminKey:    cfg.Auth.AdminKey,
		UserStore:   userStore,
	})

	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("server starting", "addr", cfg.Addr())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-sigCh
	slog.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	collector.Stop()

	return srv.Shutdown(shutdownCtx)
}
