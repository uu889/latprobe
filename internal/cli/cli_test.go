package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/uu889/latprobe/internal/config"
	"github.com/uu889/latprobe/internal/metrics"
	"github.com/uu889/latprobe/internal/output"
	"github.com/uu889/latprobe/internal/runner"
)

func tcpListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestProbeTCPJSON(t *testing.T) {
	addr := tcpListener(t)
	out, err := execute(t, "probe", "tcp", addr, "-n", "3", "-i", "10ms", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var r output.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if len(r.Targets) != 1 || r.Targets[0].Sent != 3 || r.Targets[0].Lost != 0 {
		t.Errorf("unexpected report: %+v", r)
	}
}

func TestRunWithConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "interval: 1s\ntimeout: 500ms\ntargets:\n  - {name: local, type: tcp, address: '" + tcpListener(t) + "', region: lab}\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := execute(t, "run", "-c", path, "-n", "2", "-i", "10ms")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "local") || !strings.Contains(out, "lab") {
		t.Errorf("table missing target:\n%s", out)
	}
}

func TestInvalidOutputFormat(t *testing.T) {
	if _, err := execute(t, "probe", "tcp", tcpListener(t), "-n", "1", "-o", "xml"); err == nil {
		t.Fatal("expected error for -o xml")
	}
}

func TestRoutes(t *testing.T) {
	cfg := &config.Config{Interval: time.Second, Window: 5, Targets: []config.Target{{Name: "local", Type: config.TCP, Address: tcpListener(t), Timeout: time.Second}}}
	a := &app{version: "test", log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	reg := prometheus.NewRegistry()
	var r *runner.Runner
	m := metrics.New(reg, a.version, func() []runner.TargetStats { return r.Snapshot() })
	r, err := runner.New(cfg, a.log, m)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background(), 1)

	srv := httptest.NewServer(a.routes(reg, r))
	defer srv.Close()

	for path, want := range map[string]string{
		"/healthz":        "ok",
		"/metrics":        `latprobe_probe_duration_seconds_count{probe="tcp",region="",target="local"} 1`,
		"/api/v1/summary": `"name":"local"`,
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Errorf("GET %s: status %d, body missing %q", path, resp.StatusCode, want)
		}
	}
}
