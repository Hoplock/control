// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

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

// ...and it documents every key there is (0021). Strict decoding catches a key
// the example names and this package does not know, so loading it proves one
// direction only: a key added here and left out of the example is a setting no
// operator is told exists, and loads just the same. This walks the other way,
// from every `yaml` tag reachable from Config to the example's own keys.
func TestTheExampleDocumentsEveryKey(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		switch n.Kind {
		case yaml.DocumentNode:
			walk(n.Content[0], prefix)
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := prefix + n.Content[i].Value
				documented[key] = true
				walk(n.Content[i+1], key+".")
			}
		case yaml.SequenceNode, yaml.ScalarNode, yaml.AliasNode:
			// A value, or a list of them: no key below it.
		}
	}
	walk(&doc, "")

	var keys func(t reflect.Type, prefix string) []string
	keys = func(typ reflect.Type, prefix string) []string {
		var out []string
		for i := range typ.NumField() {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			out = append(out, prefix+name)
			// A list of sections (access_context.providers) is documented
			// as the list; its entries are examples in comments.
			if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeFor[time.Time]() {
				out = append(out, keys(f.Type, prefix+name+".")...)
			}
		}
		return out
	}
	for _, key := range keys(reflect.TypeFor[config.Config](), "") {
		if !documented[key] {
			t.Errorf("config.example.yaml does not document %s", key)
		}
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

	// The ceiling itself is conformant.
	for name, doc := range map[string]string{
		"the ceiling exactly":        base + "events: {heartbeat_interval: 10s}\n",
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
}

// The two debug paths 0019 superseded are gone, and so are their keys: a
// document naming one is refused as an unknown key by the strict loader, never
// accepted and ignored — an operator who believes they bound a listener must be
// told they did not.
func TestTheSupersededDebugKeysNoLongerParse(t *testing.T) {
	base := `listeners: {south: "0.0.0.0:8443", north: "127.0.0.1:9443"}
database: {dsn: "postgres://x"}
`
	for _, doc := range []string{
		base + `events: {publish_listener: "127.0.0.1:9000"}` + "\n",
		base + `events: {publish_token: "s"}` + "\n",
		base + `audit: {read_listener: "127.0.0.1:9001"}` + "\n",
		base + `audit: {read_token: "s"}` + "\n",
	} {
		_, err := config.Parse(strings.NewReader(doc))
		if err == nil || !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "unknown") {
			t.Errorf("%q: err = %v, want an unknown-key refusal", doc, err)
		}
	}
}

// Grants and notifications (0012). Both sections are optional and both have a
// default for everything; what they refuse is a value nobody can act on.
func TestGrantAndNotifyDefaultsApplyWhenTheSectionsAreAbsent(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(validYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Grants.MaxDuration != config.DefaultGrantMaxDuration {
		t.Errorf("grants.max_duration = %v, want the default %v", cfg.Grants.MaxDuration, config.DefaultGrantMaxDuration)
	}
	if cfg.Grants.WorkflowPollInterval != config.DefaultGrantWorkflowPollInterval {
		t.Errorf("grants.workflow_poll_interval = %v, want the default %v",
			cfg.Grants.WorkflowPollInterval, config.DefaultGrantWorkflowPollInterval)
	}
	if cfg.Notify.WebhookTimeout != config.DefaultWebhookTimeout {
		t.Errorf("notify.webhook_timeout = %v, want the default %v", cfg.Notify.WebhookTimeout, config.DefaultWebhookTimeout)
	}
	// No webhook unless one is configured: notifications leave this
	// deployment only for a destination somebody chose.
	if cfg.Notify.WebhookURL != "" {
		t.Errorf("notify.webhook_url = %q, want none unless configured", cfg.Notify.WebhookURL)
	}
}

func TestGrantAndNotifySettingsNobodyCanActOnAreRefused(t *testing.T) {
	for field, section := range map[string]string{
		"grants.max_duration":           "grants: {max_duration: -1h}",
		"grants.workflow_poll_interval": "grants: {workflow_poll_interval: -5s}",
		"notify.webhook_timeout":        "notify: {webhook_url: \"https://hooks.example.com/x\", webhook_timeout: -1s}",
		// A signing key for a webhook that does not exist is a
		// configuration somebody believes does something.
		"notify.webhook_secret_env": "notify: {webhook_secret_env: HOOK_KEY}",
	} {
		t.Run(field, func(t *testing.T) {
			_, err := config.Parse(strings.NewReader(validYAML + section + "\n"))
			var fieldErr *config.FieldError
			if !errors.As(err, &fieldErr) || fieldErr.Field != field {
				t.Fatalf("Parse = %v, want a FieldError naming %s", err, field)
			}
		})
	}
}

func TestAccessContextDefaultsApplyWhenTheSectionIsAbsent(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(validYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a := cfg.AccessContext
	if a.ProbeBudget != config.DefaultProbeBudget || a.MaxWindow != config.DefaultExternalMaxWindow ||
		a.Unanswered != config.DefaultUnanswered || a.MaxPushBytes != config.DefaultMaxPushBytes {
		t.Errorf("access_context defaults = %+v", a)
	}
	// Nothing external is consulted unless somebody configured it.
	if len(a.Providers) != 0 || len(a.Egress.AllowCIDRs) != 0 {
		t.Errorf("providers %v, egress %v; want none unless configured", a.Providers, a.Egress.AllowCIDRs)
	}
	// The probe phase may never have more than half the decision budget.
	if a.ProbeBudget > cfg.Decision.Budget/2 {
		t.Errorf("the default probe budget %s is more than half the decision budget %s", a.ProbeBudget, cfg.Decision.Budget)
	}
}

func TestAccessContextSettingsNobodyCanActOnAreRefused(t *testing.T) {
	provider := func(body string) string {
		return "access_context:\n  providers:\n    - " + body + "\n"
	}
	for field, section := range map[string]string{
		// A probe that could spend the whole decision budget makes every
		// authorize call hostage to a third party.
		"access_context.probe_budget":          "access_context: {probe_budget: 1500ms}",
		"access_context.max_window":            "access_context: {max_window: 200h}",
		"access_context.unanswered":            "access_context: {unanswered: allow}",
		"access_context.max_push_bytes":        "access_context: {max_push_bytes: 99999999}",
		"access_context.egress.allow_cidrs[0]": "access_context: {egress: {allow_cidrs: [not-a-cidr]}}",
		"access_context.providers[0].name":     provider("{name: Acme_Scanner, push: {id: $.id, subject: $.s, targets: $.t, window_end: $.e}}"),
		"access_context.providers[0].probe":    provider("{name: idle}"),
		"access_context.providers[0].probe.confirm": provider(
			"{name: acme, probe: {url: \"https://s.example.com/\"}}"),
		"access_context.providers[0].probe.confirm[0]": provider(
			"{name: acme, probe: {url: \"https://s.example.com/\", confirm: [{path: $.a, equals: x, exists: true}]}}"),
		"access_context.providers[0].probe.timeout": provider(
			"{name: acme, probe: {url: \"https://s.example.com/\", timeout: 2s, confirm: [{path: $.a, exists: true}]}}"),
		"access_context.providers[0].push.window_end": provider(
			"{name: acme, push: {id: $.id, subject: $.s, targets: $.t}}"),
		// An end is read by assertions in probe.confirm's vocabulary: a path
		// and exactly one predicate (0017) ...
		"access_context.providers[0].push.ended[0]": provider(
			"{name: acme, push: {id: $.id, subject: $.s, targets: $.t, window_end: $.e, ended: [{path: $.status}]}}"),
		// ... and no placeholder, because a push names no access to expand
		// one against.
		"access_context.providers[0].push.ended[1]": provider(
			"{name: acme, push: {id: $.id, subject: $.s, targets: $.t, window_end: $.e, " +
				"ended: [{path: $.status, equals: Closed}, {path: $.ci, one_of: [\"{{target.hostname}}\"]}]}}"),
	} {
		t.Run(field, func(t *testing.T) {
			_, err := config.Parse(strings.NewReader(validYAML + section + "\n"))
			var fieldErr *config.FieldError
			if !errors.As(err, &fieldErr) || fieldErr.Field != field {
				t.Fatalf("Parse = %v, want a FieldError naming %s", err, field)
			}
		})
	}

	ended := "access_context:\n  providers:\n" +
		"    - {name: acme, push: {id: $.id, subject: $.s, targets: $.t, window_end: $.e, " +
		"ended: [{path: $.status, one_of: [Cancelled, Closed]}]}}\n"
	cfg, err := config.Parse(strings.NewReader(validYAML + ended))
	if err != nil {
		t.Fatalf("a push mapping that reads ends: %v", err)
	}
	if got := cfg.AccessContext.Providers[0].Push.Ended; len(got) != 1 || len(got[0].OneOf) != 2 {
		t.Errorf("push.ended = %+v", got)
	}

	twice := "access_context:\n  providers:\n" +
		"    - {name: acme, push: {id: $.id, subject: $.s, targets: $.t, window_end: $.e}}\n" +
		"    - {name: acme, push: {id: $.id, subject: $.s, targets: $.t, window_end: $.e}}\n"
	var fieldErr *config.FieldError
	if _, err := config.Parse(strings.NewReader(validYAML + twice)); !errors.As(err, &fieldErr) ||
		fieldErr.Field != "access_context.providers[1].name" {
		t.Errorf("a provider configured twice = %v", err)
	}
}

// A body over south.max_log_body_bytes is a 400 the proxy cures by halving the
// batch, and that holds only while one record this server would store fits the
// limit: a batch of one cannot be halved, so the proxy sets that record aside
// for good (Hoplock/proxy#71). The two sections are configured apart, so the
// pairing is refused at load rather than found as lost records.
func TestALogBodyLimitBelowOneStorableRecordIsRefused(t *testing.T) {
	// One record's room: its capture as base64, its canonical body, and the
	// request around them.
	floor := func(capture, record int) int {
		return 4*((capture+2)/3) + record + 4<<10
	}
	body := func(capture, record, limit int) string {
		return validYAML + fmt.Sprintf("audit: {max_capture_bytes: %d, max_record_bytes: %d}\nsouth: {max_log_body_bytes: %d}\n",
			capture, record, limit)
	}

	for name, doc := range map[string]string{
		"a capture larger than the default body limit": validYAML +
			"audit: {max_capture_bytes: 8388608}\n",
		"a limit one byte short of a record": body(3000, 1000, floor(3000, 1000)-1),
		"a limit below the default capture":  validYAML + "south: {max_log_body_bytes: 1048576}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Parse(strings.NewReader(doc))
			var fieldErr *config.FieldError
			if !errors.As(err, &fieldErr) || fieldErr.Field != "south.max_log_body_bytes" {
				t.Fatalf("Parse = %v, want a FieldError naming south.max_log_body_bytes", err)
			}
		})
	}

	for name, doc := range map[string]string{
		"exactly one record": body(3000, 1000, floor(3000, 1000)),
		"the shipped bounds": validYAML,
		"a larger capture with room for it": validYAML +
			"audit: {max_capture_bytes: 2097152}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.Parse(strings.NewReader(doc)); err != nil {
				t.Fatalf("Parse = %v, want the configuration accepted", err)
			}
		})
	}
}

// The archive feed's bounds (0025) are refused at start-up when they cannot be
// acted on: a batch larger than an ingest batch, a capture batch too small for
// the largest capture, a duration that is not positive, a backoff under a
// second.
func TestTheArchiveFeedIsBoundedAtStartUp(t *testing.T) {
	base := `listeners: {south: "0.0.0.0:8443", north: "127.0.0.1:9443"}
database: {dsn: "postgres://x"}
`
	cases := map[string]struct{ doc, field string }{
		"a batch over the ingest bound": {
			doc:   base + "audit: {max_batch_records: 100, archive: {batch_records: 101}}\n",
			field: "audit.archive.batch_records",
		},
		"a batch over the default ingest bound": {
			doc:   base + "audit: {archive: {batch_records: 1001}}\n",
			field: "audit.archive.batch_records",
		},
		"a negative batch": {
			doc:   base + "audit: {archive: {batch_records: -1}}\n",
			field: "audit.archive.batch_records",
		},
		"a capture batch under the largest capture": {
			doc:   base + "audit: {max_capture_bytes: 2097152, archive: {capture_batch_bytes: 2097151}}\n",
			field: "audit.archive.capture_batch_bytes",
		},
		"a negative attempt timeout": {
			doc:   base + "audit: {archive: {attempt_timeout: -1s}}\n",
			field: "audit.archive.attempt_timeout",
		},
		"a negative idle interval": {
			doc:   base + "audit: {archive: {idle_interval: -1s}}\n",
			field: "audit.archive.idle_interval",
		},
		"a backoff cap under a second": {
			doc:   base + "audit: {archive: {backoff_cap: 500ms}}\n",
			field: "audit.archive.backoff_cap",
		},
		"no batch in flight": {
			doc:   base + "audit: {archive: {max_in_flight: -1}}\n",
			field: "audit.archive.max_in_flight",
		},
	}
	for name, tc := range cases {
		_, err := config.Parse(strings.NewReader(tc.doc))
		var fe *config.FieldError
		if !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("%s: err = %v, want a refusal of %s", name, err, tc.field)
		}
	}

	cfg, err := config.Parse(strings.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}
	want := config.ArchiveFeedConfig{
		BatchRecords: config.DefaultArchiveBatchRecords, CaptureBatchBytes: config.DefaultArchiveCaptureBatchBytes,
		AttemptTimeout: config.DefaultArchiveAttemptTimeout, IdleInterval: config.DefaultArchiveIdleInterval,
		BackoffCap: config.DefaultArchiveBackoffCap, MaxInFlight: config.DefaultArchiveMaxInFlight,
	}
	if cfg.Audit.Archive != want {
		t.Errorf("defaults = %+v, want %+v", cfg.Audit.Archive, want)
	}
	// The defaults follow the ingest bounds an operator lowered or raised.
	cfg, err = config.Parse(strings.NewReader(base +
		"audit: {max_batch_records: 100, max_capture_bytes: 33554432}\nsouth: {max_log_body_bytes: 67108864}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audit.Archive.BatchRecords != 100 || cfg.Audit.Archive.CaptureBatchBytes != 33554432 {
		t.Errorf("defaults under other ingest bounds = %+v", cfg.Audit.Archive)
	}
}

// Delivery to registered sinks (0026) is bounded at start-up beside the
// archive's feed and apart from it: a batch larger than an ingest batch, a
// duration that is not positive, a backoff under a second and no batch in
// flight are refused, and each zero takes its default.
func TestSinkDeliveryIsBoundedAtStartUp(t *testing.T) {
	base := `listeners: {south: "0.0.0.0:8443", north: "127.0.0.1:9443"}
database: {dsn: "postgres://x"}
`
	cases := map[string]struct{ doc, field string }{
		"a batch over the ingest bound": {
			doc:   base + "audit: {max_batch_records: 100, sinks: {batch_records: 101}}\n",
			field: "audit.sinks.batch_records",
		},
		"a batch over the default ingest bound": {
			doc:   base + "audit: {sinks: {batch_records: 1001}}\n",
			field: "audit.sinks.batch_records",
		},
		"a negative batch": {
			doc:   base + "audit: {sinks: {batch_records: -1}}\n",
			field: "audit.sinks.batch_records",
		},
		"a negative attempt timeout": {
			doc:   base + "audit: {sinks: {attempt_timeout: -1s}}\n",
			field: "audit.sinks.attempt_timeout",
		},
		"a negative idle interval": {
			doc:   base + "audit: {sinks: {idle_interval: -1s}}\n",
			field: "audit.sinks.idle_interval",
		},
		"a backoff cap under a second": {
			doc:   base + "audit: {sinks: {backoff_cap: 500ms}}\n",
			field: "audit.sinks.backoff_cap",
		},
		"no batch in flight": {
			doc:   base + "audit: {sinks: {max_in_flight: -1}}\n",
			field: "audit.sinks.max_in_flight",
		},
	}
	for name, tc := range cases {
		_, err := config.Parse(strings.NewReader(tc.doc))
		var fe *config.FieldError
		if !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("%s: err = %v, want a refusal of %s", name, err, tc.field)
		}
	}

	cfg, err := config.Parse(strings.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}
	want := config.SinkDeliveryConfig{
		BatchRecords: config.DefaultSinkBatchRecords, AttemptTimeout: config.DefaultSinkAttemptTimeout,
		IdleInterval: config.DefaultSinkIdleInterval, BackoffCap: config.DefaultSinkBackoffCap,
		MaxInFlight: config.DefaultSinkMaxInFlight,
	}
	if cfg.Audit.Sinks != want {
		t.Errorf("defaults = %+v, want %+v", cfg.Audit.Sinks, want)
	}
	// The batch's default follows an ingest bound an operator lowered, and
	// a sink's keys never move the archive's.
	cfg, err = config.Parse(strings.NewReader(base + "audit: {max_batch_records: 100, sinks: {idle_interval: 1s}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audit.Sinks.BatchRecords != 100 || cfg.Audit.Sinks.IdleInterval != time.Second ||
		cfg.Audit.Archive.IdleInterval != config.DefaultArchiveIdleInterval {
		t.Errorf("sinks %+v, archive %+v", cfg.Audit.Sinks, cfg.Audit.Archive)
	}
}
