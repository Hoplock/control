// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import "gopkg.in/yaml.v3"

// Every variant axis in the policy vocabulary, one named enum each (PLAN M13,
// M3).
//
// These are defined types with their members declared as constants in one
// block beside the type, and that shape is a requirement rather than taste.
// Go has no sum types and no exhaustive matching, so the only thing standing
// between "the vocabulary is closed" and "somebody added an obligation kind
// and three switches silently kept their old behaviour" is the `exhaustive`
// linter — and it only sees named enums. A bare `string` or an open interface
// is invisible to it, so neither appears here.
//
// `default:` does not excuse a missing case either (`.golangci.yml` sets
// default-signifies-exhaustive: false). Where a switch is genuinely
// open-ended, it carries `//exhaustive:ignore` with a reason on the line
// above. scripts/exhaustive-guard.sh proves the linter actually rejects an
// unhandled member, because a guard that is enabled but silent is worse than
// none.
//
// This package deliberately does not import internal/contract: the compiler is
// M3's escape hatch, and an engine that imports the wire types is one that
// cannot be replaced without them. Agreement with the contract is proved by a
// test instead — see contract_agreement_test.go, which compares every shared
// axis in both directions.

// Effect is what a rule does when it matches: the decision outcome.
type Effect string

const (
	// EffectAllow serves the rule's route as the connection's snapshot.
	EffectAllow Effect = "allow"
	// EffectDeny refuses the connection and records the rule that did it.
	EffectDeny Effect = "deny"
)

var effects = []Effect{EffectAllow, EffectDeny}

// UnmarshalYAML decodes an Effect, rejecting anything outside the vocabulary.
func (e *Effect) UnmarshalYAML(n *yaml.Node) error { return decodeEnum(n, e, effects, "effect") }

// Basis is why a decision came out the way it did: a rule matched, or nothing
// did and the always-present default-deny fired (PLAN §5.3).
type Basis string

const (
	// BasisRule means an authored rule matched and decided.
	BasisRule Basis = "rule"
	// BasisDefaultDeny means no rule matched. It is always recorded as the
	// reason when it fires, never left implicit.
	BasisDefaultDeny Basis = "default-deny"
)

// MatchAxis names the input axis a matched term came from, so an explanation
// can say "the target's labels", not just "labels".
type MatchAxis string

const (
	// AxisSubject is the authenticated principal: id, source, groups,
	// claims, authentication method, MFA.
	AxisSubject MatchAxis = "subject"
	// AxisDevice is endpoint posture, which may be absent entirely.
	AxisDevice MatchAxis = "device"
	// AxisContext is time, day, source network, and the proxy asking.
	AxisContext MatchAxis = "context"
	// AxisTarget is the hostname, labels, and zone being reached.
	AxisTarget MatchAxis = "target"
	// AxisGrant is the live just-in-time grants for this subject (M10).
	AxisGrant MatchAxis = "grant"
)

// RouteIntent is what a rule says about the path to the target: whether the
// session must reach it directly or may traverse hops. Which hop is actually
// next is computed from the fleet graph (M6, phase 0006) and is not a policy
// statement, so this enum stops short of `direct`/`nexthop` on the wire.
type RouteIntent string

const (
	// RouteIntentDirect permits the target only where the enforcing proxy
	// reaches it without another hop.
	RouteIntentDirect RouteIntent = "direct"
	// RouteIntentHopsPermitted permits a path that traverses hops.
	RouteIntentHopsPermitted RouteIntent = "hops-permitted"
)

var routeIntents = []RouteIntent{RouteIntentDirect, RouteIntentHopsPermitted}

// UnmarshalYAML decodes a RouteIntent.
func (r *RouteIntent) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, r, routeIntents, "route intent")
}

// ChannelType is an SSH channel a session may open, in either direction — the
// coarsest of the proxy's three policy axes (proxy D5a).
type ChannelType string

const (
	// ChannelSession carries scp, sftp, an interactive shell and a one-shot
	// command alike, which is why permitting it says very little on its own.
	ChannelSession ChannelType = "session"
	// ChannelDirectTCPIP is a local forward. Its whole meaning is the
	// destination in its payload, so permitting it without a destination
	// list is a compile error rather than a wildcard.
	ChannelDirectTCPIP ChannelType = "direct-tcpip"
	// ChannelForwardedTCPIP is a connection arriving on a remote forward.
	ChannelForwardedTCPIP ChannelType = "forwarded-tcpip"
	// ChannelX11 is an X11 forwarding channel.
	ChannelX11 ChannelType = "x11"
	// ChannelAuthAgent is agent forwarding.
	ChannelAuthAgent ChannelType = "auth-agent@openssh.com"
)

var channelTypes = []ChannelType{
	ChannelSession, ChannelDirectTCPIP, ChannelForwardedTCPIP, ChannelX11, ChannelAuthAgent,
}

// UnmarshalYAML decodes a ChannelType.
func (c *ChannelType) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, c, channelTypes, "channel type")
}

// RequestType is an in-channel request (proxy D5a axis 2). `subsystem` is
// deliberately absent: subsystems are permitted by name so that sftp can be
// denied while a shell stays.
type RequestType string

const (
	RequestPTYReq       RequestType = "pty-req"
	RequestShell        RequestType = "shell"
	RequestExec         RequestType = "exec"
	RequestEnv          RequestType = "env"
	RequestX11Req       RequestType = "x11-req"
	RequestAuthAgentReq RequestType = "auth-agent-req"
)

var requestTypes = []RequestType{
	RequestPTYReq, RequestShell, RequestExec, RequestEnv, RequestX11Req, RequestAuthAgentReq,
}

// UnmarshalYAML decodes a RequestType.
func (r *RequestType) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, r, requestTypes, "request type")
}

// Interactive reports whether permitting this request gives the session an
// interactive shell, which is what `no-interactive-shell` claims it does not
// have (PLAN §5.2).
func (r RequestType) Interactive() bool {
	switch r {
	case RequestShell, RequestPTYReq:
		return true
	case RequestExec, RequestEnv, RequestX11Req, RequestAuthAgentReq:
		return false
	}
	return false
}

// CredentialMethod is how the proxy authenticates to the target (proxy D6a,
// D14). A route names an ordered ladder of these.
type CredentialMethod string

const (
	// CredentialEphemeralUser creates an OS user on the target for the
	// session and removes it afterwards.
	CredentialEphemeralUser CredentialMethod = "ephemeral-user"
	// CredentialEphemeralAccount creates an account on a device the proxy
	// cannot administer as a host — a firewall, a filer, a hypervisor.
	CredentialEphemeralAccount CredentialMethod = "ephemeral-account"
	// CredentialBrokeredKey hands the proxy a per-target credential for the
	// session's duration only.
	CredentialBrokeredKey CredentialMethod = "brokered-key"
	// CredentialStaticKey uses material the proxy already holds.
	CredentialStaticKey CredentialMethod = "static-key"
)

var credentialMethods = []CredentialMethod{
	CredentialEphemeralUser, CredentialEphemeralAccount,
	CredentialBrokeredKey, CredentialStaticKey,
}

// UnmarshalYAML decodes a CredentialMethod.
func (m *CredentialMethod) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, m, credentialMethods, "credential method")
}

// Provisions reports whether this method has the proxy administer the account
// on the target. Only those two methods can carry an *applied* enforcement
// rung, because only they create the account the rung is rendered onto
// (PLAN §5.2).
func (m CredentialMethod) Provisions() bool {
	switch m {
	case CredentialEphemeralUser, CredentialEphemeralAccount:
		return true
	case CredentialBrokeredKey, CredentialStaticKey:
		return false
	}
	return false
}

// UsernameSource is where a ladder entry's target account name comes from.
//
// The contract requires `params.username` on every method it defines and the
// proxy refuses a route that omits it, so a ladder entry always names one. The
// enum exists so that the one source the contract closed off — the identity's
// client-typed `login` — cannot be named at all: it is not a member, and there
// is no free-form expression language in which to spell it.
type UsernameSource string

const (
	// UsernameUnset is the zero value: no username authored. It is a
	// compile error, never a thing to fill in.
	UsernameUnset UsernameSource = ""
	// UsernameLiteral is a fixed account name written in the bundle.
	UsernameLiteral UsernameSource = "literal"
	// UsernameSubject is the authenticated subject id, which this server
	// established and no client typed.
	UsernameSubject UsernameSource = "subject"
	// UsernameSubjectLocalPart is the subject id up to the first `@`, for
	// estates whose subject ids are email-shaped and whose targets will not
	// take an `@` in a user name.
	UsernameSubjectLocalPart UsernameSource = "subject-local-part"
)

var usernameSources = []UsernameSource{
	UsernameLiteral, UsernameSubject, UsernameSubjectLocalPart,
}

// CredentialKind is the `credential_kind` parameter of an ephemeral-account
// entry. It has no absent-value default: defaulting hands out the weaker of two
// materially different exposures to a policy that never said so.
type CredentialKind string

const (
	CredentialKindUnset     CredentialKind = ""
	CredentialKindPassword  CredentialKind = "password"
	CredentialKindPublicKey CredentialKind = "publickey"
)

var credentialKinds = []CredentialKind{CredentialKindPassword, CredentialKindPublicKey}

// UnmarshalYAML decodes a CredentialKind.
func (k *CredentialKind) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, k, credentialKinds, "credential kind")
}

// ExpiryPosture is the `expiry_posture` parameter of an ephemeral-account
// entry. No absent-value default either: "the risk was accepted" is a sentence
// somebody writes down, never one inferred from an omission.
type ExpiryPosture string

const (
	ExpiryPostureUnset          ExpiryPosture = ""
	ExpiryPostureTargetEnforced ExpiryPosture = "target-enforced"
	ExpiryPostureProxyEnforced  ExpiryPosture = "proxy-enforced"
	ExpiryPostureAcceptedRisk   ExpiryPosture = "accepted-risk"
)

var expiryPostures = []ExpiryPosture{
	ExpiryPostureTargetEnforced, ExpiryPostureProxyEnforced, ExpiryPostureAcceptedRisk,
}

// UnmarshalYAML decodes an ExpiryPosture.
func (p *ExpiryPosture) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, p, expiryPostures, "expiry posture")
}

// AlgorithmProfile is the per-route algorithm profile for targets that speak
// something x/crypto does not enable by default. Unset means the default
// profile, which is the only one that is not a weakening.
type AlgorithmProfile string

const (
	AlgorithmProfileUnset         AlgorithmProfile = ""
	AlgorithmProfileDefault       AlgorithmProfile = "default"
	AlgorithmProfileLegacyRSASHA1 AlgorithmProfile = "legacy-rsa-sha1"
	AlgorithmProfileLegacyDevice  AlgorithmProfile = "legacy-device"
)

var algorithmProfiles = []AlgorithmProfile{
	AlgorithmProfileDefault, AlgorithmProfileLegacyRSASHA1, AlgorithmProfileLegacyDevice,
}

// UnmarshalYAML decodes an AlgorithmProfile.
func (p *AlgorithmProfile) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, p, algorithmProfiles, "algorithm profile")
}

// FilterMode is what happens to a command no filter rule matched. It is
// required on every route: a policy with no defined default is one that can
// fail open by omission.
type FilterMode string

const (
	FilterModeUnset     FilterMode = ""
	FilterModeWhitelist FilterMode = "whitelist"
	FilterModeBlacklist FilterMode = "blacklist"
)

var filterModes = []FilterMode{FilterModeWhitelist, FilterModeBlacklist}

// UnmarshalYAML decodes a FilterMode.
func (m *FilterMode) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, m, filterModes, "filter mode")
}

// ExecMode is which of the two tiers decides an exec request (proxy D12). The
// two are alternatives, never layers, which is why a route carrying both a
// rule list and a restricted-exec allow-list is a compile error.
type ExecMode string

const (
	// ExecModeUnset is the absent value and means ExecModeFiltered.
	ExecModeUnset ExecMode = ""
	// ExecModeFiltered is the guardrail tier: an ordered rule list.
	ExecModeFiltered ExecMode = "filtered"
	// ExecModeRestricted is the boundary tier: a default-deny allow-list of
	// executables and permitted argument shapes.
	ExecModeRestricted ExecMode = "restricted"
)

var execModes = []ExecMode{ExecModeFiltered, ExecModeRestricted}

// UnmarshalYAML decodes an ExecMode.
func (m *ExecMode) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, m, execModes, "exec mode")
}

// FilterAction is what a matched filter rule does.
type FilterAction string

const (
	FilterActionAllowAndLog     FilterAction = "allow_and_log"
	FilterActionBlockCommand    FilterAction = "block_command"
	FilterActionWarnAndContinue FilterAction = "warn_and_continue"
	FilterActionKillSession     FilterAction = "kill_session"
)

var filterActions = []FilterAction{
	FilterActionAllowAndLog, FilterActionBlockCommand,
	FilterActionWarnAndContinue, FilterActionKillSession,
}

// UnmarshalYAML decodes a FilterAction.
func (a *FilterAction) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, a, filterActions, "filter action")
}

// CommandForm is how a restricted-exec command's arguments are specified.
type CommandForm string

const (
	CommandFormUnset CommandForm = ""
	// CommandFormExact names the whole argv, position by position.
	CommandFormExact CommandForm = "exact"
	// CommandFormPositional names the shape of each argument position.
	CommandFormPositional CommandForm = "positional"
)

var commandForms = []CommandForm{CommandFormExact, CommandFormPositional}

// UnmarshalYAML decodes a CommandForm.
func (f *CommandForm) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, f, commandForms, "command form")
}

// ArgumentKind is the permitted shape of one argument position. `any` is named
// rather than smuggled in as an empty prefix, so every unconstrained position
// is greppable.
type ArgumentKind string

const (
	ArgumentKindUnset   ArgumentKind = ""
	ArgumentKindLiteral ArgumentKind = "literal"
	ArgumentKindPrefix  ArgumentKind = "prefix"
	ArgumentKindOneOf   ArgumentKind = "oneof"
	ArgumentKindAny     ArgumentKind = "any"
)

var argumentKinds = []ArgumentKind{
	ArgumentKindLiteral, ArgumentKindPrefix, ArgumentKindOneOf, ArgumentKindAny,
}

// UnmarshalYAML decodes an ArgumentKind.
func (k *ArgumentKind) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, k, argumentKinds, "argument kind")
}

// ExecutionRung is where the limit on what a session may EXECUTE is enforced.
//
// ExecutionRungUnset is the zero value and means proxy-side enforcement only —
// exactly what a server that had never heard of the axis produced. It is a
// member of the enum rather than a hole in it, so every switch has to say what
// it does about "the author said nothing", and the engine never synthesises a
// weaker rung as a fallback: a silent downgrade is what the vocabulary exists
// to prevent.
type ExecutionRung string

const (
	ExecutionRungUnset          ExecutionRung = ""
	ExecutionProxyInspected     ExecutionRung = "proxy-inspected"
	ExecutionNoInteractiveShell ExecutionRung = "no-interactive-shell"
	ExecutionAccountRestricted  ExecutionRung = "account-restricted"
	ExecutionAccountConfined    ExecutionRung = "account-confined"
	ExecutionPlatformAuthorized ExecutionRung = "platform-authorized"
	ExecutionPlatformAttested   ExecutionRung = "platform-attested"
)

var executionRungs = []ExecutionRung{
	ExecutionProxyInspected, ExecutionNoInteractiveShell, ExecutionAccountRestricted,
	ExecutionAccountConfined, ExecutionPlatformAuthorized, ExecutionPlatformAttested,
}

// UnmarshalYAML decodes an ExecutionRung.
func (r *ExecutionRung) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, r, executionRungs, "execution rung")
}

// Attested reports whether the target already enforces this rung and the proxy
// configures nothing. An attested rung requires an attestation; every other
// rung forbids one.
func (r ExecutionRung) Attested() bool {
	switch r {
	case ExecutionPlatformAttested:
		return true
	case ExecutionRungUnset, ExecutionProxyInspected, ExecutionNoInteractiveShell,
		ExecutionAccountRestricted, ExecutionAccountConfined, ExecutionPlatformAuthorized:
		return false
	}
	return false
}

// Applied reports whether this rung is one the proxy configures per session
// onto an account it administers. An applied rung on a route whose every
// ladder entry is brokered-key or static-key is refused outright by the proxy,
// so the compiler refuses it here (PLAN §5.2).
func (r ExecutionRung) Applied() bool {
	switch r {
	case ExecutionAccountRestricted, ExecutionAccountConfined, ExecutionPlatformAuthorized:
		return true
	case ExecutionRungUnset, ExecutionProxyInspected, ExecutionNoInteractiveShell,
		ExecutionPlatformAttested:
		return false
	}
	return false
}

// ReachRung is where the limit on what a session may REACH is enforced. As on
// the execution axis, the zero value means proxy-side enforcement only.
type ReachRung string

const (
	ReachRungUnset               ReachRung = ""
	ReachProxyChannelPolicy      ReachRung = "proxy-channel-policy"
	ReachAccountEgressRestricted ReachRung = "account-egress-restricted"
	ReachAccountNetworkIsolated  ReachRung = "account-network-isolated"
	ReachPlatformAttested        ReachRung = "platform-attested"
)

var reachRungs = []ReachRung{
	ReachProxyChannelPolicy, ReachAccountEgressRestricted,
	ReachAccountNetworkIsolated, ReachPlatformAttested,
}

// UnmarshalYAML decodes a ReachRung.
func (r *ReachRung) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, r, reachRungs, "reach rung")
}

// Attested reports whether the target already constrains what the account can
// reach and the proxy configures nothing.
func (r ReachRung) Attested() bool {
	switch r {
	case ReachPlatformAttested:
		return true
	case ReachRungUnset, ReachProxyChannelPolicy, ReachAccountEgressRestricted,
		ReachAccountNetworkIsolated:
		return false
	}
	return false
}

// Applied reports whether this rung is rendered onto an account the proxy
// creates.
func (r ReachRung) Applied() bool {
	switch r {
	case ReachAccountEgressRestricted, ReachAccountNetworkIsolated:
		return true
	case ReachRungUnset, ReachProxyChannelPolicy, ReachPlatformAttested:
		return false
	}
	return false
}

// ObligationKind is something a matching rule requires in addition to the
// route it serves (PLAN §5.2).
type ObligationKind string

const (
	// ObligationRecordSession requires the session to be captured.
	ObligationRecordSession ObligationKind = "record-session"
	// ObligationRequireApproval requires a human to approve before the
	// session proceeds.
	ObligationRequireApproval ObligationKind = "require-approval"
	// ObligationRequireStepUp requires a second factor beyond the one the
	// identity already presented.
	ObligationRequireStepUp ObligationKind = "require-step-up"
)

var obligationKinds = []ObligationKind{
	ObligationRecordSession, ObligationRequireApproval, ObligationRequireStepUp,
}

// UnmarshalYAML decodes an ObligationKind.
func (k *ObligationKind) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, k, obligationKinds, "obligation kind")
}

// GrantOrigin is where a live grant came from (M10, M16). All three are the
// same object to the engine — that is the point — but the record carries which
// one it was, because "explain why" that cannot name the ticket is not an
// explanation.
type GrantOrigin string

const (
	// GrantOriginAdministrator is a grant an administrator created by hand.
	GrantOriginAdministrator GrantOrigin = "administrator"
	// GrantOriginWorkflow is a grant an approval workflow produced.
	GrantOriginWorkflow GrantOrigin = "workflow"
	// GrantOriginExternal is a window an external system asserted and a
	// provider confirmed (M16).
	GrantOriginExternal GrantOrigin = "external"
)

var grantOrigins = []GrantOrigin{
	GrantOriginAdministrator, GrantOriginWorkflow, GrantOriginExternal,
}

// UnmarshalYAML decodes a GrantOrigin.
func (o *GrantOrigin) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, o, grantOrigins, "grant origin")
}

// CacheKeyComponent is one input a cache hint's key is derived from (PLAN
// §5.4). The key selects the sharing scope, so the set is closed and named
// rather than a format string: a key shared across identities serves one user
// another user's policy, and that has to be a compile error rather than a
// typo.
type CacheKeyComponent string

const (
	// CacheKeySubject is the authenticated subject id. A key without it is
	// shared across identities and is rejected.
	CacheKeySubject CacheKeyComponent = "subject"
	// CacheKeyTarget is the target hostname.
	CacheKeyTarget CacheKeyComponent = "target"
	// CacheKeyTargetPort is the target port.
	CacheKeyTargetPort CacheKeyComponent = "target-port"
	// CacheKeyProxy is the proxy that asked (conn.proxy_id).
	CacheKeyProxy CacheKeyComponent = "proxy"
	// CacheKeyAuthMethod is how the identity authenticated.
	CacheKeyAuthMethod CacheKeyComponent = "auth-method"
	// CacheKeyRule is the rule that matched, so a bundle change that moves
	// a session to a different rule cannot be served from an old entry.
	CacheKeyRule CacheKeyComponent = "rule"
)

var cacheKeyComponents = []CacheKeyComponent{
	CacheKeySubject, CacheKeyTarget, CacheKeyTargetPort,
	CacheKeyProxy, CacheKeyAuthMethod, CacheKeyRule,
}

// UnmarshalYAML decodes a CacheKeyComponent.
func (c *CacheKeyComponent) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, c, cacheKeyComponents, "cache key component")
}

// AuthMethod is how the identity authenticated to the proxy.
type AuthMethod string

const (
	AuthMethodUnset       AuthMethod = ""
	AuthMethodCert        AuthMethod = "cert"
	AuthMethodPasswordMFA AuthMethod = "password-mfa"
)

var authMethods = []AuthMethod{AuthMethodCert, AuthMethodPasswordMFA}

// UnmarshalYAML decodes an AuthMethod.
func (m *AuthMethod) UnmarshalYAML(n *yaml.Node) error {
	return decodeEnum(n, m, authMethods, "authentication method")
}

// Day is a day of the week, as a rule's context match names it.
type Day string

const (
	Monday    Day = "monday"
	Tuesday   Day = "tuesday"
	Wednesday Day = "wednesday"
	Thursday  Day = "thursday"
	Friday    Day = "friday"
	Saturday  Day = "saturday"
	Sunday    Day = "sunday"
)

var days = []Day{Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday}

// UnmarshalYAML decodes a Day.
func (d *Day) UnmarshalYAML(n *yaml.Node) error { return decodeEnum(n, d, days, "day") }

// oneOf reports whether v is a member of set.
func oneOf[T ~string](v T, set []T) bool {
	for _, m := range set {
		if v == m {
			return true
		}
	}
	return false
}
