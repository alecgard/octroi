package metrics

import "github.com/prometheus/client_golang/prometheus"

// DBPoolStatFunc returns database pool statistics without importing pgxpool.
type DBPoolStatFunc func() (total, idle, acquired int32)

// dbPoolCollector implements prometheus.Collector for DB pool stats.
type dbPoolCollector struct {
	statFunc DBPoolStatFunc

	totalDesc    *prometheus.Desc
	idleDesc     *prometheus.Desc
	acquiredDesc *prometheus.Desc
}

// NewDBPoolCollector creates a new collector that exposes DB pool gauges.
func NewDBPoolCollector(statFunc DBPoolStatFunc) prometheus.Collector {
	return &dbPoolCollector{
		statFunc: statFunc,
		totalDesc: prometheus.NewDesc(
			"octroi_db_pool_total_conns",
			"Total number of connections in the DB pool.",
			nil, nil,
		),
		idleDesc: prometheus.NewDesc(
			"octroi_db_pool_idle_conns",
			"Number of idle connections in the DB pool.",
			nil, nil,
		),
		acquiredDesc: prometheus.NewDesc(
			"octroi_db_pool_acquired_conns",
			"Number of acquired connections in the DB pool.",
			nil, nil,
		),
	}
}

// Describe sends the descriptors of each metric to the channel.
func (c *dbPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.totalDesc
	ch <- c.idleDesc
	ch <- c.acquiredDesc
}

// Collect fetches pool stats and sends them as metrics.
func (c *dbPoolCollector) Collect(ch chan<- prometheus.Metric) {
	total, idle, acquired := c.statFunc()
	ch <- prometheus.MustNewConstMetric(c.totalDesc, prometheus.GaugeValue, float64(total))
	ch <- prometheus.MustNewConstMetric(c.idleDesc, prometheus.GaugeValue, float64(idle))
	ch <- prometheus.MustNewConstMetric(c.acquiredDesc, prometheus.GaugeValue, float64(acquired))
}
