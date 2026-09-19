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
)

// logLevels is the set LogConfig.Level accepts, ordered from most to least
// verbose.
var logLevels = []string{"debug", "info", "warn", "error"}

// Config is the server's full configuration as loaded from YAML.
type Config struct {
	// Tenant names the tenant this deployment operates as (PLAN M12).
	Tenant string `yaml:"tenant"`

	Listeners ListenersConfig `yaml:"listeners"`
	Database  DatabaseConfig  `yaml:"database"`
	Log       LogConfig       `yaml:"log"`
	Fleet     FleetConfig     `yaml:"fleet"`
	South     SouthConfig     `yaml:"south"`
	MFA       MFAConfig       `yaml:"mfa"`
	UIDs      UIDConfig       `yaml:"uids"`
	Decision  DecisionConfig  `yaml:"decision"`
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
	// UIDs.LeaseTerm has no default: zero means "state no term", which is
	// a real answer rather than an unset field.
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
	return c.UIDs.validate()
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
