// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package accessctx

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/hoplock/control/ext"
)

// DefaultMaxProbesInFlight bounds how many probes one provider may have
// running at once. A provider that ignores its context is abandoned at the
// probe deadline, but its goroutine runs on; past this many, a new probe is
// answered "undetermined: saturated" at once rather than starting another.
// A hung integration then costs a bounded number of goroutines, not one per
// connection the fleet opens.
const DefaultMaxProbesInFlight = 32

// nameRule is what an access-context provider may be called. The name is a
// path segment of the push receiver, a key in the scope bindings, and the
// `system` an auditor reads on every session the provider's windows back, so
// it is a short code rather than prose.
var nameRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidName reports whether name can name an access-context provider.
func ValidName(name string) bool { return nameRule.MatchString(name) }

// Provider is one access-context provider the server runs.
type Provider struct {
	// Info is what the provider said about itself at start-up.
	Info ext.AccessContextInfo
	// Registration is how it was registered: the code behind it.
	Registration ext.Registration

	impl  ext.AccessContextProvider
	slots chan struct{}
}

// Providers is the set of providers a server runs, keyed by name. It is built
// once, from the sealed registry, and never changes afterwards — registration
// is before start and immutable (0004), and so is this.
type Providers struct {
	byName map[string]*Provider
	names  []string
}

// NewProviders builds the set from the registry's access-context providers.
//
// Two providers answering to one name is an error naming both, never a
// last-wins: the name is what a binding trusts, and a binding that silently
// starts trusting different code is the failure the registry's own duplicate
// rule exists to prevent. So is a name that is not a short code, and a
// provider that implements neither direction.
func NewProviders(bound []ext.Bound[ext.AccessContextProvider], maxInFlight int) (*Providers, error) {
	if maxInFlight <= 0 {
		maxInFlight = DefaultMaxProbesInFlight
	}
	p := &Providers{byName: make(map[string]*Provider, len(bound))}
	for _, b := range bound {
		info := b.Impl.Describe()
		switch {
		case !ValidName(info.Name):
			return nil, fmt.Errorf("accessctx: provider %s calls itself %q; a provider's name is lower-case letters, digits and hyphens, at most 63 characters",
				b.Registration, info.Name)
		case !info.Probes && !info.Pushes:
			return nil, fmt.Errorf("accessctx: provider %s (%s) implements neither direction; it would never be asked anything",
				info.Name, b.Registration)
		}
		if prev, dup := p.byName[info.Name]; dup {
			return nil, fmt.Errorf("accessctx: two providers answer to %q: %s and %s; a scope binding trusts a name, so it must name one provider",
				info.Name, prev.Registration, b.Registration)
		}
		p.byName[info.Name] = &Provider{
			Info:         info,
			Registration: b.Registration,
			impl:         b.Impl,
			slots:        make(chan struct{}, maxInFlight),
		}
		p.names = append(p.names, info.Name)
	}
	slices.Sort(p.names)
	return p, nil
}

// Lookup returns the provider with this name.
func (p *Providers) Lookup(name string) (*Provider, bool) {
	if p == nil {
		return nil, false
	}
	pr, ok := p.byName[name]
	return pr, ok
}

// Names returns every provider's name, sorted.
func (p *Providers) Names() []string {
	if p == nil {
		return nil
	}
	return slices.Clone(p.names)
}

// List returns every provider, by name: what the north-bound listing (0019)
// shows beside the registry's own rows.
func (p *Providers) List() []Provider {
	if p == nil {
		return nil
	}
	out := make([]Provider, 0, len(p.names))
	for _, n := range p.names {
		pr := p.byName[n]
		out = append(out, Provider{Info: pr.Info, Registration: pr.Registration})
	}
	return out
}
