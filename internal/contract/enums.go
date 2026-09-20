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
// 4.0.0, while this stood still at 4.
const PolicyVersion int32 = 4

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
	// PathProxyEvents is a template: {proxy_id} is the subscribing proxy.
	PathProxyEvents = "/v1/proxies/{proxy_id}/events"
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

// AlgorithmProfile is AuthorizeResponse.algorithm_profile. Absent means
// AlgorithmProfileDefault, which is the only one that is not a weakening.
type AlgorithmProfile string

const (
	AlgorithmProfileDefault       AlgorithmProfile = "default"
	AlgorithmProfileLegacyRSASHA1 AlgorithmProfile = "legacy-rsa-sha1"
	AlgorithmProfileLegacyDevice  AlgorithmProfile = "legacy-device"
)

// TargetAuthMethod is TargetAuth.method.
type TargetAuthMethod string

const (
	TargetAuthEphemeralUser    TargetAuthMethod = "ephemeral-user"
	TargetAuthBrokeredKey      TargetAuthMethod = "brokered-key"
	TargetAuthEphemeralAccount TargetAuthMethod = "ephemeral-account"
	TargetAuthStaticKey        TargetAuthMethod = "static-key"
)

// Provisions reports whether this method has the proxy administer the account on
// the target, which is what an APPLIED enforcement rung needs (PLAN §4).
func (m TargetAuthMethod) Provisions() bool {
	switch m {
	case TargetAuthEphemeralUser, TargetAuthEphemeralAccount:
		return true
	case TargetAuthBrokeredKey, TargetAuthStaticKey:
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
)
