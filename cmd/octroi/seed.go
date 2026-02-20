package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/config"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/alecgard/octroi/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var seedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Seed demo tools, agents, and users (idempotent)",
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

var seedUsers = []user.CreateUserInput{
	{
		Email:    "admin@octroi.dev",
		Password: "octroi",
		Name:     "Admin",
		Teams:    []user.TeamMembership{},
		Role:     "org_admin",
	},
	{
		Email:    "user1@octroi.dev",
		Password: "octroi",
		Name:     "User One",
		Teams:    []user.TeamMembership{{Team: "alpha", Role: "admin"}},
		Role:     "member",
	},
	{
		Email:    "user2@octroi.dev",
		Password: "octroi",
		Name:     "User Two",
		Teams:    []user.TeamMembership{{Team: "alpha", Role: "member"}},
		Role:     "member",
	},
	{
		Email:    "user3@octroi.dev",
		Password: "octroi",
		Name:     "User Three",
		Teams:    []user.TeamMembership{{Team: "beta", Role: "admin"}},
		Role:     "member",
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
	userStore := user.NewStore(pool)

	// Seed tools (skip each if a tool with that name already exists).
	existingTools, _, _ := toolService.List(ctx, registry.ToolListParams{Limit: 100})
	toolNames := make(map[string]bool, len(existingTools))
	for _, t := range existingTools {
		toolNames[t.Name] = true
	}

	var firstTool *registry.Tool
	for _, input := range demoTools {
		if toolNames[input.Name] {
			slog.Info("tool already exists, skipping", "name", input.Name)
			if firstTool == nil {
				for _, t := range existingTools {
					if t.Name == input.Name {
						firstTool = t
						break
					}
				}
			}
			continue
		}
		t, err := toolService.Create(ctx, input)
		if err != nil {
			return fmt.Errorf("creating tool %q: %w", input.Name, err)
		}
		slog.Info("created tool", "name", t.Name, "id", t.ID)
		if firstTool == nil {
			firstTool = t
		}
	}

	// Seed demo agent (skip if one named "demo-agent" already exists).
	existingAgents, _, _ := agentStore.List(ctx, agent.AgentListParams{Limit: 100})
	hasDemoAgent := false
	for _, a := range existingAgents {
		if a.Name == "demo-agent" {
			hasDemoAgent = true
			break
		}
	}

	if !hasDemoAgent {
		apiKey, plaintext, err := auth.GenerateAPIKey()
		if err != nil {
			return fmt.Errorf("generating api key: %w", err)
		}

		ag, err := agentStore.Create(ctx, agent.CreateAgentInput{
			Name:         "demo-agent",
			APIKeyHash:   apiKey.Hash,
			APIKeyPrefix: apiKey.Prefix,
			Team:         "alpha",
			RateLimit:    120,
		})
		if err != nil {
			return fmt.Errorf("creating demo agent: %w", err)
		}

		slog.Info("created demo agent", "id", ag.ID, "name", ag.Name)
		fmt.Printf("\nDemo Agent: %s (%s)\n", ag.Name, ag.ID)
		fmt.Printf("API Key:    %s\n", plaintext)
		if firstTool != nil {
			fmt.Printf("\nTry it:\n")
			fmt.Printf("  curl -H 'Authorization: Bearer %s' http://localhost:8080/proxy/%s/api/v3/simple/price?ids=bitcoin&vs_currencies=usd\n", plaintext, firstTool.ID)
		}

		if err := setEnvKey(".env", "OCTROI_DEMO_AGENT_KEY", plaintext); err != nil {
			slog.Warn("could not write demo agent key to .env", "error", err)
		} else {
			slog.Info("wrote OCTROI_DEMO_AGENT_KEY to .env")
		}
	} else {
		slog.Info("demo-agent already exists, skipping")
	}

	// Seed users (skip each if email already exists).
	for _, input := range seedUsers {
		_, err := userStore.GetByEmail(ctx, input.Email)
		if err == nil {
			slog.Info("user already exists, skipping", "email", input.Email)
			continue
		}
		u, err := userStore.Create(ctx, input)
		if err != nil {
			return fmt.Errorf("creating user %q: %w", input.Email, err)
		}
		slog.Info("created user", "email", u.Email, "role", u.Role, "teams", u.Teams)
	}

	fmt.Printf("\n=== Seed Complete ===\n")
	fmt.Printf("Tools: %d configured\n", len(demoTools))
	fmt.Printf("Users:\n")
	fmt.Printf("  admin@octroi.dev  (org_admin, no teams)\n")
	fmt.Printf("  user1@octroi.dev  (member, teams: [alpha:admin])\n")
	fmt.Printf("  user2@octroi.dev  (member, teams: [alpha:member])\n")
	fmt.Printf("  user3@octroi.dev  (member, teams: [beta:admin])\n")
	fmt.Printf("  Password for all: octroi\n")

	return nil
}

// setEnvKey upserts a KEY=value line in a .env file.
func setEnvKey(path, key, value string) error {
	line := key + "=" + value
	prefix := key + "="

	// Read existing content.
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var lines []string
	replaced := false
	if len(content) > 0 {
		sc := bufio.NewScanner(strings.NewReader(string(content)))
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), prefix) {
				lines = append(lines, line)
				replaced = true
			} else {
				lines = append(lines, sc.Text())
			}
		}
	}
	if !replaced {
		lines = append(lines, line)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
