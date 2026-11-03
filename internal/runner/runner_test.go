package runner

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uu889/latprobe/internal/config"
)

func listener(t *testing.T) string {
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

func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// testConfig probes every 10ms. Validation is skipped on purpose: the
// per-target timeout is longer than the interval to keep tests robust.
func testConfig(t *testing.T, targets ...config.Target) *config.Config {
	t.Helper()
	for i := range targets {
		targets[i].Timeout = 500 * time.Millisecond
	}
	cfg := &config.Config{Interval: 10 * time.Millisecond, Window: 10, Targets: targets}
	cfg.ApplyDefaults()
	return cfg
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRunFixedRounds(t *testing.T) {
	cfg := testConfig(t,
		config.Target{Name: "up", Type: config.TCP, Address: listener(t), Region: "local"},
		config.Target{Name: "down", Type: config.TCP, Address: closedPort(t)},
	)
	var observed atomic.Int32
	r, err := New(cfg, discard, ObserverFunc(func(Result) { observed.Add(1) }))
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background(), 3)

	if got := observed.Load(); got != 6 {
		t.Errorf("observer saw %d results, want 6", got)
	}
	snap := r.Snapshot()
	if snap[0].Summary.Sent != 3 || snap[0].Summary.Lost != 0 || snap[0].Target.Region != "local" {
		t.Errorf("up target: %+v", snap[0].Summary)
	}
	if snap[1].Summary.LossPct != 100 || snap[1].Last == nil || snap[1].Last.Err == nil {
		t.Errorf("down target should be 100%% loss with an error: %+v", snap[1])
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	cfg := testConfig(t, config.Target{Name: "up", Type: config.TCP, Address: listener(t)})
	r, err := New(cfg, discard)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, 0) // forever
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if r.Snapshot()[0].Summary.Sent == 0 {
		t.Error("expected some samples before cancel")
	}
}
