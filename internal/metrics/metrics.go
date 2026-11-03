// Package metrics exposes probe results in Prometheus format.
//
// Two kinds of series are exported:
//   - event metrics (histograms, counters) updated on every probe, which
//     are what you should aggregate and alert on in PromQL;
//   - window gauges (percentiles, jitter, loss) computed at scrape time
//     from the in-memory rolling window, handy for quick dashboards.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/uu889/latprobe/internal/runner"
)

const namespace = "latprobe"

var labels = []string{"target", "region", "probe"}

// Buckets span 0.5ms (same rack) to 10s (badly broken), dense around the
// 1-300ms range where inter-region latency lives.
var Buckets = []float64{
	.0005, .001, .0025, .005, .01, .02, .035, .05, .075,
	.1, .15, .2, .3, .5, .75, 1, 2.5, 5, 10,
}

// Metrics records probe results. It implements runner.Observer.
type Metrics struct {
	duration *prometheus.HistogramVec
	phase    *prometheus.HistogramVec
	probes   *prometheus.CounterVec
	success  *prometheus.GaugeVec
}

// New registers all latprobe metrics on reg. snapshot is called on every
// scrape to export the rolling-window gauges.
func New(reg prometheus.Registerer, version string, snapshot func() []runner.TargetStats) *Metrics {
	m := &Metrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "probe_duration_seconds",
			Help:      "Latency of successful probes (tcp: handshake, dns: query, http: full request).",
			Buckets:   Buckets,
		}, labels),
		phase: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "probe_phase_duration_seconds",
			Help:      "Duration of each probe phase (dns, connect, tls, ttfb, transfer).",
			Buckets:   Buckets,
		}, append(labels, "phase")),
		probes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "probes_total",
			Help:      "Probes executed, by result (success or failure).",
		}, append(labels, "result")),
		success: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "probe_success",
			Help:      "1 if the most recent probe succeeded, 0 otherwise.",
		}, labels),
	}
	buildInfo := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   namespace,
		Name:        "build_info",
		Help:        "Build information; value is always 1.",
		ConstLabels: prometheus.Labels{"version": version},
	})
	buildInfo.Set(1)
	reg.MustRegister(m.duration, m.phase, m.probes, m.success, buildInfo, &windowCollector{snapshot: snapshot})
	return m
}

// Observe implements runner.Observer.
func (m *Metrics) Observe(r runner.Result) {
	lv := []string{r.Target.Name, r.Target.Region, string(r.Target.Type)}
	if r.Err != nil {
		m.probes.WithLabelValues(append(lv, "failure")...).Inc()
		m.success.WithLabelValues(lv...).Set(0)
		return
	}
	m.probes.WithLabelValues(append(lv, "success")...).Inc()
	m.success.WithLabelValues(lv...).Set(1)
	m.duration.WithLabelValues(lv...).Observe(r.RTT.Seconds())
	for _, p := range r.Phases {
		m.phase.WithLabelValues(append(lv, p.Name)...).Observe(p.Duration.Seconds())
	}
}

// windowCollector exports rolling-window statistics at scrape time.
type windowCollector struct {
	snapshot func() []runner.TargetStats
}

var (
	windowLatencyDesc = prometheus.NewDesc(namespace+"_window_latency_seconds",
		"Latency percentiles (p50, p95, p99) over the rolling window.", append(labels, "percentile"), nil)
	windowJitterDesc = prometheus.NewDesc(namespace+"_window_jitter_seconds",
		"Mean absolute difference between consecutive RTTs over the rolling window.", labels, nil)
	windowLossDesc = prometheus.NewDesc(namespace+"_window_loss_ratio",
		"Fraction of failed probes over the rolling window (0-1).", labels, nil)
	windowSamplesDesc = prometheus.NewDesc(namespace+"_window_samples",
		"Number of samples currently in the rolling window.", labels, nil)
)

func (c *windowCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- windowLatencyDesc
	ch <- windowJitterDesc
	ch <- windowLossDesc
	ch <- windowSamplesDesc
}

func (c *windowCollector) Collect(ch chan<- prometheus.Metric) {
	for _, ts := range c.snapshot() {
		s := ts.Summary
		if s.Sent == 0 {
			continue
		}
		lv := []string{ts.Target.Name, ts.Target.Region, string(ts.Target.Type)}
		gauge := func(d *prometheus.Desc, v float64, extra ...string) {
			ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, append(lv, extra...)...)
		}
		gauge(windowSamplesDesc, float64(s.Sent))
		gauge(windowLossDesc, s.LossPct/100)
		if s.Sent == s.Lost {
			continue // no latency data yet
		}
		gauge(windowLatencyDesc, s.P50.Seconds(), "p50")
		gauge(windowLatencyDesc, s.P95.Seconds(), "p95")
		gauge(windowLatencyDesc, s.P99.Seconds(), "p99")
		gauge(windowJitterDesc, s.Jitter.Seconds())
	}
}
