// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/policy/model"
)

// Routing: where the answer starts from, and the three things the hop trail is
// for.
//
// `conn.hop_trail` is the ONLY view this server has of a chain. Every hop of a
// chain calls this endpoint for itself, with the same identity and the same
// final target (proxy D2), so two calls that mean entirely different things are
// byte-identical apart from `conn.proxy_id` and the trail. Three consequences,
// each built here rather than noted:
//
//   - the path starts at `conn.proxy_id`, the proxy ASKING, and never at the
//     user's entry proxy, which on a chained call is the first id in the trail
//     and is somewhere else entirely;
//   - a repeat — the asking proxy in its own trail, or a next hop already in it
//     — is a cycle in the estate's routing, which is an OUTAGE and never a
//     `401` (M11);
//   - `len(hop_trail)` is the hop count so far, and it spends the budget the
//     cap allows, so this server keeps its own answers inside `max_hops`
//     instead of relying on the proxy to notice.
//
// The trail carries no authority and is never given any. Every entry in it can
// only cause a refusal, which is exactly what makes it safe to accept from a
// caller: a forged trail restricts the forger.

// routed is the outcome of turning a snapshot plus the fleet graph into a
// route the response can carry.
type routed struct {
	// routeType is `direct` or `nexthop`.
	routeType contract.RouteType
	// target and port are what the proxy connects to: the end host on a
	// direct route, the next proxy on a chained one.
	target string
	port   int32
	// hop is the chaining metadata, nil on a direct route.
	hop *contract.HopMetadata
	// nextProxyID is the proxy the first hop reaches, empty when direct. It
	// is kept out of the hop metadata as well as in it because the loop
	// check reads it before the metadata is built.
	nextProxyID string
}

// route computes the path for one decision.
//
// Every failure here is an OUTAGE. That is the M11 assertion that matters most
// in this phase, because "deny" is the tempting shortcut: a user refused by
// policy and a user unreachable because an enclave relay is down must not get
// the same answer, and only one of them is a decision somebody made.
func (s *Service) route(g *fleet.Graph, snap *model.Snapshot, req *contract.AuthorizeRequest, in assembled) (routed, error) {
	entry := fleet.EntryPoint{ProxyID: req.Conn.ProxyID, HopTrail: in.trail}

	if !in.known {
		// A target with no row has no zone, and a zone this server cannot
		// name is a destination it cannot route to. Refusing is the only
		// honest answer: picking the asking proxy's own zone would answer
		// `direct` for every host anybody typed.
		return routed{}, &RouteError{
			Reason: "target-unknown",
			Err: fmt.Errorf("decision: no target record for %q, so its zone is unknown",
				snap.Target),
		}
	}

	hops, err := g.Path(entry, fleet.Destination{
		Zone:     in.targetZone,
		Hostname: snap.Target,
		Port:     snap.Port,
	})
	if err != nil {
		return routed{}, &RouteError{Reason: noPathReason(err), Err: err}
	}

	if len(hops) == 0 {
		return routed{
			routeType: contract.RouteTypeDirect,
			target:    snap.Target,
			port:      snap.Port,
		}, nil
	}

	// A route the policy authored as `direct` may not traverse a hop, and
	// the estate saying one is needed does not change what was authored.
	//
	// This is a DENIAL rather than an outage, and the difference is who has
	// to act: the estate is healthy, the path exists, and the reason the
	// session is refused is a rule somebody wrote. That is the same class as
	// a concurrency cap being exceeded (PLAN §5.2) and the opposite class
	// from a relay being down.
	if snap.Intent == model.RouteIntentDirect {
		return routed{}, &IntentError{
			Target:    snap.Target,
			NextProxy: hops[0].NextProxyID,
		}
	}

	first := hops[0]

	// The loop refusal, checked against the answer this server is about to
	// give rather than only against what it was handed. The graph already
	// refuses a path through a visited proxy, so this is a second net over
	// the one field the proxy will act on — and a cheap one, because the
	// cost of getting it wrong is a chain that closes on itself.
	if first.NextProxyID == req.Conn.ProxyID || slices.Contains(in.trail, first.NextProxyID) {
		return routed{}, &RouteError{
			Reason: string(fleet.NoPathLoop),
			Err: fmt.Errorf("decision: next hop %q is already in the trail %v",
				first.NextProxyID, in.trail),
		}
	}

	out := routed{
		routeType:   contract.RouteTypeNexthop,
		nextProxyID: first.NextProxyID,
		hop: &contract.HopMetadata{
			Connection:  first.Connection,
			NextProxyID: first.NextProxyID,
			FinalTarget: snap.Target,
			MaxHops:     int32(g.MaxHops()),
			// "Hop trail to forward to the next proxy, THIS PROXY
			// APPENDED" (contract, HopMetadata.hop_trail). Not the
			// proxies further along the computed path: they have not
			// been traversed, and each appends itself when it asks for
			// its own leg. A trail that ran ahead of the session would
			// make the next proxy find itself already in its own
			// incoming trail and refuse the chain as a loop.
			HopTrail: fleet.Trail(entry, nil),
		},
	}
	out.target, out.port = hopEndpoint(first)
	return out, nil
}

// hopEndpoint is what the proxy connects to for the first hop.
//
// On a `dial` hop it is the address the edge declared, split into host and
// port because the contract carries them apart. On a `relay` hop there is
// nothing to dial — the next proxy has already registered an outbound
// connection and this proxy opens a channel over it — so `target` names the
// peer instead. The contract requires the field to be present either way, and
// a relay hop's proxy reads `next_proxy_id` rather than this.
func hopEndpoint(h fleet.Hop) (string, int32) {
	if h.Connection == contract.HopConnectionRelay || h.Address == "" {
		return h.NextProxyID, 0
	}
	host, portText, err := net.SplitHostPort(h.Address)
	if err != nil {
		return h.Address, 0
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return host, 0
	}
	return host, int32(port)
}

// RouteError is the fleet being unable to carry this decision.
//
// It is an outage and carries the fleet's own reason — `no-live-route`,
// `entry-unknown`, `entry-not-live`, `loop`, `max-hops`, `search-bound`, or
// `target-unknown` — because that reason is what tells an operator whether to
// look at a dead proxy or at a topology.
type RouteError struct {
	Reason string
	Err    error
}

func (e *RouteError) Error() string {
	return fmt.Sprintf("decision: no route (%s): %v", e.Reason, e.Err)
}

func (e *RouteError) Unwrap() error { return e.Err }

// IntentError is a route the policy authored as direct-only for a target that
// is not directly reachable from the asking proxy.
//
// It is a DENIAL. Nothing is broken: the estate is healthy and the answer is
// "no", authored by whoever wrote `intent: direct`.
type IntentError struct {
	Target    string
	NextProxy string
}

func (e *IntentError) Error() string {
	return fmt.Sprintf("decision: the rule permits no hops and %s is reachable only via %s",
		e.Target, e.NextProxy)
}

// noPathReason renders the fleet's reason for a refusal.
func noPathReason(err error) string {
	if np, ok := fleet.NoPath(err); ok {
		return string(np.Reason)
	}
	return "unavailable"
}
