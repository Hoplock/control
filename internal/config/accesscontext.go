// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
)

// External access context (PLAN M16, phase 0013).
const (
	// DefaultProbeBudget is the probe phase's share of the decision budget.
	// It may be at most half of decision.budget: the other half is the
	// decision's own (M5).
	DefaultProbeBudget = 500 * time.Millisecond
	// DefaultExternalMaxWindow is the server's ceiling on an external
	// window, applied after an integration's own binding.
	DefaultExternalMaxWindow = 12 * time.Hour
	// DefaultClockSkew is the clock disagreement tolerated on an assertion.
	DefaultClockSkew = 2 * time.Minute
	// DefaultMaxAssertionAge is how old an assertion may be on arrival.
	DefaultMaxAssertionAge = 15 * time.Minute
	// DefaultUnanswered is what an unanswered probe means for a scope that is
	// not privileged and declares nothing: an outage, which grants nothing
	// and tells nobody "access denied" because a scanner's API was slow.
	DefaultUnanswered = "outage"
	// DefaultMaxProbeTTL caps how long any probe answer is reused.
	DefaultMaxProbeTTL = 5 * time.Minute
	// DefaultMaxProbesInFlight bounds one provider's concurrent probes.
	DefaultMaxProbesInFlight = 32
	// DefaultPushRate and DefaultPushBurst bound one integration's pushes in
	// one tenant.
	DefaultPushRate  = 5.0
	DefaultPushBurst = 20
	// DefaultMaxPushBytes caps a push body. A window assertion is a small
	// JSON object; a megabyte of it is not a window.
	DefaultMaxPushBytes int64 = 64 << 10
	// DefaultDeclarativeTTL is how long a declarative probe's answer is
	// reused when the provider does not say.
	DefaultDeclarativeTTL = 30 * time.Second
)

// unansweredSettings is the closed set `access_context.unanswered` takes, the
// same vocabulary as a policy scope's `unanswered`.
var unansweredSettings = []string{"closed", "outage", "open"}

// providerName is what a provider may be called: the same rule
// internal/accessctx enforces, repeated here so a typo is a load-time error
// that names the key rather than a start-up one that does not.
var providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// AccessContextConfig configures external access context (M16): the probe
// path's budget and cache, the push receiver's bounds, the egress allow-list,
// and Control's declarative HTTP providers.
//
// Every provider configured here is registered at start-up as its own
// `ext.AccessContextProvider`, named `hoplock/control/declarative/<name>`.
// None of it grants anything by itself: an integration asserts nothing in a
// tenant until a scope binding names it there.
type AccessContextConfig struct {
	// ProbeBudget is the deadline on every probe one decision makes, all of
	// them at once. At most half of decision.budget.
	ProbeBudget time.Duration `yaml:"probe_budget"`
	// MaxWindow is the server's ceiling on an external window, applied after
	// the binding's own maximum. At most grants.max_duration.
	MaxWindow time.Duration `yaml:"max_window"`
	// ClockSkew is how far an external system's clock may disagree with this
	// server's.
	ClockSkew time.Duration `yaml:"clock_skew"`
	// MaxAssertionAge is how old a pushed assertion may be when it says when
	// it was issued.
	MaxAssertionAge time.Duration `yaml:"max_assertion_age"`
	// Unanswered is the default for a scope the policy neither marks
	// privileged nor gives an answer of its own: closed, outage or open. A
	// privileged scope is closed whatever this says.
	Unanswered string `yaml:"unanswered"`
	// MaxProbeTTL caps how long any probe answer is reused.
	MaxProbeTTL time.Duration `yaml:"max_probe_ttl"`
	// MaxProbesInFlight bounds one provider's concurrent probes.
	MaxProbesInFlight int `yaml:"max_probes_in_flight"`
	// PushRate and PushBurst bound one integration's pushes per tenant:
	// sustained per second, and at once.
	PushRate  float64 `yaml:"push_rate"`
	PushBurst int     `yaml:"push_burst"`
	// MaxPushBytes caps a push body.
	MaxPushBytes int64 `yaml:"max_push_bytes"`
	// Egress is where a declarative provider may connect.
	Egress EgressConfig `yaml:"egress"`
	// Providers are Control's declarative HTTP providers.
	Providers []DeclarativeProviderConfig `yaml:"providers"`
}

// EgressConfig is the allow-list a declarative provider's connections are held
// to.
//
// A request template an administrator can point anywhere is an SSRF primitive
// with an administrator holding the pen. So a provider's host is fixed by its
// URL — a template may fill a path or a query, never a scheme, a host or a port
// — and every connection is checked AT DIAL TIME against the address actually
// being dialled, so a name that resolves somewhere else tomorrow does not get
// there either. Loopback, private, link-local (the cloud metadata service
// among them), CGNAT, multicast and unspecified addresses are refused unless a
// prefix here admits them.
type EgressConfig struct {
	// AllowCIDRs are internal prefixes a provider may reach, such as the
	// subnet an on-premises scanner lives in. Empty admits public addresses
	// only.
	AllowCIDRs []string `yaml:"allow_cidrs"`
}

// DeclarativeProviderConfig is one declarative HTTP provider: a probe, a push
// mapping, or both.
type DeclarativeProviderConfig struct {
	// Name is the external system's name: the key its scope bindings are
	// filed under, the push receiver's path segment, and the `system` every
	// grant it supports records.
	Name string `yaml:"name"`
	// Probe asks the external system on the authorize path. Nil: this
	// provider does not probe.
	Probe *DeclarativeProbeConfig `yaml:"probe"`
	// Push reads what the external system pushes. Nil: this provider takes
	// no pushes.
	Push *DeclarativePushConfig `yaml:"push"`
}

// DeclarativeProbeConfig asks an HTTP endpoint whether a window is open.
//
// Templates — the URL and the body — take a closed set of placeholders:
// `{{tenant}}`, `{{subject.id}}`, `{{target.hostname}}`, `{{target.zone}}`,
// `{{reference}}` (the pushed window's, empty for a probe-only one), `{{scope}}`
// and `{{at}}` (RFC 3339). Every value is escaped for where it lands.
//
// Paths are a small JSON path: `$.a.b`, `$.a[0]`, `$.a[*]`, `$['odd key']`.
type DeclarativeProbeConfig struct {
	// URL is the endpoint, as a template. https, or http to a loopback host
	// only; no credentials in it; placeholders only in its path and query.
	URL string `yaml:"url"`
	// Method is GET (the default) or POST.
	Method string `yaml:"method"`
	// Headers are sent as written. Placeholders are not expanded in them.
	Headers map[string]string `yaml:"headers"`
	// Body is a JSON template, POSTed as application/json.
	Body string `yaml:"body"`
	// Auth is how the request authenticates.
	Auth DeclarativeAuthConfig `yaml:"auth"`
	// Timeout bounds one probe. At most the probe budget; zero is the budget.
	Timeout time.Duration `yaml:"timeout"`
	// TTL is how long an answer is reused for the same access. Zero takes
	// DefaultDeclarativeTTL; the server's max_probe_ttl caps it.
	TTL time.Duration `yaml:"ttl"`
	// AbsentStatus are HTTP statuses that mean "no such window". Empty
	// takes [404]. Every other non-2xx status is an unreachable system.
	AbsentStatus []int `yaml:"absent_status"`
	// Confirm are the assertions over a 2xx answer, ALL of which must hold
	// for it to confirm a window. At least one.
	Confirm []DeclarativeAssertion `yaml:"confirm"`
	// Reference, WindowStart, WindowEnd and AdditionalContext are paths to
	// the window's reference, its bounds (RFC 3339, or Unix seconds), and a
	// JSON string or object to record. All optional; a confirmation must
	// still name a reference, here or by confirming a pushed one.
	Reference         string `yaml:"reference"`
	WindowStart       string `yaml:"window_start"`
	WindowEnd         string `yaml:"window_end"`
	AdditionalContext string `yaml:"additional_context"`
	// Assertions are further facts to record, by code and path.
	Assertions map[string]string `yaml:"assertions"`
}

// DeclarativeAssertion is one assertion over a probe's answer: a path and
// exactly one predicate. Values are compared as text; a template value
// (`{{target.hostname}}`) is expanded first.
type DeclarativeAssertion struct {
	Path string `yaml:"path"`
	// Equals holds when some value at Path is this value.
	Equals *string `yaml:"equals"`
	// NotEquals holds when Path has a value and none of them is this one.
	NotEquals *string `yaml:"not_equals"`
	// OneOf holds when some value at Path is one of these.
	OneOf []string `yaml:"one_of"`
	// Exists holds when Path has a value (true) or has none (false).
	Exists *bool `yaml:"exists"`
	// Contains holds when the values at Path — the elements, when Path is a
	// list — include this one.
	Contains *string `yaml:"contains"`
}

// DeclarativeAuthConfig is how a probe authenticates. Secrets are read from
// environment variables the configuration NAMES, never from the file: a secret
// in a configuration file is a secret in every copy of it.
type DeclarativeAuthConfig struct {
	// Type is none (the default), bearer, basic, or header.
	Type string `yaml:"type"`
	// TokenEnv names the variable holding a bearer token.
	TokenEnv string `yaml:"token_env"`
	// Username and PasswordEnv are basic authentication's.
	Username    string `yaml:"username"`
	PasswordEnv string `yaml:"password_env"`
	// Header and ValueEnv send a header whose value is a secret.
	Header   string `yaml:"header"`
	ValueEnv string `yaml:"value_env"`
}

// DeclarativePushConfig maps what an external system pushes onto a window, by
// JSON path. ID, Subject, Targets and WindowEnd are required.
type DeclarativePushConfig struct {
	ID                string `yaml:"id"`
	Reference         string `yaml:"reference"`
	Subject           string `yaml:"subject"`
	Targets           string `yaml:"targets"`
	Scope             string `yaml:"scope"`
	WindowStart       string `yaml:"window_start"`
	WindowEnd         string `yaml:"window_end"`
	IssuedAt          string `yaml:"issued_at"`
	AdditionalContext string `yaml:"additional_context"`
}

func (a *AccessContextConfig) applyDefaults() {
	if a.ProbeBudget == 0 {
		a.ProbeBudget = DefaultProbeBudget
	}
	if a.MaxWindow == 0 {
		a.MaxWindow = DefaultExternalMaxWindow
	}
	if a.ClockSkew == 0 {
		a.ClockSkew = DefaultClockSkew
	}
	if a.MaxAssertionAge == 0 {
		a.MaxAssertionAge = DefaultMaxAssertionAge
	}
	if a.Unanswered == "" {
		a.Unanswered = DefaultUnanswered
	}
	if a.MaxProbeTTL == 0 {
		a.MaxProbeTTL = DefaultMaxProbeTTL
	}
	if a.MaxProbesInFlight == 0 {
		a.MaxProbesInFlight = DefaultMaxProbesInFlight
	}
	if a.PushRate == 0 {
		a.PushRate = DefaultPushRate
	}
	if a.PushBurst == 0 {
		a.PushBurst = DefaultPushBurst
	}
	if a.MaxPushBytes == 0 {
		a.MaxPushBytes = DefaultMaxPushBytes
	}
	for i := range a.Providers {
		p := a.Providers[i].Probe
		if p == nil {
			continue
		}
		if p.Timeout == 0 {
			p.Timeout = a.ProbeBudget
		}
		if p.TTL == 0 {
			p.TTL = DefaultDeclarativeTTL
		}
		if len(p.AbsentStatus) == 0 {
			p.AbsentStatus = []int{404}
		}
	}
}

// validate reports the first access-context setting that cannot be acted on.
// The checks that need the declarative provider's own machinery — the URL's
// shape, the paths, the templates, the secrets being present — run when the
// provider is built at start-up, and name the provider and key the same way.
func (a AccessContextConfig) validate(decision DecisionConfig, grants GrantsConfig) error {
	field := func(name string) string { return "access_context." + name }
	switch {
	case a.ProbeBudget <= 0:
		return &FieldError{Field: field("probe_budget"), Msg: "must be a positive duration"}
	case a.ProbeBudget > decision.Budget/2:
		return &FieldError{Field: field("probe_budget"), Msg: fmt.Sprintf(
			"must be at most half of decision.budget (%s): the other half is the decision's own, and a probe that could consume all of it makes every authorize call hostage to a third party", decision.Budget)}
	case a.MaxWindow <= 0:
		return &FieldError{Field: field("max_window"), Msg: "must be a positive duration"}
	case a.MaxWindow > grants.MaxDuration:
		return &FieldError{Field: field("max_window"), Msg: fmt.Sprintf(
			"must not exceed grants.max_duration (%s): an external window is a grant", grants.MaxDuration)}
	case a.ClockSkew <= 0:
		return &FieldError{Field: field("clock_skew"), Msg: "must be a positive duration"}
	case a.MaxAssertionAge <= 0:
		return &FieldError{Field: field("max_assertion_age"), Msg: "must be a positive duration"}
	case !slices.Contains(unansweredSettings, a.Unanswered):
		return &FieldError{Field: field("unanswered"), Msg: fmt.Sprintf(
			"%q is not one of %s", a.Unanswered, strings.Join(unansweredSettings, ", "))}
	case a.MaxProbeTTL <= 0:
		return &FieldError{Field: field("max_probe_ttl"), Msg: "must be a positive duration"}
	case a.MaxProbesInFlight <= 0:
		return &FieldError{Field: field("max_probes_in_flight"), Msg: "must be positive"}
	case a.PushRate <= 0:
		return &FieldError{Field: field("push_rate"), Msg: "must be positive"}
	case a.PushBurst <= 0:
		return &FieldError{Field: field("push_burst"), Msg: "must be positive"}
	case a.MaxPushBytes <= 0 || a.MaxPushBytes > 1<<20:
		return &FieldError{Field: field("max_push_bytes"), Msg: "must be between 1 and 1048576"}
	}
	for i, c := range a.Egress.AllowCIDRs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(c)); err != nil {
			return &FieldError{Field: fmt.Sprintf("access_context.egress.allow_cidrs[%d]", i), Msg: "is not a CIDR prefix"}
		}
	}

	seen := map[string]bool{}
	for i, p := range a.Providers {
		at := func(name string) string { return fmt.Sprintf("access_context.providers[%d].%s", i, name) }
		switch {
		case !providerName.MatchString(p.Name):
			return &FieldError{Field: at("name"), Msg: "must be lower-case letters, digits and hyphens, at most 63 characters"}
		case seen[p.Name]:
			return &FieldError{Field: at("name"), Msg: fmt.Sprintf("%q is configured twice", p.Name)}
		case p.Probe == nil && p.Push == nil:
			return &FieldError{Field: at("probe"), Msg: "a provider probes, takes pushes, or both"}
		}
		seen[p.Name] = true
		if pr := p.Probe; pr != nil {
			switch {
			case strings.TrimSpace(pr.URL) == "":
				return &FieldError{Field: at("probe.url"), Msg: "is required"}
			case pr.Method != "" && pr.Method != "GET" && pr.Method != "POST":
				return &FieldError{Field: at("probe.method"), Msg: "must be GET or POST"}
			case pr.Body != "" && pr.Method != "POST":
				return &FieldError{Field: at("probe.body"), Msg: "is sent only with method POST"}
			case len(pr.Confirm) == 0:
				return &FieldError{Field: at("probe.confirm"), Msg: "needs at least one assertion: an answer nothing is checked against confirms anything"}
			case pr.Timeout < 0 || pr.Timeout > a.ProbeBudget:
				return &FieldError{Field: at("probe.timeout"), Msg: fmt.Sprintf("must be at most access_context.probe_budget (%s)", a.ProbeBudget)}
			case pr.TTL < 0:
				return &FieldError{Field: at("probe.ttl"), Msg: "must not be negative"}
			}
			switch pr.Auth.Type {
			case "", "none", "bearer", "basic", "header":
			default:
				return &FieldError{Field: at("probe.auth.type"), Msg: "must be none, bearer, basic or header"}
			}
			for j, c := range pr.Confirm {
				if strings.TrimSpace(c.Path) == "" || c.predicates() != 1 {
					return &FieldError{Field: at(fmt.Sprintf("probe.confirm[%d]", j)),
						Msg: "names a path and exactly one of equals, not_equals, one_of, exists, contains"}
				}
			}
		}
		if pu := p.Push; pu != nil {
			for name, v := range map[string]string{
				"id": pu.ID, "subject": pu.Subject, "targets": pu.Targets, "window_end": pu.WindowEnd,
			} {
				if strings.TrimSpace(v) == "" {
					return &FieldError{Field: at("push." + name), Msg: "is required"}
				}
			}
		}
	}
	return nil
}

func (c DeclarativeAssertion) predicates() int {
	n := 0
	for _, set := range []bool{c.Equals != nil, c.NotEquals != nil, len(c.OneOf) > 0, c.Exists != nil, c.Contains != nil} {
		if set {
			n++
		}
	}
	return n
}
