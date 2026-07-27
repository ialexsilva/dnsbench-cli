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
	servers     []string
	only        []string
	serversFile string
	noIPv6      bool
	protocols   []string
}

func registerSelectionFlags(cmd *cobra.Command, sel *selectionFlags) {
	cmd.Flags().BoolVar(&sel.system, "system", false, "include the system-configured DNS resolvers, even when excluded by --protocols")
	cmd.Flags().BoolVar(&sel.builtin, "builtin", false, "include the built-in public DNS resolvers")
	cmd.Flags().BoolVar(&sel.user, "user", false, "include the user resolver list")
	cmd.Flags().StringArrayVar(&sel.servers, "server", nil, "one-off resolver endpoint (IP, tls://, https://, h3:// or quic://); repeat to add more")
	cmd.Flags().StringSliceVar(&sel.only, "only", nil, "restrict to resolvers matching these IDs or addresses")
	cmd.Flags().StringVar(&sel.serversFile, "servers-file", "", "extra resolver list file to include (.json, .csv or .txt)")
	cmd.Flags().BoolVar(&sel.noIPv6, "no-ipv6", false, "exclude resolvers with IPv6 addresses")
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
	return selectServersWithDiscovery(ctx, sel, sysdns.Discover)
}

func selectServersWithDiscovery(
	ctx context.Context,
	sel selectionFlags,
	discover func(context.Context) ([]model.Server, []string, []string, error),
) (selectedServers, error) {
	var out selectedServers
	includeSystem, includeBuiltin, includeUser := sel.system, sel.builtin, sel.user
	if !includeSystem && !includeBuiltin && !includeUser && len(sel.servers) == 0 {
		includeSystem, includeBuiltin, includeUser = true, true, true
	}
	var lists [][]model.Server
	if len(sel.servers) > 0 {
		inline, err := parseInlineServers(sel.servers)
		if err != nil {
			return out, err
		}
		lists = append(lists, inline)
	}
	system, ifaces, warnings, discoverErr := discover(ctx)
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
			return out, fmt.Errorf("could not load the user resolver list: %w", err)
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
	servers = filterSelectedServers(servers, out.systemServers, protos, sel.noIPv6, sel.system)
	if len(sel.only) > 0 {
		servers = filterOnly(servers, sel.only)
	}
	out.systemIDs = matchSystemServerIDs(servers, out.systemServers)
	out.servers = servers
	return out, nil
}

func filterSelectedServers(servers, system []model.Server, protocols []model.Protocol, ipv4Only, keepSystem bool) []model.Server {
	systemKeys := make(map[string]bool, len(system))
	if keepSystem {
		for _, server := range system {
			systemKeys[server.Key()] = true
		}
	}
	out := make([]model.Server, 0, len(servers))
	for _, server := range servers {
		if ipv4Only && server.IsIPv6() {
			continue
		}
		protocolMatches := len(protocols) == 0 || slices.Contains(protocols, server.Protocol)
		if !protocolMatches && !systemKeys[server.Key()] {
			continue
		}
		out = append(out, server)
	}
	return out
}

func parseInlineServers(entries []string) ([]model.Server, error) {
	servers := make([]model.Server, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return nil, usageErrorf("--server requires a non-empty endpoint")
		}
		decoded, err := serverlist.DecodeText([]byte(entry))
		if err != nil {
			return nil, usageErrorf("invalid --server value %q: %v", raw, err)
		}
		if len(decoded) != 1 {
			return nil, usageErrorf("--server value %q must contain exactly one endpoint", raw)
		}
		server := decoded[0]
		server.Source = model.SourceUser
		if err := serverlist.ValidateAndPrepare(&server); err != nil {
			return nil, usageErrorf("invalid --server value %q: %v", raw, err)
		}
		servers = append(servers, server)
	}
	return servers, nil
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
			return nil, fmt.Errorf("invalid resolver %q in %s: %w", servers[i].DisplayName(), path, err)
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
		Short: "Characterize DNS resolvers without benchmarking them",
		Long: `Characterizes each selected DNS resolver: reachability, EDNS0, DNSSEC
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
				return fmt.Errorf("no resolvers matched the given selection")
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
			fmt.Fprintf(out, "Probing %s...\n\n", countNoun(len(selection.servers), "resolver"))
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
	cmd.Flags().IntVar(&concurrency, "concurrency", base.Concurrency, "how many resolvers to probe in parallel")
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
