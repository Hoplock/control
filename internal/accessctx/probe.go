// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// THE PROBE PATH (M16, M5). A probe is a network call to somebody else's
// system on the authorize path, while a user's SSH handshake is held open. So:
//
//   - THE BUDGET SPLIT. Every probe one decision makes runs CONCURRENTLY under
//     one deadline: the probe budget (`access_context.probe_budget`, 500 ms
//     by default), which configuration may set to at most HALF of the
//     decision budget (`decision.budget`, 2 s). The other half — at least —
//     is the decision's own: its reads, the evaluation, the record write. A
//     probe that has not answered by the deadline is abandoned, whatever it
//     is doing, and is recorded as undetermined. No provider can make an
//     authorize call wait longer than the budget, because nothing waits for a
//     provider: the call waits for the deadline.
//   - CACHED BY THE PROVIDER'S TTL. The same scan is asked about once per
//     connection and a scanner opens many, so a definite answer is reused for
//     its TTL (capped, and never past a confirmed window's end), and
//     concurrent decisions about one access share one call in flight.
//   - THREE ANSWERS. Confirmed: the window counts as a grant. Not confirmed:
//     it does not. Undetermined: the scope's `unanswered` setting decides —
//     `closed` (it does not count), `open` (a pushed window counts
//     unconfirmed), or `outage` (if the decision depends on it, the call is
//     an outage) — and a privileged scope is closed whatever the setting.
//     The decision record says which way it fell.
//   - THREE SHAPES, NO SPECIAL CASE. A push-only window is a stored grant that
//     simply counts. A push-probe window is a stored grant that counts while
//     its probe confirms it. A probe-only window is a grant built for the one
//     decision whose probe confirmed it. What reaches the engine is grants,
//     in every shape; the engine never learns that any of this happened.

// Defaults for a Prober.
const (
	// DefaultProbeBudget is the probe phase's share of the decision budget.
	DefaultProbeBudget = 500 * time.Millisecond
	// DefaultMaxProbeTTL caps how long any provider's answer is reused.
	DefaultMaxProbeTTL = 5 * time.Minute
	// DefaultCacheEntries bounds the cache. Past it, expired entries go
	// first and then the cache is cleared rather than grown: a cache that
	// can grow without bound is node-local state with no ceiling.
	DefaultCacheEntries = 10000
)

// Outcomes and causes a decision record names. Stable codes.
const (
	OutcomeConfirmed    = "confirmed"
	OutcomeNotConfirmed = "not_confirmed"
	OutcomeUndetermined = "undetermined"

	// The causes of an undetermined probe: the deadline, an unreachable
	// system, an answer that made no sense, any other failure of the
	// provider, and a provider with too many probes already in flight.
	CauseTimeout     = "timeout"
	CauseUnavailable = "unavailable"
	CauseMalformed   = "malformed"
	CauseFailed      = "failed"
	CauseSaturated   = "saturated"
	// The causes of a window that was not asked about at all, or whose
	// answer did not count, for a reason on this side.
	CauseProviderMissing      = "provider_missing"
	CauseBindingMissing       = "binding_missing"
	CauseBindingDisabled      = "binding_disabled"
	CauseBindingNotPrivileged = "binding_not_privileged"
	CauseWindowNotOpen        = "window_not_open"
	CauseWindowClosed         = "window_closed"
)

// Entry is one external window a decision considered, as its decision record
// carries it (`inputs.external_context`). Every field is always present, empty
// or not, so records differ in values and never in shape.
type Entry struct {
	// Provider is the external system; Mode is how the window arrives
	// (`push-probe` or `probe`).
	Provider string `json:"provider"`
	Mode     string `json:"mode"`
	// Grant is the grant the window is — a stored one for a pushed window,
	// the one built for this decision for a probe-only one — or empty when
	// no window was confirmed.
	Grant string `json:"grant"`
	// Reference is the window's reference: the pushed one, or what the
	// probe named.
	Reference string `json:"reference"`
	// Scope is the grant scope, and Privileged whether the policy marks it so.
	Scope      string `json:"scope"`
	Privileged bool   `json:"privileged"`
	// Outcome is confirmed, not_confirmed or undetermined, and Cause why
	// when it was not a plain answer.
	Outcome string `json:"outcome"`
	Cause   string `json:"cause"`
	// Detail is the provider's own diagnostic, safe to show an operator.
	Detail string `json:"detail"`
	// Cached reports an answer reused from the cache; ObservedAt is when it
	// was originally fetched.
	Cached     bool   `json:"cached"`
	ObservedAt string `json:"observed_at"`
	// WindowStart and WindowEnd are the window the probe asserted.
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	// Assertions are the further facts the external system stated.
	Assertions map[string]string `json:"assertions"`
	// Fell is closed, open or outage when the probe was undetermined: which
	// way the scope's setting sent it.
	Fell string `json:"fell"`
	// Counted reports that the window was an input to the decision.
	Counted bool `json:"counted"`
}

// Access is what a decision is about, as the probe path needs it.
type Access struct {
	Tenant store.Tenant
	// SubjectID and Groups are the subject as THIS server knows it: groups
	// from its own record, never from the request.
	SubjectID string
	Groups    []string
	// Target is the hostname asked for; Zone and Labels its inventory
	// record's, empty when there is none.
	Target string
	Zone   string
	Labels map[string]string
	// At is the decision's own time input.
	At time.Time
}

// ScopeLookup answers what the decision's own program declares about a scope.
type ScopeLookup func(scope string) model.ScopeDecl

// Considered is the external context one decision may count.
type Considered struct {
	// Counted are the grants the engine reads: every stored grant that needs
	// no confirmation, every pushed window its probe confirmed — narrowed to
	// what the probe said — every window only a probe asserted, and every
	// unanswered pushed window whose scope falls open.
	Counted []store.Grant
	// Pending are unanswered windows whose scope falls to outage. They are
	// never inputs; they exist so the caller can ask whether the decision
	// DEPENDS on them, which is the only case where they make it an outage.
	Pending []PendingWindow
	// Entries are one per window a probe was, or would have been, asked
	// about.
	Entries []Entry
}

// PendingWindow is an unanswered window whose scope falls to outage: the grant
// it would have been, and why it is unanswered.
type PendingWindow struct {
	Grant store.Grant
	Cause string
}

// Prober is the probe path.
type Prober struct {
	providers   *Providers
	bindings    func(ctx context.Context, tenant store.Tenant) ([]store.AccessContextBinding, error)
	budget      time.Duration
	ceiling     time.Duration
	skew        time.Duration
	maxTTL      time.Duration
	unanswered  model.Unanswered
	cache       *evidenceCache
	flights     *flights
	log         *slog.Logger
	now         func() time.Time
	bindingRead *bindingCache
}

// ProberOptions configures a Prober.
type ProberOptions struct {
	// Store is where the bindings are read from. Required.
	Store *store.Store
	// Providers is the set the server runs. Nil is the empty set.
	Providers *Providers
	// Budget is the probe phase's deadline. Zero takes DefaultProbeBudget.
	// It is the caller's to keep inside the decision budget; configuration
	// refuses one over half of it.
	Budget time.Duration
	// Ceiling is the server's ceiling on a window (as Service's).
	Ceiling time.Duration
	// ClockSkew is how far a window's start may be ahead and still be open.
	ClockSkew time.Duration
	// MaxTTL caps how long an answer is reused. Zero takes the default.
	MaxTTL time.Duration
	// Unanswered is the deployment's default for a scope that is not
	// privileged and declares no answer of its own. Unset is outage: the
	// answer that grants nothing and denies nobody.
	Unanswered model.Unanswered
	// BindingRefresh is how long the tenant's bindings are served from
	// memory before they are read again. Zero takes 5 s, the same refresh
	// the compiled policy uses.
	BindingRefresh time.Duration
	// CacheEntries bounds the answer cache. Zero takes the default.
	CacheEntries int
	// Logger is where this package writes what it alone witnesses.
	Logger *slog.Logger
	// Now is the clock the caches age by. Tests use it.
	Now func() time.Time
}

// NewProber builds the probe path.
func NewProber(o ProberOptions) (*Prober, error) {
	if o.Store == nil {
		return nil, fmt.Errorf("accessctx: a store is required")
	}
	switch o.Unanswered {
	case model.UnansweredUnset:
		o.Unanswered = model.UnansweredOutage
	case model.UnansweredClosed, model.UnansweredOutage, model.UnansweredOpen:
	default:
		return nil, fmt.Errorf("accessctx: %q is not an unanswered setting", o.Unanswered)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	p := &Prober{
		providers:  o.Providers,
		budget:     or(o.Budget, DefaultProbeBudget),
		ceiling:    or(o.Ceiling, DefaultCeiling),
		skew:       or(o.ClockSkew, DefaultClockSkew),
		maxTTL:     or(o.MaxTTL, DefaultMaxProbeTTL),
		unanswered: o.Unanswered,
		cache:      newEvidenceCache(o.CacheEntries, now),
		flights:    &flights{calls: map[string]*flight{}},
		log:        o.Logger,
		now:        now,
	}
	if p.log == nil {
		p.log = slog.Default()
	}
	p.bindingRead = newBindingCache(o.Store, or(o.BindingRefresh, 5*time.Second), now)
	p.bindings = p.bindingRead.get
	return p, nil
}

// Budget is the probe phase's deadline.
func (p *Prober) Budget() time.Duration { return p.budget }

// job is one window to ask about.
type job struct {
	provider string
	mode     store.ExternalMode
	scope    string
	ref      string
	decl     model.ScopeDecl
	binding  *store.AccessContextBinding
	// grant is the stored, pushed window; nil for a probe-only one.
	grant *store.Grant
}

// Consider decides which external windows this decision may count.
//
// The error is an outage — the bindings could not be read — and never a
// verdict. Everything a provider does wrong is an Entry, not an error: a probe
// that failed is a recorded fact about one window, and the decision goes on.
func (p *Prober) Consider(ctx context.Context, acc Access, stored []store.Grant, scopes ScopeLookup) (Considered, error) {
	var out Considered
	var jobs []job

	var bindings []store.AccessContextBinding
	needBindings := p.providers != nil && len(p.providers.names) > 0
	for _, g := range stored {
		if g.Origin == store.GrantOriginExternal && g.External.Mode == store.ExternalPushProbe {
			needBindings = true
			break
		}
	}
	if needBindings {
		var err error
		if bindings, err = p.bindings(ctx, acc.Tenant); err != nil {
			return Considered{}, err
		}
	}
	bindingOf := func(provider string) *store.AccessContextBinding {
		for i := range bindings {
			if bindings[i].Provider == provider {
				return &bindings[i]
			}
		}
		return nil
	}
	target := model.Target{Hostname: acc.Target, Zone: acc.Zone, Labels: acc.Labels}

	for i := range stored {
		g := stored[i]
		if g.Origin != store.GrantOriginExternal || g.External.Mode != store.ExternalPushProbe {
			out.Counted = append(out.Counted, g)
			continue
		}
		// A pushed window that cannot cover this target cannot matter to
		// this decision, so nobody is asked about it — and it is not an
		// input either, because it is unconfirmed.
		if !access.PolicyGrant(g).Covers(target) {
			continue
		}
		jobs = append(jobs, job{
			provider: g.External.System, mode: store.ExternalPushProbe, scope: g.Scope,
			ref: g.ExternalRef, decl: scopes(g.Scope), binding: bindingOf(g.External.System), grant: &g,
		})
	}
	for i := range bindings {
		b := &bindings[i]
		if !b.Enabled || b.Mode != store.ExternalProbe ||
			!admitsSubject(*b, acc.SubjectID, acc.Groups) || !admitsTarget(*b, acc.Target, acc.Labels, acc.Zone) {
			continue
		}
		jobs = append(jobs, job{
			provider: b.Provider, mode: store.ExternalProbe, scope: b.Scope, decl: scopes(b.Scope), binding: b,
		})
	}
	if len(jobs) == 0 {
		return out, nil
	}

	results := p.run(ctx, jobs, acc)
	for i, j := range jobs {
		p.settle(&out, j, results[i], acc)
	}
	return out, nil
}

// result is what asking about one window produced.
type result struct {
	state    ext.WindowState
	cause    string
	detail   string
	evidence ext.AccessEvidence
	// skipped reports a window nobody was asked about, for a reason on this
	// side; cause says which.
	skipped bool
}

// run asks every job's provider at once, under one deadline, and returns when
// every answer is in or the deadline has passed — whichever is first.
func (p *Prober) run(ctx context.Context, jobs []job, acc Access) []result {
	pctx, cancel := context.WithTimeout(ctx, p.budget)
	defer cancel()
	results := make([]result, len(jobs))
	var wg sync.WaitGroup
	for i := range jobs {
		if r, skip := p.precheck(jobs[i]); skip {
			results[i] = r
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = p.ask(pctx, jobs[i], acc)
		}(i)
	}
	wg.Wait()
	return results
}

// precheck refuses to ask about a window this side already knows cannot
// count: a provider this server does not run, a binding that is gone or off,
// a binding that may not open privileged access asked about a privileged
// scope.
func (p *Prober) precheck(j job) (result, bool) {
	skip := func(cause string) (result, bool) {
		return result{state: ext.WindowNotConfirmed, cause: cause, skipped: true}, true
	}
	pr, ok := p.providers.Lookup(j.provider)
	switch {
	case j.binding == nil:
		return skip(CauseBindingMissing)
	case !j.binding.Enabled:
		return skip(CauseBindingDisabled)
	case j.decl.Privileged && !j.binding.Privileged:
		return skip(CauseBindingNotPrivileged)
	case !ok || !pr.Info.Probes:
		// The window needs a probe and nothing can answer one: that is not
		// "no window", it is "could not tell", and the scope decides.
		return result{state: ext.WindowUndetermined, cause: CauseProviderMissing}, true
	}
	return result{}, false
}

// ask asks one provider about one window, through the cache and any call
// already in flight for the same question.
func (p *Prober) ask(ctx context.Context, j job, acc Access) result {
	pr, _ := p.providers.Lookup(j.provider)
	key := strings.Join([]string{string(acc.Tenant), j.provider, acc.SubjectID,
		strings.ToLower(acc.Target), j.ref, j.scope}, "\x00")
	if ev, ok := p.cache.get(key); ok {
		ev.Cached = true
		return p.classify(ev, nil, j, acc)
	}

	q := ext.AccessContextQuery{
		Tenant:      ext.Tenant(acc.Tenant),
		Subject:     ext.Subject{ID: acc.SubjectID, Groups: acc.Groups},
		Target:      ext.Target{Hostname: acc.Target, Zone: acc.Zone, Labels: acc.Labels},
		Privileges:  []string{j.scope},
		ExternalRef: j.ref,
		At:          acc.At,
	}
	f, leader := p.flights.join(key)
	if leader {
		go p.fly(context.WithoutCancel(ctx), f, key, pr, q)
	}
	select {
	case <-f.done:
		return p.classify(f.evidence, f.err, j, acc)
	case <-ctx.Done():
		return result{state: ext.WindowUndetermined, cause: CauseTimeout,
			detail: fmt.Sprintf("no answer within the probe budget (%s)", p.budget)}
	}
}

// fly makes one call to a provider on behalf of every decision waiting for it.
// It runs on its own deadline, detached from any one caller, so a waiter that
// gives up does not cancel the answer the others are waiting for.
func (p *Prober) fly(base context.Context, f *flight, key string, pr *Provider, q ext.AccessContextQuery) {
	defer p.flights.land(key, f)
	select {
	case pr.slots <- struct{}{}:
	default:
		f.err = errSaturated
		return
	}
	defer func() { <-pr.slots }()
	defer func() {
		if r := recover(); r != nil {
			// A provider is somebody else's code; its panic is a failed
			// probe, never a crashed server.
			f.err = fmt.Errorf("accessctx: provider %s panicked: %v", pr.Info.Name, r)
		}
	}()

	ctx, cancel := context.WithTimeout(base, p.budget)
	defer cancel()
	f.evidence, f.err = pr.impl.Probe(ctx, q)
	if f.err == nil && (f.evidence.State == ext.WindowConfirmed || f.evidence.State == ext.WindowNotConfirmed) {
		if f.evidence.ObservedAt.IsZero() {
			f.evidence.ObservedAt = q.At
		}
		p.cache.put(key, f.evidence, p.ttl(f.evidence))
	}
}

var errSaturated = errors.New("accessctx: too many probes already in flight to this provider")

// ttl is how long an answer is reused: the provider's own TTL, capped, and
// never past the end of the window it confirmed.
func (p *Prober) ttl(ev ext.AccessEvidence) time.Duration {
	ttl := min(ev.TTL, p.maxTTL)
	if ev.State == ext.WindowConfirmed && !ev.Window.NotAfter.IsZero() {
		ttl = min(ttl, ev.Window.NotAfter.Sub(p.now()))
	}
	return ttl
}

// classify turns what a provider returned into one of the three answers,
// checking everything a confirmation has to carry to be explainable.
func (p *Prober) classify(ev ext.AccessEvidence, err error, j job, acc Access) result {
	undetermined := func(cause string, detail string) result {
		return result{state: ext.WindowUndetermined, cause: cause, detail: detail, evidence: ev}
	}
	if err != nil {
		switch {
		case errors.Is(err, ext.ErrNoEvidence):
			return result{state: ext.WindowNotConfirmed, evidence: ev}
		case errors.Is(err, errSaturated):
			return undetermined(CauseSaturated, err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			return undetermined(CauseTimeout, err.Error())
		case ext.IsUnavailable(err):
			return undetermined(CauseUnavailable, err.Error())
		case ext.IsMalformed(err):
			return undetermined(CauseMalformed, err.Error())
		default:
			return undetermined(CauseFailed, err.Error())
		}
	}
	switch ev.State {
	case ext.WindowNotConfirmed:
		return result{state: ext.WindowNotConfirmed, evidence: ev}
	case ext.WindowConfirmed:
	default:
		return undetermined(CauseMalformed, "the provider returned no answer and no error")
	}

	// A confirmation has to be explainable: it names a window, it is the
	// window asked about, and what it says is the shape the contract
	// carries.
	ref := strings.TrimSpace(ev.Reference)
	switch {
	case ref == "" && j.ref == "":
		return undetermined(CauseMalformed, "a confirmation that names no window cannot be explained")
	case ref != "" && j.ref != "" && ref != j.ref:
		return undetermined(CauseMalformed, fmt.Sprintf("asked about %s, the provider confirmed %s", j.ref, ref))
	case !ev.Window.NotBefore.IsZero() && !ev.Window.NotAfter.IsZero() && !ev.Window.NotAfter.After(ev.Window.NotBefore):
		return undetermined(CauseMalformed, "the confirmed window ends before it begins")
	case !access.AdditionalContextValid(ev.AdditionalContext):
		return undetermined(CauseMalformed, "additional_context is neither a JSON string nor a JSON object")
	}
	if ref == "" {
		ev.Reference = j.ref
	}
	// A window the external system confirmed but that is not open at this
	// instant is not a live window: it is an answer, and the answer is no.
	if !ev.Window.NotBefore.IsZero() && ev.Window.NotBefore.After(acc.At.Add(p.skew)) {
		return result{state: ext.WindowNotConfirmed, cause: CauseWindowNotOpen, evidence: ev,
			detail: "the window opens at " + ev.Window.NotBefore.UTC().Format(time.RFC3339)}
	}
	if !ev.Window.NotAfter.IsZero() && !ev.Window.NotAfter.After(acc.At) {
		return result{state: ext.WindowNotConfirmed, cause: CauseWindowClosed, evidence: ev,
			detail: "the window closed at " + ev.Window.NotAfter.UTC().Format(time.RFC3339)}
	}
	return result{state: ext.WindowConfirmed, evidence: ev}
}

// settle records one window's answer and decides whether it counts.
func (p *Prober) settle(out *Considered, j job, r result, acc Access) {
	e := Entry{
		Provider: j.provider, Mode: string(j.mode), Reference: j.ref, Scope: j.scope,
		Privileged: j.decl.Privileged, Outcome: outcomeOf(r.state), Cause: r.cause, Detail: r.detail,
		Cached: r.evidence.Cached, Assertions: r.evidence.Assertions,
	}
	if e.Assertions == nil {
		e.Assertions = map[string]string{}
	}
	if !r.evidence.ObservedAt.IsZero() {
		e.ObservedAt = r.evidence.ObservedAt.UTC().Format(time.RFC3339Nano)
	}
	e.WindowStart = stamp(r.evidence.Window.NotBefore)
	e.WindowEnd = stamp(r.evidence.Window.NotAfter)
	if r.evidence.Reference != "" {
		e.Reference = r.evidence.Reference
	}

	switch r.state {
	case ext.WindowConfirmed:
		g := p.confirmed(j, r.evidence, acc)
		e.Grant, e.Counted = g.ID, true
		out.Counted = append(out.Counted, g)
	case ext.WindowNotConfirmed:
		// No window. Nothing counts and nothing falls.
	default:
		fell := p.fall(j)
		e.Fell = string(fell)
		switch fell {
		case model.UnansweredOpen:
			// Only a pushed window can fall open: there is a push to fall
			// open to. It counts as it was pushed.
			e.Grant, e.Counted = j.grant.ID, true
			out.Counted = append(out.Counted, *j.grant)
		case model.UnansweredOutage:
			out.Pending = append(out.Pending, PendingWindow{Grant: p.unconfirmed(j, acc), Cause: r.cause})
		}
	}
	out.Entries = append(out.Entries, e)
}

// fall is which way an unanswered window goes: the scope's own setting, else
// the deployment's default; closed for a privileged scope whatever either
// says; and closed for a probe-only window asked to fall open, because there
// is no pushed window to fall open to.
func (p *Prober) fall(j job) model.Unanswered {
	u := j.decl.Unanswered
	if u == model.UnansweredUnset {
		u = p.unanswered
		if j.decl.Privileged {
			u = model.UnansweredClosed
		}
	}
	if u == model.UnansweredOpen && (j.decl.Privileged || j.grant == nil) {
		u = model.UnansweredClosed
	}
	return u
}

// confirmed is the grant a confirmed window counts as in this decision.
//
// A pushed window is its stored grant, narrowed to what the probe said: the
// probe is the authoritative direction, so a scan the push said runs until six
// and the probe says ends at four counts until four. A probe-only window is a
// grant built for this decision — the binding's scope on this one host,
// starting now and clamped to the ceiling — with an id derived from the
// window, so every decision it supplies names the same grant.
func (p *Prober) confirmed(j job, ev ext.AccessEvidence, acc Access) store.Grant {
	if j.grant != nil {
		g := *j.grant
		if end := ev.Window.NotAfter; !end.IsZero() && end.Before(g.ExpiresAt) {
			g.ExpiresAt = end.UTC()
		}
		return g
	}
	g := p.unconfirmed(j, acc)
	g.ID = ProbeGrantID(acc.Tenant, j.provider, ev.Reference)
	g.ExternalRef = ev.Reference
	if end := ev.Window.NotAfter; !end.IsZero() && end.Before(g.ExpiresAt) {
		g.ExpiresAt = end.UTC()
	}
	g.External.WindowStart = ev.Window.NotBefore.UTC()
	g.External.WindowEnd = ev.Window.NotAfter.UTC()
	if ev.Window.NotBefore.IsZero() {
		g.External.WindowStart = time.Time{}
	}
	if ev.Window.NotAfter.IsZero() {
		g.External.WindowEnd = time.Time{}
	}
	if kind, text, ok := additional(ev); ok {
		g.External.AdditionalKind, g.External.Additional = kind, text
	}
	return g
}

// unconfirmed is the grant a window would be if it counted: a pushed window's
// stored grant, or for a probe-only window, the binding's scope on this host
// from now until the ceiling.
func (p *Prober) unconfirmed(j job, acc Access) store.Grant {
	if j.grant != nil {
		return *j.grant
	}
	ceiling := min(j.binding.MaxWindow, p.ceiling)
	at := acc.At.UTC()
	return store.Grant{
		ID:           ProbeGrantID(acc.Tenant, j.provider, ""),
		SubjectID:    acc.SubjectID,
		Scope:        j.scope,
		ScopeTargets: []string{strings.ToLower(acc.Target)},
		ScopeLabels:  j.binding.TargetLabels,
		ScopeZones:   j.binding.TargetZones,
		NotBefore:    at,
		ExpiresAt:    at.Add(ceiling),
		Origin:       store.GrantOriginExternal,
		Reason:       "window confirmed by the " + j.provider + " probe",
		External:     store.GrantExternal{System: j.provider, Mode: store.ExternalProbe},
		CreatedAt:    at,
	}
}

// ProbeGrantID is the id of the grant a probe-only window counts as. It is
// derived from the window rather than drawn at random, so every decision the
// same window supplies names the same grant, and "which sessions did this scan
// back" is one query over decisions.grant_id. The prefix keeps it apart from
// every id internal/access draws for a stored grant.
func ProbeGrantID(tenant store.Tenant, provider, reference string) string {
	sum := sha256.Sum256([]byte(string(tenant) + "\x00" + provider + "\x00" + reference))
	return "xg_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:15]))
}

func additional(ev ext.AccessEvidence) (string, string, bool) {
	t := strings.TrimSpace(string(ev.AdditionalContext))
	switch {
	case t == "" || t == "null":
		return "", "", false
	case t[0] == '"':
		return store.AdditionalContextString, t, true
	case t[0] == '{':
		return store.AdditionalContextObject, t, true
	}
	return "", "", false
}

func outcomeOf(s ext.WindowState) string {
	switch s {
	case ext.WindowConfirmed:
		return OutcomeConfirmed
	case ext.WindowNotConfirmed:
		return OutcomeNotConfirmed
	case ext.WindowUndetermined:
		return OutcomeUndetermined
	}
	return OutcomeUndetermined
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
