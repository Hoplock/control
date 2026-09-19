// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/policy/eval"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// DefaultBudget is the hard server-side deadline on one authorize call (M5).
//
// A proxy holds a user's SSH handshake open while this server answers, so the
// rule is ANSWER, NEVER HANG: a timeout the proxy classifies as an outage beats
// a slow answer that looks like one. It is well inside the south-bound
// listener's own request timeout, because a deadline that only ever fires after
// the transport's has already fired is not a deadline.
const DefaultBudget = 2 * time.Second

// Service is the composition root for `/v1/authorize`.
//
// It gathers the inputs, evaluates, computes the route, assembles the snapshot,
// writes the decision record, and decides whether a cache hint may ride along —
// in that order, in one place. Everything it composes is somebody else's: the
// engine is pure (0005), the graph is the fleet's (0006), the identity was
// established at authentication (0007). What lives here is the ORDER and the
// refusals, which is exactly the part that has no other home.
type Service struct {
	store    *store.Store
	fleet    *fleet.Registry
	subjects Subjects
	programs *programCache
	graphs   *graphCache
	now      func() time.Time
	ids      func() string
	log      *slog.Logger
	budget   time.Duration
}

// Options configures a Service.
type Options struct {
	// Store is where targets, grants and decision records live.
	Store *store.Store
	// Fleet answers the path, the target capability record and the M9
	// liveness read a cache hint needs.
	Fleet *fleet.Registry
	// Subjects resolves a subject id into the groups and claims policy
	// matches on. `identity.Directory` satisfies it; 0011 replaces the
	// implementation behind it without touching this package.
	Subjects Subjects
	// Logger is where this service writes what it alone witnesses.
	Logger *slog.Logger
	// Refresh is how long a compiled program or a fleet graph is served
	// before this server re-reads what the active one is. Zero takes
	// DefaultRefreshInterval.
	Refresh time.Duration
	// Budget is the hard deadline on one call. Zero takes DefaultBudget.
	Budget time.Duration
	// Now overrides the clock, and NewID the decision id. Tests use them;
	// nothing in production should.
	Now   func() time.Time
	NewID func() string
}

// New builds a Service.
//
// It refuses to build without a store, a fleet registry and a subject
// directory rather than accepting nil and answering `5xx` later: a listener
// that starts and cannot decide anything is a fleet-wide outage discovered one
// connection at a time.
func New(o Options) (*Service, error) {
	if o.Store == nil {
		return nil, fmt.Errorf("decision: a store is required")
	}
	if o.Fleet == nil {
		return nil, fmt.Errorf("decision: a fleet registry is required")
	}
	if o.Subjects == nil {
		return nil, fmt.Errorf("decision: a subject directory is required")
	}

	s := &Service{
		store:    o.Store,
		fleet:    o.Fleet,
		subjects: o.Subjects,
		now:      o.Now,
		ids:      o.NewID,
		log:      o.Logger,
		budget:   o.Budget,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.ids == nil {
		s.ids = newDecisionID
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.budget <= 0 {
		s.budget = DefaultBudget
	}
	refresh := o.Refresh
	if refresh <= 0 {
		refresh = DefaultRefreshInterval
	}
	s.programs = newProgramCache(o.Store, refresh, s.now)
	s.graphs = newGraphCache(o.Fleet, refresh, s.now)
	return s, nil
}

// Outcome is what one authorize call decided.
//
// It is a typed outcome rather than an error for the same structural reason
// `identity.Outcome` is (0007, M11): a DENY is a decision this server made on
// purpose and is the only thing that may become a `401`, while an error is an
// outage with nothing to inspect. A transport that receives this cannot turn
// the second into the first by accident, because the second is not in the type.
type Outcome struct {
	// Response is the whole-connection snapshot, set on an allow.
	Response *contract.AuthorizeResponse
	// Deny is set on a refusal, and only on a refusal.
	Deny *Denial
}

// Denial is a decision to refuse.
//
// The REASON is written down and never disclosed: the proxy tells the user
// "access denied" and a session id, deliberately vague, because a precise
// denial makes it an oracle for probing the estate (M4). The operator resolves
// the id here into the whole story.
type Denial struct {
	// DecisionID is what the user is given a session id to reach.
	DecisionID string
	// Rule is the rule that denied, empty on a default-deny.
	Rule string
	// Reason is for this server's own log and the decision record.
	Reason string
}

// Authorize answers one `/v1/authorize` call.
//
// The tenant is a parameter rather than something read from ambient state
// (M18): the south-bound surface resolves it from the proxy's own credential,
// and the wire contract carries no tenant field at all.
func (s *Service) Authorize(ctx context.Context, tenant store.Tenant, req *contract.AuthorizeRequest) (Outcome, error) {
	if err := validate(req); err != nil {
		return Outcome{}, err
	}

	// The budget, applied to everything below it. A request that runs out
	// of it answers — as an outage the proxy can classify — rather than
	// holding the handshake open (M5).
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()

	now := s.now()
	prog, err := s.programs.Program(ctx, tenant)
	if err != nil {
		return Outcome{}, err
	}

	in, err := s.assemble(ctx, tenant, req, now)
	if err != nil {
		return Outcome{}, err
	}

	snap, expl := eval.Evaluate(prog, in.input)

	rec := record{
		id:        s.ids(),
		inputs:    newRecordedInputs(in, req),
		expl:      newRecordedExplanation(expl),
		subjectID: in.input.Subject.ID,
		targetID:  in.targetID,
		proxyID:   req.Conn.ProxyID,
		sessionID: req.Conn.SessionID,
		decidedAt: now,
	}

	if snap == nil {
		rec.effect = EffectDeny
		if err := s.write(ctx, tenant, rec); err != nil {
			return Outcome{}, err
		}
		return Outcome{Deny: &Denial{
			DecisionID: rec.id,
			Rule:       expl.Rule,
			Reason:     expl.DenyReason,
		}}, nil
	}

	rec.expl.Obligations = obligationStrings(snap.Obligations)
	resp, denial, err := s.serve(ctx, tenant, req, in, snap, &rec)
	switch {
	case err != nil:
		// The evaluation allowed and the answer could not be given. The
		// record says so rather than claiming a session that never
		// happened, and it is written BEST EFFORT: the caller is already
		// getting an outage, and failing to record why must not replace
		// the reason it is getting one.
		rec.effect = EffectUnserved
		rec.expl.Unserved = err.Error()
		rec.snapshot = nil
		if writeErr := s.write(ctx, tenant, rec); writeErr != nil {
			s.log.ErrorContext(ctx, "an unservable decision could not be recorded",
				"event", "decision_record_failed",
				"decision_id", rec.id,
				"tenant", tenant.String(),
				"error", writeErr.Error(),
			)
		}
		return Outcome{}, err
	case denial != nil:
		rec.effect = EffectDeny
		rec.expl.DenyReason = denial.Reason
		if err := s.write(ctx, tenant, rec); err != nil {
			return Outcome{}, err
		}
		denial.DecisionID = rec.id
		return Outcome{Deny: denial}, nil
	}

	rec.effect = EffectAllow
	rec.snapshot = resp
	if err := s.write(ctx, tenant, rec); err != nil {
		return Outcome{}, err
	}
	return Outcome{Response: resp}, nil
}

// serve turns an allowed snapshot into a response, or explains why it cannot.
//
// The three answers it can give are the three this phase has to keep apart:
// a response, a DENIAL somebody authored, and an OUTAGE. Everything that is not
// a decision is the third (M11).
func (s *Service) serve(
	ctx context.Context,
	tenant store.Tenant,
	req *contract.AuthorizeRequest,
	in assembled,
	snap *model.Snapshot,
	rec *record,
) (*contract.AuthorizeResponse, *Denial, error) {
	g, err := s.graphs.Graph(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}

	rt, err := s.route(g, snap, req, in)
	if err != nil {
		var intent *IntentError
		if errors.As(err, &intent) {
			// Authored, not broken: the rule permits no hops.
			return nil, &Denial{Rule: snap.Rule, Reason: intent.Error()}, nil
		}
		return nil, nil, err
	}
	rec.expl.RouteType = string(rt.routeType)
	rec.expl.NextProxyID = rt.nextProxyID

	resp := assembleSnapshot(snap, rt, rec.id)

	if err := checkServeable(resp, snap.Rule); err != nil {
		return nil, nil, err
	}
	if err := s.checkCapabilities(ctx, tenant, req, resp, g); err != nil {
		return nil, nil, err
	}

	// The cache hint rides on a response that is otherwise complete, so
	// that a hint can never be the thing that makes an answer servable.
	s.attachCacheHint(ctx, tenant, req, snap, resp, rec)

	// The version gate runs LAST, over the assembled response, because the
	// question it answers is about what is being sent rather than about
	// what this build can express.
	if err := checkVersion(resp, declaredVersion(req)); err != nil {
		return nil, nil, err
	}
	return resp, nil, nil
}

// attachCacheHint issues a hint where policy authored one and M9 permits it.
//
// Three rules, and the third is the one that is easy to lose: the hint is
// authored per rule and never global; its key selects the sharing scope and is
// never shared across identities; and NO HINT GOES TO A PROXY WHOSE EVENT
// STREAM IS UNHEALTHY, because a cached allow that cannot be withdrawn is a
// grant with no revocation. The machinery lives in `fleet` so that the
// host-key path (0007/0009) issues through the same read rather than a second
// copy of it.
//
// A failure to determine liveness withholds the hint and does NOT fail the
// call: the decision is correct either way, and absence means what every proxy
// did before the field existed.
func (s *Service) attachCacheHint(
	ctx context.Context,
	tenant store.Tenant,
	req *contract.AuthorizeRequest,
	snap *model.Snapshot,
	resp *contract.AuthorizeResponse,
	rec *record,
) {
	if snap.Cache == nil || snap.Cache.TTLSeconds <= 0 {
		return
	}
	scope, err := cacheScope(snap, req, resp)
	if err != nil {
		s.log.WarnContext(ctx, "a rule authored a cache hint this server will not key",
			"event", "cache_hint_withheld",
			"rule", snap.Rule,
			"reason", err.Error(),
		)
		return
	}
	hint, err := s.fleet.CacheHint(ctx, tenant, fleet.CacheHintRequest{
		ProxyID: req.Conn.ProxyID,
		TTL:     time.Duration(snap.Cache.TTLSeconds) * time.Second,
		Scope:   scope,
	})
	if err != nil {
		s.log.WarnContext(ctx, "the event-stream liveness read failed; no cache hint was issued",
			"event", "cache_hint_withheld",
			"proxy_id", req.Conn.ProxyID,
			"error", err.Error(),
		)
		return
	}
	if hint == nil {
		return
	}
	resp.Cache = hint
	rec.expl.CacheKey = hint.Key
}

// cacheScope renders the sharing scope from the components the rule named.
//
// It refuses a key that does not name the subject. The compiler already refuses
// such a rule at authoring time (0005), so this is the second net over the one
// invariant whose failure mode is serving one user another user's policy.
func cacheScope(snap *model.Snapshot, req *contract.AuthorizeRequest, resp *contract.AuthorizeResponse) ([]string, error) {
	pairs := make([][2]string, 0, len(snap.Cache.Key))
	sawSubject := false
	for _, component := range snap.Cache.Key {
		switch component {
		case model.CacheKeySubject:
			sawSubject = true
			pairs = append(pairs, [2]string{"subject", req.Identity.Subject})
		case model.CacheKeyTarget:
			pairs = append(pairs, [2]string{"target", resp.Target})
		case model.CacheKeyTargetPort:
			pairs = append(pairs, [2]string{"target-port", fmt.Sprint(resp.TargetPort)})
		case model.CacheKeyProxy:
			pairs = append(pairs, [2]string{"proxy", req.Conn.ProxyID})
		case model.CacheKeyAuthMethod:
			pairs = append(pairs, [2]string{"auth-method", string(req.AuthMethod)})
		case model.CacheKeyRule:
			pairs = append(pairs, [2]string{"rule", snap.Rule})
		default:
			return nil, fmt.Errorf("cache key component %q is not one this build knows", component)
		}
	}
	if !sawSubject {
		return nil, fmt.Errorf("a cache key that does not name the subject is shared across identities")
	}
	return fleet.CacheScope(pairs...), nil
}

// validate holds the request to what the contract requires of it.
//
// Every failure here is a `400`. None of them is a `401`: nobody has been
// refused, because nothing well-formed enough to refuse arrived (M11).
func validate(req *contract.AuthorizeRequest) error {
	switch {
	case req == nil:
		return contract.Invalid("a request body is required")
	case req.Identity.Subject == "":
		return contract.Invalid("identity.subject is required")
	case req.Target == "":
		return contract.Invalid("target is required")
	case req.Conn.ProxyID == "":
		// The path starts at the proxy that is asking, so a request that
		// does not say which proxy that is cannot be routed for.
		return contract.Invalid("conn.proxy_id is required")
	case req.PolicyVersion == nil:
		// REQUIRED, with no absent-value default. Not a guessed version:
		// guessing for a proxy that cannot say what it reads is guessing
		// which restrictions it would silently drop.
		return contract.Invalid("policy_version is required")
	case *req.PolicyVersion < 1:
		return contract.Invalid("policy_version must be a positive vocabulary number")
	}
	return nil
}

func declaredVersion(req *contract.AuthorizeRequest) int32 {
	if req.PolicyVersion == nil {
		return 0
	}
	return *req.PolicyVersion
}
