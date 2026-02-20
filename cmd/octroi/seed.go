package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/config"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var seedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Seed demo data (crypto price tool + test agent)",
	RunE:  runSeed,
}

func init() {
	rootCmd.AddCommand(seedCmd)
}

func runSeed(cmd *cobra.Command, args []string) error {
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

	toolStore := registry.NewStore(pool)
	toolService := registry.NewService(toolStore)
	agentStore := agent.NewStore(pool)

	// Check if demo data already exists.
	existingTools, _, err := toolService.List(ctx, registry.ToolListParams{Query: "CoinGecko", Limit: 1})
	if err != nil {
		return fmt.Errorf("checking existing tools: %w", err)
	}
	existingAgents, _, err := agentStore.List(ctx, agent.AgentListParams{Limit: 1})
	if err != nil {
		return fmt.Errorf("checking existing agents: %w", err)
	}

	if len(existingTools) > 0 && len(existingAgents) > 0 {
		slog.Info("demo data already exists, skipping seed")
		return nil
	}

	var tool *registry.Tool
	if len(existingTools) == 0 {
		tool, err = toolService.Create(ctx, registry.CreateToolInput{
			Name:         "CoinGecko Crypto Prices",
			Description:  "Get cryptocurrency prices, market data, and historical charts. Free API, no authentication required.",
			Endpoint:     "https://api.coingecko.com",
			AuthType:     "none",
			PricingModel: "free",
		})
		if err != nil {
			return fmt.Errorf("creating demo tool: %w", err)
		}
		slog.Info("created demo tool", "id", tool.ID, "name", tool.Name)
	} else {
		tool = existingTools[0]
		slog.Info("demo tool already exists", "id", tool.ID)
	}

	if len(existingAgents) > 0 {
		slog.Info("demo agent already exists, skipping")
		return nil
	}

	apiKey, plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		return fmt.Errorf("generating api key: %w", err)
	}

	ag, err := agentStore.Create(ctx, agent.CreateAgentInput{
		Name:         "demo-agent",
		APIKeyHash:   apiKey.Hash,
		APIKeyPrefix: apiKey.Prefix,
		Team:         "demo",
		RateLimit:    120,
	})
	if err != nil {
		return fmt.Errorf("creating demo agent: %w", err)
	}

	slog.Info("created demo agent", "id", ag.ID, "name", ag.Name)
	fmt.Printf("\n=== Demo Data Seeded ===\n")
	fmt.Printf("Tool:      %s (%s)\n", tool.Name, tool.ID)
	fmt.Printf("Agent:     %s (%s)\n", ag.Name, ag.ID)
	fmt.Printf("API Key:   %s\n", plaintext)
	fmt.Printf("\nTry it:\n")
	fmt.Printf("  curl http://localhost:8080/api/v1/tools/search?q=crypto\n")
	fmt.Printf("  curl -H 'Authorization: Bearer %s' http://localhost:8080/proxy/%s/api/v3/simple/price?ids=bitcoin&vs_currencies=usd\n", plaintext, tool.ID)

	return nil
}
