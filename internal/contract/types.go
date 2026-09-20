// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

import "encoding/json"

// The wire types of the south-bound contract, one struct per payload in
// contract/control.yaml. The JSON tags are the contract: a name here that does
// not match the document is a name the proxy will not send or will refuse to
// read, so enums_test.go checks them against the vendored document rather than
// against anybody's memory.
//
// Pointers are used wherever the document gives a field an absent-value
// default whose meaning differs from the zero value — an absent `enforcement`
// is not an empty one, and an absent `require_session_capture` is not merely
// `false` arrived at by accident. Where the document says absence and a zero
// mean the same thing (`concurrency` caps, `term_seconds`,
// `report_after_seconds`, `cache.ttl_seconds`), the plain type is correct and
// says so.
//
// Resolving those defaults is not left to each caller: see resolve.go.

// ErrorResponse is the error envelope every non-2xx response carries (M11).
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the body of an ErrorResponse.
type ErrorBody struct {
	// Code is stable and machine-readable, e.g. "unauthorized".
	Code string `json:"code"`
	// Message is human-readable detail and MUST NOT contain credentials.
	Message string `json:"message"`
}

// ConnMeta is metadata about the SSH connection a request is made for.
type ConnMeta struct {
	SessionID     string `json:"session_id"`
	ProxyID       string `json:"proxy_id"`
	ClientAddr    string `json:"client_addr"`
	ServerAddr    string `json:"server_addr,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	// HopTrail carries the proxy ids the session has already traversed, oldest
	// first, and is empty on a user's first hop. It may only ever narrow a
	// decision (PLAN §4).
	HopTrail  []string `json:"hop_trail,omitempty"`
	Timestamp string   `json:"timestamp"`
}

// Identity is the authenticated principal, modelled as claims rather than a
// boolean so an IdP can be added without changing a caller.
type Identity struct {
	Subject     string            `json:"subject"`
	Login       string            `json:"login"`
	DisplayName string            `json:"display_name,omitempty"`
	Source      string            `json:"source"`
	Principals  []string          `json:"principals,omitempty"`
	Groups      []string          `json:"groups,omitempty"`
	Claims      map[string]string `json:"claims,omitempty"`
}

// PublicKeyMaterial is an SSH public key or certificate as offered on the wire.
type PublicKeyMaterial struct {
	Type          string `json:"type"`
	Blob          string `json:"blob"`
	Fingerprint   string `json:"fingerprint"`
	IsCertificate bool   `json:"is_certificate,omitempty"`
}

// AuthenticateCertRequest is POST /v1/auth/cert.
type AuthenticateCertRequest struct {
	Login     string            `json:"login"`
	Target    string            `json:"target,omitempty"`
	PublicKey PublicKeyMaterial `json:"public_key"`
	Conn      ConnMeta          `json:"conn"`
}

// AuthenticatePasswordRequest is POST /v1/auth/password. Password is never
// logged, echoed in an error, or stored (PLAN §7).
type AuthenticatePasswordRequest struct {
	Login    string   `json:"login"`
	Target   string   `json:"target,omitempty"`
	Password string   `json:"password"`
	Conn     ConnMeta `json:"conn"`
}

// MFAPollRequest is POST /v1/auth/mfa/poll.
type MFAPollRequest struct {
	Token string   `json:"token"`
	Conn  ConnMeta `json:"conn"`
}

// MFAChallenge is an out-of-band second factor the server is waiting on.
type MFAChallenge struct {
	Token       string `json:"token"`
	Prompt      string `json:"prompt,omitempty"`
	PollAfterMS int32  `json:"poll_after_ms"`
	ExpiresAt   string `json:"expires_at"`
}

// AuthenticateResponse is the answer from all three auth endpoints.
type AuthenticateResponse struct {
	Status   AuthStatus    `json:"status"`
	Identity *Identity     `json:"identity,omitempty"`
	MFA      *MFAChallenge `json:"mfa,omitempty"`
}

// ProxyCapabilities is the enforcement rungs a proxy BUILD can provide at all.
// Absent declares nothing, which is the fail-safe reading.
type ProxyCapabilities struct {
	Execution []string `json:"execution,omitempty"`
	Reach     []string `json:"reach,omitempty"`
}

// AuthorizeRequest is POST /v1/authorize.
//
// PolicyVersion is a pointer because it is REQUIRED with no absent-value
// default: a request that omits it is refused with 400 invalid_request, and a
// zero-valued int cannot tell "absent" from "sent as 0" (PLAN §4).
type AuthorizeRequest struct {
	Identity      Identity           `json:"identity"`
	Target        string             `json:"target"`
	TargetPort    int32              `json:"target_port,omitempty"`
	AuthMethod    AuthMethod         `json:"auth_method,omitempty"`
	PolicyVersion *int32             `json:"policy_version"`
	Capabilities  *ProxyCapabilities `json:"capabilities,omitempty"`
	Conn          ConnMeta           `json:"conn"`
}

// AuthorizeResponse is the whole policy for a connection. Every field in it is
// policy, which is why the proxy decodes it strictly and fails a session closed
// on one it does not recognise — and why the server must answer within the
// vocabulary the request declared (PLAN §4).
type AuthorizeResponse struct {
	RouteType               RouteType            `json:"route_type"`
	Target                  string               `json:"target"`
	TargetPort              int32                `json:"target_port,omitempty"`
	Permissions             string               `json:"permissions,omitempty"`
	PermittedChannels       []string             `json:"permitted_channels"`
	PermittedRequests       *RequestPolicy       `json:"permitted_requests,omitempty"`
	PermittedForwards       *ForwardPolicy       `json:"permitted_forwards,omitempty"`
	PermittedGlobalRequests *GlobalRequestPolicy `json:"permitted_global_requests,omitempty"`
	TargetAuthLadder        TargetAuthLadder     `json:"target_auth_ladder,omitempty"`
	AlgorithmProfile        AlgorithmProfile     `json:"algorithm_profile,omitempty"`
	FilterPolicy            FilterPolicy         `json:"filter_policy"`
	Enforcement             *EnforcementPolicy   `json:"enforcement,omitempty"`
	SessionDeadline         string               `json:"session_deadline,omitempty"`
	RequireSessionCapture   *bool                `json:"require_session_capture,omitempty"`
	GrantContext            *GrantContext        `json:"grant_context,omitempty"`
	Concurrency             *ConcurrencyLimits   `json:"concurrency,omitempty"`
	Hop                     *HopMetadata         `json:"hop,omitempty"`
	DecisionID              string               `json:"decision_id,omitempty"`
	Cache                   *CacheHint           `json:"cache,omitempty"`
}

// RequestPolicy is which in-channel requests the session may make.
// Absent object => not policed; present => an allow-list, and {} denies all.
type RequestPolicy struct {
	Types      []RequestType `json:"types,omitempty"`
	Subsystems []string      `json:"subsystems,omitempty"`
}

// ForwardPolicy is which forwarding destinations the session may reach.
type ForwardPolicy struct {
	DirectTCPIP    []ForwardDestination `json:"direct_tcpip,omitempty"`
	ForwardedTCPIP []ForwardDestination `json:"forwarded_tcpip,omitempty"`
}

// ForwardDestination is one permitted destination: a host pattern (exact,
// wildcard, or CIDR) and at most one port constraint.
type ForwardDestination struct {
	Host      string     `json:"host"`
	Port      int32      `json:"port,omitempty"`
	PortRange *PortRange `json:"port_range,omitempty"`
}

// PortRange is an inclusive port range.
type PortRange struct {
	From int32 `json:"from"`
	To   int32 `json:"to"`
}

// GlobalRequestPolicy is which connection-level global requests are relayed.
type GlobalRequestPolicy struct {
	Types []string `json:"types,omitempty"`
}

// TargetAuth is one entry of a TargetAuthLadder: a credential method the server
// named for this route, and its method-scoped parameters.
//
// Params is an open map on purpose, and `username` is required in it on every
// method this document defines — a route omitting it is refused at the first
// authorize call. That is a tightening, which is announced as a break rather
// than carried by policy_version (PLAN §4).
type TargetAuth struct {
	Method TargetAuthMethod  `json:"method"`
	Params map[string]string `json:"params,omitempty"`
}

// TargetAuthLadder is the ordered list of credential methods for a route.
// Absent => the proxy uses its locally configured method; empty => a denial;
// non-empty => walked in order. Use AuthorizeResponse.Ladder to read it.
type TargetAuthLadder []TargetAuth

// EnforcementPolicy is where a connection's policy is enforced, on each of two
// independent axes. Absent object => both axes take their default.
type EnforcementPolicy struct {
	Execution             ExecutionRung        `json:"execution,omitempty"`
	Reach                 ReachRung            `json:"reach,omitempty"`
	PlatformRole          string               `json:"platform_role,omitempty"`
	PermittedDestinations []ForwardDestination `json:"permitted_destinations,omitempty"`
	Attestation           *Attestation         `json:"attestation,omitempty"`
}

// Attestation is the source of an attested enforcement rung: a claim this
// system does not verify, made attributable instead.
type Attestation struct {
	AssertedBy string `json:"asserted_by"`
	Reference  string `json:"reference"`
	AssertedAt string `json:"asserted_at,omitempty"`
}

// GrantContext is why access was granted, as an external system asserted it.
// The proxy treats all of it as opaque: copied to every log record, never
// parsed, never matched against, never the basis of a decision.
type GrantContext struct {
	System            string             `json:"system,omitempty"`
	Reference         string             `json:"reference,omitempty"`
	WindowStart       string             `json:"window_start,omitempty"`
	WindowEnd         string             `json:"window_end,omitempty"`
	AdditionalContext *AdditionalContext `json:"additional_context,omitempty"`
}

// AdditionalContext is `grant_context.additional_context`: a JSON string or a
// JSON object, and nothing else. A number, a list, or a boolean is a contract
// violation rather than something to coerce, because this is stored verbatim
// for an auditor.
type AdditionalContext struct {
	// Text is set when the wire value was a JSON string.
	Text string
	// Fields is set when the wire value was a JSON object.
	Fields map[string]any
}

// UnmarshalJSON accepts exactly the two shapes the contract names.
func (a *AdditionalContext) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		a.Text, a.Fields = s, nil
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return &json.UnmarshalTypeError{Value: string(b), Type: nil}
	}
	a.Text, a.Fields = "", m
	return nil
}

// MarshalJSON writes back the shape that was read.
func (a AdditionalContext) MarshalJSON() ([]byte, error) {
	if a.Fields != nil {
		return json.Marshal(a.Fields)
	}
	return json.Marshal(a.Text)
}

// ConcurrencyLimits is how many sessions may be live at once. Absent — or 0 on
// either scope — is uncapped. Exceeding a cap is a policy denial, not an outage.
type ConcurrencyLimits struct {
	MaxSessionsPerSubject int32 `json:"max_sessions_per_subject,omitempty"`
	MaxSessionsPerTarget  int32 `json:"max_sessions_per_target,omitempty"`
}

// HopMetadata is the chaining constraints, present when RouteType is nexthop.
type HopMetadata struct {
	Connection  HopConnection `json:"connection,omitempty"`
	NextProxyID string        `json:"next_proxy_id,omitempty"`
	FinalTarget string        `json:"final_target,omitempty"`
	MaxHops     int32         `json:"max_hops,omitempty"`
	HopTrail    []string      `json:"hop_trail,omitempty"`
}

// CacheHint is the server authorising the proxy to reuse a decision. It rides on
// AuthorizeResponse and HostKeyReportResponse and means the same on both;
// absent means do not cache, and the proxy never invents a lifetime.
type CacheHint struct {
	Key        string `json:"key,omitempty"`
	TTLSeconds int32  `json:"ttl_seconds"`
}

// RevocationEvent is one line of GET /v1/proxies/{proxy_id}/events.
type RevocationEvent struct {
	EventID   string    `json:"event_id"`
	Type      EventType `json:"type"`
	Timestamp string    `json:"timestamp"`
	// HeartbeatIntervalSeconds is the interval the server says it is
	// keeping NOW. The server sets it on `heartbeat` events, MAY set it on
	// any event, and a reader takes it wherever it appears — a later event
	// carrying a different value is the server re-stating the interval it
	// keeps now, not contradicting an earlier claim.
	//
	// Two rules travel with this field and are worth more than the field is.
	//
	// IT ADVERTISES, IT DOES NOT CONFIGURE. Absent means what every server
	// did before the field existed: the reader falls back to its own timers.
	// That is the same absent-value discipline as HostKeyReportResponse.cache
	// and for the same reason, which is why it is read through
	// [RevocationEvent.AdvertisedHeartbeatInterval] and never off the field:
	// a decoded event cannot tell an omitted key from `0`.
	//
	// IT MAY ONLY EVER TIGHTEN DETECTION, NEVER LOOSEN IT. A reader may use
	// it to notice a dead stream SOONER than its configured timeout; it must
	// never extend that timeout to accommodate a large advertised interval.
	// Sooner is always allowed, later is not — the same rule as
	// `cache.ttl_seconds` (clamp shorter, never longer) and
	// `report_after_seconds` (re-observe sooner, never later). The inverse
	// would let a broken or hostile server silence itself indefinitely by
	// announcing that it intends to, which is the proxy's fail-closed rule
	// turned upside down.
	//
	// The ceiling in [MaxHeartbeatIntervalSeconds] is NOT replaced by this
	// field: a server advertising 600s and honestly keeping to it passes its
	// own claim and breaks every proxy in the fleet, so both halves are
	// conformance requirements.
	HeartbeatIntervalSeconds int32                 `json:"heartbeat_interval_seconds,omitempty"`
	SessionKill              *SessionKillEvent     `json:"session_kill,omitempty"`
	CacheInvalidate          *CacheInvalidateEvent `json:"cache_invalidate,omitempty"`
}

// SessionKillEvent ends sessions that are already in flight.
type SessionKillEvent struct {
	SessionIDs []string `json:"session_ids,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	All        bool     `json:"all,omitempty"`
	// Reason is shown to the user verbatim, so it must be safe to disclose.
	Reason string `json:"reason,omitempty"`
}

// CacheInvalidateEvent drops cached authorize decisions without touching
// running sessions.
type CacheInvalidateEvent struct {
	Keys    []string `json:"keys,omitempty"`
	Subject string   `json:"subject,omitempty"`
	All     bool     `json:"all,omitempty"`
}

// FilterPolicy is the command filter policy for a connection. Mode is required
// so a policy always has a defined default and cannot fail open by omission.
type FilterPolicy struct {
	Mode           FilterMode            `json:"mode"`
	Rules          []FilterRule          `json:"rules,omitempty"`
	ExecMode       ExecMode              `json:"exec_mode,omitempty"`
	RestrictedExec *RestrictedExecPolicy `json:"restricted_exec,omitempty"`
}

// RestrictedExecPolicy is the restricted exec tier: a default-deny allow-list of
// executables and the shape of their permitted arguments.
type RestrictedExecPolicy struct {
	Commands []RestrictedCommand `json:"commands"`
}

// RestrictedCommand is one permitted executable and its permitted argument shape.
type RestrictedCommand struct {
	Executable string         `json:"executable"`
	Form       CommandForm    `json:"form"`
	Argv       []string       `json:"argv,omitempty"`
	Args       []ArgumentSpec `json:"args,omitempty"`
}

// ArgumentSpec is the permitted shape of one argument position.
type ArgumentSpec struct {
	Kind     ArgumentKind `json:"kind"`
	Value    string       `json:"value,omitempty"`
	Values   []string     `json:"values,omitempty"`
	Optional bool         `json:"optional,omitempty"`
}

// FilterRule is one command pattern and the action taken when it matches.
type FilterRule struct {
	Match   string       `json:"match"`
	Action  FilterAction `json:"action"`
	Message string       `json:"message,omitempty"`
}

// HostKeyReportRequest is POST /v1/hostkeys/report.
type HostKeyReportRequest struct {
	Target     string            `json:"target"`
	TargetPort int32             `json:"target_port,omitempty"`
	HostKey    PublicKeyMaterial `json:"host_key"`
	Conn       ConnMeta          `json:"conn"`
}

// HostKeyReportResponse is the trust decision for a reported key.
//
// Cache is the contract's own worked example of a field OUTSIDE policy_version:
// a proxy that has never heard of it ignores it and keeps reporting every
// connection, which is correct behaviour rather than a thinned answer.
type HostKeyReportResponse struct {
	Decision HostKeyDecision `json:"decision"`
	Known    bool            `json:"known"`
	Reason   string          `json:"reason,omitempty"`
	Cache    *CacheHint      `json:"cache,omitempty"`
}

// TargetCapabilities is the enforcement rungs one TARGET can take, as the proxy
// found them by connecting to it.
type TargetCapabilities struct {
	Execution []string `json:"execution,omitempty"`
	Reach     []string `json:"reach,omitempty"`
	// ObservedAt is required: a record with no observation time is treated as
	// stale, because a capability with no date has no shelf life.
	ObservedAt string            `json:"observed_at"`
	Detail     map[string]string `json:"detail,omitempty"`
}

// CapabilityReportRequest is POST /v1/capabilities/report.
type CapabilityReportRequest struct {
	Target       string             `json:"target"`
	TargetPort   int32              `json:"target_port,omitempty"`
	Platform     string             `json:"platform,omitempty"`
	Capabilities TargetCapabilities `json:"capabilities"`
	Conn         ConnMeta           `json:"conn"`
}

// CapabilityReportResponse acknowledges a capability report and decides nothing.
// ReportAfterSeconds is when the server would like the next observation; absent
// or 0 leaves the interval to the proxy, which may re-observe sooner but never
// later.
type CapabilityReportResponse struct {
	Accepted           bool  `json:"accepted"`
	ReportAfterSeconds int32 `json:"report_after_seconds,omitempty"`
}

// UIDLeaseRequest is POST /v1/uids/lease. It carries no ConnMeta: a lease is per
// proxy per target and is not made on behalf of a session.
type UIDLeaseRequest struct {
	ProxyID    string `json:"proxy_id"`
	Target     string `json:"target"`
	TargetPort int32  `json:"target_port,omitempty"`
	UIDCount   int32  `json:"uid_count,omitempty"`
	RangeMin   int32  `json:"range_min,omitempty"`
	RangeMax   int32  `json:"range_max,omitempty"`
	// ObservedFloor is an observation from an untrusted party that may only ever
	// RAISE the server's cursor, never lower it.
	ObservedFloor int32 `json:"observed_floor,omitempty"`
}

// UIDLeaseResponse is an exclusive block [UIDFrom, UIDTo) for one target. A uid
// inside it is never inside any other grant, for this proxy or any other, ever
// again — used, abandoned, or expired alike.
type UIDLeaseResponse struct {
	LeaseID string `json:"lease_id"`
	UIDFrom int32  `json:"uid_from"`
	// UIDTo is EXCLUSIVE and must be greater than UIDFrom; a server that cannot
	// grant a non-empty block answers 409 instead.
	UIDTo       int32 `json:"uid_to"`
	TermSeconds int32 `json:"term_seconds,omitempty"`
}

// LogRecord is one structured session log record.
type LogRecord struct {
	RecordID   string            `json:"record_id"`
	SessionID  string            `json:"session_id"`
	Timestamp  string            `json:"timestamp"`
	Kind       string            `json:"kind"`
	Severity   LogSeverity       `json:"severity"`
	Message    string            `json:"message,omitempty"`
	Subject    string            `json:"subject,omitempty"`
	Login      string            `json:"login,omitempty"`
	Target     string            `json:"target,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Payload    string            `json:"payload,omitempty"`
}

// LogBatchRequest is POST /v1/logs/batch.
type LogBatchRequest struct {
	Records []LogRecord `json:"records"`
}

// LogBatchResponse says how many records were stored. Fewer than sent means the
// rest were duplicates, which is how record_id idempotency is observable.
type LogBatchResponse struct {
	Accepted int32 `json:"accepted"`
}

// LogPriorityRequest is POST /v1/logs/priority.
type LogPriorityRequest struct {
	Record LogRecord `json:"record"`
}

// LogPriorityResponse acknowledges a priority record. Accepted true means the
// record is DURABLE — the proxy acts on the event knowing it was recorded.
type LogPriorityResponse struct {
	Accepted  bool   `json:"accepted"`
	ReceiptID string `json:"receipt_id,omitempty"`
}
