// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/config"
)

const validYAML = `
tenant: acme
listeners:
  south: 127.0.0.1:8443
  north: 127.0.0.1:9443
database:
  dsn: postgres://hoplock@localhost:5432/hoplock?sslmode=disable
log:
  level: debug
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, validYAML))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}

	if got, want := cfg.Tenant, "acme"; got != want {
		t.Errorf("Tenant = %q, want %q", got, want)
	}
	if got, want := cfg.Listeners.South, "127.0.0.1:8443"; got != want {
		t.Errorf("Listeners.South = %q, want %q", got, want)
	}
	if got, want := cfg.Listeners.North, "127.0.0.1:9443"; got != want {
		t.Errorf("Listeners.North = %q, want %q", got, want)
	}
	if got, want := cfg.Database.DSN, "postgres://hoplock@localhost:5432/hoplock?sslmode=disable"; got != want {
		t.Errorf("Database.DSN = %q, want %q", got, want)
	}
	if got, want := cfg.Log.Level, "debug"; got != want {
		t.Errorf("Log.Level = %q, want %q", got, want)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `
listeners:
  south: :8443
  north: :9443
database:
  dsn: postgres://localhost/hoplock
`))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}

	if got, want := cfg.Tenant, config.DefaultTenant; got != want {
		t.Errorf("Tenant = %q, want default %q", got, want)
	}
	if got, want := cfg.Log.Level, config.DefaultLogLevel; got != want {
		t.Errorf("Log.Level = %q, want default %q", got, want)
	}
}

// An unknown key is an error: strict decoding is what turns a typo in a
// security-relevant setting into a refusal to start (PLAN §8).
func TestLoadRejectsUnknownKey(t *testing.T) {
	_, err := config.Load(writeConfig(t, validYAML+"\nlistener_south: :8443\n"))
	if err == nil {
		t.Fatal("Load: want error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "listener_south") {
		t.Errorf("Load error = %q, want it to name the unknown key", err)
	}
}

func TestLoadRejectsUnknownNestedKey(t *testing.T) {
	_, err := config.Load(writeConfig(t, `
listeners:
  south: :8443
  north: :9443
  middle: :9444
database:
  dsn: postgres://localhost/hoplock
`))
	if err == nil {
		t.Fatal("Load: want error for unknown nested key, got nil")
	}
	if !strings.Contains(err.Error(), "middle") {
		t.Errorf("Load error = %q, want it to name the unknown key", err)
	}
}

func TestLoadRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name: "missing south listener",
			body: `
listeners:
  north: :9443
database:
  dsn: postgres://localhost/hoplock
`,
			wantField: "listeners.south",
		},
		{
			name: "missing north listener",
			body: `
listeners:
  south: :8443
database:
  dsn: postgres://localhost/hoplock
`,
			wantField: "listeners.north",
		},
		{
			name: "missing dsn",
			body: `
listeners:
  south: :8443
  north: :9443
`,
			wantField: "database.dsn",
		},
		{
			name: "listeners share a port",
			body: `
listeners:
  south: :8443
  north: :8443
database:
  dsn: postgres://localhost/hoplock
`,
			wantField: "listeners.north",
		},
		{
			name: "unknown log level",
			body: `
listeners:
  south: :8443
  north: :9443
database:
  dsn: postgres://localhost/hoplock
log:
  level: chatty
`,
			wantField: "log.level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, tt.body))
			if err == nil {
				t.Fatalf("Load: want error, got nil")
			}

			var fieldErr *config.FieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("Load error = %v (%T), want a *config.FieldError", err, err)
			}
			if fieldErr.Field != tt.wantField {
				t.Errorf("FieldError.Field = %q, want %q", fieldErr.Field, tt.wantField)
			}
			if !strings.Contains(err.Error(), tt.wantField) {
				t.Errorf("Load error = %q, want it to name %q", err, tt.wantField)
			}
		})
	}
}

func TestLoadEmptyFile(t *testing.T) {
	if _, err := config.Load(writeConfig(t, "")); err == nil {
		t.Fatal("Load: want error for an empty file, got nil")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("Load: want error for a missing file, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load error = %v, want it to wrap os.ErrNotExist", err)
	}
}

// The DSN carries a password in every real deployment, so a Config that finds
// its way into a log line or an error must not spell it out (PLAN §8).
func TestDatabaseConfigRedactsDSN(t *testing.T) {
	cfg, err := config.Load(writeConfig(t, `
listeners:
  south: :8443
  north: :9443
database:
  dsn: postgres://hoplock:hunter2@localhost:5432/hoplock
`))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}

	if printed := fmt.Sprintf("%v", *cfg); strings.Contains(printed, "hunter2") {
		t.Errorf("printed Config = %q, want the DSN redacted", printed)
	}
}

// The example file is documentation that has to stay true: if it stops
// loading, it stops being an example.
func TestExampleConfigLoads(t *testing.T) {
	if _, err := config.Load(filepath.Join("..", "..", "config.example.yaml")); err != nil {
		t.Fatalf("config.example.yaml does not load: %v", err)
	}
}

func TestSlogLevel(t *testing.T) {
	for _, tt := range []struct {
		level string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
	} {
		if got := (config.LogConfig{Level: tt.level}).SlogLevel(); got != tt.want {
			t.Errorf("LogConfig{Level: %q}.SlogLevel() = %v, want %v", tt.level, got, tt.want)
		}
	}
}

// The fleet's staleness numbers are validated after the defaults are applied, so
// what is caught is a value no default can rescue: a negative duration, a
// non-positive hop cap, or a report interval that guarantees every record expires
// between two reports (PLAN M6, M17).
func TestFleetStalenessIsValidated(t *testing.T) {
	base := `listeners: {south: "0.0.0.0:8443", north: "127.0.0.1:9443"}
database: {dsn: "postgres://x"}
`
	cases := map[string]struct{ doc, field string }{
		"negative heartbeat ttl": {
			doc:   base + "fleet: {heartbeat_ttl: -1s}\n",
			field: "fleet.heartbeat_ttl",
		},
		"negative relay ttl": {
			doc:   base + "fleet: {relay_registration_ttl: -1s}\n",
			field: "fleet.relay_registration_ttl",
		},
		"negative capability ttl": {
			doc:   base + "fleet: {target_capability_ttl: -1h}\n",
			field: "fleet.target_capability_ttl",
		},
		"negative max hops": {
			doc:   base + "fleet: {max_hops: -1}\n",
			field: "fleet.max_hops",
		},
		"report interval at the ttl": {
			doc:   base + "fleet: {target_capability_ttl: 1h, capability_report_after: 1h}\n",
			field: "fleet.capability_report_after",
		},
	}
	for name, tc := range cases {
		_, err := config.Parse(strings.NewReader(tc.doc))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		var fe *config.FieldError
		if !errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want a *FieldError", name, err)
			continue
		}
		if fe.Field != tc.field {
			t.Errorf("%s: field = %q, want %q", name, fe.Field, tc.field)
		}
	}
}

// A document that says nothing about the fleet takes every default, so an
// operator who never wants to think about staleness never has to. A field written
// as an explicit zero takes it too: YAML cannot distinguish that from absent, and
// the safe reading of `0s` is the default rather than "everything is stale".
func TestFleetDefaultsApplyWhenTheSectionIsAbsent(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(`listeners: {south: "0.0.0.0:8443", north: "127.0.0.1:9443"}
database: {dsn: "postgres://x"}
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Fleet.HeartbeatTTL != config.DefaultHeartbeatTTL {
		t.Errorf("heartbeat ttl = %v, want %v", cfg.Fleet.HeartbeatTTL, config.DefaultHeartbeatTTL)
	}
	if cfg.Fleet.RelayRegistrationTTL != config.DefaultRelayRegistrationTTL {
		t.Errorf("relay ttl = %v, want %v", cfg.Fleet.RelayRegistrationTTL, config.DefaultRelayRegistrationTTL)
	}
	if cfg.Fleet.TargetCapabilityTTL != config.DefaultTargetCapabilityTTL {
		t.Errorf("capability ttl = %v, want %v", cfg.Fleet.TargetCapabilityTTL, config.DefaultTargetCapabilityTTL)
	}
	if cfg.Fleet.CapabilityReportAfter != config.DefaultCapabilityReportAfter {
		t.Errorf("report interval = %v, want %v", cfg.Fleet.CapabilityReportAfter, config.DefaultCapabilityReportAfter)
	}
	if cfg.Fleet.MaxHops != config.DefaultMaxHops {
		t.Errorf("max hops = %d, want %d", cfg.Fleet.MaxHops, config.DefaultMaxHops)
	}

	zeroed, err := config.Parse(strings.NewReader(`listeners: {south: "0.0.0.0:8443", north: "127.0.0.1:9443"}
database: {dsn: "postgres://x"}
fleet: {heartbeat_ttl: 0s, max_hops: 0}
`))
	if err != nil {
		t.Fatalf("Parse with explicit zeros: %v", err)
	}
	if zeroed.Fleet.HeartbeatTTL != config.DefaultHeartbeatTTL {
		t.Errorf("heartbeat ttl = %v, want the default %v", zeroed.Fleet.HeartbeatTTL, config.DefaultHeartbeatTTL)
	}
	if zeroed.Fleet.MaxHops != config.DefaultMaxHops {
		t.Errorf("max hops = %d, want the default %d", zeroed.Fleet.MaxHops, config.DefaultMaxHops)
	}
}

// ---------------------------------------------------------------------------
// the revocation stream (PLAN M9, §4)
// ---------------------------------------------------------------------------

// The heartbeat interval is REFUSED past the contract's ceiling rather than
// clamped, and that refusal is the answer to "what stops it being configured
// too high". A server advertising 600s and honestly keeping to it passes its
// own claim and takes every proxy in the fleet off cached decisions, so the
// number is not one an operator gets wrong quietly: the process does not
// start.
func TestTheHeartbeatIntervalIsRefusedPastTheContractsCeiling(t *testing.T) {
	base := `listeners: {south: "0.0.0.0:8443", north: "127.0.0.1:9443"}
database: {dsn: "postgres://x"}
`
	cases := map[string]struct{ doc, field string }{
		"past the ceiling": {
			doc:   base + "events: {heartbeat_interval: 11s}\n",
			field: "events.heartbeat_interval",
		},
		"a fraction past it, because the advertisement rounds up": {
			// 10.001s advertises 11s — this server never claims an
			// interval it does not keep — and 11s is past the ceiling.
			doc:   base + "events: {heartbeat_interval: 10001ms}\n",
			field: "events.heartbeat_interval",
		},
		"negative": {
			doc:   base + "events: {heartbeat_interval: -1s}\n",
			field: "events.heartbeat_interval",
		},
		"a replay buffer of nothing": {
			doc:   base + "events: {replay_buffer: -1}\n",
			field: "events.replay_buffer",
		},
		"a subscriber queue of nothing": {
			doc:   base + "events: {subscriber_queue: -1}\n",
			field: "events.subscriber_queue",
		},
		"a publish listener with no credential": {
			doc:   base + `events: {publish_listener: "127.0.0.1:9000"}` + "\n",
			field: "events.publish_token",
		},
		"a publish credential nothing would read": {
			doc:   base + `events: {publish_token: "secret"}` + "\n",
			field: "events.publish_token",
		},
		"a publish listener sharing the south-bound port": {
			doc:   base + `events: {publish_listener: "0.0.0.0:8443", publish_token: "s"}` + "\n",
			field: "events.publish_listener",
		},
		"a publish listener sharing the north-bound port": {
			doc:   base + `events: {publish_listener: "127.0.0.1:9443", publish_token: "s"}` + "\n",
			field: "events.publish_listener",
		},
	}
	for name, tc := range cases {
		_, err := config.Parse(strings.NewReader(tc.doc))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		var fe *config.FieldError
		if !errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want a *FieldError", name, err)
			continue
		}
		if fe.Field != tc.field {
			t.Errorf("%s: field = %q, want %q", name, fe.Field, tc.field)
		}
	}

	// The ceiling itself is conformant, and so is a deployment that binds
	// the publish listener with a credential on a port of its own.
	for name, doc := range map[string]string{
		"the ceiling exactly":        base + "events: {heartbeat_interval: 10s}\n",
		"a bound publish listener":   base + `events: {publish_listener: "127.0.0.1:9000", publish_token: "s"}` + "\n",
		"nothing said about events":  base,
		"an explicit zero interval":  base + "events: {heartbeat_interval: 0s}\n",
		"an explicit zero of buffer": base + "events: {replay_buffer: 0}\n",
	} {
		if _, err := config.Parse(strings.NewReader(doc)); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

// A document that says nothing about the stream takes every default, and an
// explicit zero takes it too: YAML cannot tell `0` from absent, and the safe
// reading of a zero interval is the default rather than a stream that never
// heartbeats.
func TestEventDefaultsApplyWhenTheSectionIsAbsent(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(`listeners: {south: "0.0.0.0:8443", north: "127.0.0.1:9443"}
database: {dsn: "postgres://x"}
events: {heartbeat_interval: 0s}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Events.HeartbeatInterval != config.DefaultHeartbeatInterval {
		t.Errorf("heartbeat_interval = %v, want the default %v", cfg.Events.HeartbeatInterval, config.DefaultHeartbeatInterval)
	}
	if cfg.Events.ReplayBuffer != config.DefaultReplayBuffer {
		t.Errorf("replay_buffer = %v, want the default %v", cfg.Events.ReplayBuffer, config.DefaultReplayBuffer)
	}
	if cfg.Events.SubscriberQueue != config.DefaultSubscriberQueue {
		t.Errorf("subscriber_queue = %v, want the default %v", cfg.Events.SubscriberQueue, config.DefaultSubscriberQueue)
	}
	// The publish listener has NO default, and that is the point: a
	// deployment that does not ask for one has no publish port at all.
	if cfg.Events.PublishListener != "" {
		t.Errorf("publish_listener = %q, want none unless configured", cfg.Events.PublishListener)
	}
}
