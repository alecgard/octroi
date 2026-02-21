package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/go-chi/chi/v5"
)

// ToolStore is the interface for looking up tools by ID.
type ToolStore interface {
	GetByID(ctx context.Context, id string) (*registry.Tool, error)
}

// BudgetChecker is the interface for checking agent and global tool budgets.
type BudgetChecker interface {
	CheckBudget(ctx context.Context, agentID, toolID string) (allowed bool, remainingDaily float64, remainingMonthly float64, err error)
	CheckToolGlobalBudget(ctx context.Context, toolID string) (allowed bool, remaining float64, err error)
}

// MeteringRecorder is the interface for recording transactions.
type MeteringRecorder interface {
	Record(tx metering.Transaction)
}

// ToolRateLimitChecker is the interface for checking per-tool rate limits.
type ToolRateLimitChecker interface {
	CheckToolRateLimit(ctx context.Context, toolID, team, agentID string) (allowed bool, limit, remaining int, resetAt time.Time, err error)
}

// MetricsRecorder is an optional interface for recording proxy-level metrics.
type MetricsRecorder interface {
	IncProxyRequests(toolID, toolName, agentID, method string, statusCode int)
	ObserveUpstreamDuration(toolID, toolName string, seconds float64)
	IncActiveRequests(toolID string)
	DecActiveRequests(toolID string)
	IncBudgetRejection(budgetType string)
	IncToolRateLimitRejection()
	IncUpstreamError(errorType, toolID, toolName string)
}

// Handler proxies requests to tool endpoints.
type Handler struct {
	tools          ToolStore
	budgets        BudgetChecker
	collector      MeteringRecorder
	toolRateLimits ToolRateLimitChecker
	client         *http.Client
	maxRequestSize int64
	metrics        MetricsRecorder
}

// NewHandler creates a new proxy handler.
func NewHandler(toolStore ToolStore, budgetStore BudgetChecker, collector MeteringRecorder, timeout time.Duration, maxRequestSize int64) *Handler {
	return &Handler{
		tools:          toolStore,
		budgets:        budgetStore,
		collector:      collector,
		client:         &http.Client{Timeout: timeout},
		maxRequestSize: maxRequestSize,
	}
}

// SetToolRateLimitChecker sets the optional tool rate limit checker.
func (h *Handler) SetToolRateLimitChecker(checker ToolRateLimitChecker) {
	h.toolRateLimits = checker
}

// SetMetrics sets the optional metrics recorder.
func (h *Handler) SetMetrics(m MetricsRecorder) {
	h.metrics = m
}

// ServeHTTP handles proxy requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "toolID")
	if toolID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing tool ID")
		return
	}

	// Look up tool.
	tool, err := h.tools.GetByID(r.Context(), toolID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "tool not found")
		return
	}

	// Extract agent from context.
	agent := auth.AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing agent credentials")
		return
	}

	// Track active requests.
	if h.metrics != nil {
		h.metrics.IncActiveRequests(tool.ID)
		defer h.metrics.DecActiveRequests(tool.ID)
	}

	// Check per-tool rate limits (global / team / agent scopes).
	if h.toolRateLimits != nil {
		tlAllowed, tlLimit, tlRemaining, tlResetAt, tlErr := h.toolRateLimits.CheckToolRateLimit(r.Context(), tool.ID, agent.Team, agent.ID)
		if tlErr == nil {
			if tlLimit > 0 {
				w.Header().Set("X-Tool-RateLimit-Limit", fmt.Sprintf("%d", tlLimit))
				w.Header().Set("X-Tool-RateLimit-Remaining", fmt.Sprintf("%d", tlRemaining))
				w.Header().Set("X-Tool-RateLimit-Reset", fmt.Sprintf("%d", tlResetAt.Unix()))
			}
			if !tlAllowed {
				if h.metrics != nil {
					h.metrics.IncToolRateLimitRejection()
				}
				writeError(w, http.StatusTooManyRequests, "tool_rate_limited", "tool rate limit exceeded")
				return
			}
		}
	}

	// Check per-agent budget.
	allowed, _, _, err := h.budgets.CheckBudget(r.Context(), agent.ID, tool.ID)
	if err == nil && !allowed {
		if h.metrics != nil {
			h.metrics.IncBudgetRejection("agent")
		}
		writeError(w, http.StatusForbidden, "budget_exceeded", "agent budget exceeded for this tool")
		return
	}

	// Check global tool budget.
	globalAllowed, _, err := h.budgets.CheckToolGlobalBudget(r.Context(), tool.ID)
	if err == nil && !globalAllowed {
		if h.metrics != nil {
			h.metrics.IncBudgetRejection("global")
		}
		writeError(w, http.StatusForbidden, "budget_exceeded", "global tool budget exceeded")
		return
	}

	// Resolve template for API mode.
	endpoint := tool.Endpoint
	if tool.Mode == "api" {
		resolved, err := registry.ResolveTemplate(tool.Endpoint, tool.Variables)
		if err != nil {
			writeError(w, http.StatusBadGateway, "proxy_error", "failed to resolve endpoint template")
			return
		}
		endpoint = resolved
	}

	// Build the upstream path by stripping the /proxy/{toolID} prefix.
	proxyPrefix := fmt.Sprintf("/proxy/%s", toolID)
	upstreamPath := strings.TrimPrefix(r.URL.Path, proxyPrefix)
	if upstreamPath == "" {
		upstreamPath = "/"
	}
	targetURL := strings.TrimRight(endpoint, "/") + upstreamPath
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// Enforce max request body size.
	var body io.Reader
	if r.Body != nil {
		body = io.LimitReader(r.Body, h.maxRequestSize+1)
	}

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "proxy_error", "failed to build upstream request")
		return
	}

	// Forward headers, excluding Authorization, Host, Connection.
	skipHeaders := map[string]bool{
		"Authorization": true,
		"Host":          true,
		"Connection":    true,
	}
	for key, values := range r.Header {
		if skipHeaders[key] {
			continue
		}
		for _, v := range values {
			outReq.Header.Add(key, v)
		}
	}

	// Inject tool auth credentials.
	switch tool.AuthType {
	case "bearer":
		outReq.Header.Set("Authorization", "Bearer "+tool.AuthConfig["key"])
	case "header":
		headerName := tool.AuthConfig["header_name"]
		if headerName != "" {
			outReq.Header.Set(headerName, tool.AuthConfig["key"])
		}
	case "query":
		paramName := tool.AuthConfig["param_name"]
		if paramName == "" {
			paramName = "api_key"
		}
		q := outReq.URL.Query()
		q.Set(paramName, tool.AuthConfig["key"])
		outReq.URL.RawQuery = q.Encode()
	case "none":
		// No auth injection.
	}

	// Execute the upstream request.
	start := time.Now()
	resp, err := h.client.Do(outReq)
	latency := time.Since(start)

	if h.metrics != nil {
		h.metrics.ObserveUpstreamDuration(tool.ID, tool.Name, latency.Seconds())
	}

	if err != nil {
		if h.metrics != nil {
			h.metrics.IncProxyRequests(tool.ID, tool.Name, agent.ID, r.Method, 502)
			h.metrics.IncUpstreamError(classifyUpstreamError(err), tool.ID, tool.Name)
		}
		h.recordTransaction(agent.ID, tool, r, 502, latency, 0, 0, false, "")
		writeError(w, http.StatusBadGateway, "proxy_error", "upstream request failed")
		return
	}
	defer resp.Body.Close()

	if h.metrics != nil {
		h.metrics.IncProxyRequests(tool.ID, tool.Name, agent.ID, r.Method, resp.StatusCode)
	}

	// Capture the upstream cost header before copying headers.
	reportedCostHeader := resp.Header.Get("X-Octroi-Cost")

	// Copy response headers.
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Copy response body.
	responseSize, _ := io.Copy(w, resp.Body)

	// Determine request size from Content-Length header, or 0.
	requestSize := r.ContentLength
	if requestSize < 0 {
		requestSize = 0
	}

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	h.recordTransaction(agent.ID, tool, r, resp.StatusCode, latency, requestSize, responseSize, success, reportedCostHeader)
}

func (h *Handler) recordTransaction(agentID string, tool *registry.Tool, r *http.Request, statusCode int, latency time.Duration, requestSize int64, responseSize int64, success bool, reportedCostHeader string) {
	cost := 0.0
	costSource := "flat"

	if reportedCostHeader != "" {
		if parsed, err := strconv.ParseFloat(reportedCostHeader, 64); err == nil && parsed >= 0 {
			cost = parsed
			costSource = "reported"
		} else if tool.PricingModel == "per_request" {
			cost = tool.PricingAmount
		}
	} else if tool.PricingModel == "per_request" {
		cost = tool.PricingAmount
	}

	h.collector.Record(metering.Transaction{
		AgentID:      agentID,
		ToolID:       tool.ID,
		Timestamp:    time.Now().UTC(),
		Method:       r.Method,
		Path:         r.URL.Path,
		StatusCode:   statusCode,
		LatencyMs:    latency.Milliseconds(),
		RequestSize:  requestSize,
		ResponseSize: responseSize,
		Success:      success,
		Cost:         cost,
		CostSource:   costSource,
	})
}

type proxyError struct {
	Error proxyErrorBody `json:"error"`
}

type proxyErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// classifyUpstreamError categorizes an upstream HTTP client error.
func classifyUpstreamError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		if netErr.Op == "dial" {
			return "connection_refused"
		}
		return "network"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	return "other"
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(proxyError{
		Error: proxyErrorBody{
			Code:    code,
			Message: message,
		},
	})
}
