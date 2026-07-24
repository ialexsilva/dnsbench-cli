# Formulas

All latency statistics are computed over the **semantically valid, non-warmup** samples of one server in one category, in milliseconds. Cached and uncached queries require `NOERROR` plus A or CNAME; recursive/TLD queries require the expected `NXDOMAIN`. Transport responses that do not satisfy those rules remain in the reliability counters but never enter the latency distribution.

When a query is retried, its latency is the complete elapsed time from the first attempt through failures, retry intervals and the final valid answer. The worked examples below use the chronological sample set

```
x = [10, 12, 11, 14, 13]   (n = 5)
```

except where noted.

## Mean

```
mean = (1/n) · Σ xᵢ
```

Example: (10 + 12 + 11 + 14 + 13) / 5 = 60 / 5 = **12.0 ms**.

## Sample variance and standard deviation

Sample (unbiased, n−1) variance:

```
s² = Σ (xᵢ − mean)² / (n − 1)
s  = √s²
```

Example: deviations (−2, 0, −1, 2, 1) → squares (4, 0, 1, 4, 1), sum = 10.
s² = 10 / 4 = **2.5 ms²**, s = √2.5 ≈ **1.581 ms**. (Defined as 0 when n < 2.)

## Median and percentiles (R-7 linear interpolation)

Percentile p (0 ≤ p ≤ 1) over the **sorted** samples x₍₀₎ … x₍ₙ₋₁₎:

```
pos  = p · (n − 1)
lo   = ⌊pos⌋ ,  hi = ⌈pos⌉ ,  frac = pos − lo
Q(p) = x₍lo₎ + frac · (x₍hi₎ − x₍lo₎)
```

The median is Q(0.50); the report also computes Q(0.90), Q(0.95), Q(0.99).

Example (sorted: [10, 11, 12, 13, 14]):
- median: pos = 0.5·4 = 2 → x₍₂₎ = **12.0 ms**
- P90: pos = 0.9·4 = 3.6 → lo = 3, frac = 0.6 → 13 + 0.6·(14−13) = **13.6 ms**
- P95: pos = 3.8 → 13 + 0.8·1 = **13.8 ms**
- P99: pos = 3.96 → 13 + 0.96·1 = **13.96 ms**

## Jitter

Mean absolute difference between consecutive samples in **chronological** order:

```
jitter = Σᵢ₌₁ⁿ⁻¹ |xᵢ − xᵢ₋₁| / (n − 1)
```

Example (chronological [10, 12, 11, 14, 13]): |12−10| + |11−12| + |14−11| + |13−14| = 2 + 1 + 3 + 1 = 7 → 7 / 4 = **1.75 ms**.

## 95% confidence interval (Student t)

```
CI95 = mean ± t(0.975, n−1) · s / √n
```

where t(0.975, df) is the two-sided 95% Student t critical value (computed numerically from the regularized incomplete beta function).

Example: t(0.975, 4) = 2.7764; margin = 2.7764 · 1.581 / √5 = 2.7764 · 0.7071 ≈ 1.963.
CI95 = 12.0 ± 1.963 = **[10.04, 13.96] ms**. (When n < 2 both bounds equal the mean.)

## Paired bootstrap of aggregate scores

The report compares the exact aggregate score used by the selected ranking. Let each complete measured round `r` contain every enabled category for both servers A and B. With `R` common rounds, one bootstrap replicate draws `R` round indices with replacement:

```
r* = [ randomChoice(1..R), …, randomChoice(1..R) ]   (R draws)

scoreA* = rankingScore(all A samples in r*)
scoreB* = rankingScore(all B samples in r*)
Δ*      = scoreA* − scoreB*
```

The resolver pair, all enabled categories, failed attempts and penalties from a chosen round remain together. This preserves the pairing created by shared network conditions. Latency distributions and the complete score are recomputed for every replicate; fixed probe penalties are retained.

dnsbench uses **1,000 deterministic replicates**, seeded from the run seed and pair identity. The 95% interval is the R-7 percentile interval `[QΔ*(0.025), QΔ*(0.975)]`. The finite-sample two-sided p-value is:

```
p = min(1, 2 · min(
    (count(Δ* ≤ 0) + 1) / (B + 1),
    (count(Δ* ≥ 0) + 1) / (B + 1)
))
```

where `B` is the number of successful bootstrap replicates. At least **5 complete paired rounds** are required.

## MDR (minimum difference of relevance) and the four-level classification

```
MDR = max( 3 ms , 10% · min(scoreA, scoreB) )
```

Classification, in order (real thresholds from the code):

| Condition | Level |
| --- | --- |
| \|Δ\| < MDR | `negligible` — "practically irrelevant" |
| bootstrap CI95 excludes 0 | `significant` — "statistically significant" |
| p < 0.20 | `likely` |
| otherwise | `inconclusive` |

(Fewer than 5 complete paired rounds short-circuits to `inconclusive`.)

## Ranking formula

Only servers in state `active` with at least one valid resolution in **every category enabled for the run** are ranked. A category with only errors or invalid DNS responses disqualifies that server instead of disappearing from its score. For a mode with weights w:

**Renormalized weighted base.** Let C be the categories enabled for the whole run, and L(c) the latency metric of category c (`median` or `p95`, per mode):

```
base = Σ_{c∈C}  ( w_c / Σ_{c'∈C} w_{c'} ) · L(c)
```

Renormalization means no server is punished for a category disabled for the whole run (e.g. uncached without a controlled zone): the configured categories' weights are rescaled to sum to 1. (If all enabled-category weights are 0, base falls back to their unweighted average.)

**Penalties**, each in milliseconds, added on top:

```
loss                  = PenaltyPerLossPctMs      · (unanswered / totalQueries · 100)
servfail              = PenaltyPerServfailPctMs  · (servfails  / totalQueries · 100)
invalid-response      = PenaltyPerInvalidPctMs   · (invalid    / totalQueries · 100)
retry                 = PenaltyPerRetryPctMs     · (retried    / totalQueries · 100)
jitter                = JitterWeight · ( Σ_{c∈C} jitter_c / |C| )
nxdomain-interception = PenaltyNXInterceptionMs        if NX interception verdict = yes
no-dnssec             = PenaltyNoDNSSECMs              if DNSSEC validating verdict ≠ yes
no-rebind-protection  = PenaltyNoRebindMs              if rebind overall = no
                      = PenaltyNoRebindMs / 2          if rebind overall = partial
```

```
total = base + Σ penalties
```

Servers are sorted by ascending total; totals within **0.01 ms** of the previous server share its rank (tie epsilon).

**Units of the penalty weights:**

| Weight | Unit |
| --- | --- |
| `PenaltyPerLossPctMs` | ms added per percentage point of unanswered queries |
| `PenaltyPerServfailPctMs` | ms added per percentage point of SERVFAIL answers |
| `PenaltyPerInvalidPctMs` | ms added per percentage point of semantically invalid DNS responses |
| `PenaltyPerRetryPctMs` | ms added per percentage point of queries that required more than one attempt |
| `PenaltyNXInterceptionMs` | flat ms, applied once |
| `PenaltyNoDNSSECMs` | flat ms, applied once |
| `PenaltyNoRebindMs` | flat ms, applied once (halved for partial protection) |
| `JitterWeight` | dimensionless multiplier on the mean per-category jitter (ms → ms) |

## Default weight tables (from `internal/rank/presets.go`)

| Mode | cached | uncached | tld | Metric | Loss | SERVFAIL | Invalid | Retry | NX interception | No DNSSEC | No rebind | Jitter |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `latency` (default) | 1/3 | 1/3 | 1/3 | median | 5 | 5 | 5 | 2 | 5 | 5 | 5 | 0.25 |
| `browsing` | 0.30 | 0.45 | 0.25 | median | 10 | 10 | 10 | 5 | 15 | 10 | 10 | 0.5 |
| `reliability` | 1/3 | 1/3 | 1/3 | p95 | 25 | 25 | 25 | 10 | 25 | 20 | 20 | 1.0 |

The Loss, SERVFAIL, Invalid and Retry columns are milliseconds per percentage point; the next three are flat milliseconds; Jitter is dimensionless.

Custom weights can be merged over these presets per mode with `--weights <file.json>`.

## Worked ranking example

Browsing mode, all three categories present, medians: cached 8.0 ms, uncached 22.0 ms, tld 30.0 ms. Weights sum to 1, so no renormalization:

```
base = 0.30·8.0 + 0.45·22.0 + 0.25·30.0 = 2.4 + 9.9 + 7.5 = 19.8 ms
```

Penalties: loss 1.0% → 10 · 1.0 = 10.0 ms; jitters (1.5, 2.0, 2.5) → mean 2.0 · 0.5 = 1.0 ms; DNSSEC not validating → 10.0 ms:

```
total = 19.8 + 10.0 + 1.0 + 10.0 = 40.8 ms
```

Renormalization example: same server but the uncached category was disabled for the run. Present weights sum to 0.30 + 0.25 = 0.55:

```
base = (0.30/0.55)·8.0 + (0.25/0.55)·30.0 = 0.5455·8.0 + 0.4545·30.0 = 4.36 + 13.64 = 18.0 ms
```
