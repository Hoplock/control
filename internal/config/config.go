// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
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
	// DefaultSouthMaxLogBodyBytes caps a body on the two log paths. A full
	// default batch of a busy session's capture is 64 records of up to
	// 32 KiB each — about 2.7 MiB once base64-encoded — and since
	// Hoplock/proxy#71 a body over the limit is halved by the proxy until
	// it fits, costing requests and protecting nothing the per-record
	// bounds do not.
	DefaultSouthMaxLogBodyBytes int64 = 4 << 20
	// DefaultAuditMaxRecordBytes and DefaultAuditMaxCaptureBytes are what a
	// zero `audit.max_record_bytes` and `audit.max_capture_bytes` take: the
	// shipped audit.DefaultLimits, restated here because this package
	// imports nothing of the server's, and held to it by a test in
	// internal/audit. Validate needs them to know how large one record can
	// be.
	DefaultAuditMaxRecordBytes  = 64 << 10
	DefaultAuditMaxCaptureBytes = 1 << 20
	// DefaultAuditMaxBatchRecords is what a zero `audit.max_batch_records`
	// takes, restated for the same reason and held by the same test: the
	// archive feed's batch may not exceed it.
	DefaultAuditMaxBatchRecords = 1000
	// The archive feed's defaults (0025), each the value a zero
	// `audit.archive.<key>` takes. A batch of 500 records is about one
	// ingest batch's worth; 16 MiB of captures is sixteen of the largest
	// capture the shipped bounds store; an attempt has as long as a
	// north-bound request does; an idle feed looks again every five seconds,
	// so a record reaches the archive within about that of its commit; and a
	// failing archive is retried at least every five minutes.
	DefaultArchiveBatchRecords            = 500
	DefaultArchiveCaptureBatchBytes int64 = 16 << 20
	DefaultArchiveAttemptTimeout          = 30 * time.Second
	DefaultArchiveIdleInterval            = 5 * time.Second
	DefaultArchiveBackoffCap              = 5 * time.Minute
	// DefaultArchiveMaxInFlight is the archive's bound on batches in flight
	// at once (0026): the four workers 0025 fixed in code, now a bound on
	// calls the archive is serving, configurable as a sink's is.
	DefaultArchiveMaxInFlight = 4
	// MinArchiveBackoffCap is the smallest backoff cap accepted: the feed's
	// first retry waits a second, and a cap below it would retry a failing
	// archive in a loop. A sink's backoff cap has the same floor.
	MinArchiveBackoffCap = time.Second
	// Delivery to registered sinks' defaults (0026), each the value a zero
	// `audit.sinks.<key>` takes. They start where the archive's do, and are
	// separate keys because a SIEM and an archive are tuned differently: a
	// SOC may want a shorter idle interval, and a SIEM's ingestion API a
	// smaller batch. Four batches in flight per sink is the archive's worker
	// bound, for the same reason: a worker holds a database connection
	// between its calls to the sink.
	DefaultSinkBatchRecords   = 500
	DefaultSinkAttemptTimeout = 30 * time.Second
	DefaultSinkIdleInterval   = 5 * time.Second
	DefaultSinkBackoffCap     = 5 * time.Minute
	DefaultSinkMaxInFlight    = 4
	// LogRecordEnvelopeBytes is the room one record needs on a log path
	// beside its capture and its canonical body: the request's own keys, the
	// record's field names and its payload key. Generous by design: it only
	// has to be more than the difference.
	LogRecordEnvelopeBytes = 4 << 10
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
	// this surface accepts (0019) and nothing on the contract is close.
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

// Access grants and notifications (0012).
const (
	// DefaultGrantMaxDuration is the longest window a grant may have: a
	// ceiling on a typo as much as a policy.
	DefaultGrantMaxDuration = 7 * 24 * time.Hour
	// DefaultGrantWorkflowPollInterval is how often pending workflow
	// requests are asked about, when a workflow is registered.
	DefaultGrantWorkflowPollInterval = 15 * time.Second
	// DefaultWebhookTimeout bounds one webhook delivery attempt.
	DefaultWebhookTimeout = 5 * time.Second
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
	Grants     GrantsConfig     `yaml:"grants"`
	Notify     NotifyConfig     `yaml:"notify"`

	AccessContext AccessContextConfig `yaml:"access_context"`
	Policy        PolicyConfig        `yaml:"policy"`
}

// PolicyConfig is what the publish-time report reads beyond the bundle (0019).
type PolicyConfig struct {
	// Interpreters are the executables that hand back a shell. A restricted
	// allow-list naming one, under a rung that claims the list is a
	// boundary, is a warning the author must accept. Empty takes the
	// shipped starting list; a list here replaces it whole, because an
	// estate knows its own tooling. It is never a data-plane deny-list.
	Interpreters []string `yaml:"interpreters"`
}

// AuditConfig bounds log ingest (PLAN §7, M8).
//
// The record read-back path that lived here until 0019 is gone: records are read
// over the north-bound audit query, under a scoped credential and RBAC, and the
// bounds below are ingest limits that nothing supersedes.
type AuditConfig struct {
	// MaxBatchRecords, MaxRecordBytes and MaxCaptureBytes bound one ingest
	// request. Zero takes the shipped default. They are configurable
	// because a fleet's record sizes are a property of its estate, and
	// bounded because an unbounded ingest path is a memory-exhaustion
	// surface reachable by any enrolled proxy.
	MaxBatchRecords int `yaml:"max_batch_records"`
	MaxRecordBytes  int `yaml:"max_record_bytes"`
	MaxCaptureBytes int `yaml:"max_capture_bytes"`
	// Archive bounds the feed to a registered ext.ArchiveStore (0025). It
	// is read only when an archive is registered.
	Archive ArchiveFeedConfig `yaml:"archive"`
	// Sinks bounds delivery to every registered ext.AuditSink (0026). It is
	// read only when a sink is registered, and applies to each sink alike: a
	// sink's own settings are its host's configuration.
	Sinks SinkDeliveryConfig `yaml:"sinks"`
}

// SinkDeliveryConfig bounds delivery to each registered ext.AuditSink: every
// committed record, per tenant and stream in ascending sequence (0026, PLAN
// §7). Each zero takes its default, and each is bounded at start-up. Delivery
// runs off ingest and the decision path (M5), so what it costs is the
// database's time and the sink's, never a proxy's.
type SinkDeliveryConfig struct {
	// BatchRecords is the most records one Export call carries, one
	// stream's. It is no larger than audit.max_batch_records.
	BatchRecords int `yaml:"batch_records"`
	// AttemptTimeout is the deadline on each call to a sink. A call that
	// runs past it confirms nothing and is offered again.
	AttemptTimeout time.Duration `yaml:"attempt_timeout"`
	// IdleInterval is how long delivery waits, with nothing to deliver,
	// before it looks again.
	IdleInterval time.Duration `yaml:"idle_interval"`
	// BackoffCap is the longest wait between attempts on a stream whose
	// Export keeps failing. The wait starts at a second and doubles.
	BackoffCap time.Duration `yaml:"backoff_cap"`
	// MaxInFlight is the most batches in flight to one sink at once, each
	// of a different stream; a call past its deadline counts until it
	// returns.
	MaxInFlight int `yaml:"max_in_flight"`
}

// ArchiveFeedConfig bounds the feed that hands a registered ext.ArchiveStore
// every committed record and capture (0025, PLAN §7). Each zero takes its
// default, and each is bounded at start-up: the feed runs off ingest and the
// decision path (M5), so what it costs is the database's time and the archive's,
// never a proxy's.
type ArchiveFeedConfig struct {
	// BatchRecords is the most records one Archive call carries, one
	// stream's. It is no larger than audit.max_batch_records.
	BatchRecords int `yaml:"batch_records"`
	// CaptureBatchBytes is the most capture bytes one ArchiveCaptures call
	// carries. It is at least audit.max_capture_bytes, so the largest
	// capture this server stores fits a batch of its own.
	CaptureBatchBytes int64 `yaml:"capture_batch_bytes"`
	// AttemptTimeout is the deadline on each call to the archive. A call
	// that runs past it confirms nothing and is retried.
	AttemptTimeout time.Duration `yaml:"attempt_timeout"`
	// IdleInterval is how long the feed waits, with nothing to deliver,
	// before it looks again.
	IdleInterval time.Duration `yaml:"idle_interval"`
	// BackoffCap is the longest wait between attempts on a stream whose
	// archive call keeps failing. The wait starts at a second and doubles.
	BackoffCap time.Duration `yaml:"backoff_cap"`
	// MaxInFlight is the most batches in flight to the archive at once,
	// each of a different stream; a call past its deadline counts until it
	// returns.
	MaxInFlight int `yaml:"max_in_flight"`
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
	// MaxBodyBytes caps a request body. Every south-bound payload but a log
	// batch is a small JSON object.
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	// MaxLogBodyBytes caps a body on the two log paths, which carry session
	// capture. A body over it is a 400 the proxy cures by halving the batch
	// (Hoplock/proxy#71), so it is sized to let a full default batch land in
	// one request.
	MaxLogBodyBytes int64 `yaml:"max_log_body_bytes"`
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
	cfg, _, err := LoadHost(path, nil)
	return cfg, err
}

// LoadHost is [Load] for a host binary that owns top-level sections of the
// file (M15). See [ParseHost].
func LoadHost(path string, host []string) (*Config, []HostSection, error) {
	if err := CheckHostSections(host); err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	cfg, sections, err := ParseHost(f, host)
	if err != nil {
		return nil, nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, sections, nil
}

// Parse decodes and validates a configuration document. Decoding is strict:
// a key the schema does not define is an error, not a shrug.
func Parse(r io.Reader) (*Config, error) {
	cfg, _, err := ParseHost(r, nil)
	return cfg, err
}

// ParseHost is [Parse] for a host binary that owns top-level sections of the
// file (M15): each name in host is a key the strict decoder accepts without
// Control defining it, and every other unknown key is still refused, with the
// same message and the same line number Parse gives. The sections present in
// the document come back re-encoded as YAML, in document order; an absent one
// does not come back at all.
func ParseHost(r io.Reader, host []string) (*Config, []HostSection, error) {
	if err := CheckHostSections(host); err != nil {
		return nil, nil, err
	}
	doc, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, fmt.Errorf("config: %w", err)
	}

	// The host's sections are found first, by key and by line, because the
	// strict decode below reports each of them as unknown and needs to know
	// which refusals are the host's to drop. A document that does not parse
	// finds none here, and the decode below says why in today's words.
	sections, expected, err := hostSections(doc, host)
	if err != nil {
		return nil, nil, err
	}

	dec := yaml.NewDecoder(bytes.NewReader(doc))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, errors.New("config: file is empty")
		}
		if err := withoutHostKeys(err, expected); err != nil {
			return nil, nil, fmt.Errorf("config: %w", err)
		}
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	return &cfg, sections, nil
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
	if c.South.MaxLogBodyBytes == 0 {
		c.South.MaxLogBodyBytes = DefaultSouthMaxLogBodyBytes
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
	if c.Grants.MaxDuration == 0 {
		c.Grants.MaxDuration = DefaultGrantMaxDuration
	}
	if c.Grants.WorkflowPollInterval == 0 {
		c.Grants.WorkflowPollInterval = DefaultGrantWorkflowPollInterval
	}
	c.AccessContext.applyDefaults()
	c.Audit.Archive.applyDefaults(c.Audit)
	c.Audit.Sinks.applyDefaults(c.Audit)
	if c.Notify.WebhookTimeout == 0 {
		c.Notify.WebhookTimeout = DefaultWebhookTimeout
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

// GrantsConfig bounds just-in-time grants (PLAN M10, 0012).
//
// There is deliberately no setting for how often expired grants are cleaned up:
// nothing has to run for a grant to expire. A grant is live exactly while the
// decision's own instant falls inside its window, so there is no sweeper whose
// schedule could leave production access standing.
type GrantsConfig struct {
	// MaxDuration is the longest window a grant may have. A request for
	// more is refused, naming this limit.
	MaxDuration time.Duration `yaml:"max_duration"`
	// WorkflowPollInterval is how often requests pending in a registered
	// Hoplock Enterprise approval workflow are asked about
	// (ext.GrantWorkflow). Unused when no workflow is registered.
	WorkflowPollInterval time.Duration `yaml:"workflow_poll_interval"`
}

// NotifyConfig configures Control's outbound webhook — its own answer at the
// notifier seam (ext.Notifier, M15). With no URL, notifications go only to
// the notifiers a host binary registered, and a deployment with neither is
// told so at start-up.
type NotifyConfig struct {
	// WebhookURL is where each notification is POSTed as JSON. https, or
	// http to a loopback host only, and never with credentials in it.
	WebhookURL string `yaml:"webhook_url"`
	// WebhookSecretEnv names the environment variable holding the key every
	// request is signed with (HMAC-SHA256). It names the variable and never
	// holds the key: a secret in a configuration file is a secret in every
	// copy of it.
	WebhookSecretEnv string `yaml:"webhook_secret_env"`
	// WebhookTimeout bounds one delivery attempt.
	WebhookTimeout time.Duration `yaml:"webhook_timeout"`
}

// validate reports the first grant setting that cannot be acted on.
func (g GrantsConfig) validate() error {
	if g.MaxDuration <= 0 {
		return &FieldError{Field: "grants.max_duration", Msg: "must be a positive duration"}
	}
	if g.WorkflowPollInterval <= 0 {
		return &FieldError{Field: "grants.workflow_poll_interval", Msg: "must be a positive duration"}
	}
	return nil
}

// validate reports the first notification setting that cannot be acted on.
// The URL's scheme and host rules are the notifier's, and are checked when
// it is built at start-up (`internal/notify`), with the one error message.
func (n NotifyConfig) validate() error {
	if n.WebhookTimeout <= 0 {
		return &FieldError{Field: "notify.webhook_timeout", Msg: "must be a positive duration"}
	}
	if n.WebhookSecretEnv != "" && n.WebhookURL == "" {
		return &FieldError{
			Field: "notify.webhook_secret_env",
			Msg:   "names a signing key for a webhook that is not configured (notify.webhook_url)",
		}
	}
	return nil
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
	if err := c.Events.validate(); err != nil {
		return err
	}
	if err := c.Audit.validate(); err != nil {
		return err
	}
	if floor := MinLogBodyBytes(c.Audit); c.South.MaxLogBodyBytes < floor {
		return &FieldError{
			Field: "south.max_log_body_bytes",
			Msg: fmt.Sprintf(
				"is %d bytes, below the %d one record this server stores can need (audit.max_capture_bytes as base64, audit.max_record_bytes, and the request around them): a batch of that one record would be refused for its size, and halving cannot cure a batch of one",
				c.South.MaxLogBodyBytes, floor),
		}
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
	if err := c.Grants.validate(); err != nil {
		return err
	}
	if err := c.Notify.validate(); err != nil {
		return err
	}
	if err := c.AccessContext.validate(c.Decision, c.Grants); err != nil {
		return err
	}
	return c.UIDs.validate()
}

// validate reports the first revocation-stream setting that cannot be acted on.
func (e EventsConfig) validate() error {
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
	return nil
}

// validate reports the first audit setting that cannot be acted on.
func (a AuditConfig) validate() error {
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

	if err := a.Archive.validate(a); err != nil {
		return err
	}
	return a.Sinks.validate(a)
}

// applyDefaults fills the archive feed's zeros. The batch's default is no
// larger than the ingest batch bound, which an operator may have set lower.
func (a *ArchiveFeedConfig) applyDefaults(audit AuditConfig) {
	if a.BatchRecords == 0 {
		a.BatchRecords = min(DefaultArchiveBatchRecords, maxBatchRecords(audit))
	}
	if a.CaptureBatchBytes == 0 {
		a.CaptureBatchBytes = max(DefaultArchiveCaptureBatchBytes, int64(maxCaptureBytes(audit)))
	}
	if a.AttemptTimeout == 0 {
		a.AttemptTimeout = DefaultArchiveAttemptTimeout
	}
	if a.IdleInterval == 0 {
		a.IdleInterval = DefaultArchiveIdleInterval
	}
	if a.BackoffCap == 0 {
		a.BackoffCap = DefaultArchiveBackoffCap
	}
	if a.MaxInFlight == 0 {
		a.MaxInFlight = DefaultArchiveMaxInFlight
	}
}

// validate reports the first archive feed setting that cannot be acted on,
// after the defaults.
func (a ArchiveFeedConfig) validate(audit AuditConfig) error {
	switch {
	case a.BatchRecords <= 0:
		return &FieldError{Field: "audit.archive.batch_records", Msg: "must be a positive number of records, or absent for the default"}
	case a.BatchRecords > maxBatchRecords(audit):
		return &FieldError{Field: "audit.archive.batch_records", Msg: fmt.Sprintf(
			"must be no more than audit.max_batch_records (%d)", maxBatchRecords(audit))}
	case a.CaptureBatchBytes < int64(maxCaptureBytes(audit)):
		return &FieldError{Field: "audit.archive.capture_batch_bytes", Msg: fmt.Sprintf(
			"must be at least audit.max_capture_bytes (%d), so the largest capture fits a batch", maxCaptureBytes(audit))}
	case a.AttemptTimeout <= 0:
		return &FieldError{Field: "audit.archive.attempt_timeout", Msg: "must be a positive duration, or absent for the default"}
	case a.IdleInterval <= 0:
		return &FieldError{Field: "audit.archive.idle_interval", Msg: "must be a positive duration, or absent for the default"}
	case a.BackoffCap < MinArchiveBackoffCap:
		return &FieldError{Field: "audit.archive.backoff_cap", Msg: "must be at least 1s, or absent for the default"}
	case a.MaxInFlight <= 0:
		return &FieldError{Field: "audit.archive.max_in_flight", Msg: "must be a positive number of batches, or absent for the default"}
	}
	return nil
}

// applyDefaults fills sink delivery's zeros. The batch's default is no larger
// than the ingest batch bound, which an operator may have set lower.
func (d *SinkDeliveryConfig) applyDefaults(audit AuditConfig) {
	if d.BatchRecords == 0 {
		d.BatchRecords = min(DefaultSinkBatchRecords, maxBatchRecords(audit))
	}
	if d.AttemptTimeout == 0 {
		d.AttemptTimeout = DefaultSinkAttemptTimeout
	}
	if d.IdleInterval == 0 {
		d.IdleInterval = DefaultSinkIdleInterval
	}
	if d.BackoffCap == 0 {
		d.BackoffCap = DefaultSinkBackoffCap
	}
	if d.MaxInFlight == 0 {
		d.MaxInFlight = DefaultSinkMaxInFlight
	}
}

// validate reports the first sink delivery setting that cannot be acted on,
// after the defaults.
func (d SinkDeliveryConfig) validate(audit AuditConfig) error {
	switch {
	case d.BatchRecords <= 0:
		return &FieldError{Field: "audit.sinks.batch_records", Msg: "must be a positive number of records, or absent for the default"}
	case d.BatchRecords > maxBatchRecords(audit):
		return &FieldError{Field: "audit.sinks.batch_records", Msg: fmt.Sprintf(
			"must be no more than audit.max_batch_records (%d)", maxBatchRecords(audit))}
	case d.AttemptTimeout <= 0:
		return &FieldError{Field: "audit.sinks.attempt_timeout", Msg: "must be a positive duration, or absent for the default"}
	case d.IdleInterval <= 0:
		return &FieldError{Field: "audit.sinks.idle_interval", Msg: "must be a positive duration, or absent for the default"}
	case d.BackoffCap < MinArchiveBackoffCap:
		return &FieldError{Field: "audit.sinks.backoff_cap", Msg: "must be at least 1s, or absent for the default"}
	case d.MaxInFlight <= 0:
		return &FieldError{Field: "audit.sinks.max_in_flight", Msg: "must be a positive number of batches, or absent for the default"}
	}
	return nil
}

func maxBatchRecords(a AuditConfig) int {
	if a.MaxBatchRecords <= 0 {
		return DefaultAuditMaxBatchRecords
	}
	return a.MaxBatchRecords
}

func maxCaptureBytes(a AuditConfig) int {
	if a.MaxCaptureBytes <= 0 {
		return DefaultAuditMaxCaptureBytes
	}
	return a.MaxCaptureBytes
}

// MinLogBodyBytes is the smallest `south.max_log_body_bytes` that holds one
// record the audit bounds let this server store: its capture as base64, its
// canonical body, and [LogRecordEnvelopeBytes].
//
// The limit exists so that a body over it is a 400 the proxy cures by halving
// the batch (PLAN §7, Hoplock/proxy#71). That is true only while every record
// this server would store fits: a batch of one cannot be halved, so the proxy
// sets the record aside and it is lost to this store, for a condition that was
// never about the record. A record whose redaction shortened a value can still
// be larger on the wire than its stored body; a capture-sized record is not
// that record.
func MinLogBodyBytes(a AuditConfig) int64 {
	record, capture := a.MaxRecordBytes, a.MaxCaptureBytes
	if record == 0 {
		record = DefaultAuditMaxRecordBytes
	}
	if capture == 0 {
		capture = DefaultAuditMaxCaptureBytes
	}
	encoded := 4 * ((int64(capture) + 2) / 3)
	return encoded + int64(record) + LogRecordEnvelopeBytes
}

// validate reports the first south-bound bound that cannot be acted on.
func (s SouthConfig) validate() error {
	if s.MaxBodyBytes <= 0 {
		return &FieldError{Field: "south.max_body_bytes", Msg: "must be a positive number of bytes"}
	}
	if s.MaxLogBodyBytes <= 0 {
		return &FieldError{Field: "south.max_log_body_bytes", Msg: "must be a positive number of bytes"}
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
