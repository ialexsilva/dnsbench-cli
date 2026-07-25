# Methodology

## The three query categories

Every measured sample is an A-record query belonging to one of three categories. Query names are generated per server, per round, from the run's seeded RNG:

```
round r, server at shuffled position pos, per-server RNG rng:

cached:    qname = cachedDomains[(r + pos) mod len(cachedDomains)]
uncached:  qname = randomLabel(rng, 16) + "." + uncachedZone     # only with --uncached-zone
tld:       qname = randomLabel(rng, 16) + "." + tldZone          # default zone: "com"

randomLabel(rng, 16) = 16 characters drawn from [a-z0-9]
```

- **cached** — a curated set of 106 popular domains, localized for Brazil. It combines the public [Similarweb Brazil ranking for June 2026](https://www.similarweb.com/top-websites/brazil/) with the [Semrush Brazil Top Websites for June 2026](https://www.semrush.com/trending-websites/br/all), then preserves useful non-Asian entries from the prior global set. Adult and gambling domains are excluded so resolvers that intentionally filter those categories are not penalized. Domains owned by benchmarked DNS operators (Google and Cloudflare) are excluded to avoid a potential home-domain advantage. Russian, Japanese and other region-specific Asian services that are not relevant to the Brazilian ranking are also excluded. The editable source is [`internal/model/cached_domains.json`](../internal/model/cached_domains.json); rebuild the executable after changing it because the file is embedded at compile time. Measures the hot-cache answer path you feel most often while browsing.
- **uncached** — a random label under a zone **you control**, so every query is a guaranteed cache miss and forces a real recursion to your zone's authoritative server. Disabled unless `--uncached-zone` is set (dnsbench prints a notice and drops the category rather than fake it against zones you do not own).
- **tld (recursive/TLD)** — a random label under a public TLD (default `com`, configurable with `--tld-zone`). Virtually all of these names do not exist, so the resolver must walk its recursion path down to the TLD infrastructure and return NXDOMAIN. This exercises the resolver's recursion machinery without needing infrastructure of your own.

Default categories are `cached` and `tld`; passing `--uncached-zone` enables all three, and `--categories` selects any subset explicitly.

## What counts as a valid resolution

A packet arriving from a resolver is not automatically a successful DNS lookup. dnsbench records transport responses separately from semantically valid resolutions:

- `cached` and `uncached` require `NOERROR` plus an A or CNAME answer;
- `tld` requires `NXDOMAIN`, which is the expected result for its random nonexistent names;
- `SERVFAIL`, `REFUSED`, `FORMERR`, `NOTIMP`, empty `NOERROR` responses and unexpected RCODEs are not latency samples.

Invalid responses remain visible in the reliability counters and penalties. Every category enabled for the run must contain at least one valid resolution for a server to receive a rank; a broken or unsupported category can therefore never disappear silently from that server's latency cost.

## Warmup: why and how it is excluded

The first `--warmup` rounds (default 3) run exactly like measured rounds — same shuffling, same query generation — but every sample is flagged `Warmup: true`, and the statistics engine skips warmup samples when computing the ranked latency distributions. Warmup exists because early queries pay one-time costs that do not represent steady state: establishing a persistent connection, the resolver populating its cache, local network warm-up (ARP/NDP resolution and route caches), and OS socket path warm-up.

Connection phase reporting retains non-reused, connection-bearing results as a separate `cold_start_ms` metric, while non-warmup reused results feed `steady_state_ms`. In the default persistent mode the cold set is normally the initial connection; in explicit cold mode it describes the average first-contact cost. The latency cost uses the non-warmup distribution only, so cold-start and steady-state costs are never blended invisibly.

## Monotonic clock

All RTTs are measured with Go's `time.Now()` / `time.Since()`, which carry a monotonic clock reading. Latency measurements are therefore immune to wall-clock steps (NTP adjustments, manual clock changes, DST) during the run.

## Per-round interleaving and randomization

Instead of testing server A to completion and then server B (which would let network conditions drift between them), dnsbench interleaves: **every round queries every active server once per category**, and no server starts round N+1 until all servers finished round N. Within a round:

- The **server order is reshuffled every round** with a seeded PCG generator, so no server systematically benefits from always being measured first or last.
- Each server runs its **categories in per-server shuffled order**. A `--gap` pause (default 40ms) is enforced from the completion of one query to the start of the next query to that server, including across category and round boundaries.
- Every query launch — across all servers, including triage probes and retries — passes through a **global pacer**: launches are spaced at least `--pace` apart (default 20ms, plus a small seeded jitter; `--pace 0` disables). Pacing bounds the instantaneous packet burst to one query per tick regardless of hardware, so the benchmark cannot congest the local Wi-Fi link, NAT router or uplink and then misattribute the resulting drops and queueing delay as server loss and latency.
- The pace **adapts AIMD-style** while the run progresses. Speed-up: every 50 answered queries with no congestion signal shorten the interval by an eighth of `--pace`, down to a floor of a quarter of `--pace`, so clean networks earn back wall-clock time. Back-off: when timeouts land on **3 or more distinct servers that had previously answered** within a 2-second window — the signature of local congestion, not of any one server — the interval doubles, up to a ceiling of 8× `--pace`, and further backoffs are suppressed for one query-timeout period so the tail of the same event is not double-counted. Servers that never answered do not count toward the signature, so dead or blackholed endpoints (e.g. unroutable IPv6) cannot slow the run. Each backoff is reported live as a pacing notice.
- Cross-server parallelism is additionally capped by the `--concurrency` in-flight limit (default 8), which acts as a safety valve: when many servers stop answering, at most that many queries wait on timeouts at once, which naturally throttles the stream.

All randomness (orders, labels) derives from a single seed. `--seed 0` (the default) picks a random seed, which is recorded in exports; re-running with `--seed N` reproduces the exact schedule and query names.

## Triage

Before the benchmark, triage cheaply removes servers that would waste the run's time budget (enabled by default; skip with `--no-triage`):

- Each server gets up to `--triage-attempts` probes (default 10) for the first cached domain, stopping early as soon as one valid A/CNAME answer arrives at or below `--triage-threshold` (default 50ms). Triage probes go through the same global pacer and in-flight cap as benchmark queries, so triage cannot flood the local network and sideline servers for self-induced loss.
- **States**: `active` (valid answer within threshold) proceeds to the benchmark; `offline` (zero valid answers — the reason records the dominant error kind, e.g. "8 of 10 probes timed out"); `benched`, shown as **"out of contention"** (valid answers arrived but the best RTT exceeded the threshold — the server works, it is just too slow to compete at this vantage point).
- `--force-all` keeps slow servers `active` instead of benching them.

**Out-of-contention is reversible.** Triage decisions apply to a single run and are never persisted. To include a benched server, re-run with a higher `--triage-threshold`, with `--force-all`, or with `--no-triage`.

## Retries and timeouts

Every query attempt has a hard `--timeout` (default 3s), enforced through context deadlines down to the socket. `--retries` (default 0) re-issues a failed query after `--retry-interval` (default 200ms). A retried success remains one sample, but its measured latency is the full end-to-end time from the first attempt through failed attempts, retry waits and the final answer. Attempts, failed attempts, timeout attempts and retried-query percentage are also exported separately.

If all attempts fail, the query contributes to loss. `SERVFAIL` is a transport response rather than loss, but it is not a valid resolution and receives its own reliability penalty. Retried queries receive a smaller separate penalty because a final success does not erase the delay and fragility the user experienced.

## Cold versus persistent connections (DoH/DoT)

Encrypted transports pay a connection cost (TCP + TLS handshake, plus HTTP setup for DoH) that plain UDP does not. dnsbench keeps two regimes strictly separate via `--session`:

- **persistent** (default) — one session per server is opened before the rounds and reused throughout: DoT/TCP reuse the established connection, DoH keeps HTTP keep-alives (HTTP/2 where negotiated). Warmup absorbs initial setup, and the measured rounds represent the steady-state behavior used by modern operating systems and applications.
- **cold** — every query opens a fresh connection and tears it down: for DoT a full TCP+TLS handshake per query; for DoH keep-alives are disabled and idle connections dropped after each request. Use this explicitly to study worst-case first-contact cost.

The mode applies to the entire run; cold and persistent numbers must never be compared as if they measured the same workload. Per-phase timings (connect, TLS handshake, HTTP setup, query), cold-start latency, steady-state latency and cold/reused sample counts are retained so encrypted transport setup remains inspectable without contaminating the default steady-state ranking.

## Test zones: setting up your own wildcard zone for the uncached category

The uncached category needs a zone where any random name has a definite, fast, non-cacheable-for-long answer. A wildcard record with a low TTL does this. BIND example for a delegated zone `bench.example.com`:

```
$TTL 60
@   IN SOA ns1.example.com. hostmaster.example.com. (
        2026072301 ; serial
        3600       ; refresh
        600        ; retry
        604800     ; expire
        60 )       ; negative-caching TTL
    IN NS  ns1.example.com.
*   IN A   192.0.2.1
```

The wildcard `*` answers every random label with an A record, and the 60-second TTL keeps any accidental repeat from being served from cache for long. Then run:

```sh
dnsbench run --uncached-zone bench.example.com
```

**RFC 8198 caveat (aggressive NSEC caching).** If the test zone is DNSSEC-signed, validating resolvers may use aggressive use of NSEC/NSEC3 (RFC 8198) to synthesize answers — including wildcard-derived answers and NXDOMAINs — from previously cached range proofs, **without ever asking your authoritative server**. That silently breaks the "every random name is a guaranteed cache miss" assumption and can make a resolver look faster than its real recursion path. For clean uncached measurements, prefer an unsigned test zone, or verify (via your authoritative server's query logs) that the resolvers under test actually reach it for every query.

## The TLD category, volume and traffic ethics

The recursive/TLD category sends random labels under a public TLD (`--tld-zone`, default `com`). These queries land on TLD-level infrastructure that is engineered for enormous query volumes, and the load dnsbench generates is deliberately modest and configurable:

- volume is bounded by `--rounds` (quick 50 / standard 250 / precise 500 per category) and can be reduced arbitrarily;
- the global `--pace` launcher (default 20ms between any two launches), the per-server `--gap` (default 40ms) and the `--concurrency` in-flight cap rate-limit the stream;
- random labels are queried once — dnsbench does not loop on the same nonexistent name.

**Do not point `--tld-zone` at someone else's domain.** Random-label floods against a third party's zone shift cost onto infrastructure not built for it and can look like an attack. If you want miss-path measurements against a normal zone, use `--uncached-zone` with a zone you own; keep `--tld-zone` at an actual TLD.

## The four NXDOMAIN checks and classification

The probe asks for names that must not exist and inspects how honestly the server reports it:

1. `nonexistent-tld` — `dnsbench-<random12>` (a bare, nonexistent TLD-level name)
2. `nonexistent-com` — `<random12>.com` (or your `--tld-zone` equivalent in the benchmark; the probe uses `com` by default)
3. `www-nonexistent` — `www.<random12>.com`
4. `controlled-zone` — `<random12>.<uncached-zone>` (only when `--uncached-zone` is set)

Random labels are 12 characters of `[a-z0-9]` from a cryptographically seeded generator. Each response is classified:

| Behavior | Meaning |
| --- | --- |
| `NXDOMAIN (correct)` | The honest answer: the name does not exist |
| `NOERROR empty` | NOERROR with no records — unusual but not an interception |
| `interception (IP)` | An A/AAAA answer was fabricated — the server redirects nonexistent names to an IP |
| `interception (CNAME)` | A CNAME answer was fabricated |
| `blocked` | REFUSED |
| `timeout` | No answer |
| `other` | Anything else (unexpected RCODE or error) |

The server's `NX interception` verdict is **yes** if any check was intercepted, **no** if every determined check was clean (`NXDOMAIN` or `NOERROR empty`), and **unknown** otherwise. **Interception is not automatically malware**: providers redirect DNS errors to search pages, advertising or filtering portals. dnsbench reports the behavior (and applies a ranking penalty because it breaks protocol semantics), and the report states explicitly that this behavior is not classified as malware.

## DNSSEC: the DO/CD matrix

Three probe queries build the matrix (DO = DNSSEC OK bit, CD = Checking Disabled bit):

| Query | DO | CD | What it observes |
| --- | --- | --- | --- |
| signed domain (default `cloudflare.com`), type A | 1 | 0 | `returns_rrsig` (RRSIG records present), `ad_on_signed` (AD bit set), `signed_resolves` |
| known-bogus domain (default `dnssec-failed.org`), type A | 1 | 0 | `bogus_servfail` — a validating resolver must answer SERVFAIL |
| known-bogus domain, type A | 1 | 1 | `bogus_with_cd_resolves` — with checking disabled, a validator should hand back the (invalid) data |

The combined `validating` verdict:

- **no** — the bogus domain resolved (the server did not validate);
- **yes** — the bogus domain SERVFAILed **and** the signed query showed RRSIG or the AD bit;
- **partial** — the bogus domain SERVFAILed but neither RRSIG nor AD was observed;
- **unknown** — the checks errored out.

The default test domains live in `internal/probe/config.go` (`Config.SignedDomain`, `Config.BogusDomain`; `Config.UnsignedDomain` is also defined). The CLI does not currently expose flags for them; to change them, edit `probe.DefaultConfig` or pass a custom `probe.Config` when using the probe package as a library.

## Rebinding protection

DNS rebinding attacks use public hostnames that resolve to private addresses to reach devices inside your network. The probe uses the public `sslip.io` service, whose hostnames encode the address they resolve to, and checks whether the server blocks such answers:

- IPv4 (A): `127.0.0.1.sslip.io` → 127.0.0.1, `10.10.10.10.sslip.io` → 10.10.10.10, `172.16.1.1.sslip.io` → 172.16.1.1, `192.168.1.1.sslip.io` → 192.168.1.1
- IPv6 (AAAA): `--1.sslip.io` → ::1, `fd00--1.sslip.io` → fd00::1

Per case: NXDOMAIN, REFUSED or an empty NOERROR count as **blocked**; an answer containing the expected private address counts as **unprotected**; timeouts and unexpected answers are **undetermined**. Per family the verdict is yes (all determined cases blocked), no (none blocked), or partial; the overall verdict combines v4 and v6.

**Guarantee: only DNS responses are examined.** dnsbench never opens an HTTP, TLS or any other connection to the returned addresses — the check is purely about what the resolver answers, and no traffic is ever sent to 127.0.0.1, 192.168.1.1 or any other resolved address.

## Modern extensions (optional, outside the ranking)

With `--extended` (on `probe` or `run`), three informational checks run. Their results are reported but **never affect latency cost or order**:

- **DNS64** — AAAA query for `ipv4only.arpa`; a synthesized AAAA answer means DNS64 is active.
- **QNAME minimization** — TXT query for `qnamemintest.internet.nl`; the answer text ("HOORAY" vs "NO") reports whether the resolver minimizes query names toward authoritative servers.
- **HTTPS record** — HTTPS-type query for the signed test domain; checks whether the resolver serves modern HTTPS/SVCB records.

## Aggregate ranking and statistical significance

The default `latency` ranking gives every enabled query category equal weight. If the uncached category is disabled for the entire run, only cached and TLD remain and each receives half of the base cost. The alternative `browsing` and `reliability` presets remain available through `--ranking`.

Pairwise conclusions compare the same aggregate latency cost shown in the selected ranking, not one arbitrarily chosen category. dnsbench performs 1,000 paired bootstrap resamples of complete rounds. Keeping both resolvers and every category from a selected round together preserves shared congestion and timing conditions; each bootstrap draw recomputes latency, jitter, loss, validity, retry and characterization penalties from scratch.

The resulting cost-difference distribution produces a two-sided p-value and a 95% percentile interval. Classifications are:

- **statistically significant** — the 95% interval excludes zero and the difference exceeds the relevance floor;
- **likely** — the difference exceeds the relevance floor and the bootstrap p-value is below 0.20, but the 95% interval still crosses zero;
- **inconclusive** — the data cannot reliably order the pair, or fewer than 5 complete paired rounds are available;
- **practically irrelevant** — the absolute cost difference is below `max(3 ms, 10% of the smaller cost)`, regardless of statistical significance.

The report declares the selected ranking's top two effectively tied when their aggregate comparison is inconclusive or negligible.

## Methodological limitations

These caveats document how the measurements should be interpreted. They are not emitted as a final verdict or recommendation in the CLI report:

1. Results are valid for the network and time of the test.
2. Congestion can change the ranking.
3. Unstable Wi-Fi can produce false loss.
4. A local router can hide the real upstream DNS.
5. Lower DNS latency does not guarantee faster full page loads.
6. CDN geolocation can vary by resolver.
7. EDNS Client Subnet can influence responses of geo-distributed services.
8. Malware, ad or content filtering can deliberately modify responses.
9. DoH and DoT protect transport to the resolver but do not by themselves prove the operator's privacy policy.
