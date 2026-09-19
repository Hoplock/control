// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"cmp"
	"fmt"
	"slices"
	"sort"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// DefaultMaxHops caps how many proxies one session may traverse.
//
// It is 4 because the proxy's routing.DefaultMaxHops is 4, and the two must
// agree: the proxy enforces the cap as a safety net against a bad answer, and
// this server should not give one. A server that answered with five hops would
// have the proxy refuse a session this server said was fine, which is an outage
// in front of a user rather than a message to an operator.
//
// The count is proxies, not edges: the asking proxy and every proxy the chain
// still has to traverse, plus whatever the trail already holds.
const DefaultMaxHops = 4

// DefaultSearchBound caps how many candidate paths the search will consider.
//
// Pathfinding is on the decision path (M5), so it must answer rather than run:
// a loop-free best-first search over a pathological graph is exponential in the
// hop cap, and a bound that returns an explicit no-path is strictly better than
// one that holds a user's SSH handshake open while it explores. Real fleets are
// nowhere near it — a proxy declares a handful of edges — so hitting it is a
// topology to look at, which is why the outcome names itself.
const DefaultSearchBound = 10000

// Zone is a routing zone. Zones are per tenant (M18), so two tenants may both
// name one `prod` and mean different things; zone names are deliberately NOT
// globally unique, because that would be a customer-visible constraint invented
// to simplify an internal lookup.
type Zone string

// Edge is one declared reachability edge: this node can reach this zone, this
// way, at this cost.
type Edge struct {
	// ToZone is the zone reached.
	ToZone Zone
	// Connection is how (proxy D11). Dial opens a connection to Address;
	// relay opens a channel over a registration the far proxy already holds
	// with this one.
	Connection contract.HopConnection
	// Address is where to dial, on a dial edge.
	Address string
	// NextProxyID pins the proxy this edge reaches. Empty means "whichever
	// live proxy in ToZone", resolved by the stable tiebreak below.
	NextProxyID string
	// Cost orders viable paths. Lower wins.
	Cost int
}

// Node is one proxy as the graph sees it.
type Node struct {
	// ProxyID identifies the proxy within the tenant.
	ProxyID string
	// Zone is the zone it serves.
	Zone Zone
	// Live is whether it is a routing option at all: enrolled, and heard from
	// recently enough (see Staleness). A proxy that has not been heard from is
	// not a routing option, and routing through one is an outage the user
	// experiences as a hang.
	Live bool
	// Edges is what it declared it can reach.
	Edges []Edge
	// Relays are the downstream proxy ids that currently hold a live outbound
	// relay registration WITH THIS NODE. It is what makes a relay edge viable,
	// and only this server knows it for the whole fleet — which is exactly why
	// direction is a routing decision and not a proxy config flag.
	Relays []string
	// Capabilities is what this proxy's build declares it can provide (M17).
	// Pathfinding does not read it; the pre-publish query and 0008 do.
	Capabilities Capabilities
}

// EntryPoint is where a path starts: the proxy that is ASKING, which is not
// necessarily where the user arrived.
//
// Each hop asks for itself (M6, proxy D2): the entry proxy is where the user
// arrived, and the proxy asking on the second leg is one step further in. A
// Path that could only start at a user's entry proxy would be one 0008 has to
// work around, so the signature takes a real asking point from the start.
type EntryPoint struct {
	// ProxyID is the proxy asking — the authorize request's `conn.proxy_id`.
	ProxyID string
	// HopTrail is the proxy ids the session has ALREADY traversed, oldest
	// first, from the request's `conn.hop_trail`. It is empty on a user's
	// first hop.
	//
	// It carries no authority and can only ever narrow the answer (PLAN §4):
	// every id in it removes a proxy from the search and brings the hop cap
	// closer, so a forged trail costs its sender the session.
	HopTrail []string
}

// Destination is where a path must end.
type Destination struct {
	// Zone is the target's zone. A path ends at any live proxy in it.
	Zone Zone
	// Hostname and Port are the final target, carried into the hop metadata
	// so every hop in the chain names the same endpoint.
	Hostname string
	Port     int32
}

// Hop is one step of a path — one edge, resolved onto a concrete proxy.
type Hop struct {
	// FromProxyID is the proxy that makes this step.
	FromProxyID string
	// NextProxyID is the proxy it reaches, and on a relay hop it names the
	// registration the channel is opened over.
	NextProxyID string
	// Zone is the zone NextProxyID serves.
	Zone Zone
	// Connection is the direction (proxy D11). It is never inferred by either
	// proxy and never downgraded here.
	Connection contract.HopConnection
	// Address is where FromProxyID dials, on a dial hop. Empty on a relay hop,
	// which opens no new connection.
	Address string
	// Cost is the declared cost of this edge.
	Cost int
}

// Graph is one tenant's fleet.
//
// It is a value, built from one tenant's rows and never mutated. A cross-tenant
// edge is therefore not pruned late — it cannot be constructed, because the
// only thing that builds a Graph from storage is [Registry.Graph], and every
// repository method it calls names the tenant (M18).
type Graph struct {
	tenant      store.Tenant
	nodes       map[string]Node
	byZone      map[Zone][]string
	maxHops     int
	searchBound int
}

// GraphOption configures a Graph at construction.
type GraphOption func(*Graph)

// WithMaxHops overrides DefaultMaxHops. A non-positive value is ignored, so a
// zero-valued config cannot accidentally forbid every route.
func WithMaxHops(n int) GraphOption {
	return func(g *Graph) {
		if n > 0 {
			g.maxHops = n
		}
	}
}

// WithSearchBound overrides DefaultSearchBound, on the same terms.
func WithSearchBound(n int) GraphOption {
	return func(g *Graph) {
		if n > 0 {
			g.searchBound = n
		}
	}
}

// NewGraph builds a tenant's graph from its nodes.
//
// It rejects a duplicate proxy id rather than picking one, because two rows
// claiming one id is a registry that cannot say where a session went, and
// answering from either is worse than refusing.
func NewGraph(tenant store.Tenant, nodes []Node, opts ...GraphOption) (*Graph, error) {
	if tenant == "" {
		return nil, fmt.Errorf("fleet.NewGraph: tenant is required")
	}

	g := &Graph{
		tenant:      tenant,
		nodes:       make(map[string]Node, len(nodes)),
		byZone:      make(map[Zone][]string),
		maxHops:     DefaultMaxHops,
		searchBound: DefaultSearchBound,
	}
	for _, opt := range opts {
		opt(g)
	}

	for _, n := range nodes {
		if n.ProxyID == "" {
			return nil, fmt.Errorf("fleet.NewGraph: a node has no proxy id")
		}
		if _, dup := g.nodes[n.ProxyID]; dup {
			return nil, fmt.Errorf("fleet.NewGraph: proxy %q appears twice", n.ProxyID)
		}
		// Defensive copies: a Graph is a value, and a caller that keeps its
		// slice must not be able to change a route after the fact.
		n.Edges = slices.Clone(n.Edges)
		n.Relays = slices.Clone(n.Relays)
		slices.Sort(n.Relays)
		sortEdges(n.Edges)

		g.nodes[n.ProxyID] = n
		g.byZone[n.Zone] = append(g.byZone[n.Zone], n.ProxyID)
	}
	for zone := range g.byZone {
		slices.Sort(g.byZone[zone])
	}
	return g, nil
}

// sortEdges puts a node's edges in a stable order. It is part of the tiebreak:
// two servers holding the same rows must expand them identically.
func sortEdges(edges []Edge) {
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.Cost != b.Cost {
			return a.Cost < b.Cost
		}
		if a.ToZone != b.ToZone {
			return a.ToZone < b.ToZone
		}
		if a.Connection != b.Connection {
			return a.Connection < b.Connection
		}
		return a.NextProxyID < b.NextProxyID
	})
}

// Tenant reports whose graph this is.
func (g *Graph) Tenant() store.Tenant { return g.tenant }

// MaxHops reports the cap in force, which is also what the hop metadata carries
// so the proxy can enforce the same number.
func (g *Graph) MaxHops() int { return g.maxHops }

// Node returns one node. It exists for the fleet view and for 0008's
// "is the asking proxy someone I know" check.
func (g *Graph) Node(proxyID string) (Node, bool) {
	n, ok := g.nodes[proxyID]
	return n, ok
}

// LiveProxiesInZone returns the live proxies serving a zone, in id order.
func (g *Graph) LiveProxiesInZone(zone Zone) []string {
	var out []string
	for _, id := range g.byZone[zone] {
		if g.nodes[id].Live {
			out = append(out, id)
		}
	}
	return out
}

// Path returns the shortest viable path from the asking proxy to the
// destination zone, as a sequence of hops.
//
// An EMPTY, non-nil result is a direct route: the asking proxy already serves
// the destination zone, so there is nothing to chain and 0008 answers
// `route_type: direct`. A non-empty result is `nexthop`, and its first hop is
// the step this proxy takes.
//
// "Viable" is three things at once, and none of them is optional:
//
//   - every proxy on the path is live (enrolled and heard from recently);
//   - every relay edge has a live registration from the proxy it reaches. A
//     relay edge without one is NOT selected and is NEVER downgraded to a dial
//     (proxy D11) — the downgrade would open the inbound path the mode exists
//     to avoid;
//   - no proxy appears twice, counting the trail the session already carries,
//     and the whole chain fits inside MaxHops.
//
// Every failure is a *NoPathError, which is an OUTAGE and never a deny (M11).
// That distinction is the point: a user denied by policy and a user unreachable
// because an enclave relay is down must not get the same answer.
func (g *Graph) Path(entry EntryPoint, dest Destination) ([]Hop, error) {
	if entry.ProxyID == "" {
		return nil, &NoPathError{Entry: entry, Destination: dest, Reason: NoPathEntryUnknown}
	}
	from, ok := g.nodes[entry.ProxyID]
	if !ok {
		// A proxy this tenant's graph does not contain. It may well exist in
		// another tenant's — which is exactly why this is not a lookup that
		// widens to find it.
		return nil, &NoPathError{Entry: entry, Destination: dest, Reason: NoPathEntryUnknown}
	}
	if !from.Live {
		return nil, &NoPathError{Entry: entry, Destination: dest, Reason: NoPathEntryNotLive}
	}

	// The trail already traversed both removes proxies from the search and
	// spends the hop budget.
	//
	// Any repeat in it is a loop that has ALREADY happened — this proxy's own id,
	// or one proxy appearing twice further back — and it is refused rather than
	// routed around. The trail may only ever narrow (PLAN §4), so a malformed one
	// costs its sender the session; treating a duplicate as one entry would make
	// it buy hop budget instead.
	visited := make(map[string]bool, len(entry.HopTrail)+1)
	visited[entry.ProxyID] = true
	for _, id := range entry.HopTrail {
		if visited[id] {
			return nil, &NoPathError{Entry: entry, Destination: dest, Reason: NoPathLoop}
		}
		visited[id] = true
	}

	// Already there: a direct route.
	if from.Zone == dest.Zone {
		return []Hop{}, nil
	}

	// The budget is proxies, not edges: the trail, this proxy, and every hop
	// still to come. It counts trail POSITIONS rather than distinct ids, the same
	// way the proxy counts them.
	budget := g.maxHops - len(entry.HopTrail) - 1
	if budget <= 0 {
		return nil, &NoPathError{Entry: entry, Destination: dest, Reason: NoPathMaxHops}
	}

	hops, reason := g.search(from, dest, visited, budget)
	if hops == nil {
		return nil, &NoPathError{Entry: entry, Destination: dest, Reason: reason}
	}
	return hops, nil
}

// candidate is one partial path in the search.
type candidate struct {
	at      string
	hops    []Hop
	cost    int
	visited map[string]bool
	// ids is the proxy-id sequence of hops, used by the tiebreak. It is kept
	// beside hops rather than recomputed because the comparator runs far more
	// often than the queue grows.
	ids []string
}

// search is a bounded, loop-free, uniform-cost best-first walk.
//
// A goal-reaching candidate is QUEUED rather than returned on sight, and the
// answer is the first goal candidate popped. Returning the first goal found
// while expanding the cheapest candidate would not be the cheapest path: a
// dearer candidate can reach the destination over a cheaper edge, and "shortest
// viable path" has to mean it.
//
// The ordering is a TOTAL order on candidate paths — (cost, hop count, then the
// proxy-id sequence lexicographically) — which is what makes the answer
// deterministic. Two nodes holding the same rows pop the same candidates in the
// same order and therefore agree, which matters because they are answering the
// same question for the same session and a disagreement builds a chain neither
// of them predicted.
func (g *Graph) search(from Node, dest Destination, visited map[string]bool, budget int) ([]Hop, NoPathReason) {
	queue := []candidate{{at: from.ProxyID, visited: visited}}
	explored := 0
	// Whether anything was cut short by the cap rather than by viability. It
	// changes the reason reported, and the reason is what tells an operator
	// whether to look at a topology or at a dead proxy.
	truncated := false

	for len(queue) > 0 {
		// Pop the smallest. The queue is kept sorted rather than heaped: it is
		// bounded by searchBound, and a sorted slice makes the total order
		// readable, which is worth more here than the constant factor.
		cur := queue[0]
		queue = queue[1:]

		explored++
		if explored > g.searchBound {
			return nil, NoPathSearchBound
		}

		node := g.nodes[cur.at]
		if len(cur.hops) > 0 && node.Zone == dest.Zone {
			return cur.hops, ""
		}

		if len(cur.hops)+1 > budget {
			// This candidate cannot be extended at all. Say so once rather
			// than per edge.
			truncated = true
			continue
		}

		for _, edge := range node.Edges {
			// EVERY viable member of the zone is a branch, not just the first
			// one. An edge names a zone, and a zone can hold a member with no
			// onward reachability beside one that has it: resolving an edge to a
			// single proxy would dead-end on the first and never find the path
			// through the second, which is not "shortest viable path".
			for _, next := range g.resolveEdge(node, edge, cur.visited) {
				hop := Hop{
					FromProxyID: cur.at,
					NextProxyID: next.ProxyID,
					Zone:        next.Zone,
					Connection:  edge.Connection,
					Cost:        edge.Cost,
				}
				// A relay hop opens no connection, so it carries no address. An
				// address on one would be a dial waiting to be attempted.
				if edge.Connection != contract.HopConnectionRelay {
					hop.Address = edge.Address
				}

				seen := make(map[string]bool, len(cur.visited)+1)
				for id := range cur.visited {
					seen[id] = true
				}
				seen[next.ProxyID] = true

				queue = append(queue, candidate{
					at:      next.ProxyID,
					hops:    append(slices.Clone(cur.hops), hop),
					cost:    cur.cost + edge.Cost,
					visited: seen,
					ids:     append(slices.Clone(cur.ids), next.ProxyID),
				})
			}
		}
		sortCandidates(queue)
	}

	if truncated {
		return nil, NoPathMaxHops
	}
	return nil, NoPathNoLiveRoute
}

// sortCandidates imposes the total order the determinism guarantee rests on.
func sortCandidates(queue []candidate) {
	slices.SortStableFunc(queue, func(a, b candidate) int {
		if c := cmp.Compare(a.cost, b.cost); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.hops), len(b.hops)); c != 0 {
			return c
		}
		return slices.Compare(a.ids, b.ids)
	})
}

// resolveEdge returns every proxy a declared edge can currently reach, in id
// order.
//
// This is where a relay edge is accepted or dropped, and it is the only place
// that decides direction. There is deliberately NO FALLBACK: an unviable relay
// edge is unviable, full stop. Downgrading it to a dial would open the inbound
// path into a protected zone that the relay mode exists to avoid, and it would do
// so silently, at connect time, on a route an operator believed was relay-only.
func (g *Graph) resolveEdge(from Node, edge Edge, visited map[string]bool) []Node {
	candidates := g.byZone[edge.ToZone]
	if edge.NextProxyID != "" {
		candidates = []string{edge.NextProxyID}
	}

	var out []Node
	for _, id := range candidates {
		if visited[id] {
			continue
		}
		next, ok := g.nodes[id]
		if !ok || !next.Live {
			continue
		}
		// A pinned next proxy must actually serve the zone the edge names. An
		// operator who moved a proxy between zones has an edge that no longer
		// means what it says, and routing over it would send a session somewhere
		// nobody authored.
		if next.Zone != edge.ToZone {
			continue
		}
		if edge.Connection == contract.HopConnectionRelay {
			// The far proxy must hold a live outbound registration with THIS
			// proxy. No registration, no hop — and never a dial instead.
			if !slices.Contains(from.Relays, id) {
				continue
			}
		}
		out = append(out, next)
	}
	return out
}

// Trail composes the hop trail from what a session has ALREADY TRAVERSED: the
// incoming trail, the asking proxy, and any hop it has actually taken.
//
// The proxy uses it for loop detection, so it must name every proxy the session
// has been through and not only the ones after this point: a hop that saw a
// shorter trail than the truth is one that cannot detect the loop it is about
// to close.
//
// PASS ONLY THE HOPS THE SESSION HAS TRAVERSED, which on an authorize answer is
// NONE. The contract defines `hop.hop_trail` as the trail "to forward to the
// next proxy, this proxy appended" — the proxies further along a computed path
// have not been traversed, each appends itself when it asks for its own leg,
// and a trail that ran ahead of the session would have the next proxy find
// itself in its own incoming trail and refuse the chain as a loop
// (proxy `routing.PlanHop`). So 0008 calls `Trail(entry, nil)`.
func Trail(entry EntryPoint, hops []Hop) []string {
	trail := make([]string, 0, len(entry.HopTrail)+1+len(hops))
	trail = append(trail, entry.HopTrail...)
	trail = append(trail, entry.ProxyID)
	for _, h := range hops {
		trail = append(trail, h.NextProxyID)
	}
	return trail
}
