package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/config"
	"github.com/alecgard/octroi/internal/crypto"
	"github.com/alecgard/octroi/internal/metering"
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

var ensureAdminCmd = &cobra.Command{
	Use:   "ensure-admin",
	Short: "Ensure the default admin account exists",
	RunE:  runEnsureAdmin,
}

func init() {
	rootCmd.AddCommand(seedCmd)
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

	userStore := user.NewStore(pool)
	input := user.CreateUserInput{
		Email:    "admin@octroi.dev",
		Password: "octroi",
		Name:     "Admin",
		Teams:    []user.TeamMembership{},
		Role:     "org_admin",
	}
	_, err = userStore.GetByEmail(ctx, input.Email)
	if err == nil {
		slog.Info("admin already exists, skipping")
		return nil
	}
	u, err := userStore.Create(ctx, input)
	if err != nil {
		return fmt.Errorf("creating admin: %w", err)
	}
	slog.Info("created admin", "email", u.Email)
	return nil
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

	cipher, err := crypto.NewCipher(cfg.Encryption.Key)
	if err != nil {
		return fmt.Errorf("initializing encryption: %w", err)
	}

	toolStore := registry.NewStore(pool, cipher)
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

	// Seed demo agents (skip each if already exists).
	demoAgents := []struct {
		Name    string
		Team    string
		EnvKey  string
	}{
		{"demo-agent", "alpha", "OCTROI_DEMO_AGENT_KEY"},
		{"scraper-bot", "beta", ""},
	}

	existingAgents, _, _ := agentStore.List(ctx, agent.AgentListParams{Limit: 100})
	agentByName := make(map[string]*agent.Agent, len(existingAgents))
	for _, a := range existingAgents {
		agentByName[a.Name] = a
	}

	for _, da := range demoAgents {
		if existing, ok := agentByName[da.Name]; ok {
			slog.Info("agent already exists, skipping", "name", da.Name)
			_ = existing
			continue
		}
		apiKey, plaintext, err := auth.GenerateAPIKey()
		if err != nil {
			return fmt.Errorf("generating api key: %w", err)
		}
		ag, err := agentStore.Create(ctx, agent.CreateAgentInput{
			Name:         da.Name,
			APIKeyHash:   apiKey.Hash,
			APIKeyPrefix: apiKey.Prefix,
			Team:         da.Team,
			RateLimit:    120,
		})
		if err != nil {
			return fmt.Errorf("creating agent %q: %w", da.Name, err)
		}
		agentByName[da.Name] = ag
		slog.Info("created agent", "id", ag.ID, "name", ag.Name)
		fmt.Printf("\nAgent: %s (%s)\n", ag.Name, ag.ID)
		fmt.Printf("API Key: %s\n", plaintext)
		if da.EnvKey != "" {
			if err := setEnvKey(".env", da.EnvKey, plaintext); err != nil {
				slog.Warn("could not write agent key to .env", "error", err)
			} else {
				slog.Info("wrote agent key to .env", "key", da.EnvKey)
			}
		}
		if firstTool != nil && da.Name == "demo-agent" {
			fmt.Printf("\nTry it:\n")
			fmt.Printf("  curl -H 'Authorization: Bearer %s' http://localhost:8080/proxy/%s/api/v3/simple/price?ids=bitcoin&vs_currencies=usd\n", plaintext, firstTool.ID)
		}
	}

	// Seed sample transactions (spread over the last 24 hours).
	meterStore := metering.NewStore(pool)
	// Refresh tool list to get all tool IDs.
	allTools, _, _ := toolService.List(ctx, registry.ToolListParams{Limit: 100})
	if len(allTools) > 0 && len(agentByName) > 0 {
		var agents []*agent.Agent
		for _, a := range agentByName {
			agents = append(agents, a)
		}

		methods := []string{"GET", "GET", "GET", "POST"}
		paths := []string{"/api/v1/data", "/api/v1/query", "/api/v1/search", "/api/v1/submit"}
		statuses := []int{200, 200, 200, 200, 200, 200, 200, 200, 201, 400, 500}

		rng := rand.New(rand.NewSource(42))
		now := time.Now()
		var txns []metering.Transaction

		for i := 0; i < 120; i++ {
			ag := agents[rng.Intn(len(agents))]
			tool := allTools[rng.Intn(len(allTools))]
			status := statuses[rng.Intn(len(statuses))]
			method := methods[rng.Intn(len(methods))]
			path := paths[rng.Intn(len(paths))]
			latency := int64(20 + rng.Intn(480))
			cost := float64(rng.Intn(50)) / 10000.0
			ts := now.Add(-time.Duration(rng.Intn(24*60)) * time.Minute)

			txns = append(txns, metering.Transaction{
				AgentID:      ag.ID,
				ToolID:       tool.ID,
				Timestamp:    ts,
				Method:       method,
				Path:         path,
				StatusCode:   status,
				LatencyMs:    latency,
				RequestSize:  int64(100 + rng.Intn(900)),
				ResponseSize: int64(200 + rng.Intn(4800)),
				Success:      status < 400,
				Cost:         cost,
			})
		}

		if err := meterStore.BatchInsert(ctx, txns); err != nil {
			slog.Warn("could not seed transactions", "error", err)
		} else {
			slog.Info("seeded transactions", "count", len(txns))
		}
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
