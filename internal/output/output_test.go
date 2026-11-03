package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/uu889/latprobe/internal/config"
	"github.com/uu889/latprobe/internal/probe"
	"github.com/uu889/latprobe/internal/runner"
	"github.com/uu889/latprobe/internal/stats"
)

func sampleSnapshot() []runner.TargetStats {
	web := config.Target{Name: "web", Type: config.HTTP, Address: "https://example.com", Region: "eu"}
	dead := config.Target{Name: "dead", Type: config.TCP, Address: "192.0.2.1:443"}
	return []runner.TargetStats{
		{
			Target:  web,
			Summary: stats.Summarize([]stats.Sample{{RTT: 10 * time.Millisecond, OK: true}, {RTT: 30 * time.Millisecond, OK: true}}),
			Last: &runner.Result{Target: web, Measurement: probe.Measurement{
				RTT:    30 * time.Millisecond,
				Phases: []probe.Phase{{Name: "dns", Duration: 2 * time.Millisecond}, {Name: "connect", Duration: 8 * time.Millisecond}},
				Detail: "HTTP/2.0 200 OK",
			}},
		},
		{
			Target:  dead,
			Summary: stats.Summarize([]stats.Sample{{OK: false}}),
			Last:    &runner.Result{Target: dead, Err: errors.New("i/o timeout")},
		},
	}
}

func TestWriteTable(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTable(&buf, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"TARGET", "web", "20.00ms", "HTTP/2.0 200 OK", "100.0%", "error: i/o timeout", "dns=2.00ms  connect=8.00ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal(buf.Bytes(), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	web := r.Targets[0]
	if web.AvgMs != 20 || web.JitterMs != 20 || web.Last.Phases["connect"] != 8 || !web.Last.OK {
		t.Errorf("unexpected web report: %+v", web)
	}
	if dead := r.Targets[1]; dead.LossPct != 100 || dead.Last.Error != "i/o timeout" {
		t.Errorf("unexpected dead report: %+v", dead)
	}
}
