// Package runner schedules probes for many targets concurrently and keeps
// per-target rolling statistics.
package runner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/uu889/latprobe/internal/config"
	"github.com/uu889/latprobe/internal/probe"
	"github.com/uu889/latprobe/internal/stats"
)

// Result is one completed probe of one target.
type Result struct {
	Target config.Target
	Time   time.Time
	probe.Measurement
	Err error
}

// Observer receives every result, e.g. to update Prometheus metrics.
// Observe is called concurrently from many goroutines.
type Observer interface {
	Observe(Result)
}

// ObserverFunc adapts a function to the Observer interface.
type ObserverFunc func(Result)

// Observe implements Observer.
func (f ObserverFunc) Observe(r Result) { f(r) }

// TargetStats is a point-in-time view of one target.
type TargetStats struct {
	Target  config.Target
	Summary stats.Summary
	// Last is the most recent result, nil before the first probe.
	Last *Result
}

type target struct {
	cfg    config.Target
	prober probe.Prober
	window *stats.Window

	mu   sync.Mutex
	last *Result
}

// Runner probes a fixed set of targets on an interval.
type Runner struct {
	interval  time.Duration
	targets   []*target
	observers []Observer
	log       *slog.Logger
}

// New builds a Runner from a validated config.
func New(cfg *config.Config, log *slog.Logger, observers ...Observer) (*Runner, error) {
	r := &Runner{interval: cfg.Interval, observers: observers, log: log}
	for _, tc := range cfg.Targets {
		p, err := probe.New(tc)
		if err != nil {
			return nil, err
		}
		r.targets = append(r.targets, &target{cfg: tc, prober: p, window: stats.NewWindow(cfg.Window)})
	}
	return r, nil
}

// Run probes every target concurrently. With rounds > 0 each target is
// probed that many times; with rounds == 0 it runs until ctx is cancelled.
// The first probe of every target starts immediately.
func (r *Runner) Run(ctx context.Context, rounds int) {
	var wg sync.WaitGroup
	for _, t := range r.targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.loop(ctx, t, rounds)
		}()
	}
	wg.Wait()
}

func (r *Runner) loop(ctx context.Context, t *target, rounds int) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for i := 0; rounds == 0 || i < rounds; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		if !r.probeOnce(ctx, t) {
			return
		}
	}
}

// probeOnce runs a single probe and records it. It returns false when the
// parent context was cancelled, in which case nothing is recorded: a probe
// interrupted by shutdown is not packet loss.
func (r *Runner) probeOnce(ctx context.Context, t *target) bool {
	pctx, cancel := context.WithTimeout(ctx, t.cfg.Timeout)
	defer cancel()

	start := time.Now()
	m, err := t.prober.Probe(pctx)
	if ctx.Err() != nil {
		return false
	}

	res := Result{Target: t.cfg, Time: start, Measurement: m, Err: err}
	t.window.Add(stats.Sample{RTT: m.RTT, OK: err == nil})
	t.mu.Lock()
	t.last = &res
	t.mu.Unlock()

	if err != nil {
		r.log.Debug("probe failed", "target", t.cfg.Name, "type", t.cfg.Type, "err", err)
	} else {
		r.log.Debug("probe ok", "target", t.cfg.Name, "type", t.cfg.Type, "rtt", m.RTT, "detail", m.Detail)
	}
	for _, o := range r.observers {
		o.Observe(res)
	}
	return true
}

// Snapshot returns current statistics for every target, in config order.
func (r *Runner) Snapshot() []TargetStats {
	out := make([]TargetStats, len(r.targets))
	for i, t := range r.targets {
		t.mu.Lock()
		last := t.last
		t.mu.Unlock()
		out[i] = TargetStats{Target: t.cfg, Summary: t.window.Summary(), Last: last}
	}
	return out
}
