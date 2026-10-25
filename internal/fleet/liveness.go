// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"errors"
	"time"

	"github.com/hoplock/control/internal/contract"
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
// event subscription (M9, `GET /v1/proxies/{id}/events`), on any node of the
// deployment.
//
// It is the SECOND liveness signal, and it is the stronger one: the subscription
// is a connection this deployment is holding open, so it cannot be believed on
// the strength of something a proxy said. A proxy with a live subscription is
// demonstrably reachable; an explicit heartbeat is what carries the health and
// capability detail the connection itself cannot.
//
// **0006 defines this interface and `internal/revoke` implements it.** The
// subscriptions live in the event broker, and a fleet package that reached into
// it would be the wrong package owning the one piece of genuinely shared state
// this system has. Since 0027 the answer covers every node: this node's own
// subscriptions, and a copy of every other node's that is at most two
// `events.sync_interval`s late, read from memory either way (M5). A nil
// SubscriptionState means "no second signal available", which leaves the
// heartbeat in charge — never "nothing is live".
type SubscriptionState interface {
	// LiveSubscriptions returns, for every proxy in the tenant holding a live
	// subscription, when it was last seen. A proxy absent from the map holds
	// none.
	//
	// It is a whole-tenant read for the graph load, which needs all of it
	// at once.
	LiveSubscriptions(ctx context.Context, tenant store.Tenant) (map[string]time.Time, error)
	// LiveSubscription is the same answer for one proxy: when it was last
	// seen, and whether it holds a subscription at all. It is the decision
	// path's read — the cache-hint gate asks it on every hinted answer — so
	// its cost does not grow with the deployment's subscription set (M5).
	// The fleet view asks it for each proxy of a page, for the same reason
	// (M26).
	LiveSubscription(ctx context.Context, tenant store.Tenant, proxyID string) (time.Time, bool, error)
}

// ConfigPublisher hands a configuration change to the event stream (0009).
//
// Delivery REUSES THE EVENT STREAM rather than inventing a second channel:
// proxies already hold one outbound subscription and must not need a second
// inbound path, which is the same reasoning that made the revocation stream
// outbound in the first place (proxy §6.4). The event is the contract's
// `config_changed` (proxy D18, Hoplock/proxy#65): it NAMES the proxy's composed
// document by version and hash and never carries it, because the stream is
// replayable and a replayed document would be a stale one applied as if
// current. The daemon wires it to the revocation bus.
type ConfigPublisher interface {
	// PublishConfigChange tells one proxy that its desired configuration
	// moved. A nil error means the stream took it: the event is logged, so
	// the node holding the proxy's stream delivers it, whichever node that
	// is, and a proxy that reconnects anywhere is replayed it — or answered
	// `resync`, and fetches, as it does on every reconnect. That settles an
	// announcement (0027): before the log was shared, a node's bus reached
	// only its own subscriptions, and only a delivery there could.
	PublishConfigChange(ctx context.Context, tenant store.Tenant, proxyID string, ev contract.ConfigChangedEvent) error
}

// noopConfigPublisher is what a Registry uses when none is supplied: a
// Registry built without a stream, in a test or a tool that publishes nothing.
//
// It answers an error rather than nil, so an announcement it could not make is
// never recorded as made: the marker stays behind and the announcer owes it,
// which is the honest state for a registry with no stream to announce on.
type noopConfigPublisher struct{}

func (noopConfigPublisher) PublishConfigChange(context.Context, store.Tenant, string, contract.ConfigChangedEvent) error {
	return errNoConfigPublisher
}

var errNoConfigPublisher = errors.New("fleet: no event stream is wired to announce configuration on")
