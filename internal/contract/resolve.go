// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

import "time"

// Absent-value defaults, resolved in one place.
//
// The contract's whole discipline here is that absence and emptiness are not
// the same thing: a server that says nothing about `permitted_requests` must
// not thereby deny every shell, while one that sends `{}` has decided to permit
// nothing. Every caller that reads these fields directly is a caller that can
// get that backwards, so nothing in this repository reads them directly — it
// calls the resolvers below, which is also what gives the conformance suite one
// definition of "the default" to grade against.

// LadderState is the three-way reading of `target_auth_ladder`: absent, present
// and empty, and present and non-empty are three different policies.
type LadderState string

const (
	// LadderUnset — the proxy uses its locally configured method, which this
	// response cannot see. A server should always name a ladder.
	LadderUnset LadderState = "unset"
	// LadderDeny — present and empty. The server named no method it will
	// accept, and that is a denial.
	LadderDeny LadderState = "deny"
	// LadderWalk — present and non-empty. Walked top-down, first satisfiable
	// entry wins, and exhausting it is an outage-class denial.
	LadderWalk LadderState = "walk"
)

// Ladder resolves the absent/empty/non-empty distinction on the credential
// ladder. Read it through this rather than through the field.
func (r *AuthorizeResponse) Ladder() (LadderState, TargetAuthLadder) {
	if r.TargetAuthLadder == nil {
		return LadderUnset, nil
	}
	if len(r.TargetAuthLadder) == 0 {
		return LadderDeny, TargetAuthLadder{}
	}
	return LadderWalk, r.TargetAuthLadder
}

// Profile resolves `algorithm_profile`: absent means `default`, the only
// profile that is not a weakening.
func (r *AuthorizeResponse) Profile() AlgorithmProfile {
	if r.AlgorithmProfile == "" {
		return AlgorithmProfileDefault
	}
	return r.AlgorithmProfile
}

// EnforcedExecution resolves axis 1: an absent `enforcement` object, or an
// object that names no execution rung, means proxy-side enforcement at the
// `exec` request.
func (r *AuthorizeResponse) EnforcedExecution() ExecutionRung {
	if r.Enforcement == nil || r.Enforcement.Execution == "" {
		return ExecutionProxyInspected
	}
	return r.Enforcement.Execution
}

// EnforcedReach resolves axis 2: absent means the proxy's channel policy, which
// covers SSH-channel forwarding and nothing else.
func (r *AuthorizeResponse) EnforcedReach() ReachRung {
	if r.Enforcement == nil || r.Enforcement.Reach == "" {
		return ReachProxyChannelPolicy
	}
	return r.Enforcement.Reach
}

// CaptureRequired resolves `require_session_capture`: absent means false —
// capture happens if the proxy is configured for it, and its absence stops
// nothing.
func (r *AuthorizeResponse) CaptureRequired() bool {
	return r.RequireSessionCapture != nil && *r.RequireSessionCapture
}

// Deadline resolves `session_deadline`: absent means no deadline at all, and the
// session is bounded by nothing but the user and the revocation stream.
func (r *AuthorizeResponse) Deadline() (string, bool) {
	if r.SessionDeadline == "" {
		return "", false
	}
	return r.SessionDeadline, true
}

// Caps resolves `concurrency`: absent — or 0 on either scope — is uncapped.
// The booleans say whether a ceiling exists, because 0 and absent are the same
// answer here and a caller must not read either as "no sessions permitted".
func (r *AuthorizeResponse) Caps() (perSubject int32, perTarget int32) {
	if r.Concurrency == nil {
		return 0, 0
	}
	return r.Concurrency.MaxSessionsPerSubject, r.Concurrency.MaxSessionsPerTarget
}

// Tier resolves `filter_policy.exec_mode`: absent means `filtered`, the ordered
// rule list. It is not called Mode because FilterPolicy.Mode is a different
// field with a different meaning — what happens to a command no rule matched.
func (f FilterPolicy) Tier() ExecMode {
	if f.ExecMode == "" {
		return ExecModeFiltered
	}
	return f.ExecMode
}

// Direction resolves `hop.connection`: absent means `dial`.
func (h *HopMetadata) Direction() HopConnection {
	if h == nil || h.Connection == "" {
		return HopConnectionDial
	}
	return h.Connection
}

// Cacheable resolves a CacheHint: absent, or a zero TTL, means do not cache.
// The proxy never invents a lifetime.
func (c *CacheHint) Cacheable() bool {
	return c != nil && c.TTLSeconds > 0
}

// MaxHeartbeatIntervalSeconds is the contract's ceiling on the revocation
// stream's heartbeat interval, in seconds.
//
// It is a number rather than a preference: the proxy's reconnect timeout
// defaults to 20s, so two consecutive intervals at this ceiling still fit
// inside it and ONE LOST HEARTBEAT IS NOT MISTAKEN FOR A DEAD STREAM. A server
// that advertises more than this and then honestly keeps to it passes its own
// claim and takes the whole fleet off cached decisions.
const MaxHeartbeatIntervalSeconds = 10

// MaxHeartbeatInterval is [MaxHeartbeatIntervalSeconds] as a duration.
const MaxHeartbeatInterval = MaxHeartbeatIntervalSeconds * time.Second

// AdvertisedHeartbeatInterval resolves `heartbeat_interval_seconds`: the
// interval the server says it is keeping now.
//
// It returns a BOOLEAN as well as the value, exactly as [AuthorizeResponse.Deadline]
// does, and for the same reason: absent means what every server did before the
// field existed — the reader falls back to its own timers — and a bare zero is
// a value a caller can misread as "no interval at all". A decoded event cannot
// tell an omitted key from `0`, so the distinction has to be made here or not
// at all.
//
// The value it returns may be used to notice a dead stream SOONER than the
// reader's own timeout and never to extend one. See the field's own
// documentation for why the inverse is a server silencing itself.
func (e *RevocationEvent) AdvertisedHeartbeatInterval() (time.Duration, bool) {
	if e == nil || e.HeartbeatIntervalSeconds <= 0 {
		return 0, false
	}
	return time.Duration(e.HeartbeatIntervalSeconds) * time.Second, true
}

// Declares resolves `capabilities` on the request: absent declares nothing, so a
// server choosing from it may only choose a rung needing no capability at all.
func (c *ProxyCapabilities) Declares() bool {
	return c != nil && (len(c.Execution) > 0 || len(c.Reach) > 0)
}

// Policed reports whether a RequestPolicy is policing in-channel requests at
// all. A nil receiver is an absent object — not policed; a non-nil one is an
// allow-list, and an empty allow-list denies every request.
func (p *RequestPolicy) Policed() bool { return p != nil }

// Policed reports whether a ForwardPolicy is policing destinations at all.
func (p *ForwardPolicy) Policed() bool { return p != nil }

// Policed reports whether a GlobalRequestPolicy is policing global requests at
// all. Absent relays every one; `{}` denies them all.
func (p *GlobalRequestPolicy) Policed() bool { return p != nil }
