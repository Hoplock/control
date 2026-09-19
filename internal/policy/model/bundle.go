// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the bundle document format this server understands. It is
// the *document's* version and has nothing to do with the contract's
// `policy_version`, which governs the wire vocabulary a proxy declares: the two
// move independently and neither is derived from the other.
const SchemaVersion = 1

// Bundle is a policy bundle as authored: a declarative YAML document over a
// closed vocabulary (M3).
//
// The bundle names its tenant, because tenancy selects which compiled program
// is served (M18) — one bundle, one program, one tenant.
type Bundle struct {
	// SchemaVersion must equal SchemaVersion. An unknown version is
	// refused rather than best-guessed: a document this server does not
	// understand is one whose omissions it would be inventing.
	SchemaVersion int `yaml:"schema_version"`
	// Tenant owns this bundle.
	Tenant Tenant `yaml:"tenant"`
	// Description is free text for whoever reads the bundle next.
	Description string `yaml:"description,omitempty"`
	// Timezone is the IANA location that a rule's `days` and `time_of_day`
	// are read in. Empty means UTC. It is resolved at parse time, so
	// evaluation stays pure.
	Timezone string `yaml:"timezone,omitempty"`

	// Labels is the declared label vocabulary: which label keys exist and
	// which values each may take. A rule matching a label outside it is a
	// compile error, which is what turns a typo into a message instead of a
	// rule that silently never fires.
	Labels map[string][]string `yaml:"labels,omitempty"`
	// Groups is the declared group vocabulary, for the same reason.
	Groups []string `yaml:"groups,omitempty"`

	// Rules are evaluated in order, first match wins. The default-deny at
	// the end is not authored and cannot be removed.
	Rules []Rule `yaml:"rules"`

	// location is Timezone, resolved.
	location *time.Location
	// digest is the SHA-256 of the source bytes, so an explanation can name
	// the exact document it came from.
	digest string
	// source is the bytes as parsed.
	source []byte
}

// Location is the location a rule's day and time-of-day terms are read in.
// It is never nil on a parsed bundle.
func (b *Bundle) Location() *time.Location {
	if b.location == nil {
		return time.UTC
	}
	return b.location
}

// Digest is the SHA-256 of the bundle source, hex encoded.
func (b *Bundle) Digest() string { return b.digest }

// Source is the bytes the bundle was parsed from.
func (b *Bundle) Source() []byte { return b.source }

// Rule is one ordered rule: what it matches, and what it does when it matches.
type Rule struct {
	// ID identifies the rule in every explanation and audit record, so it
	// is required and unique.
	ID string `yaml:"id"`
	// Description is free text.
	Description string `yaml:"description,omitempty"`
	// Match is what the rule matches on. An empty match matches everything,
	// which is how a catch-all is written — and why the compiler reports
	// every later rule as unreachable.
	Match Match `yaml:"match,omitempty"`
	// Effect is allow or deny.
	Effect Effect `yaml:"effect"`
	// Reason is what an operator reads when they resolve a denied
	// decision's id. Required on a deny.
	Reason string `yaml:"reason,omitempty"`
	// Route is the snapshot an allow serves. Required on an allow,
	// forbidden on a deny.
	Route *Route `yaml:"route,omitempty"`
	// Obligations are what a permitted session must also do.
	Obligations []Obligation `yaml:"obligations,omitempty"`
	// Cache is this rule's cache hint, authored per rule rather than
	// globally (PLAN §5.4): omit it for anything sensitive and every
	// connection is re-decided.
	Cache *CacheHint `yaml:"cache,omitempty"`

	// line is the 1-based line the rule starts on, for rejections.
	line int
}

// Line is the 1-based line the rule starts on in the authored source, or 0 for
// a rule that was not parsed from one.
func (r Rule) Line() int { return r.line }

// Match is the closed set of input axes a rule may constrain (PLAN §5.1). A nil
// axis is unconstrained; a constrained axis must be satisfied. All axes are
// ANDed, and within an axis a list of values is ORed.
type Match struct {
	Subject *SubjectMatch `yaml:"subject,omitempty"`
	Device  *DeviceMatch  `yaml:"device,omitempty"`
	Context *ContextMatch `yaml:"context,omitempty"`
	Target  *TargetMatch  `yaml:"target,omitempty"`
	Grant   *GrantMatch   `yaml:"grant,omitempty"`
}

// Unconstrained reports whether this match accepts every input — the catch-all
// shape.
func (m Match) Unconstrained() bool {
	return m.Subject == nil && m.Device == nil && m.Context == nil &&
		m.Target == nil && m.Grant == nil
}

// SubjectMatch constrains the authenticated principal.
type SubjectMatch struct {
	IDs     []string `yaml:"ids,omitempty"`
	Sources []string `yaml:"sources,omitempty"`
	Groups  []string `yaml:"groups,omitempty"`
	// Claims is key to permitted values. All listed keys must match.
	Claims      map[string][]string `yaml:"claims,omitempty"`
	AuthMethods []AuthMethod        `yaml:"auth_methods,omitempty"`
	// MFA constrains whether a second factor was used. Nil is
	// unconstrained, which is not the same as `mfa: false`.
	MFA *bool `yaml:"mfa,omitempty"`
}

// DeviceMatch constrains endpoint posture, which may be absent entirely.
type DeviceMatch struct {
	// Required demands that the endpoint supplied posture at all. Without
	// it, a posture constraint on an endpoint that supplied none does not
	// match — absence is never read as compliance.
	Required bool `yaml:"required,omitempty"`
	// Posture is attribute to permitted values.
	Posture map[string][]string `yaml:"posture,omitempty"`
}

// ContextMatch constrains when and where the connection is made from.
type ContextMatch struct {
	// Days are the days of the week the rule applies on, read in the
	// bundle's location.
	Days []Day `yaml:"days,omitempty"`
	// TimeOfDay is the window the rule applies in, read in the bundle's
	// location. A window whose end is before its start wraps midnight.
	TimeOfDay *TimeWindow `yaml:"time_of_day,omitempty"`
	// SourceCIDRs are the client networks the rule applies to.
	SourceCIDRs []string `yaml:"source_cidrs,omitempty"`
	// ProxyIDs are the proxies the rule applies to — the proxy *asking*,
	// which on a chained session is an inner hop rather than the user's
	// entry proxy.
	ProxyIDs []string `yaml:"proxy_ids,omitempty"`
}

// TargetMatch constrains the host being reached.
type TargetMatch struct {
	// Hostnames are exact names or a single leading wildcard.
	Hostnames []string `yaml:"hostnames,omitempty"`
	// Labels is key to permitted values, checked against the bundle's
	// declared vocabulary.
	Labels map[string][]string `yaml:"labels,omitempty"`
	Zones  []string            `yaml:"zones,omitempty"`
}

// GrantMatch constrains the live just-in-time grants for this subject (M10).
type GrantMatch struct {
	// Required demands that some live grant covers this target. It is what
	// "this route exists only while somebody has been granted it" is
	// written as.
	Required bool `yaml:"required,omitempty"`
	// Scopes are the grant scope names that satisfy the rule.
	Scopes []string `yaml:"scopes,omitempty"`
	// Origins are the grant origins that satisfy the rule — an
	// administrator's grant and an external system's confirmed window are
	// the same object, and a policy may still care which.
	Origins []GrantOrigin `yaml:"origins,omitempty"`
}

// Constrained reports whether this grant match demands a grant at all.
func (g *GrantMatch) Constrained() bool {
	if g == nil {
		return false
	}
	return g.Required || len(g.Scopes) > 0 || len(g.Origins) > 0
}

// TimeWindow is a time-of-day window, authored as `HH:MM-HH:MM` in 24-hour
// form. A window whose end is not after its start wraps midnight, which is how
// an out-of-hours rule is written.
type TimeWindow struct {
	// FromMinutes and ToMinutes are minutes since midnight, [0, 1440).
	FromMinutes int
	ToMinutes   int
}

// UnmarshalYAML decodes a TimeWindow.
func (w *TimeWindow) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	parsed, ok := parseTimeWindow(s)
	if !ok {
		return nodeErr(n, "%q is not a time window; use `HH:MM-HH:MM` in 24-hour form", s)
	}
	*w = parsed
	return nil
}

// Contains reports whether a minute-of-day falls in the window, wrapping
// midnight where the window does.
func (w TimeWindow) Contains(minute int) bool {
	if w.FromMinutes <= w.ToMinutes {
		return minute >= w.FromMinutes && minute <= w.ToMinutes
	}
	return minute >= w.FromMinutes || minute <= w.ToMinutes
}

// String renders the window the way it was authored.
func (w TimeWindow) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d",
		w.FromMinutes/60, w.FromMinutes%60, w.ToMinutes/60, w.ToMinutes%60)
}
