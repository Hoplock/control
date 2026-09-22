// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultTenant is the tenant a single-tenant deployment runs as. Tenancy is
// in the schema from day one (PLAN M12) even though nothing in the API exposes
// it yet, so every deployment names a tenant — this one by default.
const DefaultTenant = "default"

// DefaultLogLevel is the level used when the file does not name one.
const DefaultLogLevel = "info"

// The fleet registry's defaults (PLAN M6, M17).
//
// They are stated here rather than imported from internal/fleet because config
// is the lowest layer in this module and a dependency the other way would make
// loading a file depend on the package that consumes it. internal/fleet declares
// the same numbers as its own defaults and a test asserts the two agree, so the
// duplication cannot drift silently.
const (
	// DefaultHeartbeatTTL is how long a proxy may be silent and still be routed
	// through.
	DefaultHeartbeatTTL = 90 * time.Second
	// DefaultRelayRegistrationTTL is how long a reported relay registration
	// stays believable.
	DefaultRelayRegistrationTTL = 60 * time.Second
	// DefaultTargetCapabilityTTL is how long a target capability observation
	// stays fresh.
	DefaultTargetCapabilityTTL = 24 * time.Hour
	// DefaultCapabilityReportAfter is the re-observation interval this server
	// asks for.
	DefaultCapabilityReportAfter = 6 * time.Hour
	// DefaultMaxHops caps how many proxies one session may traverse. It matches
	// the proxy's own routing.DefaultMaxHops, and the two must agree: the proxy
	// enforces the cap as a safety net against a bad answer, and this server
	// should not give one.
	DefaultMaxHops = 4
)

// The south-bound listener's defaults (PLAN M2, M5, 0007).
//
// They are stated here for the same reason the fleet's are: config is the
// lowest layer in this module, and a dependency on the packages that consume
// these numbers would make loading a file depend on the server. Each consumer
// declares the same default and a test asserts the two agree.
const (
	// DefaultSouthMaxBodyBytes caps a south-bound request body.
	DefaultSouthMaxBodyBytes int64 = 1 << 20
	// DefaultSouthRequestTimeout bounds one south-bound request. A
	// server-side timeout that ANSWERS is strictly better than a slow
	// answer that looks like an outage (M5).
	DefaultSouthRequestTimeout = 10 * time.Second
	// DefaultMFAChallengeTTL is how long a second factor stays answerable.
	DefaultMFAChallengeTTL = 2 * time.Minute
	// DefaultMFAPollAfter is the interval a challenge advertises.
	DefaultMFAPollAfter = 2 * time.Second
	// DefaultMFAMaxPolls bounds a challenge's total cost independently of
	// how fast it is polled.
	DefaultMFAMaxPolls = 120
	// DefaultNorthMaxBodyBytes caps a north-bound request body. It is larger
	// than the south-bound cap because a policy bundle is the largest thing
	// this surface accepts (0014) and nothing on the contract is close.
	DefaultNorthMaxBodyBytes int64 = 4 << 20
	// DefaultNorthRequestTimeout bounds one north-bound request. It is longer
	// than the south-bound one because nothing is holding a user's handshake
	// open while a bundle compiles.
	DefaultNorthRequestTimeout = 30 * time.Second
	// DefaultSessionTTL is how long a console session lives. Short, because
	// M7's whole point is that identity is short-lived: a session outliving
	// the IdP's own view of the person is what federation exists to remove.
	DefaultSessionTTL = 8 * time.Hour
	// DefaultFlowTTL is how long a started login may take to come back.
	DefaultFlowTTL = 10 * time.Minute
	// DefaultCertificateValidity is how long a brokered target certificate
	// lives. Minutes: the shorter and narrower, the less a stolen one is
	// worth (proxy D6a).
	DefaultCertificateValidity = 5 * time.Minute
	// DefaultMaxCertificateValidity caps what a caller may ask for. A
	// certificate good for a day is a long-lived credential with extra steps.
	DefaultMaxCertificateValidity = time.Hour
	// DefaultRotationOverlap is how long a retired CA key stays in the trust
	// bundle after a routine rotation. It is longer than
	// DefaultMaxCertificateValidity so that no certificate the retired key
	// signed can outlive the trust in it.
	DefaultRotationOverlap = 2 * DefaultMaxCertificateValidity
	// The ephemeral-uid allocation defaults (PLAN §4). The range sits above
	// every distribution's own UID_MAX, above systemd's dynamic-user range
	// and at the top of SSSD's default id-mapping range, and below 2^31 so a
	// uid stays a positive int32.
	DefaultUIDRangeMin     int32 = 2_000_000
	DefaultUIDRangeMax     int32 = 2_147_483_646
	DefaultUIDBlockSize    int32 = 4096
	DefaultUIDMaxBlockSize int32 = 1 << 20
	// DefaultDecisionRefresh is how long a compiled bundle or a fleet graph
	// is served before this server re-reads which one is active.
	DefaultDecisionRefresh = 5 * time.Second
	// DefaultDecisionBudget is the hard deadline on one authorize call. It
	// is well inside DefaultSouthRequestTimeout, because a deadline that
	// only fires after the transport's has already fired is not one.
	DefaultDecisionBudget = 2 * time.Second
	// DefaultMaxCacheTTL is the ceiling on a cache hint's lifetime (PLAN
	// §5.4). It clamps downward only.
	DefaultMaxCacheTTL = 5 * time.Minute
	// DefaultHostKeyCacheTTL is how long a host-key decision may be reused.
	DefaultHostKeyCacheTTL = 5 * time.Minute
)

// The revocation stream's defaults (PLAN M9, §4).
//
// Stated here for the same reason as the rest, and one of them is not this
// server's to choose: MaxHeartbeatIntervalSeconds is the CONTRACT's ceiling,
// so that a configuration past it is refused when the file is loaded rather
// than discovered by a fleet that has stopped trusting its caches.
const (
	// DefaultHeartbeatInterval is how often a subscription is told the
	// stream is alive. It is also what the stream ADVERTISES, because the
	// interval kept and the interval claimed are one number (PLAN §4).
	DefaultHeartbeatInterval = 5 * time.Second
	// MaxHeartbeatIntervalSeconds is the contract's ceiling, in seconds.
	// Two consecutive intervals at it still fit inside the proxy's 20s
	// reconnect timeout, so one lost heartbeat is not a dead stream. It
	// matches contract.MaxHeartbeatIntervalSeconds and a test asserts the
	// two agree.
	MaxHeartbeatIntervalSeconds = 10
	// DefaultReplayBuffer is how many published events are kept for a
	// reconnecting subscriber.
	DefaultReplayBuffer = 1024
	// DefaultSubscriberQueue is how far one subscriber may fall behind
	// before it is dropped and left to reconnect.
	DefaultSubscriberQueue = 256
)

// logLevels is the set LogConfig.Level accepts, ordered from most to least
// verbose.
var logLevels = []string{"debug", "info", "warn", "error"}

// Config is the server's full configuration as loaded from YAML.
type Config struct {
	// Tenant names the tenant this deployment operates as (PLAN M12).
	Tenant string `yaml:"tenant"`

	Listeners  ListenersConfig  `yaml:"listeners"`
	Database   DatabaseConfig   `yaml:"database"`
	Log        LogConfig        `yaml:"log"`
	Fleet      FleetConfig      `yaml:"fleet"`
	South      SouthConfig      `yaml:"south"`
	MFA        MFAConfig        `yaml:"mfa"`
	UIDs       UIDConfig        `yaml:"uids"`
	Decision   DecisionConfig   `yaml:"decision"`
	Events     EventsConfig     `yaml:"events"`
	Audit      AuditConfig      `yaml:"audit"`
	North      NorthConfig      `yaml:"north"`
	Identity   IdentityConfig   `yaml:"identity"`
	Credential CredentialConfig `yaml:"credential"`
}

// AuditConfig bounds log ingest and configures the record read-back path
// (PLAN §7, M8).
//
// THE READ PATH IS NOT PART OF THE CONTRACT, and that is deliberate upstream:
// a proxy writes logs and never queries them, so an operator read API on `/v1`
// would be one every Hoplock Control implements and no proxy calls
// (`Hoplock/proxy#56`). The priority ack's durability guarantee is therefore
// observable only through a path this server exposes outside `/v1`, which is
// what this listener is — and what the conformance suite takes as
// `logs.read_url`.
//
// It follows `events.publish_listener` in every respect: OFF UNLESS
// CONFIGURED, refusing to bind without a credential of its own, on a port of
// its own because the south-bound listener serves the contract and nothing
// else (M2). It is superseded and DELETED by 0014's north-bound audit query
// route — `docs/PROTOCOL.md` §3 permits a debug endpoint only against a named
// successor whose own prompt carries the removal, and 0014's does, file by
// file.
type AuditConfig struct {
	// ReadListener is an OPTIONAL address for the record read-back path,
	// host:port. EMPTY IS THE DEFAULT AND MEANS NOT BOUND.
	ReadListener string `yaml:"read_listener"`
	// ReadToken is the bearer token that listener requires. It is REQUIRED
	// whenever ReadListener is set: the path reads audit records, which are
	// the most sensitive documents this server holds.
	ReadToken string `yaml:"read_token"`
	// MaxBatchRecords, MaxRecordBytes and MaxCaptureBytes bound one ingest
	// request. Zero takes the shipped default. They are configurable
	// because a fleet's record sizes are a property of its estate, and
	// bounded because an unbounded ingest path is a memory-exhaustion
	// surface reachable by any enrolled proxy.
	MaxBatchRecords int `yaml:"max_batch_records"`
	MaxRecordBytes  int `yaml:"max_record_bytes"`
	MaxCaptureBytes int `yaml:"max_capture_bytes"`
}

// EventsConfig is the revocation stream (PLAN M9, §4).
//
// ONE NUMBER, TWO OBLIGATIONS. `heartbeat_interval` is both the interval this
// server keeps and the interval it advertises on the stream — they are not
// configured separately, because two numbers that can drift apart will, and
// the drift is invisible until a fleet is already reconnecting. Configuring it
// past the contract's ceiling is refused here rather than clamped: a server
// advertising 600s and honestly keeping to it passes its own claim and takes
// every proxy in the fleet off cached decisions.
type EventsConfig struct {
	// HeartbeatInterval is kept and advertised. Zero takes the default;
	// anything past MaxHeartbeatIntervalSeconds is refused.
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	// ReplayBuffer is how many events are retained for a reconnecting
	// subscriber. Past it the answer is `resync`, which is correct but
	// costs that proxy its entire decision cache — so the buffer is sized
	// to cover a reconnect with backoff, not an outage.
	ReplayBuffer int `yaml:"replay_buffer"`
	// SubscriberQueue is how far one subscriber may fall behind before it
	// is dropped and left to reconnect. Dropping is the policy: blocking
	// the publisher would make one stalled proxy an outage for the fleet,
	// and growing the queue would make it an out-of-memory.
	SubscriberQueue int `yaml:"subscriber_queue"`
	// PublishListener is an OPTIONAL address for the local publish path,
	// host:port. EMPTY IS THE DEFAULT AND MEANS NOT BOUND.
	//
	// Publishing an event is an operator action, and the contract states
	// outright that nothing on `/v1` publishes one — the proxy-facing API
	// would otherwise carry an endpoint no proxy calls. The north-bound
	// API that will own it is 0014's, so until then the conformance suite
	// (which cannot grade gap recovery without making this server emit an
	// event while a subscriber is away) drives this listener instead.
	//
	// It is off unless configured because what it publishes is the kill
	// switch, and it is a SEPARATE port because the south-bound listener
	// serves the contract and nothing else (M2).
	PublishListener string `yaml:"publish_listener"`
	// PublishToken is the bearer token the publish listener requires. It
	// is REQUIRED whenever PublishListener is set: an unauthenticated kill
	// switch is worse than no kill switch.
	PublishToken string `yaml:"publish_token"`
}

// DecisionConfig bounds the decision path (PLAN M5, §5.4).
//
// Like FleetConfig, every field has a default and a zero takes it: a zero
// budget is not "no deadline", it is an unset field, and reading it as the
// former would be a handshake held open forever by a config nobody wrote.
type DecisionConfig struct {
	// Refresh is how long a compiled policy bundle or a fleet graph is
	// served before this server re-reads which one is active. It is a bound
	// on how stale an answer may be, not a cache of answers: every request
	// is still evaluated.
	Refresh time.Duration `yaml:"refresh"`
	// Budget is the hard server-side deadline on one authorize call. A
	// timeout the proxy classifies as an outage beats a slow answer that
	// looks like one (M5).
	Budget time.Duration `yaml:"budget"`
	// MaxCacheTTL is the ceiling on a cache hint's lifetime. It clamps
	// DOWNWARD only: the lifetime is policy's to set (PLAN §5.4) and this
	// is the bound under which "we can withdraw it" stays true.
	MaxCacheTTL time.Duration `yaml:"max_cache_ttl"`
}

// SouthConfig bounds the south-bound listener (PLAN M2, M5).
type SouthConfig struct {
	// MaxBodyBytes caps a request body. Every south-bound payload is a
	// small JSON object.
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	// RequestTimeout bounds a single request. It is a deadline on the
	// handler rather than a suggestion: a timeout the proxy classifies as
	// an outage is strictly better than a held connection.
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

// MFAConfig is the challenge conversation this server owns (PLAN §6).
//
// The numbers are here because they are operational: the TTL is how long a
// user has to reach for their phone while their SSH handshake is held open,
// and that depends on who the users are.
type MFAConfig struct {
	// ChallengeTTL is how long a challenge stays answerable. Past it a poll
	// is a DENY, never a 200 that leaves the proxy polling.
	ChallengeTTL time.Duration `yaml:"challenge_ttl"`
	// PollAfter is the interval advertised on a challenge.
	PollAfter time.Duration `yaml:"poll_after"`
	// MaxPolls bounds a challenge's total cost.
	MaxPolls int `yaml:"max_polls"`
}

// UIDConfig is the ephemeral-uid allocation policy (PLAN §4).
//
// The invariant these numbers sit under is not configurable: the per-target
// cursor only ever advances, and a granted block is never reclaimed. What is
// configurable is how much is granted at a time and out of what range.
type UIDConfig struct {
	// RangeMin and RangeMax are allocated from when a lease names no
	// range. A proxy that states its own bounds overrides these, because
	// those bounds encode facts about the fleet this server cannot know.
	RangeMin int32 `yaml:"range_min"`
	RangeMax int32 `yaml:"range_max"`
	// BlockSize is granted when the proxy asks for no particular size.
	BlockSize int32 `yaml:"block_size"`
	// MaxBlockSize caps what one lease may take. A block is never
	// reclaimed, so one oversized request is permanent.
	MaxBlockSize int32 `yaml:"max_block_size"`
	// LeaseTerm is `term_seconds`. ZERO IS THE DEFAULT AND IT IS DELIBERATE:
	// this server states no term, leaving the proxy its own. The term bounds
	// only how long the proxy keeps allocating from a block — it is not what
	// makes the uids non-reusable — so shortening it costs availability
	// during exactly the outage a held block exists to survive, and buys
	// nothing.
	LeaseTerm time.Duration `yaml:"lease_term"`
}

// FleetConfig holds the fleet registry's staleness rule and its hop cap
// (PLAN M6, M17).
//
// These are configurable because "stale" is a number an operator has to be able
// to move: it depends on how often their proxies heartbeat and how much packet
// loss their estate has, and a proxy dropped from routing for being briefly quiet
// is an outage a user experiences as a hang.
//
// Every field has a default and a zero — whether written as `0s` or left out
// entirely — takes it. YAML gives no way to tell those two apart without making
// every field a pointer, and the honest reading of "0s" is not "disable the rule":
// a zero TTL would make the whole fleet stale at once, so taking the default is
// both the safe direction and the only one worth having. A NEGATIVE value is
// something the default cannot rescue and is refused.
type FleetConfig struct {
	// HeartbeatTTL is how long a proxy may be silent and still be routed
	// through.
	HeartbeatTTL time.Duration `yaml:"heartbeat_ttl"`
	// RelayRegistrationTTL is how long a reported relay registration stays
	// believable. It is shorter than HeartbeatTTL by default: a registration is
	// a live connection rather than a fact about a configuration, so it is the
	// thing most likely to have gone away silently, and a relay hop to a dead
	// registration hangs.
	RelayRegistrationTTL time.Duration `yaml:"relay_registration_ttl"`
	// TargetCapabilityTTL is how long a target capability observation stays
	// fresh (M17). Past it, the record provides nothing that has to be applied
	// — which is the same answer as an undated or absent record.
	TargetCapabilityTTL time.Duration `yaml:"target_capability_ttl"`
	// CapabilityReportAfter is the interval this server asks a proxy to
	// re-observe on, answered in `report_after_seconds`. The server owns the
	// freshness of its own record: a proxy may re-observe sooner, never later.
	CapabilityReportAfter time.Duration `yaml:"capability_report_after"`
	// MaxHops caps how many proxies one session may traverse. It must not
	// exceed what the fleet's proxies enforce locally, or this server will
	// answer routes they refuse — an outage in front of a user rather than a
	// message to an operator.
	MaxHops int `yaml:"max_hops"`
	// HostKeyCacheTTL is how long a host-key decision may be reused, when
	// one is hinted at all. It is clamped by decision.max_cache_ttl and only
	// ever downward. Within it, a target that has rotated its key or been
	// rebuilt is reported late by the proxies already holding the answer.
	HostKeyCacheTTL time.Duration `yaml:"host_key_cache_ttl"`
}

// ListenersConfig holds the two listener addresses. South-bound and
// north-bound are separate fields from the very first release on purpose
// (PLAN M2): one field that later becomes two is a breaking config change,
// and the two surfaces must never be routable from the same port.
type ListenersConfig struct {
	// South is the proxy-facing listen address, host:port.
	South string `yaml:"south"`
	// North is the operator-facing listen address, host:port.
	North string `yaml:"north"`
}

// DatabaseConfig holds the Postgres connection settings (PLAN M13).
type DatabaseConfig struct {
	// DSN is the Postgres connection string. It usually carries a password,
	// so it is never logged; see String.
	DSN string `yaml:"dsn"`
}

// String redacts the DSN so that printing a Config — or a DatabaseConfig
// nested inside one — cannot leak a database password into a log line or an
// error message (PLAN §8).
func (DatabaseConfig) String() string { return "database{dsn:[redacted]}" }

// LogConfig holds structured-logging settings.
type LogConfig struct {
	// Level is one of debug, info, warn, error.
	Level string `yaml:"level"`
}

// SlogLevel maps the configured level onto its slog equivalent. Validate has
// already rejected anything outside the known set, so an unrecognised value
// here can only mean an unvalidated Config and falls back to the default
// rather than silencing the log.
func (l LogConfig) SlogLevel() slog.Level {
	switch l.Level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// FieldError reports a configuration field that is missing or invalid. It
// names the field with its full YAML path so an operator can find it in the
// file without reading the loader.
type FieldError struct {
	// Field is the dotted YAML path, e.g. "listeners.south".
	Field string
	// Msg says what is wrong with it.
	Msg string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("config: %s: %s", e.Field, e.Msg)
}

// Load reads and validates the configuration file at path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	cfg, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes and validates a configuration document. Decoding is strict:
// a key the schema does not define is an error, not a shrug.
func Parse(r io.Reader) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config: file is empty")
		}
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyDefaults fills in the fields that have a safe default. Everything else
// is required, because guessing a listen address or a database is worse than
// refusing to start.
func (c *Config) applyDefaults() {
	if c.Tenant == "" {
		c.Tenant = DefaultTenant
	}
	if c.Log.Level == "" {
		c.Log.Level = DefaultLogLevel
	}
	if c.Fleet.HeartbeatTTL == 0 {
		c.Fleet.HeartbeatTTL = DefaultHeartbeatTTL
	}
	if c.Fleet.RelayRegistrationTTL == 0 {
		c.Fleet.RelayRegistrationTTL = DefaultRelayRegistrationTTL
	}
	if c.Fleet.TargetCapabilityTTL == 0 {
		c.Fleet.TargetCapabilityTTL = DefaultTargetCapabilityTTL
	}
	if c.Fleet.CapabilityReportAfter == 0 {
		c.Fleet.CapabilityReportAfter = DefaultCapabilityReportAfter
	}
	if c.Fleet.MaxHops == 0 {
		c.Fleet.MaxHops = DefaultMaxHops
	}
	if c.South.MaxBodyBytes == 0 {
		c.South.MaxBodyBytes = DefaultSouthMaxBodyBytes
	}
	if c.South.RequestTimeout == 0 {
		c.South.RequestTimeout = DefaultSouthRequestTimeout
	}
	if c.MFA.ChallengeTTL == 0 {
		c.MFA.ChallengeTTL = DefaultMFAChallengeTTL
	}
	if c.MFA.PollAfter == 0 {
		c.MFA.PollAfter = DefaultMFAPollAfter
	}
	if c.MFA.MaxPolls == 0 {
		c.MFA.MaxPolls = DefaultMFAMaxPolls
	}
	if c.UIDs.RangeMin == 0 {
		c.UIDs.RangeMin = DefaultUIDRangeMin
	}
	if c.UIDs.RangeMax == 0 {
		c.UIDs.RangeMax = DefaultUIDRangeMax
	}
	if c.UIDs.BlockSize == 0 {
		c.UIDs.BlockSize = DefaultUIDBlockSize
	}
	if c.UIDs.MaxBlockSize == 0 {
		c.UIDs.MaxBlockSize = DefaultUIDMaxBlockSize
	}
	if c.Decision.Refresh == 0 {
		c.Decision.Refresh = DefaultDecisionRefresh
	}
	if c.Decision.Budget == 0 {
		c.Decision.Budget = DefaultDecisionBudget
	}
	if c.Decision.MaxCacheTTL == 0 {
		c.Decision.MaxCacheTTL = DefaultMaxCacheTTL
	}
	if c.Fleet.HostKeyCacheTTL == 0 {
		c.Fleet.HostKeyCacheTTL = DefaultHostKeyCacheTTL
	}
	if c.Events.HeartbeatInterval == 0 {
		c.Events.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if c.Events.ReplayBuffer == 0 {
		c.Events.ReplayBuffer = DefaultReplayBuffer
	}
	if c.Events.SubscriberQueue == 0 {
		c.Events.SubscriberQueue = DefaultSubscriberQueue
	}
	if c.North.MaxBodyBytes == 0 {
		c.North.MaxBodyBytes = DefaultNorthMaxBodyBytes
	}
	if c.North.RequestTimeout == 0 {
		c.North.RequestTimeout = DefaultNorthRequestTimeout
	}
	if c.Identity.SessionTTL == 0 {
		c.Identity.SessionTTL = DefaultSessionTTL
	}
	if c.Identity.FlowTTL == 0 {
		c.Identity.FlowTTL = DefaultFlowTTL
	}
	if c.Credential.CertificateValidity == 0 {
		c.Credential.CertificateValidity = DefaultCertificateValidity
	}
	if c.Credential.MaxCertificateValidity == 0 {
		c.Credential.MaxCertificateValidity = DefaultMaxCertificateValidity
	}
	if c.Credential.RotationOverlap == 0 {
		c.Credential.RotationOverlap = DefaultRotationOverlap
	}
	// UIDs.LeaseTerm has no default: zero means "state no term", which is
	// a real answer rather than an unset field.
}

// NorthConfig bounds the north-bound listener's chain (0011, M2).
//
// It is a section of its own rather than shared with `south:` because the two
// listeners share no middleware and no bounds: a policy bundle is the largest
// thing this surface accepts and it is far larger than anything on the contract.
type NorthConfig struct {
	// MaxBodyBytes caps a request body.
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	// RequestTimeout bounds a single request.
	RequestTimeout time.Duration `yaml:"request_timeout"`
	// InsecureCookies drops the Secure attribute from the session cookie. It
	// exists for a local development deployment over plain HTTP, and it is
	// spelled "insecure" so that nobody sets it by accident.
	InsecureCookies bool `yaml:"insecure_cookies"`
}

// IdentityConfig configures federation and sessions (0011, M7).
//
// NO SECRET IS IN HERE. A connector's client secret is named by an environment
// variable inside the connector's own stored document, and the MFA provider's
// shared secret is named by `secret_env` below: a secret in a config struct is a
// secret in every log line that ever prints one.
type IdentityConfig struct {
	// SessionTTL is how long a console session lives.
	SessionTTL time.Duration `yaml:"session_ttl"`
	// FlowTTL is how long a started login may take to come back. It bounds
	// the window in which a stolen `state` is worth anything.
	FlowTTL time.Duration `yaml:"flow_ttl"`
	// MFAPush configures the real out-of-band second factor. Left empty, the
	// deployment has only the deterministic CI provider — which is not a
	// second factor and says so.
	MFAPush MFAPushConfig `yaml:"mfa_push"`
}

// MFAPushConfig points at an out-of-band MFA service.
type MFAPushConfig struct {
	// Name is what an enrollment row names. Empty takes the provider's own
	// default.
	Name string `yaml:"name"`
	// BeginURL and PollURL are the service's endpoints. Setting either
	// enables the provider, so both are then required.
	BeginURL string `yaml:"begin_url"`
	PollURL  string `yaml:"poll_url"`
	// SecretEnv names the environment variable holding the shared secret
	// requests are signed with.
	SecretEnv string `yaml:"secret_env"`
	// Timeout bounds one call to the service.
	Timeout time.Duration `yaml:"timeout"`
}

// Enabled reports whether a push provider is configured.
func (m MFAPushConfig) Enabled() bool { return m.BeginURL != "" || m.PollURL != "" }

// CredentialConfig configures the per-tenant SSH certificate authority (0011,
// proxy D6a, M7).
type CredentialConfig struct {
	// KeyEncryptionKeyEnv names the environment variable holding the
	// base64-encoded 32-byte key the CA's private half is encrypted with.
	// WITHOUT IT THERE IS NO CERTIFICATE AUTHORITY: the software custodian
	// refuses to start rather than write a CA private key into a database row
	// in the clear, and a deployment that has not set it simply has no CA
	// until it does.
	KeyEncryptionKeyEnv string `yaml:"key_encryption_key_env"`
	// CertificateValidity is how long an issued certificate lives.
	CertificateValidity time.Duration `yaml:"certificate_validity"`
	// MaxCertificateValidity lowers (never raises) the ceiling a caller may
	// ask for.
	MaxCertificateValidity time.Duration `yaml:"max_certificate_validity"`
	// RotationOverlap is how long a retired CA key stays in the trust bundle
	// after a ROUTINE rotation, so certificates it already signed keep
	// working for their remaining life. A compromise rotation ignores it.
	RotationOverlap time.Duration `yaml:"rotation_overlap"`
}

// validate reports the first north-bound setting that cannot be acted on.
func (n NorthConfig) validate() error {
	if n.MaxBodyBytes < 0 {
		return &FieldError{Field: "north.max_body_bytes", Msg: "must not be negative"}
	}
	if n.RequestTimeout < 0 {
		return &FieldError{Field: "north.request_timeout", Msg: "must not be negative"}
	}
	return nil
}

// validate reports the first identity setting that cannot be acted on.
func (i IdentityConfig) validate() error {
	if i.SessionTTL < 0 {
		return &FieldError{Field: "identity.session_ttl", Msg: "must not be negative"}
	}
	if i.FlowTTL < 0 {
		return &FieldError{Field: "identity.flow_ttl", Msg: "must not be negative"}
	}
	if !i.MFAPush.Enabled() {
		return nil
	}
	// A HALF-CONFIGURED PROVIDER IS REFUSED rather than partly wired. A
	// deployment that set `begin_url` and not `poll_url` believes it has a
	// second factor, and would discover otherwise on the first challenge.
	if i.MFAPush.BeginURL == "" {
		return &FieldError{Field: "identity.mfa_push.begin_url", Msg: "is required once a push provider is configured"}
	}
	if i.MFAPush.PollURL == "" {
		return &FieldError{Field: "identity.mfa_push.poll_url", Msg: "is required once a push provider is configured"}
	}
	if i.MFAPush.SecretEnv == "" {
		return &FieldError{
			Field: "identity.mfa_push.secret_env",
			Msg:   "is required: an unsigned request to an MFA service is a request anybody on the network can forge",
		}
	}
	if i.MFAPush.Timeout < 0 {
		return &FieldError{Field: "identity.mfa_push.timeout", Msg: "must not be negative"}
	}
	return nil
}

// validate reports the first certificate-authority setting that cannot be acted
// on.
func (c CredentialConfig) validate() error {
	if c.CertificateValidity < 0 {
		return &FieldError{Field: "credential.certificate_validity", Msg: "must not be negative"}
	}
	if c.MaxCertificateValidity < 0 {
		return &FieldError{Field: "credential.max_certificate_validity", Msg: "must not be negative"}
	}
	if c.RotationOverlap < 0 {
		return &FieldError{Field: "credential.rotation_overlap", Msg: "must not be negative"}
	}
	if c.CertificateValidity > 0 && c.MaxCertificateValidity > 0 &&
		c.CertificateValidity > c.MaxCertificateValidity {
		return &FieldError{
			Field: "credential.certificate_validity",
			Msg:   "must not exceed credential.max_certificate_validity",
		}
	}
	return nil
}

// Validate reports the first field that is missing or invalid.
func (c *Config) Validate() error {
	if c.Listeners.South == "" {
		return &FieldError{Field: "listeners.south", Msg: "is required"}
	}
	if c.Listeners.North == "" {
		return &FieldError{Field: "listeners.north", Msg: "is required"}
	}
	if c.Listeners.South == c.Listeners.North {
		return &FieldError{
			Field: "listeners.north",
			Msg:   "must differ from listeners.south: the two surfaces never share a port",
		}
	}
	if c.Database.DSN == "" {
		return &FieldError{Field: "database.dsn", Msg: "is required"}
	}
	if !slices.Contains(logLevels, c.Log.Level) {
		return &FieldError{
			Field: "log.level",
			Msg:   fmt.Sprintf("%q is not one of %s", c.Log.Level, strings.Join(logLevels, ", ")),
		}
	}
	if err := c.Fleet.validate(); err != nil {
		return err
	}
	if err := c.South.validate(); err != nil {
		return err
	}
	if err := c.MFA.validate(); err != nil {
		return err
	}
	if err := c.Decision.validate(); err != nil {
		return err
	}
	if err := c.Events.validate(c.Listeners); err != nil {
		return err
	}
	if err := c.Audit.validate(c.Listeners, c.Events); err != nil {
		return err
	}
	if err := c.North.validate(); err != nil {
		return err
	}
	if err := c.Identity.validate(); err != nil {
		return err
	}
	if err := c.Credential.validate(); err != nil {
		return err
	}
	return c.UIDs.validate()
}

// validate reports the first revocation-stream setting that cannot be acted on.
func (e EventsConfig) validate(l ListenersConfig) error {
	if e.HeartbeatInterval <= 0 {
		return &FieldError{Field: "events.heartbeat_interval", Msg: "must not be negative"}
	}
	// Rounded UP, because that is what the stream advertises: this server
	// never claims an interval it does not keep, so 10.001s advertises 11s
	// and 11s is past the ceiling.
	advertised := int64(e.HeartbeatInterval / time.Second)
	if e.HeartbeatInterval%time.Second != 0 {
		advertised++
	}
	if advertised > MaxHeartbeatIntervalSeconds {
		return &FieldError{
			Field: "events.heartbeat_interval",
			Msg: fmt.Sprintf(
				"advertises %ds, past the contract's ceiling of %ds: two consecutive intervals must fit inside the proxy's 20s reconnect timeout",
				advertised, MaxHeartbeatIntervalSeconds),
		}
	}
	if e.ReplayBuffer <= 0 {
		return &FieldError{Field: "events.replay_buffer", Msg: "must be a positive number of events"}
	}
	if e.SubscriberQueue <= 0 {
		return &FieldError{Field: "events.subscriber_queue", Msg: "must be a positive number of events"}
	}
	if e.PublishListener == "" {
		if e.PublishToken != "" {
			return &FieldError{
				Field: "events.publish_token",
				Msg:   "is set but events.publish_listener is not, so nothing would ever read it",
			}
		}
		return nil
	}
	if e.PublishToken == "" {
		// The listener publishes the kill switch. A deployment that
		// binds it without a credential has opened one to anybody who
		// can reach the port.
		return &FieldError{
			Field: "events.publish_token",
			Msg:   "is required when events.publish_listener is set",
		}
	}
	if e.PublishListener == l.South || e.PublishListener == l.North {
		return &FieldError{
			Field: "events.publish_listener",
			Msg:   "must differ from listeners.south and listeners.north: the south-bound listener serves the contract and nothing else",
		}
	}
	return nil
}

// validate reports the first audit setting that cannot be acted on.
func (a AuditConfig) validate(l ListenersConfig, e EventsConfig) error {
	for _, f := range []struct {
		name  string
		value int
		unit  string
	}{
		{"audit.max_batch_records", a.MaxBatchRecords, "number of records"},
		{"audit.max_record_bytes", a.MaxRecordBytes, "number of bytes"},
		{"audit.max_capture_bytes", a.MaxCaptureBytes, "number of bytes"},
	} {
		if f.value < 0 {
			return &FieldError{Field: f.name, Msg: "must be a positive " + f.unit + ", or absent for the default"}
		}
	}

	if a.ReadListener == "" {
		if a.ReadToken != "" {
			return &FieldError{
				Field: "audit.read_token",
				Msg:   "is set but audit.read_listener is not, so nothing would ever read it",
			}
		}
		return nil
	}
	if a.ReadToken == "" {
		// The path serves audit records. A deployment that binds it
		// without a credential has published its audit store.
		return &FieldError{
			Field: "audit.read_token",
			Msg:   "is required when audit.read_listener is set",
		}
	}
	if a.ReadListener == l.South || a.ReadListener == l.North || a.ReadListener == e.PublishListener {
		return &FieldError{
			Field: "audit.read_listener",
			Msg:   "must differ from listeners.south, listeners.north and events.publish_listener: the south-bound listener serves the contract and nothing else, and a read path must not share a port with the kill switch",
		}
	}
	return nil
}

// validate reports the first south-bound bound that cannot be acted on.
func (s SouthConfig) validate() error {
	if s.MaxBodyBytes <= 0 {
		return &FieldError{Field: "south.max_body_bytes", Msg: "must be a positive number of bytes"}
	}
	if s.RequestTimeout <= 0 {
		return &FieldError{Field: "south.request_timeout", Msg: "must not be negative"}
	}
	return nil
}

// validate reports the first MFA setting that cannot be acted on.
func (m MFAConfig) validate() error {
	if m.ChallengeTTL <= 0 {
		return &FieldError{Field: "mfa.challenge_ttl", Msg: "must not be negative"}
	}
	if m.PollAfter <= 0 {
		return &FieldError{Field: "mfa.poll_after", Msg: "must not be negative"}
	}
	if m.PollAfter >= m.ChallengeTTL {
		// A challenge the proxy may poll at most once before it expires is
		// a second factor nobody can answer.
		return &FieldError{
			Field: "mfa.poll_after",
			Msg:   "must be shorter than mfa.challenge_ttl, or the challenge expires before it can be polled",
		}
	}
	if m.MaxPolls <= 0 {
		return &FieldError{Field: "mfa.max_polls", Msg: "must be a positive number of polls"}
	}
	return nil
}

// validate reports the first decision-path bound that cannot be acted on.
func (d DecisionConfig) validate() error {
	if d.Refresh <= 0 {
		return &FieldError{Field: "decision.refresh", Msg: "must not be negative"}
	}
	if d.Budget <= 0 {
		return &FieldError{Field: "decision.budget", Msg: "must not be negative"}
	}
	if d.MaxCacheTTL <= 0 {
		return &FieldError{Field: "decision.max_cache_ttl", Msg: "must not be negative"}
	}
	return nil
}

// validate reports the first uid setting that cannot be acted on.
func (u UIDConfig) validate() error {
	if u.RangeMin < 0 {
		return &FieldError{Field: "uids.range_min", Msg: "must not be negative"}
	}
	if u.RangeMax <= u.RangeMin {
		return &FieldError{Field: "uids.range_max", Msg: "must be greater than uids.range_min"}
	}
	if u.BlockSize <= 0 {
		return &FieldError{Field: "uids.block_size", Msg: "must be a positive number of uids"}
	}
	if u.MaxBlockSize < u.BlockSize {
		return &FieldError{
			Field: "uids.max_block_size",
			Msg:   "must not be smaller than uids.block_size",
		}
	}
	if u.LeaseTerm < 0 {
		return &FieldError{Field: "uids.lease_term", Msg: "must not be negative"}
	}
	return nil
}

// validate reports the first fleet field that cannot be acted on.
//
// It runs after the defaults, so a zero has already become its default and what
// is left to catch is a value the default cannot rescue: a negative duration, a
// non-positive hop cap, or a report interval that guarantees every record expires
// between two reports.
func (f FleetConfig) validate() error {
	durations := []struct {
		field string
		value time.Duration
	}{
		{"fleet.heartbeat_ttl", f.HeartbeatTTL},
		{"fleet.relay_registration_ttl", f.RelayRegistrationTTL},
		{"fleet.target_capability_ttl", f.TargetCapabilityTTL},
		{"fleet.capability_report_after", f.CapabilityReportAfter},
		{"fleet.host_key_cache_ttl", f.HostKeyCacheTTL},
	}
	for _, d := range durations {
		if d.value <= 0 {
			return &FieldError{Field: d.field, Msg: "must not be negative"}
		}
	}
	if f.MaxHops <= 0 {
		return &FieldError{Field: "fleet.max_hops", Msg: "must be a positive number of proxies"}
	}
	if f.CapabilityReportAfter >= f.TargetCapabilityTTL {
		// A report interval at or beyond the TTL means every record expires
		// between two reports, so a healthy fleet would look permanently stale
		// and the rule would fire constantly — which is how an operator learns
		// to ignore it.
		return &FieldError{
			Field: "fleet.capability_report_after",
			Msg:   "must be shorter than fleet.target_capability_ttl, or every record expires between reports",
		}
	}
	return nil
}
