package model

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	AppName    = "dnsbench"
	AppVersion = "0.5.0"
)

type Protocol string

const (
	ProtoUDP  Protocol = "udp"
	ProtoTCP  Protocol = "tcp"
	ProtoDoT  Protocol = "dot"
	ProtoDoH  Protocol = "doh"
	ProtoDoH3 Protocol = "doh3"
	ProtoDoQ  Protocol = "doq"
)

func AllProtocols() []Protocol {
	return []Protocol{ProtoUDP, ProtoTCP, ProtoDoT, ProtoDoH, ProtoDoH3, ProtoDoQ}
}

func EncryptedProtocols() []Protocol {
	var out []Protocol
	for _, p := range AllProtocols() {
		if p.Encrypted() {
			out = append(out, p)
		}
	}
	return out
}

func (p Protocol) DefaultPort() int {
	switch p {
	case ProtoDoT, ProtoDoQ:
		return 853
	case ProtoDoH, ProtoDoH3:
		return 443
	default:
		return 53
	}
}

func (p Protocol) Encrypted() bool {
	switch p {
	case ProtoDoT, ProtoDoH, ProtoDoH3, ProtoDoQ:
		return true
	}
	return false
}

func (p Protocol) UsesURL() bool { return p == ProtoDoH || p == ProtoDoH3 }

func (p Protocol) OverQUIC() bool { return p == ProtoDoH3 || p == ProtoDoQ }

func (p Protocol) Label() string {
	switch p {
	case ProtoUDP:
		return "UDP/53"
	case ProtoTCP:
		return "TCP/53"
	case ProtoDoT:
		return "DoT"
	case ProtoDoH:
		return "DoH"
	case ProtoDoH3:
		return "DoH/3"
	case ProtoDoQ:
		return "DoQ"
	}
	return string(p)
}

type Source string

const (
	SourceSystem  Source = "system"
	SourceBuiltin Source = "builtin"
	SourceUser    Source = "user"
)

func (s Source) Label() string {
	switch s {
	case SourceSystem:
		return "system"
	case SourceBuiltin:
		return "built-in"
	case SourceUser:
		return "user"
	}
	return string(s)
}

type Scope string

const (
	ScopeLoopback  Scope = "loopback"
	ScopePrivate   Scope = "private"
	ScopeLinkLocal Scope = "link-local"
	ScopeULA       Scope = "ula"
	ScopeCGNAT     Scope = "cgnat"
	ScopePublic    Scope = "public"
	ScopeUnknown   Scope = "unknown"
)

func (s Scope) IsLocal() bool { return s != ScopePublic && s != ScopeUnknown }

func (s Scope) Label() string {
	switch s {
	case ScopeLoopback:
		return "loopback"
	case ScopePrivate:
		return "private (RFC 1918)"
	case ScopeLinkLocal:
		return "link-local"
	case ScopeULA:
		return "IPv6 ULA"
	case ScopeCGNAT:
		return "CGNAT"
	case ScopePublic:
		return "public"
	}
	return "unknown"
}

type Server struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Operator    string   `json:"operator,omitempty"`
	Address     string   `json:"address,omitempty"`
	Port        int      `json:"port,omitempty"`
	TLSHostname string   `json:"tls_hostname,omitempty"`
	DoHURL      string   `json:"doh_url,omitempty"`
	Protocol    Protocol `json:"protocol"`
	Notes       string   `json:"notes,omitempty"`
	Source      Source   `json:"source"`
	Interface   string   `json:"interface,omitempty"`
	SystemRole  string   `json:"system_role,omitempty"`
	Enabled     bool     `json:"enabled"`
}

func (s Server) EffectivePort() int {
	if s.Port != 0 {
		return s.Port
	}
	return s.Protocol.DefaultPort()
}

func (s Server) Endpoint() string {
	if s.Protocol.UsesURL() && s.DoHURL != "" {
		return s.DoHURL
	}
	addr := s.Address
	if strings.Contains(addr, ":") {
		addr = "[" + addr + "]"
	}
	return fmt.Sprintf("%s:%d", addr, s.EffectivePort())
}

func (s Server) IP() (netip.Addr, bool) {
	a, err := netip.ParseAddr(s.Address)
	if err != nil {
		return netip.Addr{}, false
	}
	return a, true
}

func (s Server) IsIPv6() bool {
	a, ok := s.IP()
	return ok && a.Is6() && !a.Is4In6()
}

func (s Server) Key() string {
	return strings.ToLower(string(s.Protocol) + "|" + s.Address + "|" +
		fmt.Sprint(s.EffectivePort()) + "|" + s.DoHURL)
}

func (s Server) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	if s.Protocol.UsesURL() && s.DoHURL != "" {
		return s.DoHURL
	}
	return s.Address
}

func (s Server) BootstrapPinned() bool {
	if !s.Protocol.Encrypted() {
		return true
	}
	if !s.Protocol.UsesURL() {
		return s.Address != ""
	}
	if s.Address != "" {
		return true
	}
	host := s.DoHURL
	if u, err := url.Parse(s.DoHURL); err == nil && u.Host != "" {
		host = u.Hostname()
	}
	_, err := netip.ParseAddr(host)
	return err == nil
}

type ErrKind string

const (
	ErrTimeout  ErrKind = "timeout"
	ErrNetwork  ErrKind = "network"
	ErrTLS      ErrKind = "tls"
	ErrHTTP     ErrKind = "http"
	ErrProtocol ErrKind = "protocol"
	ErrCanceled ErrKind = "canceled"
)

type QueryError struct {
	Kind ErrKind `json:"kind"`
	Msg  string  `json:"msg"`
}

func (e *QueryError) Error() string { return string(e.Kind) + ": " + e.Msg }

func (e *QueryError) IsTimeout() bool { return e != nil && e.Kind == ErrTimeout }

type RR struct {
	Type string `json:"type"`
	TTL  uint32 `json:"ttl"`
	Data string `json:"data"`
}

type QueryPhases struct {
	Connect      time.Duration `json:"connect_ns"`
	TLSHandshake time.Duration `json:"tls_ns"`
	HTTPSetup    time.Duration `json:"http_ns"`
	Query        time.Duration `json:"query_ns"`
}

type QueryResult struct {
	RTT          time.Duration `json:"rtt_ns"`
	Phases       QueryPhases   `json:"phases"`
	Rcode        int           `json:"rcode"`
	Truncated    bool          `json:"truncated"`
	AD           bool          `json:"ad"`
	UsedTCP      bool          `json:"used_tcp"`
	Reused       bool          `json:"reused"`
	EDNSUDPSize  int           `json:"edns_udp_size"`
	ResponseSize int           `json:"response_size"`
	Answers      []RR          `json:"answers,omitempty"`
	Err          *QueryError   `json:"err,omitempty"`
}

func (r QueryResult) Answered() bool { return r.Err == nil }

func (r QueryResult) HasAnswerType(t string) bool {
	for _, a := range r.Answers {
		if strings.EqualFold(a.Type, t) {
			return true
		}
	}
	return false
}

func (r QueryResult) FirstAnswer(t string) (RR, bool) {
	for _, a := range r.Answers {
		if a.Type == t {
			return a, true
		}
	}
	return RR{}, false
}

type Category string

const (
	CatCached   Category = "cached"
	CatUncached Category = "uncached"
	CatTLD      Category = "tld"
)

func (c Category) Label() string {
	switch c {
	case CatCached:
		return "cached"
	case CatUncached:
		return "uncached"
	case CatTLD:
		return "recursive/TLD"
	}
	return string(c)
}

func AllCategories() []Category { return []Category{CatCached, CatUncached, CatTLD} }

func (r QueryResult) ValidFor(category Category) bool {
	const (
		rcodeSuccess   = 0
		rcodeNameError = 3
	)
	if !r.Answered() {
		return false
	}
	switch category {
	case CatTLD:
		return r.Rcode == rcodeNameError
	case CatCached, CatUncached:
		return r.Rcode == rcodeSuccess && (r.HasAnswerType("A") || r.HasAnswerType("CNAME"))
	default:
		return false
	}
}

type Sample struct {
	ServerID       string        `json:"server_id"`
	Category       Category      `json:"category"`
	Round          int           `json:"round"`
	Warmup         bool          `json:"warmup"`
	QName          string        `json:"qname"`
	QType          string        `json:"qtype"`
	Attempts       int           `json:"attempts"`
	FailedAttempts int           `json:"failed_attempts"`
	TimeoutCount   int           `json:"timeout_count"`
	Elapsed        time.Duration `json:"elapsed_ns"`
	At             time.Time     `json:"at"`
	Result         QueryResult   `json:"result"`
}

type Verdict string

const (
	VerdictYes     Verdict = "yes"
	VerdictNo      Verdict = "no"
	VerdictPartial Verdict = "partial"
	VerdictUnknown Verdict = "unknown"
)

func (v Verdict) Label() string {
	switch v {
	case VerdictYes:
		return "yes"
	case VerdictNo:
		return "no"
	case VerdictPartial:
		return "partial"
	}
	return "?"
}

type NXBehavior string

const (
	NXExpected      NXBehavior = "nxdomain"
	NXNoErrorEmpty  NXBehavior = "noerror-empty"
	NXInterceptedIP NXBehavior = "intercepted-ip"
	NXInterceptedCN NXBehavior = "intercepted-cname"
	NXBlocked       NXBehavior = "blocked"
	NXTimeout       NXBehavior = "timeout"
	NXOther         NXBehavior = "other"
)

func (b NXBehavior) Label() string {
	switch b {
	case NXExpected:
		return "NXDOMAIN (correct)"
	case NXNoErrorEmpty:
		return "NOERROR empty"
	case NXInterceptedIP:
		return "interception (IP)"
	case NXInterceptedCN:
		return "interception (CNAME)"
	case NXBlocked:
		return "blocked"
	case NXTimeout:
		return "timeout"
	}
	return "other"
}

type NXCheck struct {
	Label    string     `json:"label"`
	QName    string     `json:"qname"`
	Behavior NXBehavior `json:"behavior"`
	Detail   string     `json:"detail,omitempty"`
}

type DNSSECInfo struct {
	ReturnsRRSIG        Verdict `json:"returns_rrsig"`
	SignedResolves      Verdict `json:"signed_resolves"`
	BogusServfail       Verdict `json:"bogus_servfail"`
	BogusWithCDResolves Verdict `json:"bogus_with_cd_resolves"`
	ADOnSigned          Verdict `json:"ad_on_signed"`
	Validating          Verdict `json:"validating"`
}

type ExtendedInfo struct {
	DNS64             Verdict  `json:"dns64"`
	QNAMEMinimization Verdict  `json:"qname_minimization"`
	HTTPSRecord       Verdict  `json:"https_record"`
	Notes             []string `json:"notes,omitempty"`
}

type ProbeResult struct {
	ServerID          string        `json:"server_id"`
	Reachable         bool          `json:"reachable"`
	BaselineRcode     int           `json:"baseline_rcode"`
	SupportsA         Verdict       `json:"supports_a"`
	SupportsAAAA      Verdict       `json:"supports_aaaa"`
	EDNS0             Verdict       `json:"edns0"`
	AdvertisedUDPSize int           `json:"advertised_udp_size"`
	DNSSEC            DNSSECInfo    `json:"dnssec"`
	NXChecks          []NXCheck     `json:"nx_checks"`
	NXInterception    Verdict       `json:"nx_interception"`
	ReverseName       string        `json:"reverse_name,omitempty"`
	Extended          *ExtendedInfo `json:"extended,omitempty"`
	Errors            []string      `json:"errors,omitempty"`
}

type ServerState string

const (
	StateActive  ServerState = "active"
	StateBenched ServerState = "benched"
	StateOffline ServerState = "offline"
	StateError   ServerState = "error"
)

func (s ServerState) Label() string {
	switch s {
	case StateActive:
		return "active"
	case StateBenched:
		return "out of contention"
	case StateOffline:
		return "unreachable"
	case StateError:
		return "error"
	}
	return string(s)
}

type TriageResult struct {
	ServerID  string        `json:"server_id"`
	Attempts  int           `json:"attempts"`
	Responses int           `json:"responses"`
	BestRTT   time.Duration `json:"best_rtt_ns"`
	State     ServerState   `json:"state"`
	Reason    string        `json:"reason,omitempty"`
}

type Distribution struct {
	Count             int       `json:"count"`
	Answered          int       `json:"answered"`
	Valid             int       `json:"valid"`
	Attempts          int       `json:"attempts"`
	AttemptFailures   int       `json:"attempt_failures"`
	TimeoutAttempts   int       `json:"timeout_attempts"`
	Retried           int       `json:"retried"`
	Timeouts          int       `json:"timeouts"`
	Errors            int       `json:"errors"`
	Servfails         int       `json:"servfails"`
	TruncatedN        int       `json:"truncated"`
	Invalid           int       `json:"invalid"`
	LossPct           float64   `json:"loss_pct"`
	RetryPct          float64   `json:"retry_pct"`
	AttemptFailurePct float64   `json:"attempt_failure_pct"`
	MinMs             float64   `json:"min_ms"`
	MaxMs             float64   `json:"max_ms"`
	MeanMs            float64   `json:"mean_ms"`
	MedianMs          float64   `json:"median_ms"`
	StdDevMs          float64   `json:"stddev_ms"`
	VarianceMs2       float64   `json:"variance_ms2"`
	P50Ms             float64   `json:"p50_ms"`
	P90Ms             float64   `json:"p90_ms"`
	P95Ms             float64   `json:"p95_ms"`
	P99Ms             float64   `json:"p99_ms"`
	CI95LowMs         float64   `json:"ci95_low_ms"`
	CI95HighMs        float64   `json:"ci95_high_ms"`
	JitterMs          float64   `json:"jitter_ms"`
	ServfailPct       float64   `json:"servfail_pct"`
	InvalidPct        float64   `json:"invalid_pct"`
	TruncatedPct      float64   `json:"truncated_pct"`
	SamplesMs         []float64 `json:"samples_ms,omitempty"`
}

type PhaseAverages struct {
	ConnectMs     float64 `json:"connect_ms"`
	TLSMs         float64 `json:"tls_ms"`
	HTTPMs        float64 `json:"http_ms"`
	QueryMs       float64 `json:"query_ms"`
	ColdStartMs   float64 `json:"cold_start_ms"`
	SteadyStateMs float64 `json:"steady_state_ms"`
	ColdCount     int     `json:"cold_count"`
	ReusedCount   int     `json:"reused_count"`
}

type ServerStats struct {
	ServerID    string                     `json:"server_id"`
	State       ServerState                `json:"state"`
	PerCategory map[Category]*Distribution `json:"per_category"`
	Phases      *PhaseAverages             `json:"phases,omitempty"`
}

type SigLevel string

const (
	SigSignificant  SigLevel = "significant"
	SigLikely       SigLevel = "likely"
	SigInconclusive SigLevel = "inconclusive"
	SigNegligible   SigLevel = "negligible"
)

func (s SigLevel) Label() string {
	switch s {
	case SigSignificant:
		return "statistically significant"
	case SigLikely:
		return "likely"
	case SigInconclusive:
		return "inconclusive"
	case SigNegligible:
		return "practically irrelevant"
	}
	return string(s)
}

type Comparison struct {
	ServerA          string   `json:"server_a"`
	ServerB          string   `json:"server_b"`
	Category         Category `json:"category,omitempty"`
	RankingMode      RankMode `json:"ranking_mode,omitempty"`
	DeltaMeanMs      float64  `json:"delta_mean_ms,omitempty"`
	DeltaScoreMs     float64  `json:"delta_score_ms,omitempty"`
	CI95LowMs        float64  `json:"ci95_low_ms,omitempty"`
	CI95HighMs       float64  `json:"ci95_high_ms,omitempty"`
	PValue           float64  `json:"p_value"`
	BootstrapSamples int      `json:"bootstrap_samples,omitempty"`
	Level            SigLevel `json:"level"`
	Summary          string   `json:"summary"`
}

type RankMode string

const (
	RankLatency     RankMode = "latency"
	RankBrowsing    RankMode = "browsing"
	RankReliability RankMode = "reliability"
)

func (m RankMode) Label() string {
	switch m {
	case RankLatency:
		return "overall latency"
	case RankBrowsing:
		return "everyday browsing"
	case RankReliability:
		return "reliability"
	}
	return string(m)
}

func AllRankModes() []RankMode { return []RankMode{RankLatency, RankBrowsing, RankReliability} }

type Weights struct {
	Category                map[Category]float64 `json:"category"`
	LatencyMetric           string               `json:"latency_metric"`
	PenaltyPerLossPctMs     float64              `json:"penalty_per_loss_pct_ms"`
	PenaltyPerServfailPctMs float64              `json:"penalty_per_servfail_pct_ms"`
	PenaltyPerInvalidPctMs  float64              `json:"penalty_per_invalid_pct_ms"`
	PenaltyPerRetryPctMs    float64              `json:"penalty_per_retry_pct_ms"`
	PenaltyNXInterceptionMs float64              `json:"penalty_nx_interception_ms"`
	PenaltyNoDNSSECMs       float64              `json:"penalty_no_dnssec_ms"`
	JitterWeight            float64              `json:"jitter_weight"`
}

type Score struct {
	ServerID  string             `json:"server_id"`
	Mode      RankMode           `json:"mode"`
	Rank      int                `json:"rank"`
	BaseMs    float64            `json:"base_ms"`
	Penalties map[string]float64 `json:"penalties,omitempty"`
	TotalMs   float64            `json:"total_ms"`
}

type Mode string

const (
	ModeQuick    Mode = "quick"
	ModeStandard Mode = "standard"
	ModePrecise  Mode = "precise"
	ModeCustom   Mode = "custom"
)

func (m Mode) Rounds() int {
	switch m {
	case ModeQuick:
		return 50
	case ModeStandard:
		return 250
	case ModePrecise:
		return 500
	}
	return 0
}

type SessionMode string

const (
	SessionCold       SessionMode = "cold"
	SessionPersistent SessionMode = "persistent"
)

type BenchConfig struct {
	Mode              Mode          `json:"mode"`
	Rounds            int           `json:"rounds"`
	WarmupRounds      int           `json:"warmup_rounds"`
	Categories        []Category    `json:"categories"`
	CachedDomains     []string      `json:"cached_domains"`
	UncachedZone      string        `json:"uncached_zone,omitempty"`
	TLDZone           string        `json:"tld_zone"`
	Timeout           time.Duration `json:"timeout_ns"`
	Retries           int           `json:"retries"`
	RetryInterval     time.Duration `json:"retry_interval_ns"`
	Concurrency       int           `json:"concurrency"`
	PaceInterval      time.Duration `json:"pace_interval_ns"`
	PerServerGap      time.Duration `json:"per_server_gap_ns"`
	Session           SessionMode   `json:"session"`
	Seed              int64         `json:"seed"`
	TriageEnabled     bool          `json:"triage_enabled"`
	TriageAttempts    int           `json:"triage_attempts"`
	TriageThreshold   time.Duration `json:"triage_threshold_ns"`
	ForceAll          bool          `json:"force_all"`
	ConnectivityWatch bool          `json:"connectivity_watch"`
}

func DefaultBenchConfig(m Mode) BenchConfig {
	rounds := m.Rounds()
	if rounds == 0 {
		rounds = 50
	}
	return BenchConfig{
		Mode:              m,
		Rounds:            rounds,
		WarmupRounds:      3,
		Categories:        []Category{CatCached, CatTLD},
		CachedDomains:     DefaultCachedDomains(),
		TLDZone:           "com",
		Timeout:           3 * time.Second,
		Retries:           0,
		RetryInterval:     200 * time.Millisecond,
		Concurrency:       8,
		PaceInterval:      20 * time.Millisecond,
		PerServerGap:      40 * time.Millisecond,
		Session:           SessionPersistent,
		TriageEnabled:     true,
		TriageAttempts:    10,
		TriageThreshold:   50 * time.Millisecond,
		ConnectivityWatch: true,
	}
}

type EventType string

const (
	EvTriage       EventType = "triage"
	EvSample       EventType = "sample"
	EvRoundDone    EventType = "round-done"
	EvStateChange  EventType = "state-change"
	EvWarn         EventType = "warn"
	EvConnLost     EventType = "conn-lost"
	EvConnRestored EventType = "conn-restored"
	EvPaceAdjust   EventType = "pace-adjust"
	EvQueryStart   EventType = "query-start"
	EvDone         EventType = "done"
)

type Event struct {
	Type     EventType     `json:"type"`
	ServerID string        `json:"server_id,omitempty"`
	Round    int           `json:"round,omitempty"`
	Sample   *Sample       `json:"sample,omitempty"`
	Triage   *TriageResult `json:"triage,omitempty"`
	State    ServerState   `json:"state,omitempty"`
	Msg      string        `json:"msg,omitempty"`
}

type RunInfo struct {
	AppVersion string        `json:"app_version"`
	OS         string        `json:"os"`
	Arch       string        `json:"arch"`
	StartedAt  time.Time     `json:"started_at"`
	Duration   time.Duration `json:"duration_ns"`
	Interfaces []string      `json:"interfaces,omitempty"`
}

type RunResult struct {
	Info            RunInfo                  `json:"info"`
	Config          BenchConfig              `json:"config"`
	SelectedRanking RankMode                 `json:"selected_ranking"`
	Weights         map[RankMode]Weights     `json:"weights"`
	Servers         []Server                 `json:"servers"`
	SystemServers   []Server                 `json:"system_servers,omitempty"`
	SystemIDs       []string                 `json:"system_ids,omitempty"`
	Probes          map[string]*ProbeResult  `json:"probes,omitempty"`
	Triage          map[string]*TriageResult `json:"triage,omitempty"`
	Stats           map[string]*ServerStats  `json:"stats"`
	Scores          map[RankMode][]Score     `json:"scores"`
	Comparisons     []Comparison             `json:"comparisons,omitempty"`
	Samples         []Sample                 `json:"samples,omitempty"`
}

func (r *RunResult) ServerByID(id string) *Server {
	for i := range r.Servers {
		if r.Servers[i].ID == id {
			return &r.Servers[i]
		}
	}
	return nil
}
