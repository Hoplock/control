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

	"gopkg.in/yaml.v3"
)

// DefaultTenant is the tenant a single-tenant deployment runs as. Tenancy is
// in the schema from day one (PLAN M12) even though nothing in the API exposes
// it yet, so every deployment names a tenant — this one by default.
const DefaultTenant = "default"

// DefaultLogLevel is the level used when the file does not name one.
const DefaultLogLevel = "info"

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
	return nil
}
