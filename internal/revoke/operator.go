// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"context"
	"fmt"

	"github.com/hoplock/control/internal/store"
)

// The operator surface: everything an operator action can ask this server to
// publish, and nothing about how the operator was authenticated.
//
// It is MINIMAL HERE ON PURPOSE. The north-bound API is 0014's and the audit
// record of an operator action is 0010's; what this phase owes is the shape
// those two will put in front of, because whatever 0014 exposes inherits the
// vocabulary chosen here. The one place that shape is opinionated is the
// host-key asymmetry below, and it is opinionated because the alternative
// misleads.

// HostKeyDecisions resolves a recorded host-key decision to the key it was
// issued under. `fleet.Registry` implements it.
//
// It is an interface rather than an import because the dependency runs the
// other way: `fleet` owns the record and this package owns the stream, and a
// broker that reached into the fleet's storage would be the wrong package
// owning the one piece of genuinely shared state this system has.
type HostKeyDecisions interface {
	HostKeyCacheKeyOf(ctx context.Context, tenant store.Tenant, hostname string, port int32, fingerprint string) (string, error)
}

// HostKeyRef names one host-key decision, in the shape the proxy keys its
// reuse on: target, port and key fingerprint, and nothing wider.
type HostKeyRef struct {
	Target      string
	Port        int32
	Fingerprint string
}

// Operator publishes operator actions.
type Operator struct {
	bus  *Bus
	keys HostKeyDecisions
}

// NewOperator builds the surface. keys may be nil, in which case withdrawing a
// host-key decision by key is refused rather than guessed at.
func NewOperator(bus *Bus, keys HostKeyDecisions) *Operator {
	return &Operator{bus: bus, keys: keys}
}

// Kill ends sessions that are running now.
func (o *Operator) Kill(ctx context.Context, tenant store.Tenant, aud Audience, k Kill) (Receipt, error) {
	return o.bus.Kill(ctx, tenant, aud, k)
}

// Invalidate drops cached decisions.
//
// The receipt says whether what was published can reach a HOST-KEY decision,
// and a subject-scoped invalidation cannot: the proxy keys a host-key decision
// on target, port and fingerprint, so `subject` matches none of them. An
// operator who publishes "invalidate everything for Alice" and reads the
// receipt learns that, rather than assuming a target's host key went with it.
func (o *Operator) Invalidate(ctx context.Context, tenant store.Tenant, aud Audience, inv Invalidation) (Receipt, error) {
	return o.bus.Invalidate(ctx, tenant, aud, inv)
}

// Resync tells subscribers to drop everything and re-authorize.
func (o *Operator) Resync(ctx context.Context, tenant store.Tenant, aud Audience) (Receipt, error) {
	return o.bus.Resync(ctx, tenant, aud)
}

// WithdrawHostKey withdraws one host-key decision by publishing the key it was
// issued under.
//
// It RESOLVES THE KEY FROM THE RECORD rather than deriving one. The derivation
// is deterministic, so computing a key here would always produce something
// publishable — including for a target and fingerprint nobody has reported,
// and including under a scope a later revision changed. That publication would
// succeed, report success, and drop nothing. The two failures it can return
// instead are `fleet`'s: no such decision, and a decision recorded before this
// server issued keys, whose only honest withdrawal is [Operator.Resync].
func (o *Operator) WithdrawHostKey(ctx context.Context, tenant store.Tenant, aud Audience, ref HostKeyRef) (Receipt, error) {
	if o.keys == nil {
		return Receipt{}, fmt.Errorf("revoke: this server cannot resolve host-key decisions, so one cannot be withdrawn by key")
	}
	key, err := o.keys.HostKeyCacheKeyOf(ctx, tenant, ref.Target, ref.Port, ref.Fingerprint)
	if err != nil {
		return Receipt{}, err
	}
	return o.bus.Invalidate(ctx, tenant, aud, Invalidation{Keys: []string{key}})
}
