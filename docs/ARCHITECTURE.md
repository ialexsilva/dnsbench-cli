# Architecture

## Package map

| Package | Responsibility |
| --- | --- |
| `cmd/dnsbench` | CLI wiring (cobra): flag parsing and validation, server selection, orchestration of probe → bench → stats → rank → report, signal handling |
| `internal/model` | Core data types (`Server`, `Sample`, `QueryResult`, `Distribution`, `ProbeResult`, `Score`, `RunResult`, …), enums, defaults (`DefaultBenchConfig`), address scope classification |
| `internal/transport` | One `Querier` per protocol: UDP/53 (with TCP retry on truncation), TCP/53, DoT, DoH; message building/parsing, phase timing, error classification |
| `internal/sysdns` | Read-only discovery of system DNS servers per OS (`scutil --dns`, `/etc/resolv.conf` + `resolvectl status`, `netsh`), parsers, forwarder warnings |
| `internal/serverlist` | Embedded built-in server list, user list persistence, JSON/CSV/TXT codecs, merge, filter, validation and ID generation |
| `internal/probe` | Characterization checks: reachability, EDNS0, DNSSEC, NXDOMAIN handling, rebinding protection, reverse PTR, optional extensions |
| `internal/bench` | Benchmark engine: triage, warmup and measured rounds, seeded scheduling, sessions, pause gate, connectivity watchdog |
| `internal/stats` | Semantic DNS-validity filtering, per-category `Distribution` computation, phase averages and statistical primitives |
| `internal/rank` | Ranking-cost computation (weighted base + penalties), rank assignment, presets and paired bootstrap comparisons |
| `internal/report` | Factual DNS/run/comparison sections and exporters for JSON, CSV, TXT and a self-contained HTML report (with an embedded SVG chart) |
| `internal/ui` | Terminal rendering: tables, result chart, neutral overview card, formatting, colors, live progress view |
| `internal/power` | Best-effort, per-OS inhibition of system idle sleep during a run (caffeinate on macOS, a systemd-logind D-Bus lock on Linux, `SetThreadExecutionState` on Windows); no-op elsewhere |

## Dependency arrows

Arrows point from importer to imported. Every internal package except `power` depends on `model`; `power` is a standalone leaf that imports only the standard library and its OS bindings. Only `probe` and `bench` depend on `transport`; `cmd/dnsbench` depends on everything except `transport` (it passes `nil` factories and lets `probe`/`bench` default to `transport.New`).

```
                          cmd/dnsbench
   ┌────────┬────────┬────────┼────────┬────────┬────────┬────────┐
   ▼        ▼        ▼        ▼        ▼        ▼        ▼        ▼
 sysdns  serverlist  probe   bench   stats    rank    report     ui
   │        │         │        │       │        │        │        │
   │        │         ├──► transport ◄─┤(bench) │        │        │
   │        │         │        │       │        │        │        │
   └────────┴─────────┴────────┴───────┴────────┴────────┴────────┘
                                │
                                ▼
                              model
```

`model` imports nothing from the project. `rank` additionally imports `stats` so each bootstrap replicate can rebuild distributions before recomputing an aggregate latency cost.

## Data model

- **`Server`** — one benchmark target: identity (`ID`, `Name`, `Operator`), endpoint (`Address`, `Port`, `TLSHostname`, `DoHURL`), `Protocol` (`udp`, `tcp`, `dot`, `doh`), `Source` (`system`, `builtin`, `user`), and system metadata (`Interface`, `SystemRole`). `Key()` (protocol|address|port|DoH URL) de-duplicates servers across lists.
- **`QueryResult`** — the outcome of a single DNS exchange: `RTT`, per-phase timings (`Connect`, `TLSHandshake`, `HTTPSetup`, `Query`), `Rcode`, flags (`Truncated`, `AD`, `UsedTCP`, `Reused`), EDNS advertised size, wire size, parsed `Answers`, and `Err` (`nil` means answered).
- **`Sample`** — one benchmark measurement: which server, `Category` (`cached`, `uncached`, `tld`), `Round`, `Warmup` flag, query name/type, attempt/failed-attempt/timeout counters, end-to-end `Elapsed` time, timestamp, and the final `QueryResult`.
- **`ProbeResult`** — the characterization of one server: reachability, A/AAAA support, EDNS0, `DNSSECInfo` (the DO/CD matrix verdicts and the derived `Validating`), the `NXCheck` list and derived `NXInterception`, `RebindInfo` (v4/v6/overall), reverse PTR name, optional `ExtendedInfo` (DNS64, QNAME minimization, HTTPS record) and errors. Verdicts are `yes` / `no` / `partial` / `unknown`.
- **`Distribution`** — per server × category statistics over non-warmup samples: transport-answer and semantically-valid counts, attempt/retry counts, timeouts, errors, SERVFAILs, invalid/truncated responses, percentages, min/max/mean/median, sample variance and stddev, P50/P90/P95/P99, 95% CI bounds, jitter, and optional chronological valid `SamplesMs`.
- **`Score`** — the internal representation of one server's user-facing latency cost in one mode: renormalized weighted `BaseMs`, a named `Penalties` map (ms each), `TotalMs = BaseMs + Σ penalties`, and the assigned `Rank` (ties within 0.01 ms share a rank).
- **`RunResult`** — the complete run artifact serialized by the exporters: `RunInfo` (version, OS/arch, start time, duration, interfaces), `BenchConfig`, the selected ranking, weights for all three modes, selected servers, discovered system servers, matching selected system IDs, probes, triage results, stats, scores per mode, aggregate comparisons and optional raw samples.

Supporting types: `TriageResult` (attempts, valid responses, best RTT, resulting `ServerState`, reason), `ServerStats` (state + per-category distributions + cold/steady `PhaseAverages`), `Comparison` (aggregate latency-cost delta, bootstrap interval/p-value and `SigLevel`), `Weights` (category weights, latency metric, penalty rates), `Event` (engine → UI stream), `BenchConfig` (every knob of a run).

## Full benchmark flow (`dnsbench run`)

1. **Discovery and selection** — `sysdns.Discover` always identifies the system resolvers. Source flags only control which system, user and built-in endpoints (plus `--servers-file`) enter the benchmark; selected endpoints are merged by key and filtered (`--protocols`, `--no-ipv6`, `--only`). If no source flag is given, all three sources are included.
2. **Registration** — selected servers carry stable IDs. Discovered system resolvers are retained separately, and any selected endpoint with the same transport/address/port key is marked as current DNS even if its selected record came from the built-in or user list.
3. **Characterization (probe)** — unless `--skip-probe`, every server is probed concurrently: reachability, baseline (A/AAAA, EDNS0), DNSSEC matrix, NXDOMAIN checks, rebinding checks, reverse PTR, and extended checks with `--extended`.
4. **Triage** — unless `--no-triage`, each server answers up to `--triage-attempts` (default 10) cached-domain queries, launched through the same global pacer and in-flight semaphore as benchmark queries. Only valid A/CNAME answers count. A server with no valid answers becomes `offline`; a server whose best RTT exceeds `--triage-threshold` (default 50ms) becomes `benched` ("out of contention") unless `--force-all`. Only `active` servers proceed. For the default `--session persistent`, one session per server is opened now; servers whose session fails to open become `error`.
5. **Warmup** — `--warmup` (default 3) full rounds run exactly like measured rounds but with `Warmup: true`; ranked distributions discard them, while connection-phase reporting may retain their initial cold-start timing separately.
6. **Interleaved rounds** — `--rounds` per category (mode default: quick 50, standard 250, precise 500). Each round: server order is reshuffled (seeded), each server runs its categories in per-server shuffled order, and a finish-to-start `--gap` (default 40ms) applies across category and round boundaries. Every launch waits for the global `--pace` tick, and servers are bounded by the `--concurrency` in-flight semaphore. Failed queries are retried with the full sequence retained as one end-to-end sample.
7. **Statistics** — samples are grouped by server and category; `stats.Compute` admits only category-correct DNS results into latency distributions and calculates transport, validity and retry rates. `stats.ComputePhases` keeps cold-start and steady-state measurements separate.
8. **Ranking** — `rank.ScoreServers` runs for all three modes using the presets, optionally overridden by `--weights` JSON. Every enabled category must have valid data. `--ranking` selects the printed mode; equal-weight `latency` is the default.
9. **Comparisons** — the selected ranking's top three are compared pairwise, and its winner is compared with each ranked system server, using a 1,000-draw paired bootstrap that recomputes the full aggregate latency cost.
10. **Report** — ranked measurement chart, neutral benchmark overview, latency-cost table, compact server-characteristics table, optional `--details` metrics table, and factual sections for the detected DNS configuration, run coverage and statistical comparisons. It does not print highlights, issues, caveats or a final recommendation.
11. **Export** — `--export json,csv,txt,html` writes timestamped files to `--out` with `--prefix`; `--include-raw` keeps raw samples in the JSON. `--open` writes the HTML report (implying `--export html`) and opens it in the browser. The terminal prints a compact one-line run summary, a single merged ranking (bar · latency cost · loss, with the current DNS marked and statistical ties flagged) and a footer pointing to the HTML report; the full latency-cost breakdown, server-characteristics table and prose sections live in the HTML report, and `--details` adds the per-category metrics table to the terminal.

Interruption: the first Ctrl+C cancels the context, the engine stops cleanly and the report is produced from partial results; a second Ctrl+C aborts.

## Concurrency strategy

- **Global pacer** — a single seeded clock spaces all query launches at least `--pace` apart (default 20ms plus jitter; 0 disables). Rounds, retries and triage probes all pass through it, so the worst-case instantaneous burst is one packet on any hardware. This is the primary protection against self-induced congestion — without it, launch bursts inflate tail latencies and produce timeout loss that gets misattributed to the servers under test. The interval is adaptive (AIMD): every completed query feeds `pacer.observe` — 50 answered queries shorten the interval by `--pace`/8 (floor `--pace`/4); timeouts on ≥3 distinct previously-answering servers within a 2s sliding window double it (ceiling 8× `--pace`), emit an `EvPaceAdjust` event and start a one-timeout cooldown. Timeouts from servers that never answered are excluded from the signature.
- **Semaphore** — a channel of capacity `--concurrency` bounds how many queries are in flight at once (shared by triage and rounds); it acts as backpressure when many servers stop answering.
- **Per-round barrier** — a `sync.WaitGroup` closes every round: no server starts round N+1 until every server has finished round N, keeping rounds time-aligned across servers.
- **Seeded shuffling** — all randomness derives from one run seed (`--seed`, random when 0, recorded in exports). The per-round server order uses `PCG(seed, seq<<1|1)`; each server's per-round RNG uses `PCG(seed, seq<<24 | stableIdx<<1)` for category order and random labels. The same seed reproduces the same schedule and query names.
- **Cold vs persistent sessions** — persistent (default): one `Querier` per server is opened before warmup and reused (TCP/DoT keep the connection; DoH keeps HTTP keep-alives; UDP keeps one connected socket per server, so a whole run consumes one NAT/conntrack entry per server instead of one per query — small router state tables are a real-world loss source). A persistent UDP socket survives timeouts and drains stale late replies by DNS message ID before accepting an answer. Cold mode explicitly builds and tears down a querier per query. The two modes are never mixed within a run.
- **Per-server pacing** — the engine records each server's last query completion under a mutex. Before every subsequent query it waits until `lastQueryEnd + PerServerGap`, so round barriers cannot accidentally bypass pacing.
- **Context cancellation** — one `context.Context` flows from the signal handler through triage, rounds, queries and sleeps; canceled queries are marked `canceled` and not recorded as samples.
- **Pause/resume** — the engine has a pause gate (`Engine.Pause`/`Engine.Resume`); workers block at round and query boundaries while paused. In the current CLI the gate is driven by the connectivity watchdog.
- **Connectivity watchdog with canary** — enabled by default (`ConnectivityWatch`). If two consecutive rounds end with 100% of their queries timing out, the engine emits `conn-lost`, pauses the gate, and sends a canary query (first cached domain to the first active server) every 5 seconds. On the first answered canary it emits `conn-restored` and resumes. This prevents an internet outage from being recorded as server loss.

The engine communicates with the UI through a buffered event channel (`triage`, `sample`, `round-done`, `state-change`, `warn`, `conn-lost`, `conn-restored`, `done`).

For an interactive TTY, the live UI is a Bubble Tea `Model` driven by benchmark messages through `Update` and rendered from `View`. Bubble Tea owns the alternate screen, coalesces state changes at a bounded frame rate, repaints in place, and reports `WindowSizeMsg` resize events. Each visible resolver is a multi-line block with one shared-axis latency bar per enabled category. Category names and markers live only in the ordered legend; resolver rows spend that space on the bars. Adjacent categories alternate lower and upper half-height blocks so the default pair touches at the line boundary. The shared linear scale is the P90 of the displayed statistic across visible active resolvers, and values above it carry an overflow marker. The statistic follows `--sort` for median, mean and p95; loss/name sorting keeps median bars, while `cost` (alias `score`) uses median until the final ranking exists. The reliability cell labels unanswered-query loss with its numerator/denominator and reports semantically invalid answers separately.

The renderer derives name and bar widths from the viewport, switches to text-only category rows below 72 columns, never emits partial resolver blocks, and summarizes hidden active and sidelined servers in its footer. It also polls the viewport between rounds for Windows, where `SIGWINCH` is unavailable. The CLI keeps its own signal handling and disables Bubble Tea input, so Ctrl+C still cancels the engine cleanly. Non-TTY output remains line-oriented and contains no ANSI control sequences.

## Error taxonomy (`ErrKind`) and how each becomes a metric

Transport errors are classified into six kinds:

| Kind | Meaning | Effect on metrics |
| --- | --- | --- |
| `timeout` | No response within `--timeout` (context deadline or socket deadline) | Counted in `Timeouts`; contributes to `loss_pct`; drives triage "offline" reasons and the watchdog |
| `network` | Dial/read/write failure (refused, unreachable, reset, …) | Counted in `Errors`; contributes to `loss_pct` |
| `tls` | TLS handshake or certificate failure (DoT/DoH) | Counted in `Errors`; contributes to `loss_pct` |
| `http` | Non-200 status or HTTP request failure (DoH) | Counted in `Errors`; contributes to `loss_pct` |
| `protocol` | Malformed DNS message, ID mismatch, empty message, wrong Content-Type | Counted in `Errors`; contributes to `loss_pct`; on persistent streams it also invalidates the session's connection |
| `canceled` | The run's context was canceled (Ctrl+C) | The sample is discarded, not recorded; never pollutes statistics |

Transport responses (no error) increment `Answered`, but latency accepts only `QueryResult.ValidFor(category)`: cached/uncached need `NOERROR` with A or CNAME; TLD needs `NXDOMAIN`. `SERVFAIL` increments `Servfails`; other semantically wrong responses increment `Invalid`. Neither enters latency samples. Truncated UDP answers are retried over TCP transparently (`UsedTCP`). `loss_pct = (count − answered) / count × 100`; SERVFAIL and invalid replies are distinct reliability signals, and retried queries are tracked even when their final response succeeds.
