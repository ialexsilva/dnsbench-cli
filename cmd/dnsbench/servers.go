package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"dnsbench/internal/model"
	"dnsbench/internal/serverlist"
	"dnsbench/internal/sysdns"
	"dnsbench/internal/ui"
)

func newServersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "servers",
		Short: "Manage the DNS server lists (system, built-in and user)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newServersListCmd(), newServersAddCmd(), newServersImportCmd(), newServersExportCmd())
	return cmd
}

func parseProtocols(values []string) ([]model.Protocol, error) {
	var out []model.Protocol
	for _, v := range values {
		p := model.Protocol(strings.ToLower(strings.TrimSpace(v)))
		if !slices.Contains(model.AllProtocols(), p) {
			return nil, usageErrorf("invalid protocol %q (accepted: udp, tcp, dot, doh, doh3, doq)", v)
		}
		out = append(out, p)
	}
	return out, nil
}

func parseSources(values []string) ([]model.Source, error) {
	var out []model.Source
	for _, v := range values {
		s := model.Source(strings.ToLower(strings.TrimSpace(v)))
		switch s {
		case model.SourceSystem, model.SourceBuiltin, model.SourceUser:
			out = append(out, s)
		default:
			return nil, usageErrorf("invalid source %q (accepted: system, builtin, user)", v)
		}
	}
	return out, nil
}

func mergedServerList(ctx context.Context, warn func(string)) ([]model.Server, error) {
	system, _, warnings, err := sysdns.Discover(ctx)
	if err != nil {
		warn("system DNS discovery failed: " + err.Error())
	}
	for _, w := range warnings {
		warn(w)
	}
	user, err := serverlist.LoadUser("")
	if err != nil {
		return nil, fmt.Errorf("could not load the user server list: %w", err)
	}
	return serverlist.Merge(system, user, serverlist.Builtin()), nil
}

func newServersListCmd() *cobra.Command {
	var protocols, operators, sources []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all known DNS servers (system + user + built-in)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			protos, err := parseProtocols(protocols)
			if err != nil {
				return err
			}
			srcs, err := parseSources(sources)
			if err != nil {
				return err
			}
			warn := func(msg string) { fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+msg) }
			servers, err := mergedServerList(ctx, warn)
			if err != nil {
				return err
			}
			servers = serverlist.Filter(servers, serverlist.FilterOptions{
				Protocols: protos,
				Operators: operators,
				Sources:   srcs,
			})
			out := cmd.OutOrStdout()
			if len(servers) == 0 {
				fmt.Fprintln(out, "No servers matched the given filters.")
				return nil
			}
			fmt.Fprint(out, ui.RenderServersTable(servers, nil, nil))
			fmt.Fprintf(out, "\n%d servers listed.\n", len(servers))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&protocols, "protocol", nil, "only show servers using these protocols (udp, tcp, dot, doh, doh3, doq)")
	cmd.Flags().StringSliceVar(&operators, "operator", nil, "only show servers run by these operators")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "only show servers from these sources (system, builtin, user)")
	return cmd
}

func newServersAddCmd() *cobra.Command {
	var name, operator, protocol, address, tlsHostname, dohURL, notes string
	var port int
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a DNS server to the user list",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			server := model.Server{
				Name:        name,
				Operator:    operator,
				Protocol:    model.Protocol(strings.ToLower(strings.TrimSpace(protocol))),
				Address:     address,
				Port:        port,
				TLSHostname: tlsHostname,
				DoHURL:      dohURL,
				Notes:       notes,
				Source:      model.SourceUser,
			}
			if err := serverlist.ValidateAndPrepare(&server); err != nil {
				return usageErr{err}
			}
			existing, err := serverlist.LoadUser("")
			if err != nil {
				return fmt.Errorf("could not load the user server list: %w", err)
			}
			for _, s := range existing {
				if s.Key() == server.Key() {
					return fmt.Errorf("a server with the same endpoint already exists in the user list: %s", s.DisplayName())
				}
			}
			existing = append(existing, server)
			if err := serverlist.SaveUser("", existing); err != nil {
				return fmt.Errorf("could not save the user server list: %w", err)
			}
			dir, err := serverlist.UserDir("")
			if err != nil {
				dir = ""
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added %s (%s) to the user list", server.DisplayName(), server.Endpoint())
			if dir != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " at %s", dir)
			}
			fmt.Fprintln(cmd.OutOrStdout(), ".")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name for the server")
	cmd.Flags().StringVar(&operator, "operator", "", "organization operating the server")
	cmd.Flags().StringVar(&protocol, "protocol", "udp", "protocol: udp, tcp, dot, doh, doh3 or doq")
	cmd.Flags().StringVar(&address, "address", "", "IP address (required for udp, tcp, dot and doq; pins the bootstrap IP for doh and doh3)")
	cmd.Flags().IntVar(&port, "port", 0, "port (0 uses the protocol default)")
	cmd.Flags().StringVar(&tlsHostname, "tls-hostname", "", "TLS server name for DoT and DoQ")
	cmd.Flags().StringVar(&dohURL, "doh-url", "", "https URL for DoH and DoH/3")
	cmd.Flags().StringVar(&notes, "notes", "", "free-form notes")
	return cmd
}

func resolveFormat(file, format string) (serverlist.Format, error) {
	if format != "" {
		switch strings.ToLower(strings.TrimSpace(format)) {
		case "json":
			return serverlist.FormatJSON, nil
		case "csv":
			return serverlist.FormatCSV, nil
		case "txt", "text":
			return serverlist.FormatText, nil
		default:
			return "", usageErrorf("invalid format %q (accepted: json, csv, txt)", format)
		}
	}
	f, err := serverlist.DetectFormat(file)
	if err != nil {
		return "", usageErr{err}
	}
	return f, nil
}

func decodeServers(data []byte, format serverlist.Format) ([]model.Server, error) {
	switch format {
	case serverlist.FormatJSON:
		return serverlist.DecodeJSON(data)
	case serverlist.FormatCSV:
		return serverlist.DecodeCSV(data)
	default:
		return serverlist.DecodeText(data)
	}
}

func encodeServers(servers []model.Server, format serverlist.Format) ([]byte, error) {
	switch format {
	case serverlist.FormatJSON:
		return serverlist.EncodeJSON(servers)
	case serverlist.FormatCSV:
		return serverlist.EncodeCSV(servers)
	default:
		return serverlist.EncodeText(servers)
	}
}

func newServersImportCmd() *cobra.Command {
	var file, format string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import DNS servers from a file into the user list",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := resolveFormat(file, format)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("could not read %s: %w", file, err)
			}
			imported, err := decodeServers(data, f)
			if err != nil {
				return fmt.Errorf("could not parse %s: %w", file, err)
			}
			existing, err := serverlist.LoadUser("")
			if err != nil {
				return fmt.Errorf("could not load the user server list: %w", err)
			}
			seen := make(map[string]bool, len(existing))
			for _, s := range existing {
				seen[s.Key()] = true
			}
			added, skipped := 0, 0
			for i := range imported {
				s := imported[i]
				s.Source = model.SourceUser
				if err := serverlist.ValidateAndPrepare(&s); err != nil {
					return fmt.Errorf("invalid server %q in %s: %w", s.DisplayName(), file, err)
				}
				if seen[s.Key()] {
					skipped++
					continue
				}
				seen[s.Key()] = true
				existing = append(existing, s)
				added++
			}
			if added > 0 {
				if err := serverlist.SaveUser("", existing); err != nil {
					return fmt.Errorf("could not save the user server list: %w", err)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported %d servers into the user list (%d duplicates skipped).\n", added, skipped)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file to import (.json, .csv or .txt)")
	cmd.Flags().StringVar(&format, "format", "", "force the input format: json, csv or txt")
	cmd.MarkFlagRequired("file")
	return cmd
}

func newServersExportCmd() *cobra.Command {
	var file, format, source string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the known DNS servers to a file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			f, err := resolveFormat(file, format)
			if err != nil {
				return err
			}
			var srcs []model.Source
			if source != "" {
				srcs, err = parseSources([]string{source})
				if err != nil {
					return err
				}
			}
			warn := func(msg string) { fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+msg) }
			servers, err := mergedServerList(ctx, warn)
			if err != nil {
				return err
			}
			if len(srcs) > 0 {
				servers = serverlist.Filter(servers, serverlist.FilterOptions{Sources: srcs})
			}
			if len(servers) == 0 {
				return fmt.Errorf("no servers to export")
			}
			data, err := encodeServers(servers, f)
			if err != nil {
				return fmt.Errorf("could not encode the server list: %w", err)
			}
			if err := os.WriteFile(file, data, 0o644); err != nil {
				return fmt.Errorf("could not write %s: %w", file, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Exported %d servers to %s.\n", len(servers), file)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "destination file (.json, .csv or .txt)")
	cmd.Flags().StringVar(&format, "format", "", "force the output format: json, csv or txt")
	cmd.Flags().StringVar(&source, "source", "", "export only one source: system, builtin or user")
	cmd.MarkFlagRequired("file")
	return cmd
}
