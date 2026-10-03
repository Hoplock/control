// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// Defaults for a Service.
const (
	// DefaultCeiling is the server's ceiling on an external window's length
	// (M16), applied after the binding's own maximum and regardless of what
	// the external system asserted. A window longer than a working shift is
	// standing access with a timer on it, and the integration asking for one
	// is told how long it got, not refused.
	DefaultCeiling = 12 * time.Hour
	// DefaultClockSkew is how far an external system's clock may disagree
	// with this server's before an assertion's start or issue time is read
	// as meaning something.
	DefaultClockSkew = 2 * time.Minute
	// DefaultMaxAssertionAge is how old an assertion may be when it arrives,
	// when it says when it was issued. Older is the replay an id cannot catch.
	DefaultMaxAssertionAge = 15 * time.Minute
	// DefaultPushRate and DefaultPushBurst bound one integration's pushes in
	// one tenant: a sustained rate per second, and how many may arrive at
	// once. A scanner starting a scan pushes a burst; one pushing without
	// pause is broken or hostile, and either way the audit chain should not
	// be what absorbs it.
	DefaultPushRate  = 5.0
	DefaultPushBurst = 20
	// DefaultInterpretTimeout bounds one call to a provider's Interpret.
	DefaultInterpretTimeout = 2 * time.Second
)

// ScopePolicy answers what the active policy declares about a grant scope
// (the bundle's `scopes` section). `decision.Service` implements it over the
// compiled program it already holds, so the push path and the decision path
// read one declaration, never two copies of it.
type ScopePolicy interface {
	ScopeDeclaration(ctx context.Context, tenant store.Tenant, scope string) (model.ScopeDecl, error)
}

// Service is the administrative half of external access context: the scope
// bindings, and the push receiver that admits a window through them.
type Service struct {
	store     *store.Store
	grants    *access.Service
	recorder  Recorder
	providers *Providers
	policy    ScopePolicy
	limiter   *limiter

	ceiling          time.Duration
	skew             time.Duration
	maxAge           time.Duration
	interpretTimeout time.Duration

	now func() time.Time
	log *slog.Logger
}

// Options configures a Service.
type Options struct {
	// Store holds the bindings and the grants. Required.
	Store *store.Store
	// Grants creates the grant an admitted push becomes. Required: a pushed
	// window is created through the same path as every other grant, audited
	// and announced, or not at all.
	Grants *access.Service
	// Recorder writes the audit record of every refusal and binding change.
	// Required.
	Recorder Recorder
	// Providers is the set the server runs. Nil is the empty set.
	Providers *Providers
	// Policy answers whether the policy marks a scope privileged. Required.
	Policy ScopePolicy
	// Ceiling is the server's ceiling on a window. Zero takes DefaultCeiling.
	Ceiling time.Duration
	// ClockSkew and MaxAssertionAge bound an assertion's clock. Zero takes
	// the default.
	ClockSkew       time.Duration
	MaxAssertionAge time.Duration
	// PushRate and PushBurst bound one integration's pushes in one tenant.
	// Zero takes the default.
	PushRate  float64
	PushBurst int
	// InterpretTimeout bounds one call to a provider's Interpret. Zero takes
	// the default.
	InterpretTimeout time.Duration
	// Logger is where this package writes what it alone witnesses.
	Logger *slog.Logger
	// Now overrides the clock. Tests use it.
	Now func() time.Time
}

// New builds a Service.
func New(o Options) (*Service, error) {
	switch {
	case o.Store == nil:
		return nil, fmt.Errorf("accessctx: a store is required")
	case o.Grants == nil:
		return nil, fmt.Errorf("accessctx: the grant service is required: a pushed window becomes a grant through it or not at all")
	case o.Recorder == nil:
		return nil, fmt.Errorf("accessctx: an audit recorder is required: a refused escalation nobody wrote down did not happen")
	case o.Policy == nil:
		return nil, fmt.Errorf("accessctx: a scope policy is required: whether a window is privileged is the policy's to say")
	}
	s := &Service{
		store:            o.Store,
		grants:           o.Grants,
		recorder:         o.Recorder,
		providers:        o.Providers,
		policy:           o.Policy,
		ceiling:          or(o.Ceiling, DefaultCeiling),
		skew:             or(o.ClockSkew, DefaultClockSkew),
		maxAge:           or(o.MaxAssertionAge, DefaultMaxAssertionAge),
		interpretTimeout: or(o.InterpretTimeout, DefaultInterpretTimeout),
		now:              o.Now,
		log:              o.Logger,
	}
	rate, burst := o.PushRate, o.PushBurst
	if rate <= 0 {
		rate = DefaultPushRate
	}
	if burst <= 0 {
		burst = DefaultPushBurst
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	s.limiter = newLimiter(rate, burst, s.now)
	return s, nil
}

// Providers is the set the service admits pushes for.
func (s *Service) Providers() *Providers { return s.providers }

// Ceiling is the server's ceiling on an external window.
func (s *Service) Ceiling() time.Duration { return s.ceiling }

func or(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
