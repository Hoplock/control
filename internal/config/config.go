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
	return c.Fleet.validate()
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
