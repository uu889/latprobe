package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/uu889/latprobe/internal/config"
	"github.com/uu889/latprobe/internal/metrics"
	"github.com/uu889/latprobe/internal/output"
	"github.com/uu889/latprobe/internal/runner"
)

const shutdownTimeout = 5 * time.Second

func (a *app) serveCmd() *cobra.Command {
	var cfgPath, listen string
	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Probe continuously and expose Prometheus metrics",
		Example: "  latprobe serve -c config.yaml --listen :9310",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			return a.serve(cmd.Context(), cfg, listen)
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.yaml", "path to config file")
	cmd.Flags().StringVar(&listen, "listen", ":9310", "HTTP listen address")
	return cmd
}

func (a *app) serve(ctx context.Context, cfg *config.Config, listen string) error {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// The metrics collector reads window stats from the runner at scrape
	// time, and the runner feeds every result to the metrics.
	var r *runner.Runner
	m := metrics.New(reg, a.version, func() []runner.TargetStats { return r.Snapshot() })
	r, err := runner.New(cfg, a.log, m)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              listen,
		Handler:           a.routes(reg, r),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Cancelling ctx stops the runner, both on signal and on server error.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		r.Run(ctx, 0)
	}()

	serveErr := make(chan error, 1)
	go func() {
		a.log.Info("serving metrics", "addr", listen, "targets", len(cfg.Targets), "interval", cfg.Interval)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case <-ctx.Done():
		a.log.Info("shutting down")
	case err := <-serveErr:
		cancel()
		<-runnerDone
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	<-runnerDone
	a.log.Info("stopped")
	return nil
}

func (a *app) routes(reg *prometheus.Registry, r *runner.Runner) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /api/v1/summary", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(output.NewReport(r.Snapshot(), time.Now()))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<h1>latprobe %s</h1><ul><li><a href="/metrics">/metrics</a></li><li><a href="/api/v1/summary">/api/v1/summary</a></li><li><a href="/healthz">/healthz</a></li></ul>`, a.version)
	})
	return mux
}
