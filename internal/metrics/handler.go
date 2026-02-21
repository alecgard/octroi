package metrics

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"time"

	dto "github.com/prometheus/client_model/go"
)

// Summary is the JSON response for the metrics endpoint.
type Summary struct {
	Mode       string          `json:"mode"`
	HTTP       httpSummary     `json:"http"`
	Management httpSummary     `json:"management"`
	Proxy      proxySummary    `json:"proxy"`
	RateLimit  rateLimitInfo   `json:"rateLimit"`
	Budget     budgetInfo      `json:"budget"`
	Collector  collectorInfo   `json:"collector"`
	Auth       authInfo        `json:"auth"`
	DB         dbInfo          `json:"db"`
	Server     serverInfo      `json:"server"`
}

type httpSummary struct {
	TotalRequests float64 `json:"totalRequests"`
	ErrorRate     float64 `json:"errorRate"`
	P50Latency    float64 `json:"p50Latency"`
	P95Latency    float64 `json:"p95Latency"`
	P99Latency    float64 `json:"p99Latency"`
}

type proxySummary struct {
	TotalRequests  float64 `json:"totalRequests"`
	ActiveRequests float64 `json:"activeRequests"`
	P50Upstream    float64 `json:"p50Upstream"`
	P95Upstream    float64 `json:"p95Upstream"`
}

type rateLimitInfo struct {
	Rejections float64 `json:"rejections"`
}

type budgetInfo struct {
	Rejections float64 `json:"rejections"`
}

type collectorInfo struct {
	BufferSize   float64 `json:"bufferSize"`
	TotalFlushes float64 `json:"totalFlushes"`
	FlushErrors  float64 `json:"flushErrors"`
	Transactions float64 `json:"transactions"`
}

type authInfo struct {
	Failures  float64 `json:"failures"`
	Successes float64 `json:"successes"`
}

type serverInfo struct {
	StartTime      float64 `json:"startTime"`
	UptimeSeconds  float64 `json:"uptimeSeconds"`
	UpstreamErrors float64 `json:"upstreamErrors"`
}

type dbInfo struct {
	TotalConns    float64 `json:"totalConns"`
	IdleConns     float64 `json:"idleConns"`
	AcquiredConns float64 `json:"acquiredConns"`
}

// Handler returns an http.HandlerFunc that serves live metrics in JSON format.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.handleLive(w)
	}
}

func (m *Metrics) handleLive(w http.ResponseWriter) {
	families, err := m.registry.Gather()
	if err != nil {
		http.Error(w, "failed to gather metrics", http.StatusInternalServerError)
		return
	}

	fam := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		fam[f.GetName()] = f
	}

	summary := Summary{
		Mode: "live",
		HTTP: httpSummary{
			TotalRequests: sumCounterWithLabel(fam["octroi_http_requests_total"], "kind", "proxy"),
			ErrorRate:     computeErrorRateWithLabel(fam["octroi_http_requests_total"], "kind", "proxy"),
			P50Latency:    histogramPercentileWithLabel(fam["octroi_http_request_duration_seconds"], 0.50, "kind", "proxy"),
			P95Latency:    histogramPercentileWithLabel(fam["octroi_http_request_duration_seconds"], 0.95, "kind", "proxy"),
			P99Latency:    histogramPercentileWithLabel(fam["octroi_http_request_duration_seconds"], 0.99, "kind", "proxy"),
		},
		Management: httpSummary{
			TotalRequests: sumCounterWithLabel(fam["octroi_http_requests_total"], "kind", "management"),
			ErrorRate:     computeErrorRateWithLabel(fam["octroi_http_requests_total"], "kind", "management"),
			P50Latency:    histogramPercentileWithLabel(fam["octroi_http_request_duration_seconds"], 0.50, "kind", "management"),
			P95Latency:    histogramPercentileWithLabel(fam["octroi_http_request_duration_seconds"], 0.95, "kind", "management"),
			P99Latency:    histogramPercentileWithLabel(fam["octroi_http_request_duration_seconds"], 0.99, "kind", "management"),
		},
		Proxy: proxySummary{
			TotalRequests:  sumCounter(fam["octroi_proxy_requests_total"]),
			ActiveRequests: sumGauge(fam["octroi_proxy_active_requests"]),
			P50Upstream:    histogramPercentile(fam["octroi_proxy_upstream_duration_seconds"], 0.50),
			P95Upstream:    histogramPercentile(fam["octroi_proxy_upstream_duration_seconds"], 0.95),
		},
		RateLimit: rateLimitInfo{
			Rejections: sumCounter(fam["octroi_ratelimit_rejections_total"]),
		},
		Budget: budgetInfo{
			Rejections: sumCounter(fam["octroi_budget_rejections_total"]),
		},
		Collector: collectorInfo{
			BufferSize:   gaugeValue(fam["octroi_collector_buffer_size"]),
			TotalFlushes: sumCounter(fam["octroi_collector_flushes_total"]),
			FlushErrors:  counterWithLabel(fam["octroi_collector_flushes_total"], "status", "error"),
			Transactions: counterValue(fam["octroi_collector_transactions_total"]),
		},
		Auth: authInfo{
			Failures:  sumCounter(fam["octroi_auth_failures_total"]),
			Successes: sumCounter(fam["octroi_auth_successes_total"]),
		},
		DB: dbInfo{
			TotalConns:    gaugeValue(fam["octroi_db_pool_total_conns"]),
			IdleConns:     gaugeValue(fam["octroi_db_pool_idle_conns"]),
			AcquiredConns: gaugeValue(fam["octroi_db_pool_acquired_conns"]),
		},
		Server: serverInfo{
			StartTime:      gaugeValue(fam["octroi_server_start_time_seconds"]),
			UptimeSeconds:  float64(time.Now().Unix()) - gaugeValue(fam["octroi_server_start_time_seconds"]),
			UpstreamErrors: sumCounter(fam["octroi_proxy_upstream_errors_total"]),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	_ = json.NewEncoder(w).Encode(summary)
}

// --- Prometheus metric helpers ---

func sumCounter(f *dto.MetricFamily) float64 {
	if f == nil {
		return 0
	}
	var total float64
	for _, m := range f.GetMetric() {
		if m.GetCounter() != nil {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

func sumGauge(f *dto.MetricFamily) float64 {
	if f == nil {
		return 0
	}
	var total float64
	for _, m := range f.GetMetric() {
		if m.GetGauge() != nil {
			total += m.GetGauge().GetValue()
		}
	}
	return total
}

func gaugeValue(f *dto.MetricFamily) float64 {
	if f == nil {
		return 0
	}
	ms := f.GetMetric()
	if len(ms) == 0 {
		return 0
	}
	if ms[0].GetGauge() != nil {
		return ms[0].GetGauge().GetValue()
	}
	return 0
}

func counterValue(f *dto.MetricFamily) float64 {
	if f == nil {
		return 0
	}
	ms := f.GetMetric()
	if len(ms) == 0 {
		return 0
	}
	if ms[0].GetCounter() != nil {
		return ms[0].GetCounter().GetValue()
	}
	return 0
}

func counterWithLabel(f *dto.MetricFamily, labelName, labelValue string) float64 {
	if f == nil {
		return 0
	}
	for _, m := range f.GetMetric() {
		for _, lp := range m.GetLabel() {
			if lp.GetName() == labelName && lp.GetValue() == labelValue {
				if m.GetCounter() != nil {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func computeErrorRate(f *dto.MetricFamily) float64 {
	if f == nil {
		return 0
	}
	var total, errors float64
	for _, m := range f.GetMetric() {
		if m.GetCounter() == nil {
			continue
		}
		v := m.GetCounter().GetValue()
		total += v
		for _, lp := range m.GetLabel() {
			if lp.GetName() == "status_code" {
				code := lp.GetValue()
				if len(code) > 0 && code[0] >= '4' {
					errors += v
				}
			}
		}
	}
	if total == 0 {
		return 0
	}
	return errors / total
}

func hasLabel(m *dto.Metric, name, value string) bool {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == name && lp.GetValue() == value {
			return true
		}
	}
	return false
}

func sumCounterWithLabel(f *dto.MetricFamily, labelName, labelValue string) float64 {
	if f == nil {
		return 0
	}
	var total float64
	for _, m := range f.GetMetric() {
		if hasLabel(m, labelName, labelValue) && m.GetCounter() != nil {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

func computeErrorRateWithLabel(f *dto.MetricFamily, labelName, labelValue string) float64 {
	if f == nil {
		return 0
	}
	var total, errors float64
	for _, m := range f.GetMetric() {
		if !hasLabel(m, labelName, labelValue) || m.GetCounter() == nil {
			continue
		}
		v := m.GetCounter().GetValue()
		total += v
		for _, lp := range m.GetLabel() {
			if lp.GetName() == "status_code" {
				code := lp.GetValue()
				if len(code) > 0 && code[0] >= '4' {
					errors += v
				}
			}
		}
	}
	if total == 0 {
		return 0
	}
	return errors / total
}

func histogramPercentileWithLabel(f *dto.MetricFamily, q float64, labelName, labelValue string) float64 {
	if f == nil {
		return 0
	}

	type bucket struct {
		upperBound      float64
		cumulativeCount uint64
	}
	var totalCount uint64
	bucketMap := make(map[float64]uint64)

	for _, m := range f.GetMetric() {
		if !hasLabel(m, labelName, labelValue) {
			continue
		}
		h := m.GetHistogram()
		if h == nil {
			continue
		}
		totalCount += h.GetSampleCount()
		for _, b := range h.GetBucket() {
			bucketMap[b.GetUpperBound()] += b.GetCumulativeCount()
		}
	}

	if totalCount == 0 {
		return 0
	}

	buckets := make([]bucket, 0, len(bucketMap))
	for ub, count := range bucketMap {
		buckets = append(buckets, bucket{upperBound: ub, cumulativeCount: count})
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].upperBound < buckets[j].upperBound
	})

	rank := q * float64(totalCount)

	var prevBound float64
	var prevCount uint64
	for _, b := range buckets {
		if math.IsInf(b.upperBound, 1) {
			break
		}
		if float64(b.cumulativeCount) >= rank {
			bucketCount := b.cumulativeCount - prevCount
			if bucketCount == 0 {
				return b.upperBound
			}
			fraction := (rank - float64(prevCount)) / float64(bucketCount)
			return prevBound + fraction*(b.upperBound-prevBound)
		}
		prevBound = b.upperBound
		prevCount = b.cumulativeCount
	}

	if len(buckets) > 0 {
		for i := len(buckets) - 1; i >= 0; i-- {
			if !math.IsInf(buckets[i].upperBound, 1) {
				return buckets[i].upperBound
			}
		}
	}
	return 0
}

// histogramPercentile computes a percentile from aggregated histogram buckets
// using linear interpolation.
func histogramPercentile(f *dto.MetricFamily, q float64) float64 {
	if f == nil {
		return 0
	}

	// Aggregate all histogram metrics in the family.
	type bucket struct {
		upperBound      float64
		cumulativeCount uint64
	}
	var totalCount uint64
	bucketMap := make(map[float64]uint64)

	for _, m := range f.GetMetric() {
		h := m.GetHistogram()
		if h == nil {
			continue
		}
		totalCount += h.GetSampleCount()
		for _, b := range h.GetBucket() {
			bucketMap[b.GetUpperBound()] += b.GetCumulativeCount()
		}
	}

	if totalCount == 0 {
		return 0
	}

	buckets := make([]bucket, 0, len(bucketMap))
	for ub, count := range bucketMap {
		buckets = append(buckets, bucket{upperBound: ub, cumulativeCount: count})
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].upperBound < buckets[j].upperBound
	})

	rank := q * float64(totalCount)

	var prevBound float64
	var prevCount uint64
	for _, b := range buckets {
		if math.IsInf(b.upperBound, 1) {
			break
		}
		if float64(b.cumulativeCount) >= rank {
			// Linear interpolation within this bucket.
			bucketCount := b.cumulativeCount - prevCount
			if bucketCount == 0 {
				return b.upperBound
			}
			fraction := (rank - float64(prevCount)) / float64(bucketCount)
			return prevBound + fraction*(b.upperBound-prevBound)
		}
		prevBound = b.upperBound
		prevCount = b.cumulativeCount
	}

	// If we didn't find it, return the last finite bucket upper bound.
	if len(buckets) > 0 {
		for i := len(buckets) - 1; i >= 0; i-- {
			if !math.IsInf(buckets[i].upperBound, 1) {
				return buckets[i].upperBound
			}
		}
	}
	return 0
}
