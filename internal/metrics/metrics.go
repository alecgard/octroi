package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics holds all Prometheus metric collectors for the Octroi gateway.
type Metrics struct {
	registry *prometheus.Registry

	// HTTP metrics.
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	HTTPRequestSize      *prometheus.HistogramVec
	HTTPResponseSize     *prometheus.HistogramVec

	// Proxy metrics.
	ProxyRequestsTotal       *prometheus.CounterVec
	ProxyUpstreamDuration    *prometheus.HistogramVec
	ProxyActiveRequests      *prometheus.GaugeVec

	// Rate limiting and budget metrics.
	RateLimitRejectionsTotal *prometheus.CounterVec
	BudgetRejectionsTotal    *prometheus.CounterVec

	// Collector (metering) metrics.
	CollectorBufferSize         prometheus.Gauge
	CollectorFlushesTotal       *prometheus.CounterVec
	CollectorFlushDuration      prometheus.Histogram
	CollectorTransactionsTotal  prometheus.Counter

	// Auth metrics.
	AuthFailuresTotal  *prometheus.CounterVec
	AuthSuccessesTotal *prometheus.CounterVec

	// Proxy upstream error metrics.
	ProxyUpstreamErrorsTotal *prometheus.CounterVec

	// Server lifecycle.
	ServerStartTime prometheus.Gauge
}

// New creates and registers all Prometheus metrics on a private registry.
func New() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		registry: reg,

		HTTPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "octroi_http_requests_total",
			Help: "Total number of HTTP requests.",
		}, []string{"kind", "method", "path_pattern", "status_code"}),

		HTTPRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "octroi_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"kind", "method", "path_pattern"}),

		HTTPRequestSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "octroi_http_request_size_bytes",
			Help:    "HTTP request size in bytes.",
			Buckets: prometheus.ExponentialBuckets(100, 10, 6),
		}, []string{"kind", "method", "path_pattern"}),

		HTTPResponseSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "octroi_http_response_size_bytes",
			Help:    "HTTP response size in bytes.",
			Buckets: prometheus.ExponentialBuckets(100, 10, 6),
		}, []string{"kind", "method", "path_pattern"}),

		ProxyRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "octroi_proxy_requests_total",
			Help: "Total number of proxy requests.",
		}, []string{"tool_id", "tool_name", "agent_id", "method", "status_code"}),

		ProxyUpstreamDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "octroi_proxy_upstream_duration_seconds",
			Help:    "Upstream request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"tool_id", "tool_name"}),

		ProxyActiveRequests: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "octroi_proxy_active_requests",
			Help: "Number of currently active proxy requests.",
		}, []string{"tool_id"}),

		RateLimitRejectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "octroi_ratelimit_rejections_total",
			Help: "Total number of rate limit rejections.",
		}, []string{"limiter_type", "scope"}),

		BudgetRejectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "octroi_budget_rejections_total",
			Help: "Total number of budget rejections.",
		}, []string{"budget_type"}),

		CollectorBufferSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "octroi_collector_buffer_size",
			Help: "Current number of buffered metering transactions.",
		}),

		CollectorFlushesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "octroi_collector_flushes_total",
			Help: "Total number of collector flushes.",
		}, []string{"status"}),

		CollectorFlushDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "octroi_collector_flush_duration_seconds",
			Help:    "Duration of collector flush operations in seconds.",
			Buckets: prometheus.DefBuckets,
		}),

		CollectorTransactionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "octroi_collector_transactions_total",
			Help: "Total number of metering transactions recorded.",
		}),

		AuthFailuresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "octroi_auth_failures_total",
			Help: "Total number of authentication failures.",
		}, []string{"auth_type"}),

		AuthSuccessesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "octroi_auth_successes_total",
			Help: "Total number of successful authentications.",
		}, []string{"auth_type"}),

		ProxyUpstreamErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "octroi_proxy_upstream_errors_total",
			Help: "Total number of upstream request errors by error type.",
		}, []string{"error_type", "tool_id", "tool_name"}),

		ServerStartTime: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "octroi_server_start_time_seconds",
			Help: "Unix timestamp when the server started.",
		}),
	}

	// Register all metrics.
	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.HTTPRequestSize,
		m.HTTPResponseSize,
		m.ProxyRequestsTotal,
		m.ProxyUpstreamDuration,
		m.ProxyActiveRequests,
		m.RateLimitRejectionsTotal,
		m.BudgetRejectionsTotal,
		m.CollectorBufferSize,
		m.CollectorFlushesTotal,
		m.CollectorFlushDuration,
		m.CollectorTransactionsTotal,
		m.AuthFailuresTotal,
		m.AuthSuccessesTotal,
		m.ProxyUpstreamErrorsTotal,
		m.ServerStartTime,
	)

	// Set server start time.
	m.ServerStartTime.Set(float64(time.Now().Unix()))

	// Register Go runtime and process collectors.
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// Registry returns the private Prometheus registry.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// RegisterDBPoolCollector registers a custom DB pool stats collector.
func (m *Metrics) RegisterDBPoolCollector(statFunc DBPoolStatFunc) {
	m.registry.MustRegister(NewDBPoolCollector(statFunc))
}

// IncAuthFailure increments the auth failure counter for the given auth type.
func (m *Metrics) IncAuthFailure(authType string) {
	m.AuthFailuresTotal.WithLabelValues(authType).Inc()
}

// IncRateLimitRejection increments the rate limit rejection counter.
func (m *Metrics) IncRateLimitRejection(limiterType, scope string) {
	m.RateLimitRejectionsTotal.WithLabelValues(limiterType, scope).Inc()
}

// IncProxyRequests increments the proxy requests counter.
func (m *Metrics) IncProxyRequests(toolID, toolName, agentID, method string, statusCode int) {
	m.ProxyRequestsTotal.WithLabelValues(toolID, toolName, agentID, method, fmt.Sprintf("%d", statusCode)).Inc()
}

// ObserveUpstreamDuration records the upstream request duration.
func (m *Metrics) ObserveUpstreamDuration(toolID, toolName string, seconds float64) {
	m.ProxyUpstreamDuration.WithLabelValues(toolID, toolName).Observe(seconds)
}

// IncActiveRequests increments the active proxy requests gauge.
func (m *Metrics) IncActiveRequests(toolID string) {
	m.ProxyActiveRequests.WithLabelValues(toolID).Inc()
}

// DecActiveRequests decrements the active proxy requests gauge.
func (m *Metrics) DecActiveRequests(toolID string) {
	m.ProxyActiveRequests.WithLabelValues(toolID).Dec()
}

// IncBudgetRejection increments the budget rejection counter.
func (m *Metrics) IncBudgetRejection(budgetType string) {
	m.BudgetRejectionsTotal.WithLabelValues(budgetType).Inc()
}

// IncToolRateLimitRejection increments the tool-level rate limit rejection counter.
func (m *Metrics) IncToolRateLimitRejection() {
	m.RateLimitRejectionsTotal.WithLabelValues("tool", "tool").Inc()
}

// IncAuthSuccess increments the auth success counter for the given auth type.
func (m *Metrics) IncAuthSuccess(authType string) {
	m.AuthSuccessesTotal.WithLabelValues(authType).Inc()
}

// IncUpstreamError increments the upstream error counter with error type classification.
func (m *Metrics) IncUpstreamError(errorType, toolID, toolName string) {
	m.ProxyUpstreamErrorsTotal.WithLabelValues(errorType, toolID, toolName).Inc()
}
