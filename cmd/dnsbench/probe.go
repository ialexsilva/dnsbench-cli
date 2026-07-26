package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dnsbench/internal/model"
	"dnsbench/internal/probe"
	"dnsbench/internal/serverlist"
	"dnsbench/internal/sysdns"
	"dnsbench/internal/ui"
)

type selectionFlags struct {
	system      bool
	builtin     bool
	user        bool
	only        []string
	serversFile string
	noIPv6      bool
	protocols   []string
}

func registerSelectionFlags(cmd *cobra.Command, sel *selectionFlags) {
	cmd.Flags().BoolVar(&sel.system, "system", false, "include the system-configured DNS servers")
	cmd.Flags().BoolVar(&sel.builtin, "builtin", false, "include the built-in public DNS servers")
	cmd.Flags().BoolVar(&sel.user, "user", false, "include the user server list")
	cmd.Flags().StringSliceVar(&sel.only, "only", nil, "restrict to servers matching these IDs or addresses")
	cmd.Flags().StringVar(&sel.serversFile, "servers-file", "", "extra server list file to include (.json, .csv or .txt)")
	cmd.Flags().BoolVar(&sel.noIPv6, "no-ipv6", false, "exclude servers with IPv6 addresses")
	cmd.Flags().StringSliceVar(&sel.protocols, "protocols", nil, "only include these protocols (udp, tcp, dot, doh, doh3, doq)")
}

type selectedServers struct {
	servers       []model.Server
	systemServers []model.Server
	systemIDs     []string
	interfaces    []string
	warnings      []string
}

func selectServers(ctx context.Context, sel selectionFlags) (selectedServers, error) {
	var out selectedServers
	includeSystem, includeBuiltin, includeUser := sel.system, sel.builtin, sel.user
	if !includeSystem && !includeBuiltin && !includeUser {
		includeSystem, includeBuiltin, includeUser = true, true, true
	}
	var lists [][]model.Server
	system, ifaces, warnings, discoverErr := sysdns.Discover(ctx)
	if discoverErr != nil {
		out.warnings = append(out.warnings, "system DNS discovery failed: "+discoverErr.Error())
	} else {
		out.systemServers = system
		out.interfaces = ifaces
	}
	out.warnings = append(out.warnings, warnings...)
	if includeSystem {
		lists = append(lists, system)
	}
	if includeUser {
		user, err := serverlist.LoadUser("")
		if err != nil {
			return out, fmt.Errorf("could not load the user server list: %w", err)
		}
		lists = append(lists, user)
	}
	if sel.serversFile != "" {
		extra, err := loadServersFile(sel.serversFile)
		if err != nil {
			return out, err
		}
		lists = append(lists, extra)
	}
	if includeBuiltin {
		lists = append(lists, serverlist.Builtin())
	}
	servers := serverlist.Merge(lists...)
	protos, err := parseProtocols(sel.protocols)
	if err != nil {
		return out, err
	}
	servers = serverlist.Filter(servers, serverlist.FilterOptions{
		Protocols: protos,
		IPv4Only:  sel.noIPv6,
	})
	if len(sel.only) > 0 {
		servers = filterOnly(servers, sel.only)
	}
	out.systemIDs = matchSystemServerIDs(servers, out.systemServers)
	out.servers = servers
	return out, nil
}

func matchSystemServerIDs(selected, system []model.Server) []string {
	systemKeys := make(map[string]bool, len(system))
	for _, server := range system {
		systemKeys[server.Key()] = true
	}
	var ids []string
	for _, server := range selected {
		if systemKeys[server.Key()] {
			ids = append(ids, server.ID)
		}
	}
	return ids
}

func loadServersFile(path string) ([]model.Server, error) {
	format, err := resolveFormat(path, "")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	servers, err := decodeServers(data, format)
	if err != nil {
		return nil, fmt.Errorf("could not parse %s: %w", path, err)
	}
	for i := range servers {
		if servers[i].Source == "" {
			servers[i].Source = model.SourceUser
		}
		if err := serverlist.ValidateAndPrepare(&servers[i]); err != nil {
			return nil, fmt.Errorf("invalid server %q in %s: %w", servers[i].DisplayName(), path, err)
		}
	}
	return servers, nil
}

func countNoun(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func filterOnly(servers []model.Server, only []string) []model.Server {
	wanted := make([]string, 0, len(only))
	for _, o := range only {
		wanted = append(wanted, strings.ToLower(strings.TrimSpace(o)))
	}
	var out []model.Server
	for _, s := range servers {
		if slices.Contains(wanted, strings.ToLower(s.ID)) || slices.Contains(wanted, strings.ToLower(s.Address)) {
			out = append(out, s)
		}
	}
	return out
}

func newProbeCmd() *cobra.Command {
	var sel selectionFlags
	var extended, verbose bool
	var timeout time.Duration
	var concurrency int
	var jsonPath string
	base := probe.DefaultConfig()
	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Characterize DNS servers without benchmarking them",
		Long: `Characterizes each selected DNS server: reachability, EDNS0, DNSSEC
validation and NXDOMAIN handling. No latency benchmark is run; use
"dnsbench run" for the full measurement.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			selection, err := selectServers(ctx, sel)
			for _, w := range selection.warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+w)
			}
			if err != nil {
				return err
			}
			if len(selection.servers) == 0 {
				return fmt.Errorf("no servers matched the given selection")
			}
			cfg := probe.DefaultConfig()
			cfg.Extended = extended
			if timeout > 0 {
				cfg.Timeout = timeout
			}
			if concurrency > 0 {
				cfg.Concurrency = concurrency
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Probing %s...\n\n", countNoun(len(selection.servers), "server"))
			results := probe.Run(ctx, selection.servers, cfg)
			states := make(map[string]model.ServerState, len(results))
			for id, r := range results {
				if r != nil && r.Reachable {
					states[id] = model.StateActive
				} else {
					states[id] = model.StateOffline
				}
			}
			fmt.Fprint(out, ui.RenderServersTable(selection.servers, results, states))
			if verbose {
				printProbeDetails(out, selection.servers, results)
			}
			if jsonPath != "" {
				if err := writeProbeJSON(jsonPath, results); err != nil {
					return err
				}
				fmt.Fprintf(out, "\nRaw probe results written to %s.\n", jsonPath)
			}
			return nil
		},
	}
	registerSelectionFlags(cmd, &sel)
	cmd.Flags().BoolVar(&extended, "extended", false, "run extended checks (DNS64, QNAME minimization, HTTPS records)")
	cmd.Flags().DurationVar(&timeout, "timeout", base.Timeout, "timeout per query")
	cmd.Flags().IntVar(&concurrency, "concurrency", base.Concurrency, "how many servers to probe in parallel")
	cmd.Flags().StringVar(&jsonPath, "json", "", "write raw probe results to this JSON file")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "show per-check NXDOMAIN details")
	return cmd
}

func printProbeDetails(out io.Writer, servers []model.Server, results map[string]*model.ProbeResult) {
	for _, s := range servers {
		r := results[s.ID]
		if r == nil {
			continue
		}
		fmt.Fprintf(out, "\n%s (%s)\n", ui.Bold(s.DisplayName()), s.Endpoint())
		if !r.Reachable {
			fmt.Fprintln(out, "  unreachable")
			for _, e := range r.Errors {
				fmt.Fprintln(out, "  error: "+e)
			}
			continue
		}
		if len(r.NXChecks) > 0 {
			fmt.Fprintln(out, "  NXDOMAIN checks:")
			for _, c := range r.NXChecks {
				line := fmt.Sprintf("    %-16s %-40s %s", c.Label, c.QName, c.Behavior.Label())
				if c.Detail != "" {
					line += " (" + c.Detail + ")"
				}
				fmt.Fprintln(out, line)
			}
		}
		if r.Extended != nil {
			fmt.Fprintf(out, "  extended: DNS64 %s, QNAME minimization %s, HTTPS record %s\n",
				r.Extended.DNS64.Label(), r.Extended.QNAMEMinimization.Label(), r.Extended.HTTPSRecord.Label())
			for _, n := range r.Extended.Notes {
				fmt.Fprintln(out, "    "+n)
			}
		}
		for _, e := range r.Errors {
			fmt.Fprintln(out, "  error: "+e)
		}
	}
}

func writeProbeJSON(path string, results map[string]*model.ProbeResult) error {
	ids := make([]string, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ordered := make([]*model.ProbeResult, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, results[id])
	}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode probe results: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	return nil
}
