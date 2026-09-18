// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"time"

	"github.com/hoplock/control/internal/store"
)

// Default staleness bounds.
//
// STALE IS A PRECISE WORD HERE, and it has to be: a proxy that has not been
// heard from is not a routing option, and routing through one is an outage the
// user experiences as a hang while their handshake is held open. So each bound
// is a number, each is configurable, and none of them is "recently".
//
// The bounds are generous relative to a healthy heartbeat interval on purpose. A
// proxy is dropped from routing for being silent, which is the correct answer to
// a dead proxy and the wrong answer to one packet lost, so the window has to be
// several intervals wide.
const (
	// DefaultHeartbeatTTL is how long a proxy may be silent and still be a
	// routing option.
	DefaultHeartbeatTTL = 90 * time.Second
	// DefaultRelayRegistrationTTL is how long a reported relay registration
	// stays believable. It is shorter than the heartbeat TTL because a
	// registration is a live TCP connection rather than a fact about a
	// configuration: it is the thing most likely to have gone away silently,
	// and a relay hop to a dead registration hangs.
	DefaultRelayRegistrationTTL = 60 * time.Second
)

// Liveness is the staleness rule, in one value so that it is configured rather
// than scattered.
type Liveness struct {
	// HeartbeatTTL bounds how long since a proxy was last heard from.
	HeartbeatTTL time.Duration
	// RelayRegistrationTTL bounds how long since a relay registration was
	// last confirmed.
	RelayRegistrationTTL time.Duration
	// TargetCapabilityTTL bounds how long a target capability observation
	// stays fresh (M17).
	TargetCapabilityTTL time.Duration
	// ReportAfter is the interval this server asks a proxy to re-observe on.
	// The server owns the freshness of its own record: a proxy may re-observe
	// sooner, never later.
	ReportAfter time.Duration
}

// DefaultLiveness is the rule with nothing configured.
func DefaultLiveness() Liveness {
	return Liveness{
		HeartbeatTTL:         DefaultHeartbeatTTL,
		RelayRegistrationTTL: DefaultRelayRegistrationTTL,
		TargetCapabilityTTL:  DefaultTargetCapabilityTTL,
		ReportAfter:          DefaultReportAfter,
	}
}

// withDefaults fills in anything a caller left at zero. A zero TTL would make
// everything stale and take the whole fleet out of routing, so the zero value
// means "unset" rather than "immediately stale".
func (l Liveness) withDefaults() Liveness {
	d := DefaultLiveness()
	if l.HeartbeatTTL <= 0 {
		l.HeartbeatTTL = d.HeartbeatTTL
	}
	if l.RelayRegistrationTTL <= 0 {
		l.RelayRegistrationTTL = d.RelayRegistrationTTL
	}
	if l.TargetCapabilityTTL <= 0 {
		l.TargetCapabilityTTL = d.TargetCapabilityTTL
	}
	if l.ReportAfter <= 0 {
		l.ReportAfter = d.ReportAfter
	}
	return l
}

// ProxyIsLive reports whether a proxy is a routing option at instant now.
//
// Two conditions, both necessary: it is enrolled — pending and revoked are not
// routable, and a revoked proxy that kept heartbeating must not come back by
// itself — and it has been heard from inside the heartbeat TTL.
//
// A proxy that has NEVER reported has a zero heartbeat and is not live. That is
// the right answer even immediately after enrollment: enrollment says an
// operator approved this proxy, not that it is running.
func (l Liveness) ProxyIsLive(p store.Proxy, now time.Time) bool {
	if p.State != store.EnrollmentEnrolled {
		return false
	}
	if p.LastHeartbeatAt.IsZero() {
		return false
	}
	return !now.After(p.LastHeartbeatAt.Add(l.HeartbeatTTL))
}

// RelayIsLive reports whether a reported relay registration still counts.
func (l Liveness) RelayIsLive(reg store.RelayRegistration, now time.Time) bool {
	if reg.LastSeenAt.IsZero() {
		return false
	}
	return !now.After(reg.LastSeenAt.Add(l.RelayRegistrationTTL))
}

// SubscriptionState reports which proxies currently hold the long-lived outbound
// event subscription (M9, `GET /v1/proxies/{id}/events`).
//
// It is the SECOND liveness signal, and it is the stronger one: the subscription
// is a connection this server is holding open, so it cannot be believed on the
// strength of something a proxy said. A proxy with a live subscription is
// demonstrably reachable; an explicit heartbeat is what carries the health and
// capability detail the connection itself cannot.
//
// **0006 defines this interface and 0009 implements it.** The subscription lives
// in the event broker, which is 0009's, and a fleet package that reached into it
// would be the wrong package owning the one piece of genuinely shared state this
// system has. A nil SubscriptionState means "no second signal available", which
// leaves the heartbeat in charge — never "nothing is live".
type SubscriptionState interface {
	// LiveSubscriptions returns, for every proxy in the tenant holding a live
	// subscription, when it was last seen. A proxy absent from the map holds
	// none.
	//
	// It is a whole-tenant read rather than a per-proxy one because the graph
	// load needs all of it at once and the fleet is small; asking per proxy
	// would put N calls on the decision path (M5).
	LiveSubscriptions(ctx context.Context, tenant store.Tenant) (map[string]time.Time, error)
}

// ConfigPublisher hands a configuration change to the event stream (0009).
//
// Delivery REUSES THE EVENT STREAM rather than inventing a second channel:
// proxies already hold one outbound subscription and must not need a second
// inbound path, which is the same reasoning that made the revocation stream
// outbound in the first place (proxy §6.4).
//
// **This seam cannot be connected to the wire yet, and that is a finding rather
// than an omission.** `contract/control.yaml` enumerates the event types
// `session_kill`, `cache_invalidate`, `heartbeat` and `resync`, and NONE of them
// can carry a configuration change. The contract is owned upstream and vendored
// read-only (M1), so adding one is a change in `hoplock/proxy` — an event type
// (or a field on the heartbeat event) naming the proxy's desired config version
// and hash, which the proxy answers by fetching and then reporting what it
// runs. Until that lands, everything below the wire is built and tested here and
// the publisher is a no-op an operator can see: the desired version is stored,
// the drift is visible in the fleet view and in the API, and a proxy that has
// not caught up is reported rather than assumed current.
//
// Inventing the event type locally was the alternative, and it is the one M1
// exists to prevent: it would make CI green here while the two components
// silently disagreed about what a `config` event is.
type ConfigPublisher interface {
	// PublishConfigChange tells a proxy that its desired configuration moved.
	PublishConfigChange(ctx context.Context, tenant store.Tenant, proxyID string, version int64, hash string) error
}

// noopConfigPublisher is what a Registry uses when none is supplied.
//
// It returns nil rather than an error: a configuration publish that failed
// because nothing is wired up must not fail the publish itself, or an operator
// could not stage a rollout before the stream exists. What makes that safe is
// that the desired version is durable and the drift is visible — the proxy finds
// out late rather than never.
type noopConfigPublisher struct{}

func (noopConfigPublisher) PublishConfigChange(context.Context, store.Tenant, string, int64, string) error {
	return nil
}
