package main

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"
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

const rootLong = `dnsbench benchmarks and diagnoses recursive DNS resolvers as seen from YOUR network.

It measures cached, uncached and recursive-path latency, packet loss, stability,
DNSSEC validation and NXDOMAIN handling, then ranks the resolvers and explains the
results in plain language.

Every number produced by this tool is specific to this network, this ISP, this
location and this time of day. A resolver that wins here can lose on another
network or at another hour, so treat the ranking as a local snapshot, not a
universal truth.

dnsbench sends no telemetry, never changes your system DNS configuration, and
nothing leaves this machine beyond the DNS test queries themselves.`

func newRootCmd() *cobra.Command {
	var noColor bool
	cmd := &cobra.Command{
		Use:           "dnsbench",
		Short:         "Benchmark and diagnose recursive DNS resolvers from your own network",
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

func terminalWidth(out io.Writer) int {
	withFD, ok := out.(interface{ Fd() uintptr })
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(withFD.Fd())
	if err != nil {
		return 0
	}
	return width
}

const localForwarderSentence = "This system DNS endpoint was detected successfully. It is a local/private forwarder, so only the upstream behind this endpoint is unknown; other detected DNS endpoints are listed separately."
