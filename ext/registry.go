// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"fmt"
	"sort"
	"strings"
)

// ProviderControl is the provider name this repository registers its own
// implementations under. It is also the only name a Registration may carry
// when Default is set: an out-of-tree registration claiming to be Control's
// default would make the registry listing lie about where behaviour came from,
// and the listing is the only way an operator can tell.
const ProviderControl = "hoplock/control"

// Registration is who is registering, and in what capacity. It travels with
// the implementation so that every later listing — a start-up log line, the
// north-bound registry endpoint (phase 0014) — can name the code actually in
// play. An extension nobody can see is indistinguishable from a bug in
// Control, which is why this is required rather than optional.
type Registration struct {
	// Provider is the module or component registering, e.g.
	// "github.com/hoplock/enterprise/siem". Required, and required to be
	// unique within a point.
	Provider string
	// Version is the provider's own version, free-form. Optional; it is for
	// an operator reading the listing, and nothing branches on it.
	Version string
	// Default marks this as Control's own implementation rather than an
	// extension. Only this repository's wiring sets it, and only with
	// Provider == ProviderControl. A default is superseded by an extension
	// at a single-implementation point, and the listing records that it was.
	Default bool
}

// String renders the registration for logs and error messages.
func (r Registration) String() string {
	s := r.Provider
	if s == "" {
		s = "(unnamed)"
	}
	if r.Version != "" {
		s += "@" + r.Version
	}
	if r.Default {
		s += " (control default)"
	}
	return s
}

// DuplicateError reports a second registration where only one is allowed. It
// names both sides, because the whole point of rejecting it is that the
// operator has to be told which two things are fighting: silent last-wins is
// how two Enterprise modules disagree invisibly for a release.
type DuplicateError struct {
	// Point is the contested extension point.
	Point Point
	// First is the registration already in place.
	First Registration
	// Second is the registration that was refused.
	Second Registration
	// SameProvider reports that the collision is one provider registering
	// twice, which is always an error even where several providers may
	// register.
	SameProvider bool
}

// Error implements error.
func (e *DuplicateError) Error() string {
	if e.SameProvider {
		return fmt.Sprintf("ext: %s already has a registration from %s; %s may not register twice",
			e.Point, e.First, e.Second.Provider)
	}
	return fmt.Sprintf("ext: %s accepts one implementation and %s registered it; %s was refused",
		e.Point, e.First, e.Second)
}

// binding is one registered implementation and its provenance.
type binding struct {
	reg  Registration
	impl any
}

// Registry collects extension implementations before the server starts. It is
// deliberately write-only: there is no way to read an implementation out of a
// Registry, so no request can ever see a half-registered extension. Reading
// requires Seal, which yields an immutable *Extensions and closes the registry
// for good.
//
// A Registry is not safe for concurrent use. It is populated by one goroutine
// at start-up — a host binary's main, wiring interfaces — and that is the only
// shape this seam supports. There is no plugin loading and no reflection: an
// Enterprise binary is a Go program that imports this module, registers its
// implementations, and starts the server.
type Registry struct {
	single map[Point]binding
	multi  map[Point][]binding
	sealed bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		single: make(map[Point]binding),
		multi:  make(map[Point][]binding),
	}
}

// Seal closes the registry and returns the immutable set of extensions the
// server will run with. After Seal, every Register call fails, so the set a
// request sees can never change underneath it.
//
// Seal is where the catalogue is checked against what was actually registered,
// so a point whose PointInfo promises Control's own default but where the
// wiring registered none is a start-up failure rather than a nil dereference
// on the first request that needs it.
func (r *Registry) Seal() (*Extensions, error) {
	if r.sealed {
		return nil, fmt.Errorf("ext: Registry.Seal: %w: the registry is already sealed", ErrConflict)
	}
	for _, info := range points {
		if info.WhenAbsent != WhenAbsentDefault {
			continue
		}
		if _, ok := r.single[info.Point]; ok {
			continue
		}
		if len(r.multi[info.Point]) > 0 {
			continue
		}
		return nil, Errorf(info.Point, ProviderControl, "Registry.Seal", KindInvalid,
			"%s promises an implementation from Control's own wiring and none was registered", info.Point)
	}
	r.sealed = true

	x := &Extensions{
		single: make(map[Point]binding, len(r.single)),
		multi:  make(map[Point][]binding, len(r.multi)),
	}
	for p, b := range r.single {
		x.single[p] = b
	}
	for p, bs := range r.multi {
		x.multi[p] = append([]binding(nil), bs...)
	}
	// The registry keeps its bindings so that Status stays truthful after
	// sealing. Nothing can be read out of it: there is no accessor for an
	// implementation on this type, only for the metadata a host binary logs.
	return x, nil
}

// Status returns what has been registered so far, one row per extension point
// and in Point order, so a host binary can log its own wiring before handing
// the registry over. It exposes registrations, never implementations: reading
// an implementation requires Seal, which is what makes a half-registered
// extension unreachable rather than merely discouraged.
func (r *Registry) Status() []Status {
	out := make([]Status, 0, len(points))
	for _, info := range points {
		s := Status{Info: info}
		if b, ok := r.single[info.Point]; ok {
			s.Registrations = []Registration{b.reg}
		}
		for _, b := range r.multi[info.Point] {
			s.Registrations = append(s.Registrations, b.reg)
		}
		out = append(out, s)
	}
	return out
}

// register is the shared body of every RegisterX method.
func register(r *Registry, p Point, reg Registration, impl any) error {
	info, ok := Lookup(p)
	if !ok {
		return Errorf(p, reg.Provider, "Registry.Register", KindInvalid, "unknown extension point %d", int(p))
	}
	if r.sealed {
		return Errorf(p, reg.Provider, "Registry.Register", KindConflict,
			"the registry is sealed; extensions are registered before the server starts and never after")
	}
	if strings.TrimSpace(reg.Provider) == "" {
		return Errorf(p, reg.Provider, "Registry.Register", KindInvalid,
			"a registration must name its provider: an extension nobody can see in the registry listing is indistinguishable from a bug in Control")
	}
	if reg.Default && reg.Provider != ProviderControl {
		return Errorf(p, reg.Provider, "Registry.Register", KindInvalid,
			"only %s may register a default; %s must register as an extension", ProviderControl, reg.Provider)
	}
	if impl == nil {
		return Errorf(p, reg.Provider, "Registry.Register", KindInvalid, "a nil implementation was registered for %s", p)
	}

	if !info.Multiple {
		existing, held := r.single[p]
		if held {
			// A default and an extension are not a conflict: the
			// extension supersedes it, and the listing says so. Two
			// extensions, or two defaults, are.
			switch {
			case existing.reg.Default && !reg.Default:
				r.single[p] = binding{reg: reg, impl: impl}
				return nil
			case !existing.reg.Default && reg.Default:
				return nil
			default:
				return &DuplicateError{Point: p, First: existing.reg, Second: reg,
					SameProvider: existing.reg.Provider == reg.Provider}
			}
		}
		r.single[p] = binding{reg: reg, impl: impl}
		return nil
	}

	for _, b := range r.multi[p] {
		if b.reg.Provider == reg.Provider {
			return &DuplicateError{Point: p, First: b.reg, Second: reg, SameProvider: true}
		}
	}
	r.multi[p] = append(r.multi[p], binding{reg: reg, impl: impl})
	return nil
}

// RegisterAuditSink adds an audit export destination. Several may register.
func (r *Registry) RegisterAuditSink(reg Registration, s AuditSink) error {
	return register(r, PointAuditSink, reg, s)
}

// RegisterArchiveStore sets the long-term audit archive. One may register.
func (r *Registry) RegisterArchiveStore(reg Registration, s ArchiveStore) error {
	return register(r, PointArchiveStore, reg, s)
}

// RegisterGrantWorkflow sets the grant approval workflow. One may register.
func (r *Registry) RegisterGrantWorkflow(reg Registration, w GrantWorkflow) error {
	return register(r, PointGrantWorkflow, reg, w)
}

// RegisterIdentitySync sets the identity provisioning source. One may register.
func (r *Registry) RegisterIdentitySync(reg Registration, s IdentitySync) error {
	return register(r, PointIdentitySync, reg, s)
}

// RegisterKeyStore sets key custody. One may register.
func (r *Registry) RegisterKeyStore(reg Registration, k KeyStore) error {
	return register(r, PointKeyStore, reg, k)
}

// RegisterNotifier adds a notification channel. Several may register.
func (r *Registry) RegisterNotifier(reg Registration, n Notifier) error {
	return register(r, PointNotifier, reg, n)
}

// RegisterClusterCoordinator sets cluster membership, singletons and the event
// bus. One may register; Control's wiring registers the single-node default.
func (r *Registry) RegisterClusterCoordinator(reg Registration, c ClusterCoordinator) error {
	return register(r, PointClusterCoordinator, reg, c)
}

// RegisterActionHandler sets the inbound action channel. One may register.
func (r *Registry) RegisterActionHandler(reg Registration, h ActionHandler) error {
	return register(r, PointActionHandler, reg, h)
}

// RegisterReportProvider adds a reporting pack. Several may register.
func (r *Registry) RegisterReportProvider(reg Registration, p ReportProvider) error {
	return register(r, PointReportProvider, reg, p)
}

// RegisterPolicyValidator adds a validator to the chain. Several may register;
// all of them run.
func (r *Registry) RegisterPolicyValidator(reg Registration, v PolicyValidator) error {
	return register(r, PointPolicyValidator, reg, v)
}

// RegisterAccessContextProvider adds an external access-context source (M16).
// Several may register.
func (r *Registry) RegisterAccessContextProvider(reg Registration, p AccessContextProvider) error {
	return register(r, PointAccessContextProvider, reg, p)
}

// Bound is one registered implementation together with its provenance, so a
// caller iterating a multiple-implementation point can attribute what happened
// to the provider that did it — in a decision record, in an audit line, or in
// an error.
type Bound[T any] struct {
	// Registration is who supplied Impl.
	Registration Registration
	// Impl is the implementation.
	Impl T
}

// Extensions is the sealed, immutable set of extensions a server runs with. It
// is safe for concurrent use because nothing can change it: the only way to
// obtain one is Registry.Seal, and the only operations on it are reads.
type Extensions struct {
	single map[Point]binding
	multi  map[Point][]binding
}

// NoExtensions returns a sealed, empty set. It is what a server runs with when
// nothing at all is registered — every point then behaves as its PointInfo's
// Absent line says — and it is the convenient value for a test that does not
// care about extensions.
//
// Note that it deliberately skips the Seal check for Control's own defaults:
// a test wanting the defaults should build them through Control's wiring.
func NoExtensions() *Extensions {
	return &Extensions{single: map[Point]binding{}, multi: map[Point][]binding{}}
}

// lookupSingle fetches a single-implementation point.
func lookupSingle[T any](x *Extensions, p Point) (T, bool) {
	var zero T
	b, ok := x.single[p]
	if !ok {
		return zero, false
	}
	impl, ok := b.impl.(T)
	return impl, ok
}

// lookupMulti fetches every implementation at a multiple-implementation point,
// in registration order.
func lookupMulti[T any](x *Extensions, p Point) []Bound[T] {
	bs := x.multi[p]
	if len(bs) == 0 {
		return nil
	}
	out := make([]Bound[T], 0, len(bs))
	for _, b := range bs {
		if impl, ok := b.impl.(T); ok {
			out = append(out, Bound[T]{Registration: b.reg, Impl: impl})
		}
	}
	return out
}

// AuditSinks returns every registered export destination, in registration
// order. Empty means audit records stay in the local store and go nowhere
// else, which is a complete deployment rather than a broken one.
func (x *Extensions) AuditSinks() []Bound[AuditSink] {
	return lookupMulti[AuditSink](x, PointAuditSink)
}

// ArchiveStore returns the long-term archive, if one is registered.
func (x *Extensions) ArchiveStore() (ArchiveStore, bool) {
	return lookupSingle[ArchiveStore](x, PointArchiveStore)
}

// GrantWorkflow returns the approval workflow, if one is registered.
func (x *Extensions) GrantWorkflow() (GrantWorkflow, bool) {
	return lookupSingle[GrantWorkflow](x, PointGrantWorkflow)
}

// IdentitySync returns the provisioning source, if one is registered.
func (x *Extensions) IdentitySync() (IdentitySync, bool) {
	return lookupSingle[IdentitySync](x, PointIdentitySync)
}

// KeyStore returns external key custody, if any is registered.
func (x *Extensions) KeyStore() (KeyStore, bool) {
	return lookupSingle[KeyStore](x, PointKeyStore)
}

// Notifiers returns every registered notification channel, in registration
// order.
func (x *Extensions) Notifiers() []Bound[Notifier] {
	return lookupMulti[Notifier](x, PointNotifier)
}

// ClusterCoordinator returns the coordinator. The second result is false only
// for a set built by NoExtensions: a server sealed through Registry.Seal
// always has one, because the catalogue requires Control's wiring to register
// the single-node default.
func (x *Extensions) ClusterCoordinator() (ClusterCoordinator, bool) {
	return lookupSingle[ClusterCoordinator](x, PointClusterCoordinator)
}

// ActionHandler returns the inbound action channel, if one is registered.
func (x *Extensions) ActionHandler() (ActionHandler, bool) {
	return lookupSingle[ActionHandler](x, PointActionHandler)
}

// ReportProviders returns every registered reporting pack, in registration
// order.
func (x *Extensions) ReportProviders() []Bound[ReportProvider] {
	return lookupMulti[ReportProvider](x, PointReportProvider)
}

// PolicyValidators returns the validator chain, in registration order. Every
// validator runs; the compiler's own checks run regardless and are not part of
// this chain.
func (x *Extensions) PolicyValidators() []Bound[PolicyValidator] {
	return lookupMulti[PolicyValidator](x, PointPolicyValidator)
}

// AccessContextProviders returns every registered external access-context
// source (M16), in registration order.
func (x *Extensions) AccessContextProviders() []Bound[AccessContextProvider] {
	return lookupMulti[AccessContextProvider](x, PointAccessContextProvider)
}

// Status is one row of the operator-facing view of what is in play: the point,
// what is registered there, and what happens when nothing is. It is what the
// start-up log prints and what the north-bound API exposes (phase 0014).
type Status struct {
	// Info is the point's catalogue entry.
	Info PointInfo
	// Registrations are the implementations in play, in registration order.
	// Empty means nothing is registered and Info.Absent describes what
	// happens instead.
	Registrations []Registration
}

// Registered reports whether anything is registered at this point.
func (s Status) Registered() bool { return len(s.Registrations) > 0 }

// String renders one operator-readable line.
func (s Status) String() string {
	if !s.Registered() {
		return fmt.Sprintf("%s: none registered (%s) — %s", s.Info.Point, s.Info.WhenAbsent, s.Info.Absent)
	}
	names := make([]string, 0, len(s.Registrations))
	for _, r := range s.Registrations {
		names = append(names, r.String())
	}
	return fmt.Sprintf("%s: %s", s.Info.Point, strings.Join(names, ", "))
}

// Status returns one row per extension point, in Point order — including the
// points where nothing is registered, because "nothing is registered here" is
// exactly what an operator debugging behaviour needs to be able to see.
func (x *Extensions) Status() []Status {
	out := make([]Status, 0, len(points))
	for _, info := range points {
		s := Status{Info: info}
		if b, ok := x.single[info.Point]; ok {
			s.Registrations = []Registration{b.reg}
		}
		for _, b := range x.multi[info.Point] {
			s.Registrations = append(s.Registrations, b.reg)
		}
		out = append(out, s)
	}
	return out
}

// Providers returns the distinct provider names with anything registered,
// sorted. It is the short form of Status for a log line that only needs to say
// which code is in play.
func (x *Extensions) Providers() []string {
	seen := make(map[string]struct{})
	for _, b := range x.single {
		seen[b.reg.Provider] = struct{}{}
	}
	for _, bs := range x.multi {
		for _, b := range bs {
			seen[b.reg.Provider] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
