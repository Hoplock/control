// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"time"
)

// ProxyEnrollment is the grant a proxy enrolls against (0006).
//
// Enrollment is an administrative act: an operator pre-registers the id, the
// zones it may claim, and a one-time token. A proxy that could enroll itself
// into any zone would let anyone who can reach this server insert a hop into
// other people's routes.
type ProxyEnrollment struct {
	// ProxyID is the id this grant is for.
	ProxyID string
	// GrantedZones are the zones the proxy may claim. Empty grants nothing,
	// which is the fail-safe reading of a half-filled row.
	GrantedZones []string
	// TokenHash is the SHA-256 of the token's secret half. The secret is
	// never stored, so a dump of this table cannot enroll anything.
	TokenHash []byte
	// ExpiresAt bounds how long the token may be used. Zero means no bound.
	ExpiresAt time.Time
	// ConsumedAt is when the token was used. Non-zero means the grant is
	// spent: a second enrollment with the same token is refused.
	ConsumedAt time.Time
	// CreatedBy names the operator that issued it.
	CreatedBy string

	CreatedAt time.Time
}

// HopDirection is how one proxy reaches the next (proxy D11). The values match
// the contract's `hop.connection`; this package does not import the contract,
// because a storage column that changes when a wire enum is renamed is a
// migration bought for nothing.
type HopDirection string

const (
	// HopDial means this proxy opens a connection to the next one, which
	// needs an inbound rule at the far end.
	HopDial HopDirection = "dial"
	// HopRelay means the far proxy has already registered an outbound relay
	// connection with this one, so the protected zone needs no inbound rule.
	// A relay edge with no live registration is never downgraded to a dial.
	HopRelay HopDirection = "relay"
)

// ProxyEdge is one declared reachability edge: this proxy can reach this zone,
// this way, at this cost (M6).
type ProxyEdge struct {
	// ProxyID is the proxy that declared the edge.
	ProxyID string
	// ToZone is the zone it reaches.
	ToZone string
	// Direction is dial or relay.
	Direction HopDirection
	// Address is where to dial. Required on a dial edge with no pinned next
	// proxy, meaningless on a relay one.
	Address string
	// NextProxyID pins the proxy this edge reaches. Empty means "whichever
	// live proxy in ToZone", resolved deterministically at path time.
	NextProxyID string
	// Cost orders equal-viability paths. Lower wins.
	Cost int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// RelayRegistration records that a downstream proxy currently holds an
// outbound relay connection open to an upstream one.
//
// The registration is proxy-to-proxy plumbing and not part of the contract
// (proxy §6.1). What this server needs is the fact of it, reported by the
// upstream, because that fact is what makes a relay edge viable — and this
// server is the only party that knows it for the whole fleet, which is why
// direction is a routing decision and not a proxy config flag.
type RelayRegistration struct {
	// UpstreamProxyID is the proxy holding the listener.
	UpstreamProxyID string
	// DownstreamProxyID is the proxy that registered to it.
	DownstreamProxyID string
	// RegisteredAt is when the link was first seen.
	RegisteredAt time.Time
	// LastSeenAt is when it was last confirmed. This is what goes stale.
	LastSeenAt time.Time
}

// ConfigScopeKind says whether a configuration document applies to a zone or
// to one proxy. Closed set: a scope the server does not recognise is not one it
// can compose.
type ConfigScopeKind string

const (
	// ConfigScopeZone is everything a zone's members share.
	ConfigScopeZone ConfigScopeKind = "zone"
	// ConfigScopeProxy is the overrides one member needs.
	ConfigScopeProxy ConfigScopeKind = "proxy"
)

// ConfigScope identifies what a configuration document applies to.
type ConfigScope struct {
	Kind ConfigScopeKind
	// ID is the zone name or the proxy id, per Kind.
	ID string
}

// ProxyConfigVersion is one immutable version of a scope's configuration
// document.
//
// Rows are never updated. Rolling out a bad config must be survivable, and a
// version that can be edited in place is a rollback target that lies.
type ProxyConfigVersion struct {
	Scope ConfigScope
	// Version is monotonic within the tenant and scope.
	Version int64
	// Document is the configuration as published, stored as the exact bytes
	// given. The column is text rather than jsonb so that those bytes survive
	// the round trip: jsonb re-renders whitespace and key order, and Hash below
	// would stop being the digest of what comes back.
	Document json.RawMessage
	// Hash is the digest of Document's exact bytes, computed by the caller so
	// the value stored is the one the publisher was told.
	Hash string
	// CreatedBy names the principal that published it.
	CreatedBy string

	CreatedAt time.Time
}

// ProxyConfigDesired is which version of a scope is currently published.
type ProxyConfigDesired struct {
	Scope ConfigScope
	// Version is the published version.
	Version int64
	// PreviousVersion is what this scope was on before, and therefore what a
	// rollback republishes. Zero means there was nothing before.
	PreviousVersion int64
	// PublishedBy names the principal that published it.
	PublishedBy string

	PublishedAt time.Time
}

// ProxyConfigState is the composed document one proxy should be running, and
// what it says it is running.
//
// Drift between the two is a value rather than a derivation because silent
// drift across a fleet is indistinguishable from a broken rollout.
type ProxyConfigState struct {
	ProxyID string
	// DesiredVersion is monotonic per proxy and names the composed document.
	DesiredVersion  int64
	DesiredHash     string
	DesiredDocument json.RawMessage
	DesiredAt       time.Time
	// RunningVersion is what the proxy reported. Zero means it never has.
	RunningVersion int64
	RunningHash    string
	ReportedAt     time.Time
}

// Drifted reports whether the proxy is running something other than what it
// was told to run.
func (s ProxyConfigState) Drifted() bool { return s.DesiredVersion != s.RunningVersion }

// TargetCapabilityRecord is what one TARGET can take, as a proxy found it by
// probing (M17).
//
// It is an observation and grants nothing: the authority for a rung is the
// authorize response, and the proxy re-checks the rung against the live target
// when it provisions. So the worst a stale record can cause is a refused
// session, never a session running below the rung its own audit record claims.
type TargetCapabilityRecord struct {
	// Hostname and Port name the target, exactly as the proxy reported them.
	Hostname string
	Port     int32
	// Platform is the driver the target was observed through, empty where
	// none was reported. It is part of the key: two drivers can see the same
	// host differently.
	Platform string
	// Execution and Reach are the rungs observed, stored as reported. The
	// vocabulary is the contract's, and this layer does not police it — an
	// unknown value is a value that matches nothing, not a write to refuse.
	Execution []string
	Reach     []string
	// ObservedAt is when the proxy saw this. ZERO IS A REAL STATE and means
	// undated, which is treated as stale: a capability with no date has no
	// shelf life. Storing an invented date here is the fail-open the rule
	// exists to prevent.
	ObservedAt time.Time
	// Detail is whatever the driver added, carried opaquely.
	Detail map[string]string
	// ReportedBy is the proxy that reported it.
	ReportedBy string

	ReceivedAt time.Time
}
