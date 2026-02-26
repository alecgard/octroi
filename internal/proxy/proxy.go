package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alecgard/octroi/internal/auth"
	"github.com/alecgard/octroi/internal/metering"
	"github.com/alecgard/octroi/internal/registry"
	"github.com/go-chi/chi/v5"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPCaller calls an MCP upstream tool by upstream ID and tool name.
type MCPCaller interface {
	Call(ctx context.Context, upstreamID string, toolName string, arguments map[string]any) (*mcp.CallToolResult, error)
}

// ToolStore is the interface for looking up tools by ID.
type ToolStore interface {
	GetByID(ctx context.Context, id string) (*registry.Tool, error)
	// GetByIDIncludeArchived returns a tool even if it's archived (for recording failed txns).
	GetByIDIncludeArchived(ctx context.Context, id string) (*registry.Tool, error)
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

// BodyRecorder stores request/response bodies for a transaction.
type BodyRecorder interface {
	RecordBody(ctx context.Context, transactionID string, reqBody, respBody []byte)
}

// PermissionChecker checks if an agent is allowed to use a tool.
type PermissionChecker interface {
	IsAllowed(ctx context.Context, agentID, toolID string) (bool, error)
	IsSubToolAllowed(ctx context.Context, agentID, toolID, subTool string) (bool, error)
}

// WebhookDispatcher dispatches budget threshold webhooks.
type WebhookDispatcher interface {
	MaybeDispatch(toolID, toolName, webhookURL string, thresholdPct int, budgetLimit, currentUsage float64)
}

// CircuitBreakerChecker checks whether a tool's circuit is open and records results.
type CircuitBreakerChecker interface {
	Allow(toolID string, cfg CircuitBreakerConfig) bool
	RecordResult(toolID string, cfg CircuitBreakerConfig, statusCode int)
}

// CircuitBreakerConfig holds CB settings passed from the tool.
type CircuitBreakerConfig struct {
	Enabled           bool
	ErrorThresholdPct int
	WindowSeconds     int
	CooldownSeconds   int
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
	permissions    PermissionChecker
	bodyRecorder   BodyRecorder
	webhooks       WebhookDispatcher
	circuitBreaker CircuitBreakerChecker
	client         *http.Client
	maxRequestSize int64
	metrics        MetricsRecorder
	mcpCaller      MCPCaller
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

// SetPermissionChecker sets the optional permission checker for agent tool allowlists.
func (h *Handler) SetPermissionChecker(c PermissionChecker) {
	h.permissions = c
}

// SetBodyRecorder sets the optional body recorder for request/response body logging.
func (h *Handler) SetBodyRecorder(r BodyRecorder) {
	h.bodyRecorder = r
}

// SetCircuitBreaker sets the optional circuit breaker checker.
func (h *Handler) SetCircuitBreaker(cb CircuitBreakerChecker) {
	h.circuitBreaker = cb
}

// SetWebhookDispatcher sets the optional webhook dispatcher for budget threshold alerts.
func (h *Handler) SetWebhookDispatcher(d WebhookDispatcher) {
	h.webhooks = d
}

// SetMCPCaller sets the optional MCP upstream caller for proxying MCP tools.
func (h *Handler) SetMCPCaller(c MCPCaller) {
	h.mcpCaller = c
}

// ServeHTTP handles proxy requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	toolID := chi.URLParam(r, "toolID")
	if toolID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing tool ID")
		return
	}

	// Extract agent from context.
	agent := auth.AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing agent credentials")
		return
	}

	// Look up tool (archived tools return not-found).
	tool, err := h.tools.GetByID(r.Context(), toolID)
	if err != nil {
		// Check if the tool exists but is archived — if so, record the failed transaction.
		// No tool_name is stored: post-deletion failures show just the tool ID in usage.
		if archived, archErr := h.tools.GetByIDIncludeArchived(r.Context(), toolID); archErr == nil {
			h.collector.Record(metering.Transaction{
				AgentID:    agent.ID,
				AgentName:  agent.Name,
				ToolID:     archived.ID,
				Timestamp:  time.Now().UTC(),
				Method:     r.Method,
				Path:       r.URL.Path,
				StatusCode: http.StatusNotFound,
				Success:    false,
				Error:      "tool not found",
			})
		}
		writeError(w, http.StatusNotFound, "not_found", "tool not found")
		return
	}

	// Check agent tool permissions.
	if h.permissions != nil {
		allowed, permErr := h.permissions.IsAllowed(r.Context(), agent.ID, tool.ID)
		if permErr == nil && !allowed {
			writeError(w, http.StatusForbidden, "permission_denied", "agent not permitted to use this tool")
			return
		}
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
	globalAllowed, globalRemaining, err := h.budgets.CheckToolGlobalBudget(r.Context(), tool.ID)
	if err == nil && !globalAllowed {
		if h.metrics != nil {
			h.metrics.IncBudgetRejection("global")
		}
		writeError(w, http.StatusForbidden, "budget_exceeded", "global tool budget exceeded")
		return
	}

	// Check circuit breaker.
	cbCfg := CircuitBreakerConfig{
		Enabled:           tool.CBEnabled,
		ErrorThresholdPct: tool.CBErrorThresholdPct,
		WindowSeconds:     tool.CBWindowSeconds,
		CooldownSeconds:   tool.CBCooldownSeconds,
	}
	if h.circuitBreaker != nil && tool.CBEnabled {
		if !h.circuitBreaker.Allow(tool.ID, cbCfg) {
			writeError(w, http.StatusServiceUnavailable, "circuit_open", "circuit breaker is open for this tool")
			return
		}
	}

	// Handle MCP mode: forward to upstream MCP server.
	if tool.Mode == "mcp" {
		h.serveMCP(w, r, tool, agent)
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

	// Generate transaction ID for body linking.
	txnID := uuid.New().String()

	// Enforce max request body size and optionally buffer for body logging.
	var body io.Reader
	var capturedReqBody []byte
	if r.Body != nil {
		if tool.LogBodies && h.bodyRecorder != nil {
			data, readErr := io.ReadAll(io.LimitReader(r.Body, h.maxRequestSize+1))
			if readErr != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "failed to read request body")
				return
			}
			capturedReqBody = data
			body = bytes.NewReader(data)
		} else {
			body = io.LimitReader(r.Body, h.maxRequestSize+1)
		}
	}

	// Determine per-tool timeout (fall back to client default).
	toolTimeout := h.client.Timeout
	if tool.TimeoutMs > 0 {
		toolTimeout = time.Duration(tool.TimeoutMs) * time.Millisecond
	}

	// Determine request size from Content-Length header, or 0.
	requestSize := r.ContentLength
	if requestSize < 0 {
		requestSize = 0
	}

	// Collect forwarded headers once (reused across retries).
	forwardHeaders := make(http.Header)
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
			forwardHeaders.Add(key, v)
		}
	}

	maxAttempts := 1 + tool.MaxRetries
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	// When retries are configured and body was not already buffered for logging, buffer it now.
	if tool.MaxRetries > 0 && capturedReqBody == nil && body != nil {
		data, readErr := io.ReadAll(body)
		if readErr != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "failed to read request body")
			return
		}
		capturedReqBody = data
		body = bytes.NewReader(data)
	}

	var lastResp *http.Response
	var lastErr error
	var lastLatency time.Duration

	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptTxnID := txnID
		if attempt > 0 {
			attemptTxnID = uuid.New().String()

			// Exponential backoff before retry.
			backoff := time.Duration(tool.RetryBackoffMs) * time.Millisecond * time.Duration(math.Pow(2, float64(attempt-1)))
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			slog.Info("retrying upstream request", "tool", tool.Name, "attempt", attempt+1, "backoff_ms", backoff.Milliseconds())
			time.Sleep(backoff)

			// Reset body reader for retry.
			if capturedReqBody != nil {
				body = bytes.NewReader(capturedReqBody)
			}
		}

		// Build upstream request with per-tool timeout.
		reqCtx, reqCancel := context.WithTimeout(r.Context(), toolTimeout)
		outReq, err := http.NewRequestWithContext(reqCtx, r.Method, targetURL, body)
		if err != nil {
			reqCancel()
			writeError(w, http.StatusBadGateway, "proxy_error", "failed to build upstream request")
			return
		}

		// Copy forwarded headers.
		for key, values := range forwardHeaders {
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
		resp, doErr := h.client.Do(outReq)
		latency := time.Since(start)
		reqCancel()

		if h.metrics != nil {
			h.metrics.ObserveUpstreamDuration(tool.ID, tool.Name, latency.Seconds())
		}

		if doErr != nil {
			if h.metrics != nil {
				h.metrics.IncProxyRequests(tool.ID, tool.Name, agent.ID, r.Method, 502)
				h.metrics.IncUpstreamError(classifyUpstreamError(doErr), tool.ID, tool.Name)
			}
			if h.circuitBreaker != nil && tool.CBEnabled {
				h.circuitBreaker.RecordResult(tool.ID, cbCfg, 502)
			}
			h.recordTransactionWithID(attemptTxnID, agent.ID, agent.Name, tool, r, 502, latency, requestSize, 0, false, "")
			lastErr = doErr
			lastLatency = latency
			lastResp = nil
			continue // retry
		}

		if h.metrics != nil {
			h.metrics.IncProxyRequests(tool.ID, tool.Name, agent.ID, r.Method, resp.StatusCode)
		}

		// Only retry on 5xx (server errors). 4xx and success are final.
		if resp.StatusCode >= 500 && attempt < maxAttempts-1 {
			// Record failed attempt, drain body, and retry.
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if h.circuitBreaker != nil && tool.CBEnabled {
				h.circuitBreaker.RecordResult(tool.ID, cbCfg, resp.StatusCode)
			}
			h.recordTransactionWithID(attemptTxnID, agent.ID, agent.Name, tool, r, resp.StatusCode, latency, requestSize, 0, false, resp.Header.Get("X-Octroi-Cost"))
			lastErr = nil
			lastResp = nil
			lastLatency = latency
			continue
		}

		// Final response — either success, 4xx, or last retry attempt.
		reportedCostHeader := resp.Header.Get("X-Octroi-Cost")

		// Copy response headers.
		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Copy response body, optionally capturing it for logging.
		var responseSize int64
		var capturedRespBody []byte
		if tool.LogBodies && h.bodyRecorder != nil {
			respData, _ := io.ReadAll(resp.Body)
			capturedRespBody = respData
			responseSize = int64(len(respData))
			w.Write(respData)
		} else {
			responseSize, _ = io.Copy(w, resp.Body)
		}
		resp.Body.Close()

		if h.circuitBreaker != nil && tool.CBEnabled {
			h.circuitBreaker.RecordResult(tool.ID, cbCfg, resp.StatusCode)
		}

		success := resp.StatusCode >= 200 && resp.StatusCode < 300
		h.recordTransactionWithID(attemptTxnID, agent.ID, agent.Name, tool, r, resp.StatusCode, latency, requestSize, responseSize, success, reportedCostHeader)

		// Record bodies asynchronously if enabled.
		if tool.LogBodies && h.bodyRecorder != nil && (len(capturedReqBody) > 0 || len(capturedRespBody) > 0) {
			redactCfg := metering.RedactConfig{AuthType: tool.AuthType, AuthConfig: tool.AuthConfig}
			reqRedacted := metering.RedactBody(capturedReqBody, redactCfg)
			respRedacted := metering.RedactBody(capturedRespBody, redactCfg)
			go h.bodyRecorder.RecordBody(context.Background(), attemptTxnID, reqRedacted, respRedacted)
		}

		// Check budget threshold webhooks.
		if h.webhooks != nil && tool.WebhookURL != "" && tool.BudgetLimit > 0 {
			currentUsage := tool.BudgetLimit - globalRemaining
			h.webhooks.MaybeDispatch(tool.ID, tool.Name, tool.WebhookURL, tool.WebhookThresholdPct, tool.BudgetLimit, currentUsage)
		}
		return
	}

	// All retries exhausted — return 502.
	_ = lastLatency
	_ = lastErr
	writeError(w, http.StatusBadGateway, "proxy_error", "upstream request failed after retries")
	_ = lastResp
}

func (h *Handler) recordTransaction(agentID, agentName string, tool *registry.Tool, r *http.Request, statusCode int, latency time.Duration, requestSize int64, responseSize int64, success bool, reportedCostHeader string) {
	h.recordTransactionWithID("", agentID, agentName, tool, r, statusCode, latency, requestSize, responseSize, success, reportedCostHeader)
}

func (h *Handler) recordTransactionWithID(txnID string, agentID, agentName string, tool *registry.Tool, r *http.Request, statusCode int, latency time.Duration, requestSize int64, responseSize int64, success bool, reportedCostHeader string) {
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
		ID:           txnID,
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
		AgentName:    agentName,
		ToolName:     tool.Name,
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

// serveMCP handles proxy requests for MCP mode tools. The path after /proxy/{toolID}/
// is treated as the MCP tool name, and the JSON body becomes the tool arguments.
func (h *Handler) serveMCP(w http.ResponseWriter, r *http.Request, tool *registry.Tool, agent *auth.Agent) {
	if h.mcpCaller == nil {
		writeError(w, http.StatusBadGateway, "proxy_error", "MCP proxy not configured")
		return
	}

	// Extract sub-tool name from path.
	proxyPrefix := fmt.Sprintf("/proxy/%s", tool.ID)
	subTool := strings.TrimPrefix(r.URL.Path, proxyPrefix)
	subTool = strings.TrimPrefix(subTool, "/")
	if subTool == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing MCP tool name in path")
		return
	}

	// Check sub-tool permissions.
	if h.permissions != nil {
		allowed, permErr := h.permissions.IsSubToolAllowed(r.Context(), agent.ID, tool.ID, subTool)
		if permErr == nil && !allowed {
			writeError(w, http.StatusForbidden, "permission_denied", "agent not permitted to use this sub-tool")
			return
		}
	}

	// Parse arguments from body.
	var arguments map[string]any
	var capturedReqBody []byte
	if r.Body != nil {
		body := io.LimitReader(r.Body, h.maxRequestSize+1)
		data, err := io.ReadAll(body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "failed to read request body")
			return
		}
		capturedReqBody = data
		if len(data) > 0 {
			if err := json.Unmarshal(data, &arguments); err != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
				return
			}
		}
	}

	txnID := uuid.New().String()

	start := time.Now()
	result, err := h.mcpCaller.Call(r.Context(), tool.ID, subTool, arguments)
	latency := time.Since(start)

	if h.metrics != nil {
		h.metrics.ObserveUpstreamDuration(tool.ID, tool.Name, latency.Seconds())
	}

	if err != nil {
		if h.metrics != nil {
			h.metrics.IncProxyRequests(tool.ID, tool.Name, agent.ID, r.Method, 502)
			h.metrics.IncUpstreamError("mcp_call", tool.ID, tool.Name)
		}
		h.recordTransactionWithID(txnID, agent.ID, agent.Name, tool, r, 502, latency, 0, 0, false, "")
		writeError(w, http.StatusBadGateway, "proxy_error", "MCP upstream call failed")
		return
	}

	success := result == nil || !result.IsError
	statusCode := 200
	if !success {
		statusCode = 500
	}

	if h.metrics != nil {
		h.metrics.IncProxyRequests(tool.ID, tool.Name, agent.ID, r.Method, statusCode)
	}

	// Serialize result as JSON response.
	respBytes, _ := json.Marshal(result)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write(respBytes)

	h.recordTransactionWithID(txnID, agent.ID, agent.Name, tool, r, statusCode, latency, 0, int64(len(respBytes)), success, "")

	// Record bodies asynchronously if enabled.
	if tool.LogBodies && h.bodyRecorder != nil && (len(capturedReqBody) > 0 || len(respBytes) > 0) {
		go h.bodyRecorder.RecordBody(context.Background(), txnID, capturedReqBody, respBytes)
	}
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
