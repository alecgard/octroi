package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/proxy"
	"github.com/alecgard/octroi/internal/ratelimit"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// RouterDeps holds all dependencies for the API router.
type RouterDeps struct {
	ToolService *registry.Service
	ToolStore   *registry.Store
	AgentStore  *agent.Store
	BudgetStore *agent.BudgetStore
	MeterStore  *metering.Store
	Collector   *metering.Collector
	Auth        *auth.Service
	Limiter     *ratelimit.Limiter
	Proxy       *proxy.Handler
	AdminKey    string
}

// NewRouter builds the chi router with all routes and middleware.
func NewRouter(deps RouterDeps) http.Handler {
	r := chi.NewRouter()

	// Global middleware.
	r.Use(chimw.Recoverer)
	r.Use(slogRequestLogger)

	// Handlers.
	tools := newToolsHandler(deps.ToolService)
	agents := newAgentsHandler(deps.AgentStore, deps.BudgetStore)
	search := newSearchHandler(deps.ToolService)
	usage := newUsageHandler(deps.MeterStore)

	// Health check.
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Well-known manifest.
	r.Get("/.well-known/octroi.json", WellKnownHandler)

	// Public (unauthenticated) routes.
	r.Get("/api/v1/tools/search", search.SearchTools)
	r.Get("/api/v1/tools", tools.ListTools)
	r.Get("/api/v1/tools/{id}", tools.GetTool)

	// Admin routes (require admin key).
	r.Route("/api/v1/admin", func(ar chi.Router) {
		ar.Use(auth.AdminAuthMiddleware(deps.AdminKey))

		// Tool management.
		ar.Post("/tools", tools.CreateTool)
		ar.Put("/tools/{id}", tools.UpdateTool)
		ar.Delete("/tools/{id}", tools.DeleteTool)

		// Agent management.
		ar.Post("/agents", agents.CreateAgent)
		ar.Get("/agents", agents.ListAgents)
		ar.Put("/agents/{id}", agents.UpdateAgent)
		ar.Delete("/agents/{id}", agents.DeleteAgent)

		// Budget management.
		ar.Put("/agents/{agentID}/budgets/{toolID}", agents.SetBudget)
		ar.Get("/agents/{agentID}/budgets/{toolID}", agents.GetBudget)
		ar.Get("/agents/{agentID}/budgets", agents.ListBudgets)

		// Admin usage queries.
		ar.Get("/usage", usage.GetUsageAdmin)
		ar.Get("/usage/agents/{agentID}", usage.GetUsageByAgent)
		ar.Get("/usage/tools/{toolID}", usage.GetUsageByTool)
		ar.Get("/usage/agents/{agentID}/tools/{toolID}", usage.GetUsageByAgentTool)
		ar.Get("/usage/transactions", func(w http.ResponseWriter, r *http.Request) {
			usage.ListTransactions(w, r, true)
		})
	})

	// Agent-authed routes (require agent API key + rate limiting).
	r.Route("/api/v1", func(ar chi.Router) {
		ar.Use(auth.AgentAuthMiddleware(deps.Auth))
		ar.Use(ratelimit.Middleware(deps.Limiter))

		ar.Get("/agents/me", agents.GetSelfAgent)
		ar.Get("/usage", usage.GetUsage)
		ar.Get("/usage/transactions", func(w http.ResponseWriter, r *http.Request) {
			usage.ListTransactions(w, r, false)
		})
	})

	// Proxy routes (agent-authed + rate limited).
	r.Route("/proxy", func(pr chi.Router) {
		pr.Use(auth.AgentAuthMiddleware(deps.Auth))
		pr.Use(ratelimit.Middleware(deps.Limiter))

		pr.Handle("/{toolID}/*", deps.Proxy)
	})

	return r
}

// slogRequestLogger is a simple structured logging middleware using slog.
func slogRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", ww.BytesWritten(),
		)
	})
}
