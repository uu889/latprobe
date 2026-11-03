package metrics

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/uu889/latprobe/internal/config"
	"github.com/uu889/latprobe/internal/probe"
	"github.com/uu889/latprobe/internal/runner"
	"github.com/uu889/latprobe/internal/stats"
)

func TestMetricsExposition(t *testing.T) {
	tgt := config.Target{Name: "edge", Type: config.HTTP, Region: "ap-east"}
	snapshot := func() []runner.TargetStats {
		return []runner.TargetStats{{
			Target: tgt,
			Summary: stats.Summarize([]stats.Sample{
				{RTT: 10 * time.Millisecond, OK: true},
				{RTT: 20 * time.Millisecond, OK: true},
				{OK: false},
				{RTT: 30 * time.Millisecond, OK: true},
			}),
		}}
	}
	reg := prometheus.NewPedanticRegistry()
	m := New(reg, "test", snapshot)

	m.Observe(runner.Result{Target: tgt, Measurement: probe.Measurement{
		RTT:    25 * time.Millisecond,
		Phases: []probe.Phase{{Name: "dns", Duration: time.Millisecond}, {Name: "tls", Duration: 5 * time.Millisecond}},
	}})
	m.Observe(runner.Result{Target: tgt, Err: errors.New("boom")})

	want := `
# HELP latprobe_probes_total Probes executed, by result (success or failure).
# TYPE latprobe_probes_total counter
latprobe_probes_total{probe="http",region="ap-east",result="failure",target="edge"} 1
latprobe_probes_total{probe="http",region="ap-east",result="success",target="edge"} 1
# HELP latprobe_probe_success 1 if the most recent probe succeeded, 0 otherwise.
# TYPE latprobe_probe_success gauge
latprobe_probe_success{probe="http",region="ap-east",target="edge"} 0
# HELP latprobe_window_loss_ratio Fraction of failed probes over the rolling window (0-1).
# TYPE latprobe_window_loss_ratio gauge
latprobe_window_loss_ratio{probe="http",region="ap-east",target="edge"} 0.25
# HELP latprobe_window_latency_seconds Latency percentiles (p50, p95, p99) over the rolling window.
# TYPE latprobe_window_latency_seconds gauge
latprobe_window_latency_seconds{percentile="p50",probe="http",region="ap-east",target="edge"} 0.02
latprobe_window_latency_seconds{percentile="p95",probe="http",region="ap-east",target="edge"} 0.029
latprobe_window_latency_seconds{percentile="p99",probe="http",region="ap-east",target="edge"} 0.0298
`
	err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"latprobe_probes_total", "latprobe_probe_success",
		"latprobe_window_loss_ratio", "latprobe_window_latency_seconds")
	if err != nil {
		t.Fatal(err)
	}

	if n := testutil.CollectAndCount(m.phase); n != 2 {
		t.Errorf("want 2 phase series, got %d", n)
	}
	problems, err := testutil.GatherAndLint(reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("lint %s: %s", p.Metric, p.Text)
	}
}
