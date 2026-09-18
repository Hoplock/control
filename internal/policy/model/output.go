// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The output vocabulary: what an allow rule emits, and what an evaluation
// produces from it (PLAN §5.2).
//
// It is exactly the proxy's enforcement surface and nothing wider. Every type
// here has a wire shape on the other side of the contract, and
// contract_agreement_test.go proves the enums agree member for member — but
// nothing in this package imports the wire types, because the compiler is the
// boundary M3 keeps open.
//
// Route is what an author writes; Snapshot is what an evaluation yields. They
// share every field but one: a route authors a session's *maximum duration*
// and a snapshot carries the absolute instant the session ends. Time is an
// input to evaluation, so the instant is computable and total — and an instant
// is what a chained route needs, because a duration re-anchors on every hop and
// silently multiplies the window.

// Route is the whole-connection snapshot an allow rule emits, as authored.
type Route struct {
	// Intent is whether the path may traverse hops (PLAN §5.2). Which hop
	// is next is computed from the fleet graph, not authored here.
	Intent RouteIntent `yaml:"intent"`
	// Target overrides the requested hostname. Empty means the target the
	// proxy asked about.
	Target string `yaml:"target,omitempty"`
	// Port overrides the requested port. Zero means the one asked about.
	Port int32 `yaml:"port,omitempty"`

	// Channels is the channel-type allow-list. The key is required and the
	// pointer is how absence is told from `channels: []`, which denies
	// every channel and is a policy an author may well mean.
	Channels *[]ChannelType `yaml:"channels"`
	// Requests is the in-channel request allow-list. Absent means the axis
	// is not policed; present means an allow-list, and an empty one denies
	// every request.
	Requests *RequestPermissions `yaml:"requests,omitempty"`
	// Forwards is the permitted forwarding destinations. Absent means no
	// forwarding is described, which is a compile error on a route that
	// permits a forwarding channel.
	Forwards *ForwardPermissions `yaml:"forwards,omitempty"`
	// GlobalRequests is the connection-level global request allow-list.
	// Absent means they are relayed unpoliced.
	GlobalRequests *GlobalRequestPermissions `yaml:"global_requests,omitempty"`

	// Filter is the command filter policy: a rule list or a restricted-exec
	// allow-list, never both (proxy D12).
	Filter FilterPolicy `yaml:"filter"`

	// Credentials is the ordered target-credential ladder (proxy D14). Nil
	// leaves the proxy its locally configured method; a declared but empty
	// ladder is a compile error, because an empty ladder on the wire is a
	// denial written as a list.
	Credentials []CredentialEntry `yaml:"credentials,omitempty"`
	// AlgorithmProfile is the per-route algorithm profile, for targets that
	// speak something x/crypto does not enable by default.
	AlgorithmProfile AlgorithmProfile `yaml:"algorithm_profile,omitempty"`

	// Enforcement is where this route's claim is actually enforced, on each
	// of two independent axes. It is a property of the route, never of a
	// ladder entry: one policy stating two guarantees would leave the audit
	// record unable to say which was in force.
	Enforcement Enforcement `yaml:"enforcement,omitempty"`

	// MaxSessionDuration bounds the session's life. Evaluation turns it
	// into Snapshot.SessionDeadline, an absolute instant.
	MaxSessionDuration Duration `yaml:"max_session_duration,omitempty"`
	// RequireSessionCapture makes the route run only if the session is
	// recorded. The pointer distinguishes "said nothing" from an explicit
	// `false`, which is what makes the contradiction with a record-session
	// obligation detectable.
	RequireSessionCapture *bool `yaml:"require_session_capture,omitempty"`
	// Concurrency caps live sessions per subject and per target. Only the
	// proxy can count them; exceeding one is a policy denial, not an outage.
	Concurrency Concurrency `yaml:"concurrency,omitempty"`
}

// RequestPermissions is which in-channel requests a session may make.
type RequestPermissions struct {
	// Types is the permitted request types.
	Types []RequestType `yaml:"types,omitempty"`
	// Subsystems are permitted by name, so `sftp` is deniable on its own.
	// The pointer tells "no subsystems are permitted" (absent) from "the
	// author opened the subsystem axis and named nothing", which is the
	// unconstrained axis the compiler refuses.
	Subsystems *[]string `yaml:"subsystems,omitempty"`
}

// Permits reports whether this allow-list permits a request type.
func (p *RequestPermissions) Permits(t RequestType) bool {
	if p == nil {
		// An absent allow-list is not policed, so everything passes.
		return true
	}
	for _, have := range p.Types {
		if have == t {
			return true
		}
	}
	return false
}

// ForwardPermissions is which forwarding destinations a session may reach.
type ForwardPermissions struct {
	// DirectTCPIP constrains local forwards.
	DirectTCPIP []Destination `yaml:"direct_tcpip,omitempty"`
	// ForwardedTCPIP constrains connections arriving on a remote forward.
	ForwardedTCPIP []Destination `yaml:"forwarded_tcpip,omitempty"`
}

// GlobalRequestPermissions is which connection-level global requests are
// relayed.
//
// Types stays a plain named string rather than a closed enum, and the reason is
// the contract's: a global request is matched exactly against the name on the
// wire, and that name space is open — `tcpip-forward` sits beside
// `streamlocal-forward@openssh.com` and beside whatever a vendor ships next.
// Closing it here would make an estate's own request unauthorable without a
// release of this server, which is the same mistake as enumerating device
// fields. What is checked is the shape.
type GlobalRequestPermissions struct {
	Types []GlobalRequestType `yaml:"types,omitempty"`
}

// GlobalRequestType is a connection-level global request name.
type GlobalRequestType string

// The global requests SSH and OpenSSH define today. They are named so that a
// bundle can be grepped for them, not to close the set.
const (
	GlobalRequestTCPIPForward             GlobalRequestType = "tcpip-forward"
	GlobalRequestCancelTCPIPForward       GlobalRequestType = "cancel-tcpip-forward"
	GlobalRequestStreamLocalForward       GlobalRequestType = "streamlocal-forward@openssh.com"
	GlobalRequestCancelStreamLocalForward GlobalRequestType = "cancel-streamlocal-forward@openssh.com"
)

// Destination is one permitted forwarding destination: a host pattern (exact,
// single leading wildcard, or CIDR prefix) and at most one port constraint.
type Destination struct {
	Host      string     `yaml:"host"`
	Port      int32      `yaml:"port,omitempty"`
	PortRange *PortRange `yaml:"port_range,omitempty"`
}

// String renders a destination the way a rejection names it.
func (d Destination) String() string {
	switch {
	case d.PortRange != nil:
		return fmt.Sprintf("%s:%d-%d", d.Host, d.PortRange.From, d.PortRange.To)
	case d.Port != 0:
		return fmt.Sprintf("%s:%d", d.Host, d.Port)
	default:
		return d.Host
	}
}

// PortRange is an inclusive port range.
type PortRange struct {
	From int32 `yaml:"from"`
	To   int32 `yaml:"to"`
}

// FilterPolicy is the command filter policy for a connection. The two exec
// tiers are alternatives, never layers (proxy D12).
type FilterPolicy struct {
	// Mode is what happens to a command no rule matched. It is required.
	Mode FilterMode `yaml:"mode"`
	// Rules is the guardrail tier: an ordered rule list.
	Rules []FilterRule `yaml:"rules,omitempty"`
	// ExecMode names which tier decides an exec request.
	ExecMode ExecMode `yaml:"exec_mode,omitempty"`
	// RestrictedExec is the boundary tier: a default-deny allow-list of
	// executables and the shape of their permitted arguments.
	RestrictedExec *RestrictedExec `yaml:"restricted_exec,omitempty"`
}

// FilterRule is one command pattern and what happens when it matches.
type FilterRule struct {
	Match   string       `yaml:"match"`
	Action  FilterAction `yaml:"action"`
	Message string       `yaml:"message,omitempty"`
}

// RestrictedExec is the restricted tier's allow-list.
type RestrictedExec struct {
	Commands []RestrictedCommand `yaml:"commands"`
}

// RestrictedCommand is one permitted executable and the shape of its arguments.
type RestrictedCommand struct {
	Executable string         `yaml:"executable"`
	Form       CommandForm    `yaml:"form"`
	Argv       []string       `yaml:"argv,omitempty"`
	Args       []ArgumentSpec `yaml:"args,omitempty"`
}

// ArgumentSpec is the permitted shape of one argument position.
type ArgumentSpec struct {
	Kind     ArgumentKind `yaml:"kind"`
	Value    string       `yaml:"value,omitempty"`
	Values   []string     `yaml:"values,omitempty"`
	Optional bool         `yaml:"optional,omitempty"`
}

// CredentialEntry is one rung of the target-credential ladder: a method, the
// account it logs in as, and that method's parameters.
//
// The documented parameters are fields rather than an open map, because the
// contract documents exactly these and a typo in an open map is a route that
// fails at connect time in front of a user. The one namespace that stays open
// is DeviceFields, and it is open for the opposite reason: the contract refuses
// to enumerate it because customer-written drivers are first-class, so a closed
// list here would make an estate's own driver unauthorable without a release of
// this server.
type CredentialEntry struct {
	// Method is which credential method this rung uses.
	Method CredentialMethod `yaml:"method"`
	// Username names the account to log in as. Required on every method the
	// contract defines, and never derived from the identity's `login`.
	Username Username `yaml:"username"`

	// KeyType is `ephemeral-user`'s key type; empty leaves the proxy's default.
	KeyType string `yaml:"key_type,omitempty"`
	// CredentialRef is `brokered-key`'s opaque handle for local material on
	// the proxy. No credential material travels on this API.
	CredentialRef string `yaml:"credential_ref,omitempty"`
	// Platform is `ephemeral-account`'s driver. Required, and never
	// inferred: guessing wrong runs configuration commands against the
	// wrong parser.
	Platform string `yaml:"platform,omitempty"`
	// CredentialKind is `ephemeral-account`'s credential kind. Required.
	CredentialKind CredentialKind `yaml:"credential_kind,omitempty"`
	// ExpiryPosture is `ephemeral-account`'s expiry posture. Required.
	ExpiryPosture ExpiryPosture `yaml:"expiry_posture,omitempty"`
	// LifetimeSeconds bounds the provisioned credential's life. Required on
	// `ephemeral-account` unless the posture is `accepted-risk`.
	LifetimeSeconds int32 `yaml:"lifetime_seconds,omitempty"`

	// DeviceFields is the open `device_field.<name>` namespace that sits
	// beside the named `ephemeral-account` parameters. It is policy
	// metadata and never credential material: nothing here brokers one as a
	// secret, and nothing reads one back as an authorisation input. This
	// engine is entirely opaque to their meaning — it checks the shape, and
	// meaning is the driver's.
	DeviceFields map[string]string `yaml:"device_fields,omitempty"`
}

// Username is the account a ladder entry logs in as.
//
// It is a closed source plus an optional literal rather than a string or a
// template, so that the one derivation the contract closed off — the
// identity's client-typed `login` — is not expressible. There is no syntax in
// which to write it.
type Username struct {
	Source UsernameSource
	Value  string
}

// UnmarshalYAML accepts `username: svc-admin` (a literal) and
// `username: {from: subject}` (a derivation), and nothing else.
func (u *Username) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		u.Source, u.Value = UsernameLiteral, s
		return nil
	}
	var m struct {
		From UsernameSource `yaml:"from"`
	}
	if err := n.Decode(&m); err != nil {
		return err
	}
	if !oneOf(m.From, usernameSources) || m.From == UsernameLiteral {
		return nodeErr(n, "username source %q is not one of %s (a literal is written as a plain string)",
			string(m.From), joinEnum([]UsernameSource{UsernameSubject, UsernameSubjectLocalPart}))
	}
	u.Source, u.Value = m.From, ""
	return nil
}

// Resolve renders the username for one subject. It is total: a source with no
// value to render from yields the empty string, which the compiler has already
// refused, so no caller ever fills one in.
func (u Username) Resolve(subject string) string {
	switch u.Source {
	case UsernameLiteral:
		return u.Value
	case UsernameSubject:
		return subject
	case UsernameSubjectLocalPart:
		if i := strings.IndexByte(subject, '@'); i >= 0 {
			return subject[:i]
		}
		return subject
	case UsernameUnset:
		return ""
	}
	return ""
}

// Enforcement is where a route's claim is actually enforced, on two independent
// axes. The absent value on either axis is proxy-side enforcement only, which
// is exactly what a rule that says nothing about enforcement emits — the engine
// never synthesises a weaker rung as a fallback.
type Enforcement struct {
	Execution ExecutionRung `yaml:"execution,omitempty"`
	Reach     ReachRung     `yaml:"reach,omitempty"`
	// PlatformRole is the authorization `platform-authorized` claims.
	PlatformRole string `yaml:"platform_role,omitempty"`
	// PermittedDestinations is what `account-egress-restricted` is made of.
	PermittedDestinations []Destination `yaml:"permitted_destinations,omitempty"`
	// Attestation names who asserts an attested rung. This system does not
	// verify the claim; it makes it attributable.
	Attestation *Attestation `yaml:"attestation,omitempty"`
}

// Stated reports whether this route stands anywhere other than proxy-side
// enforcement. Where it does not, 0008 emits no enforcement object at all: an
// emitted default is noise in an audit record.
func (e Enforcement) Stated() bool {
	return e.Execution != ExecutionRungUnset || e.Reach != ReachRungUnset ||
		e.PlatformRole != "" || len(e.PermittedDestinations) > 0 || e.Attestation != nil
}

// Attestation is the source of an attested enforcement rung.
type Attestation struct {
	AssertedBy string `yaml:"asserted_by"`
	Reference  string `yaml:"reference"`
	AssertedAt string `yaml:"asserted_at,omitempty"`
}

// Concurrency caps live sessions. Zero on either scope is uncapped.
type Concurrency struct {
	MaxSessionsPerSubject int32 `yaml:"max_sessions_per_subject,omitempty"`
	MaxSessionsPerTarget  int32 `yaml:"max_sessions_per_target,omitempty"`
}

// CacheHint is this server authorising the proxy to reuse a decision (PLAN
// §5.4). It is authored per rule, not globally: the lifetime is policy, so
// omitting it for anything sensitive is how every connection stays re-decided.
type CacheHint struct {
	// Key is the components the sharing scope is derived from. It must
	// include CacheKeySubject: a key shared across identities serves one
	// user another user's policy.
	Key []CacheKeyComponent `yaml:"key"`
	// TTLSeconds is the lifetime, which is this server's to set.
	TTLSeconds int32 `yaml:"ttl_seconds"`
}

// Obligation is something a matching rule requires in addition to its route.
type Obligation struct {
	Kind ObligationKind `yaml:"kind"`
	// ApproverGroups names who may approve, on a require-approval
	// obligation. The names are checked against the bundle's declared
	// groups like any other group reference.
	ApproverGroups []string `yaml:"approver_groups,omitempty"`
	// Message is shown to the user when the obligation is raised.
	Message string `yaml:"message,omitempty"`
}

// GrantContext is why access was granted, carried from the grant that supplied
// it (M10, M16) rather than authored.
//
// It is recorded, never read: the proxy copies all of it into every log record
// for the session and never parses it, matches on it, or decides from it — and
// neither does anything here. The decision was made before the snapshot was
// written.
type GrantContext struct {
	// GrantID is the grant this came from.
	GrantID string
	// Origin is how the grant came to exist.
	Origin GrantOrigin
	// System and Reference name the external system and its ticket, scan,
	// or incident. "Explain why" that cannot name the ticket is not an
	// explanation.
	System    string
	Reference string
	// WindowStart and WindowEnd are what the external system asserted. They
	// are recorded, not enforced: the bound the proxy enforces is the
	// session deadline, which this server set having weighed the window.
	WindowStart time.Time
	WindowEnd   time.Time
	// AdditionalContext is a JSON string or a JSON object, and nothing else.
	AdditionalContext *AdditionalContext
}

// AdditionalContext is `grant_context.additional_context`: a string or an
// object. A number, a list or a boolean is a contract violation rather than
// something to coerce, because this is stored verbatim for an auditor.
type AdditionalContext struct {
	// Text is set when the value is a string.
	Text string
	// Fields is set when the value is an object.
	Fields map[string]any
}

// Snapshot is what an evaluation produces: the whole-connection policy, with
// the route's authored bounds resolved against the evaluation's time input.
type Snapshot struct {
	// Rule is the id of the rule that produced this snapshot. It is on the
	// snapshot as well as the explanation because a decision record read
	// back years later is one object, not two.
	Rule string

	Intent RouteIntent
	Target string
	Port   int32

	// Channels is the channel-type allow-list. A non-nil empty slice denies
	// every channel; it is never nil on a snapshot, because the contract
	// requires the field.
	Channels       []ChannelType
	Requests       *RequestPermissions
	Forwards       *ForwardPermissions
	GlobalRequests *GlobalRequestPermissions

	Filter FilterPolicy

	Credentials      []CredentialEntry
	AlgorithmProfile AlgorithmProfile

	Enforcement Enforcement

	// SessionDeadline is the absolute instant the session ends. The zero
	// time means the route sets no deadline.
	SessionDeadline time.Time
	// RequireSessionCapture is resolved: a record-session obligation makes
	// it true whatever the route said, and the compiler has already refused
	// the rule that said both.
	RequireSessionCapture bool
	Concurrency           Concurrency

	// GrantContext is carried from the grant that supplied the access, when
	// one did.
	GrantContext *GrantContext

	Cache       *CacheHint
	Obligations []Obligation
}

// Duration is a time.Duration authored as a YAML string (`8h`, `45m`).
type Duration time.Duration

// UnmarshalYAML decodes a Duration. A malformed value is reported with the text
// the author wrote, not with Go's parser error, because the author is the
// reader.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return nodeErr(n, "%q is not a duration; use a form such as `8h` or `45m`", s)
	}
	*d = Duration(parsed)
	return nil
}

// Duration returns the value as a time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// String renders the duration the way it was meant to be written.
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalJSON writes the shape that was read: a string or an object, and
// nothing else. A number, a list or a boolean never round-trips through this
// type, which is what keeps a decision record's copy verbatim.
func (a AdditionalContext) MarshalJSON() ([]byte, error) {
	if a.Fields != nil {
		return json.Marshal(a.Fields)
	}
	return json.Marshal(a.Text)
}

// UnmarshalJSON accepts exactly the two shapes the contract names.
func (a *AdditionalContext) UnmarshalJSON(b []byte) error {
	// `null` has to be refused explicitly: encoding/json treats it as a
	// no-op for every destination type, so it would otherwise arrive here as
	// an empty string that nobody wrote.
	if string(bytes.TrimSpace(b)) == "null" {
		return fmt.Errorf("additional_context is a JSON string or object, and nothing else")
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		a.Text, a.Fields = s, nil
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return fmt.Errorf("additional_context is a JSON string or object, and nothing else")
	}
	a.Text, a.Fields = "", m
	return nil
}
