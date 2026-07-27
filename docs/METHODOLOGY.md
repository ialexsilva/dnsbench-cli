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

- **cached** — a curated set of 72 popular domains on generic TLDs, ranked globally rather than for one country. 66 of them come from the [Semrush most visited websites in the world](https://www.semrush.com/trending-websites/global/all) top 100 for May 2026; the other 6 are carried over from the previous set because they were already vetted and still widely used. Exclusions: adult and gambling domains, so resolvers that intentionally filter those categories are not penalized; **every country-edition domain** (`.br`, `.de`, `.it`, `.co.uk`, `.pt`, `.co.jp`, `.in`, `.vn`, `.ru`, `.su`), because a national storefront or edition measures one country rather than the global web — a brand on a generic TLD such as `globo.com` is kept, so the rule is about the extension, not the company; region-specific Asian services on generic TLDs (naver, rakuten, note); volatile piracy aggregators whose traffic appears and vanishes between snapshots; and any domain owned by a resolver dnsbench benchmarks (Google, Cloudflare, Cisco/OpenDNS, Mullvad, NextDNS, AdGuard, Quad9, Control D) to avoid a home-domain advantage. `.ai`, `.io`, `.net`, `.org`, `.tv` and `.us` are kept: those domains are the service's global home, not a national edition. A maintainer pass then trims the filtered result by hand, dropping redundant corporate properties, redirect-only short-link domains, and entries whose traffic came from a single event — so the file is smaller than the ranking it derives from, on purpose. Rank order inside the file is informational only — `buildQName` cycles the list uniformly, so every domain is queried equally often, and the count is therefore not a tuning knob. The editable source is [`internal/model/cached_domains.json`](../internal/model/cached_domains.json); rebuild the executable after changing it because the file is embedded at compile time. Measures the hot-cache answer path you feel most often while browsing.
- **uncached** — a random label under a zone **you control**, so every query is a guaranteed cache miss and forces a real recursion to your zone's authoritative server. Disabled unless `--uncached-zone` is set (dnsbench prints a notice and drops the category rather than fake it against zones you do not own).
- **tld (recursive/TLD)** — a random label under a public TLD (default `com`, configurable with `--tld-zone`). Virtually all of these names do not exist, so the resolver must walk its recursion path down to the TLD infrastructure and return NXDOMAIN. This exercises the resolver's recursion machinery without needing infrastructure of your own.

Default categories are `cached` and `tld`; passing `--uncached-zone` enables all three, and `--categories` selects any subset explicitly.

## Characterization sessions

Before latency measurement, the characterization phase checks reachability, DNSSEC and NXDOMAIN handling. It creates one persistent `Querier` per server and reuses that session for every check. This matters most for DoT, DoH, DoH/3 and DoQ: opening a fresh encrypted connection for every qualitative check would turn a normal probe into a burst of TCP, TLS or QUIC handshakes and could trigger NAT pressure or server-side connection limits.

Characterization has no launch pacer: its small qualitative workload is already bounded by the `--concurrency` limit (default 8), and its timing is never admitted into benchmark latency distributions.

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
- Every benchmark query launch — across all servers, including triage probes and retries — passes through a **global pacer** (`--pace 0` disables it). Pacing bounds the instantaneous packet burst to one query per tick regardless of hardware, so the benchmark cannot congest the local Wi-Fi link, NAT router or uplink and then misattribute the resulting drops and queueing delay as server loss and latency.
- When `--pace` is omitted, the 20ms default uses a small seeded jitter and adaptive safety backoff, but **never launches faster than 20ms**. A backoff requires timeouts from **3 or more independent resolver failure domains** whose attempts started within the same 2-second window and whose individual endpoints answered within the preceding 30 seconds. Known operators define failure domains; without operator metadata, the dial address defines the domain even when protocols differ, with a DoH hostname used only when no address is pinned. This prevents several Google, Cloudflare or same-address transports from impersonating independent evidence. The 30-second freshness rule also excludes endpoints that answered only much earlier in the run.
- The signal is deliberately reported as **possible shared-path congestion**, not proof of local congestion: an ISP, route, authoritative target or other shared dependency can produce the same pattern. The live notice stays compact, while its structured event and JSON `pace_adjustments` evidence include the affected endpoint IDs, failure-domain labels, query categories and protocols, making category- or protocol-concentrated incidents visible instead of silently overstating confidence. The correlation window uses attempt launch times rather than timeout completion times, so transports with different timeout behavior can still be compared correctly.
- A qualifying burst moves the interval first to 40ms and then at most 80ms. Further backoffs are suppressed for at least 3 seconds or one query timeout, whichever is longer, so the tail of the same event is not double-counted. After that cooldown, every 50 answered queries recover by 10ms until the baseline is restored: `80ms → 70ms → 60ms → … → 20ms`. Runs with fewer than three independent, recently healthy domains retain baseline pacing because DNS observations alone cannot separate one resolver's instability from path trouble.
- Passing `--pace DURATION` selects **fixed pacing**: there is no adaptation and no jitter. The pacer uses `DURATION` for every reservation and never changes that value; observed launch timing can still vary because of occupied query slots and OS scheduling. This distinction is retained in exports as `pace_adaptive`.
- Cross-server parallelism is additionally capped by the `--concurrency` limit (default 8). A permit is acquired for each actual query attempt, immediately before its global pacing reservation, and released when that attempt finishes; per-server gaps, category processing and retry delays do not retain it. This acts as a safety valve when many servers stop answering while allowing another resolver to use capacity during those waits.

All randomness (orders, labels) derives from a single seed. `--seed 0` (the default) picks a random seed, which is recorded in exports; re-running with `--seed N` reproduces shuffled positions, per-server category order and query names. Wall-clock admission order can still vary with goroutine and network timing.

## Triage

Before the benchmark, triage cheaply removes servers that would waste the run's time budget (enabled by default; skip with `--no-triage`):

- Each server gets up to `--triage-attempts` probes (default 10) for the first cached domain. Built-in resolvers stop early as soon as one valid A/CNAME answer arrives at or below `--triage-threshold` (default 200ms). Custom, user-list and system resolvers stop on the first valid answer regardless of RTT. Triage probes go through the same global pacer and in-flight cap as benchmark queries, so triage cannot flood the local network and sideline servers for self-induced loss.
- **States**: `active` proceeds to the benchmark; `offline` means zero valid answers (the reason records the dominant error kind, e.g. "8 of 10 probes timed out"); `benched`, shown as **"out of contention"**, applies only to built-in resolvers whose valid answers all exceeded the threshold.
- `--force-all` keeps slow built-in resolvers `active` instead of benching them.

**Out-of-contention is reversible.** Triage decisions apply to a single run and are never persisted. To include a benched built-in server, re-run with a higher `--triage-threshold`, with `--force-all`, or with `--no-triage`.

## Retries and timeouts

Every query attempt has a hard `--timeout` (default 3s), enforced through context deadlines down to the socket. `--retries` (default 0) re-issues a failed query after `--retry-interval` (default 200ms). A retried success remains one sample, but its measured latency is the full end-to-end time from the first attempt through failed attempts, retry waits and the final answer. Attempts, failed attempts, timeout attempts and retried-query percentage are also exported separately.

If all attempts fail, the query contributes to loss. `SERVFAIL` is a transport response rather than loss, but it is not a valid resolution and receives its own reliability penalty. Retried queries receive a smaller separate penalty because a final success does not erase the delay and fragility the user experienced.

## Cold versus persistent connections (DoT/DoH/DoH3/DoQ)

Encrypted transports pay a connection cost (TCP + TLS handshake, plus HTTP setup for DoH) that plain UDP does not. dnsbench keeps two regimes strictly separate via `--session`:

- **persistent** (default) — one session per server is opened before the rounds and reused throughout: DoT/TCP reuse the established connection, DoH keeps HTTP keep-alives (HTTP/2 where negotiated), and DoH/3 and DoQ keep the QUIC connection, opening one new stream per query. Warmup absorbs initial setup, and the measured rounds represent the steady-state behavior used by modern operating systems and applications.
- **cold** — every query opens a fresh connection and tears it down: for DoT a full TCP+TLS handshake per query; for DoH keep-alives are disabled and idle connections dropped after each request; for DoH/3 and DoQ a fresh QUIC handshake per query. Use this explicitly to study worst-case first-contact cost.

DoQ timeouts and caller cancellation abort only the affected stream with `DOQ_REQUEST_CANCELLED`; a healthy persistent QUIC connection remains available for the next query. A response is accepted only after exactly one framed DNS message followed by the stream FIN. Missing FIN, trailing response bytes, malformed DNS and message-ID violations are protocol errors and invalidate the connection with `DOQ_PROTOCOL_ERROR`. Ordinary EDNS-enabled DoQ queries also carry EDNS Padding to a 128-byte DNS-message boundary.

The mode applies to latency-measurement samples; cold and persistent numbers must never be compared as if they measured the same workload. Characterization is qualitative, always reuses one session per server and bounds simultaneous server checks with its concurrency limit. Per-phase timings (connect, TLS handshake, HTTP setup, query), cold-start latency, steady-state latency and cold/reused sample counts are retained so encrypted transport setup remains inspectable without contaminating the default steady-state ranking.

QUIC establishes the transport and the cryptographic handshake together, so `doh3` and `doq` report that single handshake under the TLS phase and leave the connect phase at zero. A QUIC transport is therefore expected to show a lower total setup cost than DoT or DoH at the same round-trip distance; that difference is real, not an artifact of how the phases are attributed.

## Bootstrap resolution

An encrypted endpoint identified by hostname has a chicken-and-egg problem: the hostname has to be resolved before the encrypted connection exists, and that lookup travels in plaintext to whatever resolver the system is configured with. On a network that intercepts port 53 — a common router default — those lookups are answered by the interceptor, so a run that appears to measure encrypted resolvers still emits plaintext DNS.

dnsbench avoids this by pinning: every encrypted endpoint in the built-in list carries the resolver's IP address, dials it directly, and uses the hostname only for SNI and certificate validation. No name resolution happens before an encrypted query.

Use `--protocols dot,doh,doh3,doq` to select only encrypted transports. Explicitly select `--system` together with the desired encrypted sources when the system-configured UDP resolver should remain in the same run as a comparison baseline despite that filter. Protocol selection does not exclude hostname-only custom entries, so it is a transport filter rather than a guarantee about their bootstrap lookup.

When an explicitly configured DoH/3 endpoint is not pinned, its hostname is resolved by the HTTP/3 transport with the query context. The same cancellation or deadline that bounds the request therefore also bounds bootstrap resolution.

Pinning fixes the IP for endpoints that would otherwise be selected by the operator's own DNS-based steering. For the anycast addresses that public resolvers publish this is the intended behavior — it is the same address their plaintext and DoT endpoints use — but it means a provider that steers clients to per-region hosts is measured at its published address rather than at whatever host its DNS would have returned for your location.

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
