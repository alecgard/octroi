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
	Short: "Seed demo tools and a test agent",
	RunE:  runSeed,
}

func init() {
	rootCmd.AddCommand(seedCmd)
}

var demoTools = []registry.CreateToolInput{
	{
		Name:         "CoinGecko Crypto Prices",
		Description:  "Cryptocurrency prices, market data, and historical charts from CoinGecko.",
		Endpoint:     "https://api.coingecko.com",
		AuthType:     "none",
		PricingModel: "free",
	},
	{
		Name:         "Open-Meteo Weather",
		Description:  "Weather forecasts, current conditions, and historical weather data. Global coverage, high resolution.",
		Endpoint:     "https://api.open-meteo.com",
		AuthType:     "none",
		PricingModel: "free",
	},
	{
		Name:         "REST Countries",
		Description:  "Country data: population, currencies, languages, borders, timezones, and calling codes.",
		Endpoint:     "https://restcountries.com",
		AuthType:     "none",
		PricingModel: "free",
	},
	{
		Name:         "Exchange Rates",
		Description:  "Latest and historical foreign exchange rates published by the European Central Bank.",
		Endpoint:     "https://api.frankfurter.app",
		AuthType:     "none",
		PricingModel: "free",
	},
	{
		Name:         "HTTPBin",
		Description:  "HTTP request and response inspection service. Useful for testing and debugging HTTP clients.",
		Endpoint:     "https://httpbin.org",
		AuthType:     "none",
		PricingModel: "free",
	},
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

	// Check if seed has already run.
	existing, _, err := toolService.List(ctx, registry.ToolListParams{Limit: 1})
	if err != nil {
		return fmt.Errorf("checking existing tools: %w", err)
	}
	if len(existing) > 0 {
		slog.Info("demo data already exists, skipping seed")
		return nil
	}

	// Create tools.
	var firstTool *registry.Tool
	for _, input := range demoTools {
		t, err := toolService.Create(ctx, input)
		if err != nil {
			return fmt.Errorf("creating tool %q: %w", input.Name, err)
		}
		slog.Info("created tool", "name", t.Name, "id", t.ID)
		if firstTool == nil {
			firstTool = t
		}
	}

	// Create demo agent.
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
	fmt.Printf("Tools:     %d registered\n", len(demoTools))
	fmt.Printf("Agent:     %s (%s)\n", ag.Name, ag.ID)
	fmt.Printf("API Key:   %s\n", plaintext)
	fmt.Printf("\nTry it:\n")
	fmt.Printf("  curl http://localhost:8080/api/v1/tools/search?q=weather\n")
	fmt.Printf("  curl -H 'Authorization: Bearer %s' http://localhost:8080/proxy/%s/api/v3/simple/price?ids=bitcoin&vs_currencies=usd\n", plaintext, firstTool.ID)

	return nil
}
