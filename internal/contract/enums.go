// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

// Named constants for every enum in contract/control.yaml, and for the paths it
// defines. enums_test.go asserts that this file and the vendored document agree
// — in both directions, so a value deleted upstream fails here as loudly as one
// added. That test is what catches an enum drifting, and it is the reason these
// are constants rather than string literals scattered through handlers.
//
// The types are named (M13): the `exhaustive` linter only sees named enums, and
// a switch that forgot a new rung is exactly the failure it exists to catch.

// PolicyVersion is the policy vocabulary this server speaks — the number a
// proxy declares on AuthorizeRequest and the highest this server may answer
// within (PLAN §4).
//
// It is NOT the document's own version (`info.version`). The two move
// independently and neither is derived from the other: policy_version governs
// /v1/authorize and nothing else, so a field on another endpoint, a whole new
// endpoint, and a tightening all move the document without moving this number
// — and upstream Hoplock/proxy#53 moved the document version DOWN, 4.3.0 to
// 4.0.0, while this stood still at 4. A version string therefore identifies no
// document; the checksum in contract/UPSTREAM does.
//
// It is the vocabulary of the vendored document, and nothing ties the two
// together but this line until phase 0038: a re-vendor that moves the number
// must move this constant in the same change, or this server hands a proxy a
// field it refuses the whole response over.
const PolicyVersion int32 = VocabularyAlgorithmFloor

// The vocabulary tiers: the policy_version that first carried each field a
// response may hold (PLAN §4). Each is a constant of its own and never an
// offset from PolicyVersion, because a later revision moves PolicyVersion and
// leaves every earlier tier where it was — Hoplock/proxy#69 moved it to 6 and
// `brokered-certificate` stayed a 5. The decision path's requiredVersion picks
// one per response; the proxy's cmd/mock-control keeps the same three
// (baselineVocabulary, vocabularyBrokeredCertificate, vocabularyAlgorithmFloor).
const (
	// VocabularyBaseline is the vocabulary every response this server can
	// build without either of the fields below needs.
	VocabularyBaseline int32 = 4
	// VocabularyBrokeredCertificate added the `brokered-certificate` method
	// (Hoplock/proxy#68). An unknown method refuses the whole response,
	// exactly as an unknown field does, which is why a method moves it.
	VocabularyBrokeredCertificate int32 = 5
	// VocabularyAlgorithmFloor added `algorithm_floor` and `algorithm_bans`
	// (Hoplock/proxy#69).
	VocabularyAlgorithmFloor int32 = 6
)

// The paths the contract defines.
const (
	PathAuthCert           = "/v1/auth/cert"
	PathAuthPassword       = "/v1/auth/password"
	PathAuthMFAPoll        = "/v1/auth/mfa/poll"
	PathAuthorize          = "/v1/authorize"
	PathHostKeyReport      = "/v1/hostkeys/report"
	PathCapabilitiesReport = "/v1/capabilities/report"
	PathUIDLease           = "/v1/uids/lease"
	PathLogsBatch          = "/v1/logs/batch"
	PathLogsPriority       = "/v1/logs/priority"
	// PathCredentialCertificate issues one brokered certificate per session
	// (Hoplock/proxy#68).
	PathCredentialCertificate = "/v1/credentials/certificate"
	// PathProxyEvents is a template: {proxy_id} is the subscribing proxy.
	PathProxyEvents = "/v1/proxies/{proxy_id}/events"
	// PathProxyConfig is the proxy's desired configuration document (proxy
	// D18). A template, like PathProxyEvents.
	PathProxyConfig = "/v1/proxies/{proxy_id}/config"
	// PathProxyConfigReport is what the proxy runs, and what became of its
	// desired document (proxy D18).
	PathProxyConfigReport = "/v1/proxies/{proxy_id}/config/report"
)

// MediaTypeNDJSON is the content type of the revocation stream: one
// RevocationEvent per line, for as long as the connection lasts. It is not
// `application/json` — the body is never a single document and a client that
// waited for one would wait for the life of the subscription.
const MediaTypeNDJSON = "application/x-ndjson"

// AuthStatus is AuthenticateResponse.status.
type AuthStatus string

const (
	AuthStatusAuthenticated AuthStatus = "authenticated"
	AuthStatusMFARequired   AuthStatus = "mfa_required"
)

// AuthMethod is AuthorizeRequest.auth_method: how the identity was
// authenticated, for policy and audit.
type AuthMethod string

const (
	AuthMethodCert        AuthMethod = "cert"
	AuthMethodPasswordMFA AuthMethod = "password-mfa"
)

// RouteType is AuthorizeResponse.route_type.
type RouteType string

const (
	RouteTypeDirect  RouteType = "direct"
	RouteTypeNexthop RouteType = "nexthop"
)

// RequestType is a member of RequestPolicy.types. `subsystem` is deliberately
// not one: a subsystem is permitted by name, so sftp can be denied while shell
// stays.
type RequestType string

const (
	RequestTypePTYReq       RequestType = "pty-req"
	RequestTypeShell        RequestType = "shell"
	RequestTypeExec         RequestType = "exec"
	RequestTypeEnv          RequestType = "env"
	RequestTypeX11Req       RequestType = "x11-req"
	RequestTypeAuthAgentReq RequestType = "auth-agent-req"
)

// AlgorithmProfile is AuthorizeResponse.algorithm_profile, and
// AlgorithmProfileCapability.profile. Absent means AlgorithmProfileDefault,
// which is the only one that is not a weakening. What each offers is the
// contract's to say (its `algorithm_profile` description) and each proxy build's
// to declare (`capabilities.algorithm_profiles`); no list of it is copied here.
type AlgorithmProfile string

const (
	AlgorithmProfileDefault       AlgorithmProfile = "default"
	AlgorithmProfileLegacyRSASHA1 AlgorithmProfile = "legacy-rsa-sha1"
	AlgorithmProfileLegacyDevice  AlgorithmProfile = "legacy-device"
)

// WidensKeyExchange reports whether the profile widens the key-exchange axis,
// which is the axis an algorithm_floor narrows. The proxy refuses a floor with
// such a profile, because the pair describes a leg that can never connect; the
// rule is the axis, not the word "legacy" — legacy-rsa-sha1 changes only
// signatures and takes a floor.
func (p AlgorithmProfile) WidensKeyExchange() bool {
	switch p {
	case AlgorithmProfileLegacyDevice:
		return true
	case AlgorithmProfileDefault, AlgorithmProfileLegacyRSASHA1:
		return false
	default:
		return false
	}
}

// AlgorithmFloor is AuthorizeResponse.algorithm_floor and
// AlgorithmFloorCapability.level: the minimum key exchange the proxy→target
// leg must negotiate. Absent means no floor. The levels are an ordered ladder
// compared by [AlgorithmFloor.Rank] alone; which exchanges each accepts is each
// proxy build's declaration and never a list here.
type AlgorithmFloor string

const (
	AlgorithmFloorModernKex   AlgorithmFloor = "modern-kex"
	AlgorithmFloorPQHybridKex AlgorithmFloor = "pq-hybrid-kex"
)

// Rank orders the levels: absent 0, modern-kex 1, pq-hybrid-kex 2. A level
// added later goes above the top (the contract's nesting rule), so it takes the
// next number. An unknown level ranks -1, which nothing compares above.
func (f AlgorithmFloor) Rank() int {
	switch f {
	case "":
		return 0
	case AlgorithmFloorModernKex:
		return 1
	case AlgorithmFloorPQHybridKex:
		return 2
	default:
		return -1
	}
}

// FloorMet is KexObservation.floor_met: the highest floor level a target was
// observed to meet, or FloorMetNone.
type FloorMet string

const (
	FloorMetNone        FloorMet = "none"
	FloorMetModernKex   FloorMet = FloorMet(AlgorithmFloorModernKex)
	FloorMetPQHybridKex FloorMet = FloorMet(AlgorithmFloorPQHybridKex)
)

// Rank places an observation on the floor's ladder: none 0, then each level's
// own rank. An unknown value ranks -1, below everything.
func (f FloorMet) Rank() int {
	if f == FloorMetNone {
		return 0
	}
	if r := AlgorithmFloor(f).Rank(); r > 0 {
		return r
	}
	return -1
}

// The algorithm axes, as AlgorithmBans, OfferableAlgorithms and
// AlgorithmProfileCapability name them. A ban is per axis because some
// identifiers live on more than one: ssh-rsa is a host-key and a public-key
// algorithm.
const (
	AxisKeyExchanges  = "key_exchanges"
	AxisCiphers       = "ciphers"
	AxisMACs          = "macs"
	AxisHostKeys      = "host_keys"
	AxisPublicKeyAuth = "public_key_auth"
)

// Axes is every axis, in the contract's order.
var Axes = []string{AxisKeyExchanges, AxisCiphers, AxisMACs, AxisHostKeys, AxisPublicKeyAuth}

// The two spellings of one key exchange. The proxy's SSH library offers the
// second immediately after the first and cannot offer either alone, so a ban on
// either removes both, and every judgement treats them as one.
const (
	KexCurve25519       = "curve25519-sha256"
	KexCurve25519LibSSH = "curve25519-sha256@libssh.org"
)

// TargetAuthMethod is TargetAuth.method.
type TargetAuthMethod string

const (
	TargetAuthEphemeralUser    TargetAuthMethod = "ephemeral-user"
	TargetAuthBrokeredKey      TargetAuthMethod = "brokered-key"
	TargetAuthEphemeralAccount TargetAuthMethod = "ephemeral-account"
	TargetAuthStaticKey        TargetAuthMethod = "static-key"
	// TargetAuthBrokeredCertificate has the proxy present a per-session SSH
	// user certificate this server signs over a key the proxy generates
	// (Hoplock/proxy#68, proxy D6a). The ladder entry carries policy only —
	// username, key_type, lifetime_seconds — and the certificate comes from
	// POST /v1/credentials/certificate, once per session.
	TargetAuthBrokeredCertificate TargetAuthMethod = "brokered-certificate"
)

// Provisions reports whether this method has the proxy administer the account on
// the target, which is what an APPLIED enforcement rung needs (PLAN §4).
func (m TargetAuthMethod) Provisions() bool {
	switch m {
	case TargetAuthEphemeralUser, TargetAuthEphemeralAccount:
		return true
	case TargetAuthBrokeredKey, TargetAuthStaticKey:
		return false
	case TargetAuthBrokeredCertificate:
		// It creates nothing on the target: the account exists and trusts
		// the tenant CA already. So a route that names only this method can
		// reach only an attested rung.
		return false
	default:
		return false
	}
}

// The parameter names the contract documents for TargetAuth.params. The map is
// open on purpose — a future method arrives with its own names — but these are
// the ones that exist today, and ParamUsername is required on every method.
const (
	ParamUsername        = "username"
	ParamKeyType         = "key_type"
	ParamLifetimeSeconds = "lifetime_seconds"
	ParamCredentialRef   = "credential_ref"
	ParamPlatform        = "platform"
	ParamCredentialKind  = "credential_kind"
	ParamExpiryPosture   = "expiry_posture"

	// DeviceFieldPrefix opens the platform-specific namespace on an
	// `ephemeral-account` entry. The contract does not enumerate what follows
	// it and never will: the set of fields is as open as the set of platforms.
	// Only the SHAPE is specified — see DeviceFieldNamePattern and the limits
	// beside it.
	DeviceFieldPrefix = "device_field."
)

// The shape rules for a `device_field.<name>` parameter. They are the whole of
// what the contract checks: which names are meaningful is the driver's business
// and a suite that hard-codes one fails the first customer driver.
const (
	// DeviceFieldNamePattern matches <name> after DeviceFieldPrefix.
	DeviceFieldNamePattern = `^[a-z0-9_-]{1,64}$`
	// DeviceFieldMaxNameLen is the bound the pattern above also encodes.
	DeviceFieldMaxNameLen = 64
	// DeviceFieldMaxValueLen bounds the value; it must also be non-empty.
	DeviceFieldMaxValueLen = 256
	// DeviceFieldMaxPerEntry bounds how many ride on one ladder entry.
	DeviceFieldMaxPerEntry = 16
)

// CredentialKind is the `credential_kind` parameter of an `ephemeral-account`
// entry. It has no absent-value default: defaulting would hand out the weaker of
// two materially different exposures to a policy that never said so.
type CredentialKind string

const (
	CredentialKindPassword  CredentialKind = "password"
	CredentialKindPublicKey CredentialKind = "publickey"
)

// ExpiryPosture is the `expiry_posture` parameter of an `ephemeral-account`
// entry. It has no absent-value default either: "the risk was accepted" is a
// sentence somebody writes down, never one a proxy infers from an omission.
type ExpiryPosture string

const (
	ExpiryPostureTargetEnforced ExpiryPosture = "target-enforced"
	ExpiryPostureProxyEnforced  ExpiryPosture = "proxy-enforced"
	ExpiryPostureAcceptedRisk   ExpiryPosture = "accepted-risk"
)

// ExecutionRung is EnforcementPolicy.execution — where the limit on what the
// session may EXECUTE is enforced. Absent means ExecutionProxyInspected.
type ExecutionRung string

const (
	ExecutionProxyInspected     ExecutionRung = "proxy-inspected"
	ExecutionNoInteractiveShell ExecutionRung = "no-interactive-shell"
	ExecutionAccountRestricted  ExecutionRung = "account-restricted"
	ExecutionAccountConfined    ExecutionRung = "account-confined"
	ExecutionPlatformAuthorized ExecutionRung = "platform-authorized"
	ExecutionPlatformAttested   ExecutionRung = "platform-attested"
)

// Attested reports whether the target already enforces this rung and the proxy
// configures nothing. An attested rung requires an Attestation; an applied one
// forbids it.
func (r ExecutionRung) Attested() bool { return r == ExecutionPlatformAttested }

// RequiresProvisioning reports whether this rung needs the proxy to administer
// the account on the target, which only an `ephemeral-user` or
// `ephemeral-account` ladder entry does.
func (r ExecutionRung) RequiresProvisioning() bool {
	switch r {
	case ExecutionAccountRestricted, ExecutionAccountConfined, ExecutionPlatformAuthorized:
		return true
	case ExecutionProxyInspected, ExecutionNoInteractiveShell, ExecutionPlatformAttested:
		return false
	default:
		return false
	}
}

// ReachRung is EnforcementPolicy.reach — where the limit on what the session may
// REACH is enforced. Absent means ReachProxyChannelPolicy.
type ReachRung string

const (
	ReachProxyChannelPolicy      ReachRung = "proxy-channel-policy"
	ReachAccountEgressRestricted ReachRung = "account-egress-restricted"
	ReachAccountNetworkIsolated  ReachRung = "account-network-isolated"
	ReachPlatformAttested        ReachRung = "platform-attested"
)

// Attested reports whether the target already constrains what the account can
// reach and the proxy configures nothing.
func (r ReachRung) Attested() bool { return r == ReachPlatformAttested }

// RequiresProvisioning reports whether this rung is rendered onto an account the
// proxy creates.
func (r ReachRung) RequiresProvisioning() bool {
	switch r {
	case ReachAccountEgressRestricted, ReachAccountNetworkIsolated:
		return true
	case ReachProxyChannelPolicy, ReachPlatformAttested:
		return false
	default:
		return false
	}
}

// HopConnection is HopMetadata.connection: how this proxy reaches the next one.
// Absent means HopConnectionDial, the original next-hop behaviour. A `relay` hop
// with no live registration is an outage and is NEVER downgraded to a dial.
type HopConnection string

const (
	HopConnectionDial  HopConnection = "dial"
	HopConnectionRelay HopConnection = "relay"
)

// EventType is RevocationEvent.type.
type EventType string

const (
	EventTypeSessionKill     EventType = "session_kill"
	EventTypeCacheInvalidate EventType = "cache_invalidate"
	EventTypeHeartbeat       EventType = "heartbeat"
	EventTypeResync          EventType = "resync"
	// EventTypeConfigChanged names the proxy's desired configuration
	// document; it never carries it (proxy D18).
	EventTypeConfigChanged EventType = "config_changed"
)

// ConfigState is ProxyConfigReport.state: what became of a proxy's desired
// configuration document (proxy D18).
type ConfigState string

const (
	ConfigStateApplied        ConfigState = "applied"
	ConfigStatePendingRestart ConfigState = "pending_restart"
	ConfigStateRejected       ConfigState = "rejected"
	ConfigStateFetchFailed    ConfigState = "fetch_failed"
)

// FilterMode is FilterPolicy.mode: what happens to a command no rule matched.
type FilterMode string

const (
	FilterModeWhitelist FilterMode = "whitelist"
	FilterModeBlacklist FilterMode = "blacklist"
)

// ExecMode is FilterPolicy.exec_mode: which tier decides an exec request.
// Absent means ExecModeFiltered. The two are alternatives, never layers.
type ExecMode string

const (
	ExecModeFiltered   ExecMode = "filtered"
	ExecModeRestricted ExecMode = "restricted"
)

// CommandForm is RestrictedCommand.form.
type CommandForm string

const (
	CommandFormExact      CommandForm = "exact"
	CommandFormPositional CommandForm = "positional"
)

// ArgumentKind is ArgumentSpec.kind. `any` is named rather than smuggled in as
// an empty prefix so that every unconstrained position is greppable.
type ArgumentKind string

const (
	ArgumentKindLiteral ArgumentKind = "literal"
	ArgumentKindPrefix  ArgumentKind = "prefix"
	ArgumentKindOneOf   ArgumentKind = "oneof"
	ArgumentKindAny     ArgumentKind = "any"
)

// FilterAction is FilterRule.action.
type FilterAction string

const (
	FilterActionAllowAndLog     FilterAction = "allow_and_log"
	FilterActionBlockCommand    FilterAction = "block_command"
	FilterActionWarnAndContinue FilterAction = "warn_and_continue"
	FilterActionKillSession     FilterAction = "kill_session"
)

// HostKeyDecision is HostKeyReportResponse.decision.
type HostKeyDecision string

const (
	HostKeyAccept HostKeyDecision = "accept"
	HostKeyReject HostKeyDecision = "reject"
)

// LogSeverity is LogRecord.severity. `critical` records are the ones shipped via
// /v1/logs/priority.
type LogSeverity string

const (
	SeverityInfo     LogSeverity = "info"
	SeverityWarn     LogSeverity = "warn"
	SeverityCritical LogSeverity = "critical"
)

// The error codes this server uses on the envelope. `code` is a free string in
// the document, so these are conventions rather than an enum — but a malformed
// request and a deny are two different answers (M11) and the code is where a
// caller reads which.
const (
	ErrCodeInvalidRequest     = "invalid_request"
	ErrCodeUnauthorized       = "unauthorized"
	ErrCodeExhausted          = "uid_range_exhausted"
	ErrCodeInternal           = "internal_error"
	ErrCodeVersionUnsupported = "policy_version_unsupported"
	// ErrCodeNotEnrolled is a configuration fetch or report for a proxy id
	// the registry does not hold in the caller's tenant. A registry fact,
	// answered 404 and never 401 (M11).
	ErrCodeNotEnrolled = "not_enrolled"
	// ErrCodeUnknownDecision and ErrCodeDecisionMismatch are a refusal to
	// mint a brokered certificate: the decision cited is not one this server
	// made for the method, or the target or account does not match it. The
	// proxy's mock uses the same two codes.
	ErrCodeUnknownDecision  = "unknown_decision"
	ErrCodeDecisionMismatch = "decision_mismatch"
	// ErrCodeNoAuthority is an issuance with no certificate authority behind
	// it — a tenant with none, or a deployment that configured none. 503: an
	// operator's fix, not a deny and not a 500.
	ErrCodeNoAuthority = "no_authority"
)
