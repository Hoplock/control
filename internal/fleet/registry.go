// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// nowFunc is the clock, injected so that staleness is testable without sleeping.
type nowFunc func() time.Time

// Registry is the fleet: enrollment, liveness, capabilities, configuration, and
// the graph the routes are computed over.
//
// It is the only thing in this package that touches storage. Everything worth
// proving — pathfinding, the capability fail-safe rule, the pre-publish query —
// is a pure function over values, and this type is what loads those values.
type Registry struct {
	st       *store.Store
	liveness Liveness
	subs     SubscriptionState
	pub      ConfigPublisher
	now      nowFunc
	maxHops  int
	uids     UIDAllocation
	log      *slog.Logger
	// maxCacheTTL is the ceiling on a hint's lifetime (PLAN §5.4). Zero
	// takes DefaultMaxCacheTTL.
	maxCacheTTL time.Duration
}

// Option configures a Registry.
type Option func(*Registry)

// WithLiveness sets the staleness rule. Zero-valued fields keep their defaults,
// so a partially filled config cannot take the whole fleet out of routing.
func WithLiveness(l Liveness) Option {
	return func(r *Registry) { r.liveness = l.withDefaults() }
}

// WithSubscriptionState supplies the second liveness signal (0009 implements it).
func WithSubscriptionState(s SubscriptionState) Option {
	return func(r *Registry) { r.subs = s }
}

// WithConfigPublisher supplies the configuration delivery path (0009 implements
// it). Without one, a publish is durable and visible but not pushed — see
// [ConfigPublisher] for why that is the honest state today.
func WithConfigPublisher(p ConfigPublisher) Option {
	return func(r *Registry) {
		if p != nil {
			r.pub = p
		}
	}
}

// WithLogger sets where this registry writes the events it is the only witness
// to — a target presenting a host key it has not presented before, and a uid
// cursor raised by a floor observed on a target.
//
// 0010 moves both into the audit store. Until then these log lines ARE the
// record, which is why the shape is fixed now and the destination is a option
// rather than slog's default: a deployment that routes its logs somewhere must
// not lose the one it most needs to keep.
func WithLogger(l *slog.Logger) Option {
	return func(r *Registry) {
		if l != nil {
			r.log = l
		}
	}
}

// WithMaxCacheTTL sets the ceiling on a cache hint's lifetime. It clamps
// DOWNWARD only: a bundle asking for longer gets the ceiling, and one asking
// for less gets what it asked for. A non-positive value keeps the default.
func WithMaxCacheTTL(d time.Duration) Option {
	return func(r *Registry) {
		if d > 0 {
			r.maxCacheTTL = d
		}
	}
}

// WithClock overrides the clock. Tests use it; nothing in production should.
func WithClock(now func() time.Time) Option {
	return func(r *Registry) {
		if now != nil {
			r.now = now
		}
	}
}

// WithRegistryMaxHops sets the hop cap the graphs this registry builds carry.
func WithRegistryMaxHops(n int) Option {
	return func(r *Registry) {
		if n > 0 {
			r.maxHops = n
		}
	}
}

// New builds a Registry over a store.
func New(st *store.Store, opts ...Option) *Registry {
	r := &Registry{
		st:          st,
		liveness:    DefaultLiveness(),
		pub:         noopConfigPublisher{},
		now:         time.Now,
		maxHops:     DefaultMaxHops,
		uids:        DefaultUIDAllocation(),
		log:         slog.Default(),
		maxCacheTTL: DefaultMaxCacheTTL,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Liveness reports the staleness rule in force, so an operator surface can show
// the numbers a proxy is being judged against.
func (r *Registry) Liveness() Liveness { return r.liveness }

// ---------------------------------------------------------------------------
// enrollment
// ---------------------------------------------------------------------------

// IssueGrant pre-registers a proxy and returns the token it must present.
//
// The token is returned ONCE and only its hash is stored. The caller shows it to
// the operator and must not log it (see [MintEnrollmentToken]).
func (r *Registry) IssueGrant(ctx context.Context, tenant store.Tenant, g EnrollmentGrant, expiresAt time.Time) (EnrollmentToken, error) {
	if g.ProxyID == "" {
		return EnrollmentToken{}, fmt.Errorf("fleet.IssueGrant: proxy id is required")
	}
	if len(g.GrantedZones) == 0 {
		// An empty grant is refused at issuance rather than stored and refused
		// at enrollment. A grant that can never succeed is an operator's typo,
		// and the moment to say so is while they are still looking.
		return EnrollmentToken{}, fmt.Errorf("fleet.IssueGrant: at least one granted zone is required")
	}

	token, err := MintEnrollmentToken(tenant)
	if err != nil {
		return EnrollmentToken{}, err
	}

	zones := make([]string, 0, len(g.GrantedZones))
	for _, z := range g.GrantedZones {
		zones = append(zones, string(z))
	}
	slices.Sort(zones)

	err = r.st.ProxyEnrollments().Create(ctx, tenant, store.ProxyEnrollment{
		ProxyID:      g.ProxyID,
		GrantedZones: zones,
		TokenHash:    token.Hash(),
		ExpiresAt:    expiresAt,
		CreatedBy:    g.CreatedBy,
	})
	if err != nil {
		return EnrollmentToken{}, err
	}
	return token, nil
}

// Enrollment is what an accepted enrollment produced.
type Enrollment struct {
	// Tenant is the tenant resolved FROM THE CREDENTIAL, never from anything
	// the proxy said (M18).
	Tenant store.Tenant
	// ProxyID and Zone are what was accepted.
	ProxyID string
	Zone    Zone
	// Config is the configuration this proxy should be running, as of now. It
	// is handed back on enrollment because a proxy must not have to ask a
	// second time for the thing it needs in order to work.
	Config store.ProxyConfigState
	// APIToken is the south-bound channel credential this enrollment minted,
	// bound to this proxy (M2, 0007). It is returned ONCE, for the same
	// reason the enrollment token is: only its hash is stored.
	//
	// It is handed back here rather than fetched afterwards because a proxy
	// with no channel credential cannot call anything — including whatever
	// endpoint would have issued it.
	APIToken ProxyToken
}

// Enroll admits a proxy to the fleet, or refuses it.
//
// The tenant comes out of the credential and the zone is checked against the
// grant. A refusal is one of [ErrEnrollmentUnknown], [ErrEnrollmentSpent],
// [ErrEnrollmentExpired], [ErrEnrollmentRejected] or [ErrZoneNotGranted]; any
// other error is this server failing rather than deciding, and the caller must
// keep the two apart for the same reason M11 exists.
//
// The whole thing is one transaction. A half-enrollment — a consumed token with
// no proxy row, or a proxy row with no edges — is a fleet an operator has to
// repair by hand, and the failure mode of the second one is a proxy that is
// routable and reaches nothing.
func (r *Registry) Enroll(ctx context.Context, req EnrollmentRequest) (Enrollment, error) {
	token, err := ParseEnrollmentToken(req.Token)
	if err != nil {
		return Enrollment{}, err
	}
	if req.ProxyID == "" {
		return Enrollment{}, fmt.Errorf("fleet.Enroll: proxy id is required")
	}
	if req.Zone == "" {
		return Enrollment{}, fmt.Errorf("fleet.Enroll: zone is required")
	}
	if len(req.PublicKey) == 0 {
		// The key is what the proxy will later authenticate a chain leg with
		// (`/v1/auth/cert`), and a fleet member with no key is one nothing can
		// verify. Refusing here beats discovering it on the first hop.
		return Enrollment{}, fmt.Errorf("fleet.Enroll: public key is required")
	}
	tenant := token.Tenant

	caps, err := MarshalCapabilities(req.Capabilities)
	if err != nil {
		return Enrollment{}, fmt.Errorf("fleet.Enroll: %w", err)
	}

	out := Enrollment{Tenant: tenant, ProxyID: req.ProxyID, Zone: req.Zone}
	err = r.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		grant, err := tx.ProxyEnrollments().Get(ctx, tenant, req.ProxyID)
		if err != nil {
			return errNoGrant(err)
		}
		if err := checkGrant(grant, req, token, r.now); err != nil {
			return err
		}
		if err := tx.ProxyEnrollments().Consume(ctx, tenant, req.ProxyID, r.now()); err != nil {
			if store.IsConflict(err) {
				return ErrEnrollmentSpent
			}
			return err
		}

		// The proxy row carries no heartbeat: enrollment says an operator
		// approved this proxy, not that it is running. It becomes a routing
		// option when it first reports.
		if err := tx.Proxies().Upsert(ctx, tenant, store.Proxy{
			ID:                   req.ProxyID,
			Zone:                 string(req.Zone),
			PublicKey:            req.PublicKey,
			State:                store.EnrollmentEnrolled,
			ContractVersion:      req.ContractVersion,
			DeclaredCapabilities: caps,
		}); err != nil {
			return err
		}
		if err := tx.ProxyEdges().ReplaceForProxy(ctx, tenant, req.ProxyID, edgesToStore(req.Edges)); err != nil {
			return err
		}

		// The channel credential is minted inside the same transaction as
		// the row it authenticates. A proxy admitted to the fleet with no
		// way to call the API is a half-enrollment an operator has to
		// repair by hand, which is what this transaction exists to prevent.
		token, err := MintProxyToken(tenant)
		if err != nil {
			return err
		}
		tokenID, err := newTokenID()
		if err != nil {
			return err
		}
		if err := tx.ProxyTokens().Insert(ctx, tenant, store.ProxyAPIToken{
			TokenID:   tokenID,
			ProxyID:   req.ProxyID,
			TokenHash: token.Hash(),
			Label:     "enrollment",
			IssuedAt:  r.now(),
		}); err != nil {
			return err
		}
		out.APIToken = token

		state, err := materialiseConfig(ctx, tx, tenant, req.ProxyID, string(req.Zone), r.now())
		if err != nil {
			return err
		}
		out.Config = state
		return nil
	})
	if err != nil {
		return Enrollment{}, err
	}
	return out, nil
}

// EnrollmentRefused reports whether err is a refusal this server decided rather
// than a failure it suffered.
func EnrollmentRefused(err error) bool { return err != nil && asEnrollmentFailure(err) }

// edgesToStore converts declared edges for storage.
func edgesToStore(edges []Edge) []store.ProxyEdge {
	out := make([]store.ProxyEdge, 0, len(edges))
	for _, e := range edges {
		cost := e.Cost
		if cost <= 0 {
			cost = 1
		}
		out = append(out, store.ProxyEdge{
			ToZone:      string(e.ToZone),
			Direction:   store.HopDirection(e.Connection),
			Address:     e.Address,
			NextProxyID: e.NextProxyID,
			Cost:        cost,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// liveness
// ---------------------------------------------------------------------------

// HeartbeatReport is what a proxy reports, explicitly, on its own schedule.
//
// It is the second half of liveness. The subscription proves reachability; this
// carries what a held-open connection cannot say: how many sessions are running,
// what went wrong last, which relay registrations this proxy is currently
// holding, and what its build can do.
type HeartbeatReport struct {
	// ProxyID is who reported.
	ProxyID string
	// SessionCount is how many sessions it holds. Only the proxy can count
	// them — it owns the session registry.
	SessionCount int
	// LastError is the last error it wants an operator to see, empty for none.
	LastError string
	// ContractVersion, when non-zero, updates the enrolled vocabulary.
	ContractVersion int
	// Capabilities, when non-nil, REPLACES the declared set. A proxy that has
	// been upgraded advertises more and one that has been downgraded advertises
	// less, and the freshness of the set is the freshness of the heartbeat that
	// carried it — one lever, not two (M17). A nil pointer says nothing about
	// capabilities and leaves the stored set alone; a non-nil empty one
	// withdraws everything.
	Capabilities *Capabilities
	// RelayRegistrations are the downstream proxy ids currently holding an
	// outbound relay connection open TO THIS PROXY. It is a replacement: one it
	// stops reporting has dropped, and a relay edge over a dropped registration
	// must leave routing at once rather than hang a session.
	RelayRegistrations []string
	// RunningConfigVersion and RunningConfigHash are what it is actually
	// running. Zero leaves the stored value alone.
	RunningConfigVersion int64
	RunningConfigHash    string
}

// Heartbeat records a report.
//
// A heartbeat from a proxy with no enrolled row is [ErrNotEnrolled] and NEVER a
// row this call creates. A heartbeat that enrolled its sender would be exactly
// the auto-enrollment this phase exists to refuse, arriving through the one
// endpoint nobody thinks of as an enrollment path.
func (r *Registry) Heartbeat(ctx context.Context, tenant store.Tenant, h HeartbeatReport) error {
	if h.ProxyID == "" {
		return fmt.Errorf("fleet.Heartbeat: proxy id is required")
	}
	now := r.now()

	report := store.ProxyHealthReport{
		ProxyID:      h.ProxyID,
		At:           now,
		SessionCount: h.SessionCount,
		LastError:    h.LastError,
	}
	if h.ContractVersion != 0 {
		v := h.ContractVersion
		report.ContractVersion = &v
	}
	if h.Capabilities != nil {
		raw, err := MarshalCapabilities(*h.Capabilities)
		if err != nil {
			return fmt.Errorf("fleet.Heartbeat: %w", err)
		}
		report.DeclaredCapabilities = raw
	}

	return r.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.Proxies().RecordHealth(ctx, tenant, report); err != nil {
			if store.IsNotFound(err) {
				return ErrNotEnrolled
			}
			return err
		}
		if err := tx.RelayRegistrations().ReplaceForUpstream(ctx, tenant, h.ProxyID, h.RelayRegistrations, now); err != nil {
			return err
		}
		if h.RunningConfigVersion != 0 {
			err := tx.ProxyConfigs().ReportRunning(ctx, tenant, h.ProxyID,
				h.RunningConfigVersion, h.RunningConfigHash, now)
			if err != nil && !store.IsNotFound(err) {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// the graph
// ---------------------------------------------------------------------------

// Graph loads one tenant's graph.
//
// It is three whole-tenant reads rather than a join, because the fleet is small
// relative to the estate and pathfinding needs all of it at once. It is also
// where the staleness rule is applied: a proxy that is not live is marked so
// here, once, rather than tested at every edge.
func (r *Registry) Graph(ctx context.Context, tenant store.Tenant) (*Graph, error) {
	now := r.now()

	proxies, err := r.st.Proxies().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	edges, err := r.st.ProxyEdges().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	regs, err := r.st.RelayRegistrations().List(ctx, tenant)
	if err != nil {
		return nil, err
	}

	// The subscription is the stronger liveness signal, so it is allowed to
	// keep a proxy live whose explicit heartbeat has aged out: this server is
	// holding that connection open, which is a fact it observed rather than one
	// it was told. A nil SubscriptionState means the signal is unavailable, and
	// the heartbeat stays in charge — never "nothing is live".
	var subs map[string]time.Time
	if r.subs != nil {
		subs, err = r.subs.LiveSubscriptions(ctx, tenant)
		if err != nil {
			return nil, err
		}
	}

	edgesByProxy := make(map[string][]Edge, len(proxies))
	for _, e := range edges {
		edgesByProxy[e.ProxyID] = append(edgesByProxy[e.ProxyID], Edge{
			ToZone:      Zone(e.ToZone),
			Connection:  contract.HopConnection(e.Direction),
			Address:     e.Address,
			NextProxyID: e.NextProxyID,
			Cost:        e.Cost,
		})
	}

	relaysByUpstream := make(map[string][]string, len(regs))
	for _, reg := range regs {
		if !r.liveness.RelayIsLive(reg, now) {
			// A stale registration is dropped here rather than carried and
			// filtered later. A relay edge is viable only while its
			// registration is, and an edge that looks viable at one layer and
			// is rejected at another is the shape a refactor forgets.
			continue
		}
		relaysByUpstream[reg.UpstreamProxyID] = append(relaysByUpstream[reg.UpstreamProxyID], reg.DownstreamProxyID)
	}

	nodes := make([]Node, 0, len(proxies))
	for _, p := range proxies {
		nodes = append(nodes, Node{
			ProxyID:      p.ID,
			Zone:         Zone(p.Zone),
			Live:         r.isLive(p, subs, now),
			Edges:        edgesByProxy[p.ID],
			Relays:       relaysByUpstream[p.ID],
			Capabilities: UnmarshalCapabilities(p.DeclaredCapabilities),
		})
	}
	return NewGraph(tenant, nodes, WithMaxHops(r.maxHops))
}

// isLive folds the two liveness signals together.
func (r *Registry) isLive(p store.Proxy, subs map[string]time.Time, now time.Time) bool {
	if p.State != store.EnrollmentEnrolled {
		// A revoked proxy is not live however loudly it reports. Enrollment
		// state is an operator's decision and liveness may not overrule it.
		return false
	}
	if r.liveness.ProxyIsLive(p, now) {
		return true
	}
	seen, ok := subs[p.ID]
	if !ok || seen.IsZero() {
		return false
	}
	return !now.After(seen.Add(r.liveness.HeartbeatTTL))
}

// Path is the convenience form: load the graph, compute the path.
//
// 0008 will hold a graph rather than reload it per request (M5 has no room for
// three reads on the decision path), so this exists for operator surfaces and
// for tests. The error is a *NoPathError on every no-path outcome, which 0008
// translates into an OUTAGE and never a deny.
func (r *Registry) Path(ctx context.Context, tenant store.Tenant, entry EntryPoint, dest Destination) ([]Hop, error) {
	g, err := r.Graph(ctx, tenant)
	if err != nil {
		return nil, err
	}
	return g.Path(entry, dest)
}

// ---------------------------------------------------------------------------
// health
// ---------------------------------------------------------------------------

// ProxyHealth is what the console's fleet screen (0016) renders and what an
// operator looks at first during an incident.
type ProxyHealth struct {
	ProxyID string
	Zone    Zone
	// State is the enrollment state, and Live is whether it is a routing
	// option right now. The two are separate because "approved" and "reachable"
	// are different questions and an operator needs both answers.
	State store.EnrollmentState
	Live  bool
	// LastHeartbeatAt is zero when the proxy has never reported.
	LastHeartbeatAt time.Time
	// SubscriptionSeenAt is when its event subscription was last seen, zero
	// when it holds none or when the signal is unavailable.
	SubscriptionSeenAt time.Time
	// ContractVersion is the vocabulary it declared at enrollment. It is a
	// readiness signal, never the authority for a connection.
	ContractVersion int
	// Capabilities is what its build declares (M17).
	Capabilities Capabilities
	// RelayRegistrations are the downstream proxies currently registered with
	// it, live ones only, in id order.
	RelayRegistrations []string
	// DesiredConfigVersion, RunningConfigVersion and ConfigDrift are the
	// rollout. Drift is a value rather than something the reader derives,
	// because silent drift across a fleet is indistinguishable from a broken
	// rollout.
	DesiredConfigVersion int64
	RunningConfigVersion int64
	ConfigDrift          bool
	// SessionCount, LastError and LastErrorAt are the rest of the picture.
	SessionCount int
	LastError    string
	LastErrorAt  time.Time
}

// Health returns the fleet view for one tenant, in proxy-id order.
//
// It never reaches another tenant's rows: every read below names the tenant, so
// two tenants running identically named proxies in identically named zones see
// only their own (M18).
func (r *Registry) Health(ctx context.Context, tenant store.Tenant) ([]ProxyHealth, error) {
	now := r.now()

	proxies, err := r.st.Proxies().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	regs, err := r.st.RelayRegistrations().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	states, err := r.st.ProxyConfigs().ListStates(ctx, tenant)
	if err != nil {
		return nil, err
	}

	var subs map[string]time.Time
	if r.subs != nil {
		subs, err = r.subs.LiveSubscriptions(ctx, tenant)
		if err != nil {
			return nil, err
		}
	}

	relays := make(map[string][]string, len(regs))
	for _, reg := range regs {
		if !r.liveness.RelayIsLive(reg, now) {
			continue
		}
		relays[reg.UpstreamProxyID] = append(relays[reg.UpstreamProxyID], reg.DownstreamProxyID)
	}
	byProxy := make(map[string]store.ProxyConfigState, len(states))
	for _, s := range states {
		byProxy[s.ProxyID] = s
	}

	out := make([]ProxyHealth, 0, len(proxies))
	for _, p := range proxies {
		cfg := byProxy[p.ID]
		out = append(out, ProxyHealth{
			ProxyID:              p.ID,
			Zone:                 Zone(p.Zone),
			State:                p.State,
			Live:                 r.isLive(p, subs, now),
			LastHeartbeatAt:      p.LastHeartbeatAt,
			SubscriptionSeenAt:   subs[p.ID],
			ContractVersion:      p.ContractVersion,
			Capabilities:         UnmarshalCapabilities(p.DeclaredCapabilities),
			RelayRegistrations:   relays[p.ID],
			DesiredConfigVersion: cfg.DesiredVersion,
			RunningConfigVersion: cfg.RunningVersion,
			ConfigDrift:          cfg.Drifted(),
			SessionCount:         p.SessionCount,
			LastError:            p.LastError,
			LastErrorAt:          p.LastErrorAt,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// target capabilities (M17, second source)
// ---------------------------------------------------------------------------

// ReportTargetCapabilities records what a proxy observed about one target and
// answers when the next observation is due.
//
// THE SERVER OWNS THE FRESHNESS OF ITS OWN RECORD: the returned interval is what
// `/v1/capabilities/report` puts in `report_after_seconds` (0007 serves the
// endpoint). A proxy may re-observe sooner, never later — the same reasoning as a
// cache TTL, because the party that depends on the value is the party that
// decides how old it may get.
//
// The report GRANTS NOTHING. It is stored, and what it can affect is which rungs
// a policy may name; the authority for a rung is the authorize response, and the
// proxy re-checks it against the live target when it provisions.
func (r *Registry) ReportTargetCapabilities(ctx context.Context, tenant store.Tenant, rec TargetCapabilities) (time.Duration, error) {
	if rec.Key.Hostname == "" {
		return 0, fmt.Errorf("fleet.ReportTargetCapabilities: hostname is required")
	}
	// An undated report is stored undated. Stamping it with arrival time would
	// make a record the proxy could not date look fresh, which is exactly the
	// fail-open the rule exists to prevent — and the proxy's own clock is the
	// only one that saw the target.
	if err := r.st.TargetCapabilities().Put(ctx, tenant, targetCapabilitiesToStore(rec, r.now())); err != nil {
		return 0, err
	}
	return r.liveness.ReportAfter, nil
}

// TargetRungs answers what a target may be asked for, applying the fail-safe
// rule.
//
// Absent, stale and undated are ONE case and are not distinguished to the
// caller: all three provide nothing that has to be applied, and all three leave
// every rung that needs nothing of the target available. A caller that wanted to
// tell them apart would be a caller about to treat one of them as fresh.
func (r *Registry) TargetRungs(ctx context.Context, tenant store.Tenant, key TargetCapabilityKey) (TargetRungs, error) {
	rec, err := r.st.TargetCapabilities().Get(ctx, tenant, key.Hostname, key.Port, key.Platform)
	if err != nil {
		if store.IsNotFound(err) {
			return ResolveTargetRungs(nil, r.now(), r.liveness.TargetCapabilityTTL), nil
		}
		// A read failure is NOT "no record": it is this server not knowing, and
		// answering the fail-safe set here would make a database outage look
		// like an appliance estate. M11 again.
		return TargetRungs{}, err
	}
	got := targetCapabilitiesFromStore(rec)
	return ResolveTargetRungs(&got, r.now(), r.liveness.TargetCapabilityTTL), nil
}
