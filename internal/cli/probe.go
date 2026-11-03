package cli

import (
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/uu889/latprobe/internal/config"
)

// probeCmd runs ad-hoc probes without a config file.
func (a *app) probeCmd() *cobra.Command {
	var flags runFlags
	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Probe a single target ad hoc (tcp, dns or http)",
	}
	// Persistent so they apply to every probe subcommand.
	flags.register(cmd.PersistentFlags(), time.Second)

	run := func(cmd *cobra.Command, t config.Target) error {
		if flags.count < 1 {
			return errors.New("--count must be >= 1")
		}
		t.Timeout = flags.timeout
		if t.Timeout == 0 {
			t.Timeout = min(2*time.Second, flags.interval)
		}
		cfg := &config.Config{Interval: flags.interval, Window: flags.count, Targets: []config.Target{t}}
		cfg.ApplyDefaults()
		if err := cfg.Validate(); err != nil {
			return err
		}
		return a.runOnce(cmd.Context(), cmd.OutOrStdout(), cfg, flags.count, flags.output)
	}

	var resolver, record string
	dns := &cobra.Command{
		Use:     "dns HOSTNAME",
		Short:   "Time DNS resolution, optionally against a specific resolver",
		Example: "  latprobe probe dns github.com --resolver 1.1.1.1",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, config.Target{Type: config.DNS, Address: args[0], Resolver: resolver, Record: record})
		},
	}
	dns.Flags().StringVar(&resolver, "resolver", "", "DNS server ip[:port] (default: system resolver)")
	dns.Flags().StringVar(&record, "record", "A", "record type: A or AAAA")

	var expect []int
	http := &cobra.Command{
		Use:     "http URL",
		Short:   "Time an HTTP(S) GET with a dns/connect/tls/ttfb breakdown",
		Example: "  latprobe probe http https://www.cloudflare.com/ -n 3",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, config.Target{Type: config.HTTP, Address: args[0], ExpectStatus: expect})
		},
	}
	http.Flags().IntSliceVar(&expect, "expect", nil, "acceptable status codes (default: any < 400)")

	tcp := &cobra.Command{
		Use:     "tcp HOST:PORT",
		Short:   "Time the TCP handshake to host:port",
		Example: "  latprobe probe tcp github.com:443 -n 10",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, config.Target{Type: config.TCP, Address: args[0]})
		},
	}

	cmd.AddCommand(tcp, dns, http)
	return cmd
}
