// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// Defaults for a Service.
const (
	// DefaultMaxDuration is the longest window a grant may have. It is a
	// ceiling on a typo as much as on a policy: "prod for a week" is a
	// just-in-time grant with a long time-box, "prod for ten years" is
	// standing access nobody will remember to revoke.
	DefaultMaxDuration = 7 * 24 * time.Hour
	// DefaultSettle is how long revocation waits before its second pass.
	// It must be longer than one authorize call can take (decision.budget,
	// 2s by default): an authorize that read the grant as live just before
	// the revocation committed finishes, records its session, and is ended
	// by the second pass rather than outliving the revocation.
	DefaultSettle = 2500 * time.Millisecond
	// DefaultWorkflowTimeout bounds one call to a registered workflow.
	DefaultWorkflowTimeout = 10 * time.Second
	// DefaultKillLimit is how many sessions revocation ends by name before
	// it ends every session of the holder instead. Past it, a list of
	// session ids is no longer a precise instrument, and the safe direction
	// is wider.
	DefaultKillLimit = 5000
	// inspectDecisions is how many of a grant's decisions Inspect returns.
	inspectDecisions = 50
	// pollBatch is how many pending requests one poll of a tenant advances.
	pollBatch = 50
)

// Service creates, reads and revokes grants, and routes creation through a
// registered workflow when there is one.
type Service struct {
	store    *store.Store
	recorder Recorder
	revoker  Revoker
	notifier Notifier
	workflow ext.GrantWorkflow
	provider string

	maxDuration     time.Duration
	settle          time.Duration
	workflowTimeout time.Duration
	killLimit       int

	now   func() time.Time
	newID func(prefix string) string
	log   *slog.Logger

	mu      sync.Mutex
	tenants map[store.Tenant]struct{}
}

// Options configures a Service.
type Options struct {
	// Store holds grants, requests and decision records. Required.
	Store *store.Store
	// Recorder writes the audit record of every act, inside the act's
	// transaction. Required: a grant this server cannot write down is one it
	// does not create.
	Recorder Recorder
	// Revoker publishes session_kill and cache_invalidate. Required: a
	// revocation that cannot end the sessions a grant backed is not one.
	Revoker Revoker
	// Notifier announces each act. Nil announces nothing.
	Notifier Notifier
	// Workflow is the registered ext.GrantWorkflow, if any, and
	// WorkflowProvider the name it was registered under. With a workflow,
	// every administrator's grant is a request it decides.
	Workflow         ext.GrantWorkflow
	WorkflowProvider string
	// MaxDuration is the longest window a grant may have. Zero takes
	// DefaultMaxDuration.
	MaxDuration time.Duration
	// Settle is how long revocation waits before its second pass. Zero
	// takes DefaultSettle.
	Settle time.Duration
	// WorkflowTimeout bounds one call to the workflow. Zero takes
	// DefaultWorkflowTimeout.
	WorkflowTimeout time.Duration
	// KillLimit is how many sessions revocation ends by name. Zero takes
	// DefaultKillLimit.
	KillLimit int
	// Logger is where this package writes what it alone witnesses.
	Logger *slog.Logger
	// Now and NewID override the clock and the id generator. Tests use them.
	Now   func() time.Time
	NewID func(prefix string) string
}

// New builds a Service.
func New(o Options) (*Service, error) {
	switch {
	case o.Store == nil:
		return nil, fmt.Errorf("access: a store is required")
	case o.Recorder == nil:
		return nil, fmt.Errorf("access: an audit recorder is required: a grant this server cannot write down is one it does not create")
	case o.Revoker == nil:
		return nil, fmt.Errorf("access: a revocation publisher is required: a revocation that cannot end the sessions a grant backed is not one")
	case o.Workflow != nil && o.WorkflowProvider == "":
		return nil, fmt.Errorf("access: a registered workflow must be named, so every request can say who decided it")
	}
	s := &Service{
		store:           o.Store,
		recorder:        o.Recorder,
		revoker:         o.Revoker,
		notifier:        o.Notifier,
		workflow:        o.Workflow,
		provider:        o.WorkflowProvider,
		maxDuration:     o.MaxDuration,
		settle:          o.Settle,
		workflowTimeout: o.WorkflowTimeout,
		killLimit:       o.KillLimit,
		now:             o.Now,
		newID:           o.NewID,
		log:             o.Logger,
		tenants:         map[store.Tenant]struct{}{},
	}
	if s.maxDuration <= 0 {
		s.maxDuration = DefaultMaxDuration
	}
	if s.settle <= 0 {
		s.settle = DefaultSettle
	}
	if s.workflowTimeout <= 0 {
		s.workflowTimeout = DefaultWorkflowTimeout
	}
	if s.killLimit <= 0 {
		s.killLimit = DefaultKillLimit
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newID == nil {
		s.newID = newID
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s, nil
}

// WorkflowProvider names the registered workflow, empty when there is none.
func (s *Service) WorkflowProvider() string { return s.provider }

// MaxDuration is the longest window a grant may have.
func (s *Service) MaxDuration() time.Duration { return s.maxDuration }

// Created is what a create produced.
//
// Without a workflow it is always a grant. With one it is always a request,
// plus the grant when the workflow approved at once; a request still pending,
// or one the workflow refused, has no grant — and never will have one it did
// not approve.
type Created struct {
	// Grant is the grant that now exists, if one does.
	Grant *store.Grant
	// Request is the workflow request, when a workflow is registered.
	Request *store.GrantRequest
	// Unconfirmed reports that the workflow could not be reached. The
	// request is kept and submitted again under the same id until it is
	// answered or its window closes.
	Unconfirmed bool
}

// Create grants access, or asks the registered workflow to.
func (s *Service) Create(ctx context.Context, tenant store.Tenant, actor Actor, spec Spec) (Created, error) {
	if err := actor.check(); err != nil {
		return Created{}, err
	}
	now := s.now().UTC()
	spec, _, err := normalize(spec, now, s.maxDuration)
	if err != nil {
		return Created{}, err
	}
	if s.workflow != nil {
		// While a workflow is registered an administrator's grant goes
		// through it, always: a create that fell back to the direct path
		// whenever the workflow was slow or said no would make the
		// workflow advisory.
		return s.request(ctx, tenant, actor, spec, now)
	}
	g, err := s.createDirect(ctx, tenant, actor, spec, now)
	if err != nil {
		return Created{}, err
	}
	return Created{Grant: &g}, nil
}

// createDirect is the path with no workflow: an authorised administrator
// creates the grant, and that is a complete just-in-time story on its own.
func (s *Service) createDirect(ctx context.Context, tenant store.Tenant, actor Actor, spec Spec, now time.Time) (store.Grant, error) {
	g := grantFor(spec)
	g.ID = s.newID("g")
	g.Origin = store.GrantOriginManual
	g.CreatedBy = actor.grantActor()

	err := s.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.Grants().Insert(ctx, tenant, g); err != nil {
			return err
		}
		stored, err := tx.Grants().Get(ctx, tenant, g.ID)
		if err != nil {
			return err
		}
		g = stored
		return s.recorder.GrantEvent(ctx, tx, tenant, Event{
			Name: EventGrantCreated, Actor: actor, Grant: &g, At: now,
		})
	})
	if err != nil {
		return store.Grant{}, err
	}
	s.announce(ctx, tenant, Event{Name: EventGrantCreated, Actor: actor, Grant: &g, At: now})
	return g, nil
}

// Get returns one grant.
func (s *Service) Get(ctx context.Context, tenant store.Tenant, grantID string) (store.Grant, error) {
	return s.store.Grants().Get(ctx, tenant, grantID)
}

// Inspect returns one grant and the most recent decisions it supplied: what it
// let anybody do, which is the first thing asked of a grant.
func (s *Service) Inspect(ctx context.Context, tenant store.Tenant, grantID string) (store.Grant, []store.Decision, error) {
	g, err := s.store.Grants().Get(ctx, tenant, grantID)
	if err != nil {
		return store.Grant{}, nil, err
	}
	decisions, err := s.store.Decisions().ListByGrant(ctx, tenant, grantID, inspectDecisions)
	if err != nil {
		return store.Grant{}, nil, err
	}
	return g, decisions, nil
}

// List returns a tenant's grants, newest first. A state filter is judged at
// this server's current instant, which is the only instant a list is about.
func (s *Service) List(ctx context.Context, tenant store.Tenant, q store.GrantQuery) ([]store.Grant, error) {
	if q.State != "" {
		q.At = s.now().UTC()
	}
	return s.store.Grants().List(ctx, tenant, q)
}

// Now is the service's clock, so a transport renders a grant's state at the
// same instant the service judged it.
func (s *Service) Now() time.Time { return s.now().UTC() }

// Track adds a tenant to the set the workflow poller walks.
//
// The set is this process's, and it has to be: every repository method names
// its tenant and none can enumerate them (M18), which is the property that
// stops a cross-tenant read from being writable at all. So a tenant is polled
// once this process has seen a request in it — the deployment's configured
// tenant from the start, any other from its first request or its first read —
// and a request nobody asks about after a restart is advanced the moment
// somebody does.
func (s *Service) Track(tenant store.Tenant) {
	if tenant == "" {
		return
	}
	s.mu.Lock()
	s.tenants[tenant] = struct{}{}
	s.mu.Unlock()
}

func (s *Service) tracked() []store.Tenant {
	s.mu.Lock()
	out := make([]store.Tenant, 0, len(s.tenants))
	for t := range s.tenants {
		out = append(out, t)
	}
	s.mu.Unlock()
	slices.Sort(out)
	return out
}

// announce hands an event to the notifier. It never fails the act: the act has
// already committed, and a notification is news about it, not part of it.
func (s *Service) announce(ctx context.Context, tenant store.Tenant, ev Event) {
	if s.notifier == nil {
		return
	}
	s.notifier.Notify(context.WithoutCancel(ctx), notification(tenant, ev))
}

// idAlphabet is base32 without padding: an id that survives being read aloud,
// pasted into a ticket, and typed back in — the same shape as a decision id.
var idAlphabet = base32.StdEncoding.WithPadding(base32.NoPadding)

// newID mints `<prefix>_<random>`. Random rather than derived, so two grants
// for the same thing a week apart are two grants.
func newID(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on any supported platform; if it ever
		// does, a grant with no id is not one to create.
		panic("access: reading randomness for an id: " + err.Error())
	}
	return prefix + "_" + strings.ToLower(idAlphabet.EncodeToString(b[:]))
}
