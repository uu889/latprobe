package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/uu889/latprobe/internal/config"
)

func (a *app) runCmd() *cobra.Command {
	var (
		cfgPath string
		flags   runFlags
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Probe every target in a config file N times and print a summary",
		Example: `  latprobe run -c config.yaml
  latprobe run -c config.yaml -n 20 -i 500ms -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if flags.count < 1 {
				return errors.New("--count must be >= 1")
			}
			// Flags override the file only when set explicitly.
			if cmd.Flags().Changed("interval") {
				cfg.Interval = flags.interval
				// A shorter interval caps per-target timeouts so probes of
				// one target never overlap.
				for i := range cfg.Targets {
					cfg.Targets[i].Timeout = min(cfg.Targets[i].Timeout, cfg.Interval)
				}
			}
			if cmd.Flags().Changed("timeout") {
				for i := range cfg.Targets {
					cfg.Targets[i].Timeout = flags.timeout
				}
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			return a.runOnce(cmd.Context(), cmd.OutOrStdout(), cfg, flags.count, flags.output)
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.yaml", "path to config file")
	flags.register(cmd.Flags(), config.DefaultInterval)
	return cmd
}
