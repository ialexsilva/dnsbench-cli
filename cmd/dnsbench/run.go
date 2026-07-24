package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dnsbench/internal/bench"
	"dnsbench/internal/model"
	"dnsbench/internal/power"
	"dnsbench/internal/probe"
	"dnsbench/internal/rank"
	"dnsbench/internal/report"
	"dnsbench/internal/stats"
	"dnsbench/internal/ui"
)

type runFlags struct {
	sel             selectionFlags
	skipProbe       bool
	extended        bool
	mode            string
	rounds          int
	warmup          int
	categories      []string
	uncachedZone    string
	tldZone         string
	timeout         time.Duration
	retries         int
	retryInterval   time.Duration
	concurrency     int
	pace            time.Duration
	gap             time.Duration
	session         string
	seed            int64
	noTriage        bool
	triageThreshold time.Duration
	triageAttempts  int
	forceAll        bool
	ranking         string
	sortKey         string
	category        string
	details         bool
	quiet           bool
	weightsFile     string
	exports         []string
	outDir          string
	prefix          string
	includeRaw      bool
	open            bool
	noKeepAwake     bool
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the full DNS benchmark and print a ranked report",
		Long: `Runs the full flow: discovers the system DNS servers, characterizes every
selected server, benchmarks them across query categories, ranks the results
and explains them. Results are specific to this network, ISP, location and
time of day.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.rounds = mustInt(cmd, "rounds")
			return executeRun(cmd, &f)
		},
	}
	registerSelectionFlags(cmd, &f.sel)
	base := model.DefaultBenchConfig(model.ModeStandard)
	cmd.Flags().BoolVar(&f.skipProbe, "skip-probe", false, "skip the characterization phase")
	cmd.Flags().BoolVar(&f.extended, "extended", false, "run extended probe checks (DNS64, QNAME minimization, HTTPS records)")
	cmd.Flags().StringVar(&f.mode, "mode", string(model.ModeStandard), "benchmark mode: quick, standard, precise or custom")
	cmd.Flags().Int("rounds", 0, "rounds per category (overrides the mode; required with --mode custom)")
	cmd.Flags().IntVar(&f.warmup, "warmup", base.WarmupRounds, "warmup rounds excluded from statistics")
	cmd.Flags().StringSliceVar(&f.categories, "categories", nil, "query categories to measure: cached, uncached, tld")
	cmd.Flags().StringVar(&f.uncachedZone, "uncached-zone", "", "zone you control, required to enable the uncached category")
	cmd.Flags().StringVar(&f.tldZone, "tld-zone", base.TLDZone, "TLD zone used by the recursive-path category")
	cmd.Flags().DurationVar(&f.timeout, "timeout", base.Timeout, "timeout per query")
	cmd.Flags().IntVar(&f.retries, "retries", base.Retries, "retries per failed query")
	cmd.Flags().DurationVar(&f.retryInterval, "retry-interval", base.RetryInterval, "wait between retries")
	cmd.Flags().IntVar(&f.concurrency, "concurrency", base.Concurrency, "maximum queries in flight at once")
	cmd.Flags().DurationVar(&f.pace, "pace", base.PaceInterval, "minimum spacing between any two query launches, across all servers (0 disables pacing)")
	cmd.Flags().DurationVar(&f.gap, "gap", base.PerServerGap, "gap between consecutive queries to the same server")
	cmd.Flags().StringVar(&f.session, "session", string(base.Session), "connection reuse: cold or persistent")
	cmd.Flags().Int64Var(&f.seed, "seed", 0, "random seed (0 picks a random seed)")
	cmd.Flags().BoolVar(&f.noTriage, "no-triage", false, "skip the triage phase and benchmark every server")
	cmd.Flags().DurationVar(&f.triageThreshold, "triage-threshold", base.TriageThreshold, "best RTT above this benches a server during triage")
	cmd.Flags().IntVar(&f.triageAttempts, "triage-attempts", base.TriageAttempts, "probes per server during triage")
	cmd.Flags().BoolVar(&f.forceAll, "force-all", false, "keep slow servers active instead of benching them")
	cmd.Flags().StringVar(&f.ranking, "ranking", string(model.RankLatency), "ranking mode: latency, browsing or reliability")
	cmd.Flags().StringVar(&f.sortKey, "sort", "median", "sort for the detailed table: score, mean, median, p95, loss or name")
	cmd.Flags().StringVar(&f.category, "category", "", "category shown in the detailed table (default: first enabled)")
	cmd.Flags().BoolVar(&f.details, "details", false, "show the per-category detailed metrics table")
	cmd.Flags().BoolVar(&f.quiet, "quiet", false, "suppress the header and the live progress view")
	cmd.Flags().StringVar(&f.weightsFile, "weights", "", "JSON file with custom ranking weights, merged over the presets")
	cmd.Flags().StringSliceVar(&f.exports, "export", nil, "export formats: json, csv, txt, html")
	cmd.Flags().StringVar(&f.outDir, "out", ".", "directory for exported reports")
	cmd.Flags().StringVar(&f.prefix, "prefix", "dnsbench", "file name prefix for exported reports")
	cmd.Flags().BoolVar(&f.includeRaw, "include-raw", false, "include raw samples in the JSON export")
	cmd.Flags().BoolVar(&f.open, "open", false, "open the HTML report in your browser when the run finishes (implies --export html)")
	cmd.Flags().BoolVar(&f.noKeepAwake, "no-keep-awake", false, "do not prevent the system from sleeping during the run")
	return cmd
}

func mustInt(cmd *cobra.Command, name string) int {
	v, _ := cmd.Flags().GetInt(name)
	return v
}

func executeRun(cmd *cobra.Command, f *runFlags) error {
	mode, rankingMode, session, err := validateRunFlags(cmd, f)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	ctx, cleanup := interruptibleContext(errOut)
	defer cleanup()
	if !f.noKeepAwake {
		release, err := power.Acquire()
		defer release()
		if err != nil && !f.quiet {
			fmt.Fprintln(errOut, "notice: could not prevent system sleep during the run: "+err.Error())
		}
	}
	if !f.quiet {
		printRunHeader(out)
	}
	selection, err := selectServers(ctx, f.sel)
	for _, w := range selection.warnings {
		fmt.Fprintln(errOut, "warning: "+w)
	}
	if err != nil {
		return err
	}
	if len(selection.servers) == 0 {
		return fmt.Errorf("no servers matched the given selection")
	}
	if !f.quiet {
		printForwarderNotes(out, systemServers(selection))
	}
	weights, err := loadWeights(f.weightsFile)
	if err != nil {
		return err
	}
	cfg, notices := buildBenchConfig(f, mode, session)
	for _, n := range notices {
		fmt.Fprintln(errOut, "notice: "+n)
	}
	if len(cfg.Categories) == 0 {
		return usageErrorf("no query categories are enabled")
	}
	startedAt := time.Now()
	var probes map[string]*model.ProbeResult
	if !f.skipProbe {
		var spinner *ui.Spinner
		if !f.quiet {
			fmt.Fprintln(out)
			label := fmt.Sprintf("Characterizing %s — DNSSEC, NXDOMAIN, rebinding", countNoun(len(selection.servers), "server"))
			spinner = ui.NewSpinner(out, stdoutIsTTY(), label, len(selection.servers))
			spinner.Start()
		}
		probeStart := time.Now()
		probes = runProbePhase(ctx, selection.servers, f, func() {
			if spinner != nil {
				spinner.Inc()
			}
		})
		if spinner != nil {
			final := ui.Green("✓") + fmt.Sprintf(" Characterized %s in %s", countNoun(len(selection.servers), "server"), time.Since(probeStart).Round(100*time.Millisecond))
			if ctx.Err() != nil {
				final = ui.Yellow("⚠") + " Characterization interrupted"
			}
			spinner.Stop(final)
		}
	}
	if !f.quiet {
		fmt.Fprintln(out)
	}
	engine := bench.NewEngine(selection.servers, cfg, nil)
	done := make(chan struct{})
	if f.quiet {
		go func() {
			for range engine.Events() {
			}
			close(done)
		}()
	} else {
		live := ui.NewLive(selection.servers, cfg, out, stdoutIsTTY(), f.sortKey)
		go func() {
			live.Run(engine.Events())
			close(done)
		}()
	}
	runErr := engine.Run(ctx)
	<-done
	interrupted := ctx.Err() != nil || errors.Is(runErr, context.Canceled)
	if runErr != nil && !interrupted {
		return fmt.Errorf("benchmark failed: %w", runErr)
	}
	if interrupted && !f.quiet {
		fmt.Fprintln(errOut, "benchmark interrupted — reporting partial results")
	}
	samples, triage, states, seed := engine.Result()
	cfg.Seed = seed
	statsMap := computeStatsMap(selection.servers, samples, states)
	scores := make(map[model.RankMode][]model.Score, len(model.AllRankModes()))
	for _, m := range model.AllRankModes() {
		scores[m] = rank.ScoreServers(statsMap, probes, cfg.Categories, weights[m], m)
	}
	res := &model.RunResult{
		Info: model.RunInfo{
			AppVersion: model.AppVersion,
			OS:         runtime.GOOS,
			Arch:       runtime.GOARCH,
			StartedAt:  startedAt,
			Duration:   time.Since(startedAt),
			Interfaces: selection.interfaces,
		},
		Config:          cfg,
		SelectedRanking: rankingMode,
		Weights:         weights,
		Servers:         selection.servers,
		SystemServers:   selection.systemServers,
		SystemIDs:       selection.systemIDs,
		Probes:          probes,
		Triage:          triage,
		Stats:           statsMap,
		Scores:          scores,
		Samples:         samples,
	}
	res.Comparisons = buildComparisons(res, rankingMode)
	exportPaths, exportErr := writeExportFiles(res, f, rankingMode)
	printFinalReport(out, res, f, rankingMode, exportPaths)
	if exportErr != nil {
		return exportErr
	}
	if f.open {
		for _, p := range exportPaths {
			if strings.HasSuffix(strings.ToLower(p), ".html") {
				if err := openInBrowser(p); err != nil {
					fmt.Fprintf(errOut, "warning: could not open the report in your browser: %v\n", err)
				}
				break
			}
		}
	}
	return nil
}

func validateRunFlags(cmd *cobra.Command, f *runFlags) (model.Mode, model.RankMode, model.SessionMode, error) {
	mode := model.Mode(strings.ToLower(strings.TrimSpace(f.mode)))
	switch mode {
	case model.ModeQuick, model.ModeStandard, model.ModePrecise, model.ModeCustom:
	default:
		return "", "", "", usageErrorf("invalid mode %q (accepted: quick, standard, precise, custom)", f.mode)
	}
	if mode == model.ModeCustom && !cmd.Flags().Changed("rounds") {
		return "", "", "", usageErrorf("--mode custom requires --rounds")
	}
	rankingMode := model.RankMode(strings.ToLower(strings.TrimSpace(f.ranking)))
	switch rankingMode {
	case model.RankLatency, model.RankBrowsing, model.RankReliability:
	default:
		return "", "", "", usageErrorf("invalid ranking %q (accepted: latency, browsing, reliability)", f.ranking)
	}
	session := model.SessionMode(strings.ToLower(strings.TrimSpace(f.session)))
	switch session {
	case model.SessionCold, model.SessionPersistent:
	default:
		return "", "", "", usageErrorf("invalid session %q (accepted: cold, persistent)", f.session)
	}
	switch strings.ToLower(strings.TrimSpace(f.sortKey)) {
	case "score", "mean", "median", "p95", "loss", "name":
	default:
		return "", "", "", usageErrorf("invalid sort %q (accepted: score, mean, median, p95, loss, name)", f.sortKey)
	}
	if f.category != "" {
		if _, err := parseCategories([]string{f.category}); err != nil {
			return "", "", "", err
		}
	}
	if _, err := parseCategories(f.categories); err != nil {
		return "", "", "", err
	}
	return mode, rankingMode, session, nil
}

func parseCategories(values []string) ([]model.Category, error) {
	var out []model.Category
	for _, v := range values {
		c := model.Category(strings.ToLower(strings.TrimSpace(v)))
		switch c {
		case model.CatCached, model.CatUncached, model.CatTLD:
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		default:
			return nil, usageErrorf("invalid category %q (accepted: cached, uncached, tld)", v)
		}
	}
	return out, nil
}

func interruptibleContext(errOut io.Writer) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Fprintln(errOut, "\ninterrupt received — finishing up and keeping partial results (press Ctrl+C again to abort)")
		cancel()
		<-sigCh
		fmt.Fprintln(errOut, "aborted")
		os.Exit(1)
	}()
	return ctx, func() {
		signal.Stop(sigCh)
		cancel()
	}
}

func printRunHeader(out io.Writer) {
	fmt.Fprintln(out, ui.Bold(fmt.Sprintf("dnsbench %s — DNS benchmark measured from this network", model.AppVersion)))
	fmt.Fprintln(out, `Tip: run "dnsbench intro" to understand what is measured and how to read the results.`)
	fmt.Fprintln(out, "Keep the network idle during the test: downloads, streaming and calls distort the numbers.")
	fmt.Fprintln(out, "dnsbench sends no telemetry and never changes your system DNS configuration.")
}

func systemServers(selection selectedServers) []model.Server {
	var out []model.Server
	for _, s := range selection.servers {
		if s.Source == model.SourceSystem {
			out = append(out, s)
		}
	}
	return out
}

func runProbePhase(ctx context.Context, servers []model.Server, f *runFlags, onResult func()) map[string]*model.ProbeResult {
	cfg := probe.DefaultConfig()
	cfg.Extended = f.extended
	cfg.UncachedZone = f.uncachedZone
	cfg.OnResult = onResult
	if f.timeout > 0 {
		cfg.Timeout = f.timeout
	}
	if f.concurrency > 0 {
		cfg.Concurrency = f.concurrency
	}
	return probe.Run(ctx, servers, cfg)
}

func buildBenchConfig(f *runFlags, mode model.Mode, session model.SessionMode) (model.BenchConfig, []string) {
	cfg := model.DefaultBenchConfig(mode)
	var notices []string
	if f.rounds > 0 {
		cfg.Rounds = f.rounds
	}
	cfg.WarmupRounds = f.warmup
	cats := cfg.Categories
	if len(f.categories) > 0 {
		cats, _ = parseCategories(f.categories)
	} else if f.uncachedZone != "" {
		cats = model.AllCategories()
	}
	if slices.Contains(cats, model.CatUncached) && f.uncachedZone == "" {
		filtered := slices.DeleteFunc(slices.Clone(cats), func(c model.Category) bool { return c == model.CatUncached })
		cats = filtered
		notices = append(notices, "the uncached category was disabled: it requires a zone you control (--uncached-zone) so that every query is a guaranteed cache miss")
	}
	cfg.Categories = cats
	cfg.UncachedZone = f.uncachedZone
	cfg.TLDZone = f.tldZone
	cfg.Timeout = f.timeout
	cfg.Retries = f.retries
	cfg.RetryInterval = f.retryInterval
	cfg.Concurrency = f.concurrency
	cfg.PaceInterval = f.pace
	cfg.PerServerGap = f.gap
	cfg.Session = session
	cfg.Seed = f.seed
	cfg.TriageEnabled = !f.noTriage
	cfg.TriageAttempts = f.triageAttempts
	cfg.TriageThreshold = f.triageThreshold
	cfg.ForceAll = f.forceAll
	return cfg, notices
}

func loadWeights(path string) (map[model.RankMode]model.Weights, error) {
	weights := rank.Presets()
	if path == "" {
		return weights, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read the weights file %s: %w", path, err)
	}
	var custom map[string]json.RawMessage
	if err := json.Unmarshal(data, &custom); err != nil {
		return nil, fmt.Errorf("invalid weights file %s: %w", path, err)
	}
	for key, raw := range custom {
		m := model.RankMode(strings.ToLower(strings.TrimSpace(key)))
		switch m {
		case model.RankLatency, model.RankBrowsing, model.RankReliability:
		default:
			return nil, usageErrorf("invalid ranking mode %q in %s (accepted: latency, browsing, reliability)", key, path)
		}
		w := weights[m]
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, fmt.Errorf("invalid weights for mode %q in %s: %w", key, path, err)
		}
		weights[m] = w
	}
	return weights, nil
}

func computeStatsMap(servers []model.Server, samples []model.Sample, states map[string]model.ServerState) map[string]*model.ServerStats {
	byServer := make(map[string][]model.Sample)
	for _, s := range samples {
		byServer[s.ServerID] = append(byServer[s.ServerID], s)
	}
	out := make(map[string]*model.ServerStats)
	for _, srv := range servers {
		list := byServer[srv.ID]
		state, known := states[srv.ID]
		if !known && len(list) == 0 {
			continue
		}
		if !known || state == "" {
			state = model.StateActive
		}
		st := &model.ServerStats{
			ServerID:    srv.ID,
			State:       state,
			PerCategory: make(map[model.Category]*model.Distribution),
		}
		byCat := make(map[model.Category][]model.Sample)
		for _, s := range list {
			byCat[s.Category] = append(byCat[s.Category], s)
		}
		for cat, ss := range byCat {
			st.PerCategory[cat] = stats.Compute(ss)
		}
		if len(list) > 0 {
			st.Phases = stats.ComputePhases(list)
		}
		out[srv.ID] = st
	}
	return out
}

func buildComparisons(res *model.RunResult, rankingMode model.RankMode) []model.Comparison {
	if len(res.Config.Categories) == 0 {
		return nil
	}
	ranked := res.Scores[rankingMode]
	var comparisons []model.Comparison
	seen := make(map[string]bool)
	pair := func(a, b string) {
		key := a + "|" + b
		if a == b || seen[key] || seen[b+"|"+a] {
			return
		}
		seen[key] = true
		comparisons = append(comparisons, rank.CompareScores(
			a,
			b,
			res.Samples,
			res.Config.Categories,
			res.Probes,
			res.Weights[rankingMode],
			rankingMode,
			res.Config.Seed,
			rank.DefaultScoreCompareConfig(),
		))
	}
	top := ranked
	if len(top) > 3 {
		top = top[:3]
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			pair(top[i].ServerID, top[j].ServerID)
		}
	}
	if len(ranked) > 0 {
		winner := ranked[0].ServerID
		for _, sysID := range res.SystemIDs {
			for _, score := range ranked {
				if score.ServerID == sysID {
					pair(winner, sysID)
					break
				}
			}
		}
	}
	return comparisons
}

func printFinalReport(out io.Writer, res *model.RunResult, f *runFlags, rankingMode model.RankMode, exportPaths []string) {
	fmt.Fprintln(out)
	fmt.Fprint(out, ui.RenderRunSummary(res))
	fmt.Fprintln(out)
	tieA, tieB, _ := report.TopTwoTied(res, rankingMode)
	fmt.Fprint(out, ui.RenderRankingList(res, rankingMode, tieA, tieB))
	if f.details {
		cat := res.Config.Categories[0]
		if f.category != "" {
			if parsed, err := parseCategories([]string{f.category}); err == nil && len(parsed) > 0 {
				cat = parsed[0]
			}
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "%s\n", ui.Bold("Detailed metrics — "+cat.Label()+" category"))
		fmt.Fprint(out, ui.RenderMetricsTable(res, cat, f.sortKey))
	}
	fmt.Fprintln(out)
	fmt.Fprint(out, ui.RenderReportFooter(exportPaths, f.open))
}

func writeExportFiles(res *model.RunResult, f *runFlags, rankingMode model.RankMode) ([]string, error) {
	exports := f.exports
	if f.open && !slices.ContainsFunc(exports, func(s string) bool {
		return strings.EqualFold(strings.TrimSpace(s), "html")
	}) {
		exports = append(append([]string(nil), exports...), "html")
	}
	if len(exports) == 0 {
		return nil, nil
	}
	paths, err := report.WriteExports(f.outDir, f.prefix, res, exports, f.includeRaw, rankingMode)
	for i, p := range paths {
		if abs, absErr := filepath.Abs(p); absErr == nil {
			paths[i] = abs
		}
	}
	if err != nil {
		return paths, fmt.Errorf("export failed: %w", err)
	}
	return paths, nil
}
