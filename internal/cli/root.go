// Package cli wires latprobe's commands together.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/uu889/latprobe/internal/config"
	"github.com/uu889/latprobe/internal/output"
	"github.com/uu889/latprobe/internal/probe"
	"github.com/uu889/latprobe/internal/runner"
)

type app struct {
	version   string
	logLevel  string
	logFormat string
	log       *slog.Logger
}

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	// SIGINT/SIGTERM cancel the context; every command shuts down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	probe.UserAgent = "latprobe/" + version
	if err := newRootCmd(version).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRootCmd(version string) *cobra.Command {
	a := &app{version: version}
	root := &cobra.Command{
		Use:           "latprobe",
		Short:         "Multi-target network latency prober (TCP, DNS, HTTP) with Prometheus metrics",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			l, err := newLogger(cmd.ErrOrStderr(), a.logLevel, a.logFormat)
			a.log = l
			return err
		},
	}
	root.PersistentFlags().StringVar(&a.logLevel, "log-level", "info", "log level: debug, info, warn, error")
	root.PersistentFlags().StringVar(&a.logFormat, "log-format", "text", "log format: text or json")

	root.AddCommand(a.runCmd(), a.probeCmd(), a.serveCmd(), &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run:   func(cmd *cobra.Command, _ []string) { fmt.Fprintln(cmd.OutOrStdout(), "latprobe", version) },
	})
	return root
}

func newLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid --log-level %q", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(format) {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("invalid --log-format %q (want text or json)", format)
	}
}

// runOnce probes cfg for a fixed number of rounds and prints a report.
// If interrupted, it still prints whatever was collected so far.
func (a *app) runOnce(ctx context.Context, w io.Writer, cfg *config.Config, rounds int, format string) error {
	if format != "table" && format != "json" {
		return fmt.Errorf("invalid --output %q (want table or json)", format)
	}
	r, err := runner.New(cfg, a.log)
	if err != nil {
		return err
	}
	a.log.Debug("starting", "targets", len(cfg.Targets), "rounds", rounds, "interval", cfg.Interval)
	r.Run(ctx, rounds)
	if ctx.Err() != nil {
		a.log.Warn("interrupted, showing partial results")
	}
	if format == "json" {
		return output.WriteJSON(w, r.Snapshot())
	}
	return output.WriteTable(w, r.Snapshot())
}

// runFlags are shared by `run` and `probe`.
type runFlags struct {
	count    int
	interval time.Duration
	timeout  time.Duration
	output   string
}

func (f *runFlags) register(fs *pflag.FlagSet, defaultInterval time.Duration) {
	fs.IntVarP(&f.count, "count", "n", 5, "probes per target")
	fs.DurationVarP(&f.interval, "interval", "i", defaultInterval, "time between probes of the same target")
	fs.DurationVarP(&f.timeout, "timeout", "t", 0, "per-probe timeout (default from config, or 2s)")
	fs.StringVarP(&f.output, "output", "o", "table", "output format: table or json")
}
