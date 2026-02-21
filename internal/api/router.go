package api

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/alecgard/octroi/internal/agent"
	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/metrics"
	"github.com/alecgard/octroi/internal/proxy"
	"github.com/alecgard/octroi/internal/ratelimit"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/alecgard/octroi/internal/ui"
	"github.com/alecgard/octroi/internal/user"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// loginRateLimiter tracks per-IP login attempt counts within a sliding window.
type loginRateLimiter struct {
	entries sync.Map // IP string -> *loginEntry
	limit   int
	window  time.Duration
}

type loginEntry struct {
	mu      sync.Mutex
	count   int
	windowStart time.Time
}

func newLoginRateLimiter(limit int, window time.Duration) *loginRateLimiter {
	return &loginRateLimiter{
		limit:  limit,
		window: window,
	}
}

// allow checks whether the given IP is within the rate limit.
// It returns (allowed, retryAfterSeconds).
func (l *loginRateLimiter) allow(ip string) (bool, int) {
	now := time.Now()
	val, _ := l.entries.LoadOrStore(ip, &loginEntry{windowStart: now})
	entry := val.(*loginEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Reset window if expired.
	if now.Sub(entry.windowStart) >= l.window {
		entry.count = 0
		entry.windowStart = now
	}

	if entry.count >= l.limit {
		remaining := l.window - now.Sub(entry.windowStart)
		retryAfter := int(math.Ceil(remaining.Seconds()))
		if retryAfter < 1 {
			retryAfter = 1
		}
		return false, retryAfter
	}

	entry.count++
	return true, 0
}

// cleanup removes entries whose window has expired.
func (l *loginRateLimiter) cleanup() {
	now := time.Now()
	l.entries.Range(func(key, value any) bool {
		entry := value.(*loginEntry)
		entry.mu.Lock()
		expired := now.Sub(entry.windowStart) >= l.window
		entry.mu.Unlock()
		if expired {
			l.entries.Delete(key)
		}
		return true
	})
}

// startCleanup runs periodic cleanup in a background goroutine until ctx is cancelled.
func (l *loginRateLimiter) startCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.cleanup()
			}
		}
	}()
}

// RouterDeps holds all dependencies for the API router.
type RouterDeps struct {
	DBPool             *pgxpool.Pool
	ToolService        *registry.Service
	ToolStore          *registry.Store
	AgentStore         *agent.Store
	BudgetStore        *agent.BudgetStore
	MeterStore         *metering.Store
	Collector          *metering.Collector
	Auth               *auth.Service
	Limiter            *ratelimit.Limiter
	Proxy              *proxy.Handler
	UserStore          *user.Store
	ToolRateLimitStore *ratelimit.ToolRateLimitStore
	AllowedOrigins     []string
	Metrics            *metrics.Metrics
}

// NewRouter builds the chi router with all routes and middleware.
func NewRouter(deps RouterDeps) http.Handler {
	r := chi.NewRouter()

	// Global middleware.
	r.Use(chimw.Recoverer)
	r.Use(secureHeaders)
	r.Use(corsMiddleware(deps.AllowedOrigins))
	r.Use(requestIDMiddleware)
	if deps.Metrics != nil {
		r.Use(metricsMiddleware(deps.Metrics))
	}
	r.Use(slogRequestLogger)

	// Handlers.
	tools := newToolsHandler(deps.ToolService)
	agents := newAgentsHandler(deps.AgentStore, deps.BudgetStore)
	search := newSearchHandler(deps.ToolService)
	usage := newUsageHandler(deps.MeterStore, deps.AgentStore)

	// Login rate limiter: 5 attempts per IP per minute.
	loginRL := newLoginRateLimiter(5, time.Minute)
	loginRL.startCleanup(context.Background(), 5*time.Minute)

	// Session lookup adapter for user auth middlewares.
	var sessionLookup auth.SessionLookup
	if deps.UserStore != nil {
		sessionLookup = user.NewAuthAdapter(deps.UserStore)
	}

	// Auth failure/success and rate limit callbacks for metrics.
	agentAuthFail := func() {}
	agentAuthSuccess := func() {}
	adminAuthFail := func() {}
	adminAuthSuccess := func() {}
	memberAuthFail := func() {}
	memberAuthSuccess := func() {}
	rateLimitReject := func() {}
	if deps.Metrics != nil {
		agentAuthFail = func() { deps.Metrics.IncAuthFailure("agent") }
		agentAuthSuccess = func() { deps.Metrics.IncAuthSuccess("agent") }
		adminAuthFail = func() { deps.Metrics.IncAuthFailure("admin_session") }
		adminAuthSuccess = func() { deps.Metrics.IncAuthSuccess("admin_session") }
		memberAuthFail = func() { deps.Metrics.IncAuthFailure("member_session") }
		memberAuthSuccess = func() { deps.Metrics.IncAuthSuccess("member_session") }
		rateLimitReject = func() { deps.Metrics.IncRateLimitRejection("agent", "global") }
	}

	// Admin UI.
	uiHandler := ui.Handler()
	r.Handle("/ui", uiHandler)
	r.Handle("/ui/*", uiHandler)

	// Health check.
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if deps.DBPool != nil {
			pingCtx, pingCancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer pingCancel()
			if err := deps.DBPool.Ping(pingCtx); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"degraded","database":"unreachable"}`))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","database":"connected"}`))
	})

	// Prometheus metrics endpoint (unauthenticated for scraping).
	if deps.Metrics != nil {
		r.Handle("/metrics", promhttp.HandlerFor(deps.Metrics.Registry(), promhttp.HandlerOpts{}))
	}

	// Well-known manifest.
	r.Get("/.well-known/octroi.json", WellKnownHandler)

	// Public (unauthenticated) routes.
	r.Get("/api/v1/tools/search", search.SearchTools)
	r.Get("/api/v1/tools", tools.ListTools)
	r.Get("/api/v1/tools/{id}", tools.GetTool)

	// Public auth routes.
	if deps.UserStore != nil {
		authH := newAuthHandler(deps.UserStore)
		r.Post("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
				ip = fwd
			}
			allowed, retryAfter := loginRL.allow(ip)
			if !allowed {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts, try again later")
				return
			}
			authH.Login(w, r)
		})

		// User-authed routes (any logged-in user).
		r.Route("/api/v1/auth", func(ar chi.Router) {
			ar.Use(auth.MemberAuthMiddleware(sessionLookup, memberAuthFail, memberAuthSuccess))
			ar.Get("/me", authH.Me)
			ar.Post("/logout", authH.Logout)
		})
	}

	// Admin routes (require org_admin session).
	r.Route("/api/v1/admin", func(ar chi.Router) {
		ar.Use(auth.AdminSessionMiddleware(sessionLookup, adminAuthFail, adminAuthSuccess))

		// Admin metrics JSON endpoint.
		if deps.Metrics != nil {
			ar.Get("/metrics", deps.Metrics.Handler())
		}

		// Tool management.
		ar.Get("/tools", tools.AdminListTools)
		ar.Post("/tools", tools.CreateTool)
		ar.Put("/tools/{id}", tools.UpdateTool)
		ar.Delete("/tools/{id}", tools.DeleteTool)

		// Agent management.
		ar.Post("/agents", agents.CreateAgent)
		ar.Get("/agents", agents.ListAgents)
		ar.Put("/agents/{id}", agents.UpdateAgent)
		ar.Delete("/agents/{id}", agents.DeleteAgent)
		ar.Post("/agents/{id}/regenerate-key", agents.RegenerateKey)

		// Budget management.
		ar.Put("/agents/{agentID}/budgets/{toolID}", agents.SetBudget)
		ar.Get("/agents/{agentID}/budgets/{toolID}", agents.GetBudget)
		ar.Get("/agents/{agentID}/budgets", agents.ListBudgets)

		// Admin usage queries.
		ar.Get("/usage", usage.GetUsageAdmin)
		ar.Get("/usage/agents/{agentID}", usage.GetUsageByAgent)
		ar.Get("/usage/tools/calls", usage.GetToolCallCounts)
		ar.Get("/usage/tools/{toolID}", usage.GetUsageByTool)
		ar.Get("/usage/agents/{agentID}/tools/{toolID}", usage.GetUsageByAgentTool)
		ar.Get("/usage/transactions", func(w http.ResponseWriter, r *http.Request) {
			usage.ListTransactions(w, r, true)
		})

		// User management (admin only).
		if deps.UserStore != nil {
			users := newUsersHandler(deps.UserStore)
			ar.Post("/users", users.CreateUser)
			ar.Get("/users", users.ListUsers)
			ar.Put("/users/{id}", users.UpdateUser)
			ar.Delete("/users/{id}", users.DeleteUser)
		}

		// Tool rate limit overrides.
		if deps.ToolRateLimitStore != nil {
			trl := newToolRateLimitsHandler(deps.ToolRateLimitStore, deps.ToolStore)
			ar.Get("/tools/{toolID}/rate-limits", trl.ListToolRateLimits)
			ar.Put("/tools/{toolID}/rate-limits", trl.SetToolRateLimit)
			ar.Delete("/tools/{toolID}/rate-limits/{scope}/{scopeID}", trl.DeleteToolRateLimit)
		}

		// Teams (admin).
		if deps.UserStore != nil {
			teams := newTeamsHandler(deps.AgentStore, deps.UserStore)
			ar.Get("/teams", teams.AdminListTeams)
		}
	})

	// Member routes (require any valid session).
	if deps.UserStore != nil && sessionLookup != nil {
		member := newMemberHandler(deps.AgentStore, deps.ToolService, deps.MeterStore)
		teams := newTeamsHandler(deps.AgentStore, deps.UserStore)
		users := newUsersHandler(deps.UserStore)
		r.Route("/api/v1/member", func(mr chi.Router) {
			mr.Use(auth.MemberAuthMiddleware(sessionLookup, memberAuthFail, memberAuthSuccess))

			mr.Get("/agents", member.ListAgents)
			mr.Post("/agents", member.CreateAgent)
			mr.Put("/agents/{id}", member.UpdateAgent)
			mr.Delete("/agents/{id}", member.DeleteAgent)
			mr.Post("/agents/{id}/regenerate-key", member.RegenerateKey)
			mr.Get("/tools", member.ListTools)
			mr.Get("/usage", member.GetUsage)
			mr.Get("/usage/transactions", member.ListTransactions)
			mr.Get("/teams", teams.MemberListTeams)
			mr.Put("/teams/{team}/members/{userId}", teams.AddTeamMember)
			mr.Delete("/teams/{team}/members/{userId}", teams.RemoveTeamMember)
			mr.Get("/users", users.MemberListUsers)
			mr.Put("/users/me", users.UpdateSelf)
			mr.Put("/users/me/password", users.ChangePassword)
		})
	}

	// Agent-authed routes (require agent API key + rate limiting).
	r.Route("/api/v1", func(ar chi.Router) {
		ar.Use(auth.AgentAuthMiddleware(deps.Auth, agentAuthFail, agentAuthSuccess))
		ar.Use(ratelimit.Middleware(deps.Limiter, rateLimitReject))

		ar.Get("/agents/me", agents.GetSelfAgent)
		ar.Get("/usage", usage.GetUsage)
		ar.Get("/usage/transactions", func(w http.ResponseWriter, r *http.Request) {
			usage.ListTransactions(w, r, false)
		})
	})

	// Proxy routes (agent-authed + rate limited).
	r.Route("/proxy", func(pr chi.Router) {
		pr.Use(auth.AgentAuthMiddleware(deps.Auth, agentAuthFail, agentAuthSuccess))
		pr.Use(ratelimit.Middleware(deps.Limiter, rateLimitReject))

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
			"request_id", RequestIDFromContext(r.Context()),
		)
	})
}
