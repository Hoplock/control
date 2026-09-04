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
