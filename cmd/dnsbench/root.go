package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dnsbench/internal/model"
	"dnsbench/internal/ui"
)

type usageErr struct{ err error }

func (e usageErr) Error() string { return e.err.Error() }

func (e usageErr) Unwrap() error { return e.err }

func usageErrorf(format string, args ...any) error {
	return usageErr{fmt.Errorf(format, args...)}
}

const rootLong = `dnsbench benchmarks and diagnoses recursive DNS servers as seen from YOUR network.

It measures cached, uncached and recursive-path latency, packet loss, stability,
DNSSEC validation, NXDOMAIN handling and DNS rebinding protection, then ranks the
servers and explains the results in plain language.

Every number produced by this tool is specific to this network, this ISP, this
location and this time of day. A server that wins here can lose on another
network or at another hour, so treat the ranking as a local snapshot, not a
universal truth.

dnsbench sends no telemetry, never changes your system DNS configuration, and
nothing leaves this machine beyond the DNS test queries themselves.`

func newRootCmd() *cobra.Command {
	var noColor bool
	cmd := &cobra.Command{
		Use:           "dnsbench",
		Short:         "Benchmark and diagnose recursive DNS servers from your own network",
		Long:          rootLong,
		Version:       model.AppVersion,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if noColor {
				ui.SetColorEnabled(false)
			}
		},
	}
	cmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colored output")
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return usageErr{err}
	})
	cmd.AddCommand(newIntroCmd(), newDiscoverCmd(), newServersCmd(), newProbeCmd(), newRunCmd())
	return cmd
}

func stdoutIsTTY() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

const localForwarderSentence = "This system DNS endpoint was detected successfully. It is a local/private forwarder, so only the upstream behind this endpoint is unknown; other detected DNS endpoints are listed separately."
