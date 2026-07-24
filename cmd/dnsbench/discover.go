package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"dnsbench/internal/model"
	"dnsbench/internal/sysdns"
	"dnsbench/internal/ui"
)

func newDiscoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "discover",
		Short: "Show the DNS servers configured on this system",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			servers, _, warnings, err := sysdns.Discover(ctx)
			if err != nil {
				return fmt.Errorf("could not discover system DNS servers: %w", err)
			}
			out := cmd.OutOrStdout()
			if len(servers) == 0 {
				fmt.Fprintln(out, "No system DNS servers were found.")
			} else {
				fmt.Fprint(out, renderDiscoverTable(servers))
				printForwarderNotes(out, servers)
			}
			for _, w := range warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+w)
			}
			return nil
		},
	}
}

func renderDiscoverTable(servers []model.Server) string {
	headers := []string{"address", "interface", "role", "scope"}
	rows := make([][]string, 0, len(servers))
	for _, s := range servers {
		iface := s.Interface
		if iface == "" {
			iface = "-"
		}
		role := s.SystemRole
		if role == "" {
			role = "-"
		}
		rows = append(rows, []string{
			s.Address,
			iface,
			role,
			model.ScopeOfString(s.Address).Label(),
		})
	}
	return ui.RenderTable(headers, nil, rows)
}

func printForwarderNotes(out io.Writer, servers []model.Server) {
	printed := false
	for _, s := range servers {
		if !model.ScopeOfString(s.Address).IsLocal() {
			continue
		}
		if !printed {
			fmt.Fprintln(out)
			printed = true
		}
		fmt.Fprintf(out, "%s: %s\n", s.Address, localForwarderSentence)
	}
}
