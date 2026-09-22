// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/hoplock/control/internal/store"
)

// The north-bound credential model (M2) and the tenancy rule it exists to
// enforce (M18).
//
// A CALLER NEVER ASSERTS ITS OWN TENANT. A principal carries the set of tenants
// it may act in; a tenant named in a path, a query or a body is a SELECTOR
// within that set. A selector naming a tenant outside it is refused — before a
// handler runs, in the middleware every north-bound route goes through, because
// the failure mode is one handler that forgot.
//
// The credential is `<tenant>.<secret>` with a one-character kind prefix, which
// is M22's shape reused on this surface and for the same reason: THE CREDENTIAL
// CARRIES ITS OWN TENANT, so nothing is ever looked up across tenants. A forged
// prefix fails the digest comparison in a tenant where no such credential
// exists. The set of tenants the principal may act in is then read from the
// row, which this server wrote.

const (
	credentialSeparator = "."
	// credentialSecretBytes is how much entropy a north-bound credential
	// carries. It is the same as the proxy's, because the consequence of
	// guessing one is comparable: a session or a token here authors policy.
	credentialSecretBytes = 32
	// sessionKindPrefix and tokenKindPrefix make the two kinds
	// distinguishable without a database round trip, so a session secret
	// presented as a token (or the reverse) is refused rather than looked up
	// in the wrong column.
	sessionKindPrefix = "hs"
	tokenKindPrefix   = "ht"
)

// Credential is a north-bound secret in the form it crosses the wire.
type Credential struct {
	// Kind is session or token.
	Kind store.PrincipalKind
	// Tenant is the tenant the credential lives in — the ISSUING tenant,
	// which is not necessarily the only one the principal may act in.
	Tenant store.Tenant
	// Secret is the high-entropy half. Only its digest is ever stored.
	Secret string
}

// String renders the credential in the form it is handed over once.
//
// It is deliberately not a redaction: this value exists to be transported. What
// must never happen is logging it, and that is a rule about callers — the
// north-bound access log writes no header values, for exactly this reason.
func (c Credential) String() string {
	return c.prefix() + credentialSeparator + string(c.Tenant) + credentialSeparator + c.Secret
}

func (c Credential) prefix() string {
	if c.Kind == store.PrincipalSession {
		return sessionKindPrefix
	}
	return tokenKindPrefix
}

// Digest is what the row stores: hex-encoded SHA-256 of the secret.
func (c Credential) Digest() string {
	sum := sha256.Sum256([]byte(c.Secret))
	return hex.EncodeToString(sum[:])
}

// MintCredential issues a north-bound credential for a tenant.
func MintCredential(kind store.PrincipalKind, tenant store.Tenant) (Credential, error) {
	if tenant == "" {
		return Credential{}, fmt.Errorf("identity.MintCredential: tenant is required")
	}
	if strings.Contains(string(tenant), credentialSeparator) {
		return Credential{}, fmt.Errorf(
			"identity.MintCredential: tenant %q contains %q, which a credential cannot carry",
			tenant, credentialSeparator)
	}
	buf := make([]byte, credentialSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return Credential{}, fmt.Errorf("identity.MintCredential: %w", err)
	}
	return Credential{
		Kind:   kind,
		Tenant: tenant,
		Secret: base64.RawURLEncoding.EncodeToString(buf),
	}, nil
}

// ParseCredential reads a presented credential, resolving the kind and the
// tenant and nothing else.
//
// A malformed credential is not an error: "this is not shaped like a token" and
// "this token's secret is wrong" are the same fact to a caller, and telling
// them apart would make the listener an oracle.
func ParseCredential(presented string) (Credential, bool) {
	prefix, rest, ok := strings.Cut(presented, credentialSeparator)
	if !ok {
		return Credential{}, false
	}
	tenant, secret, ok := strings.Cut(rest, credentialSeparator)
	if !ok || tenant == "" || secret == "" {
		return Credential{}, false
	}
	switch prefix {
	case sessionKindPrefix:
		return Credential{Kind: store.PrincipalSession, Tenant: store.Tenant(tenant), Secret: secret}, true
	case tokenKindPrefix:
		return Credential{Kind: store.PrincipalToken, Tenant: store.Tenant(tenant), Secret: secret}, true
	}
	return Credential{}, false
}

// Principal is an authenticated north-bound caller.
//
// It is what the middleware puts on the request context, and the ONLY thing a
// handler may consult about who is calling. There is deliberately no accessor
// that answers "all the tenants this request covers": every request resolves to
// exactly one tenant, so an audit record never has to say "some of them" (M18).
type Principal struct {
	// ID identifies the credential. Safe to log.
	ID string
	// Kind is session or token.
	Kind store.PrincipalKind
	// Tenant is the tenant that ISSUED the credential. It is the default
	// selector and never the limit: what a principal may reach is Scopes.
	Tenant store.Tenant
	// Subject is the person behind a session; empty for a machine token.
	Subject string
	// DisplayName is for operators and for the console.
	DisplayName string
	// Source is "local" for the break-glass path, or the connector that
	// asserted the identity.
	Source string
	// BreakGlass reports that this credential came from the local
	// development and break-glass path. It is asserted when the credential
	// is minted and is carried into every decision and audit record this
	// principal touches (M7): a break-glass login that looks like a normal
	// one is an audit failure.
	BreakGlass bool
	// MappingVersion is the claim-mapping version that produced this
	// principal's attributes; zero when no mapping was involved.
	MappingVersion int
	// Groups are the groups this principal holds, local and mapped
	// together. A rule must not be able to tell which was which.
	Groups []string
	// Attributes are the mapped claims. Nothing that did not pass through
	// the mapping is in here.
	Attributes map[string]string

	// scopes is the set of tenants this principal may act in, with the roles
	// it holds in each. It is unexported so that the only way to ask a
	// question of it is through the two methods below — a handler cannot
	// iterate it, which is what stops a cross-tenant aggregate route from
	// being easy to write.
	scopes map[store.Tenant]RoleSet
}

// NewPrincipal builds a principal with a scope set. It is exported for tests
// and for the operator commands; the middleware builds one from a row.
func NewPrincipal(id string, kind store.PrincipalKind, tenant store.Tenant, scopes map[store.Tenant]RoleSet) *Principal {
	p := &Principal{ID: id, Kind: kind, Tenant: tenant, scopes: map[store.Tenant]RoleSet{}}
	for t, rs := range scopes {
		p.scopes[t] = slices.Clone(rs)
	}
	return p
}

// MayActIn reports whether a tenant is inside this principal's scope.
//
// This is the whole of M18's north-bound half, and it is one method so that
// there is one place to read and one place to test.
func (p *Principal) MayActIn(tenant store.Tenant) bool {
	if p == nil || tenant == "" {
		return false
	}
	_, ok := p.scopes[tenant]
	return ok
}

// RolesIn returns the roles this principal holds in one tenant, and nothing
// outside it. A role granted in tenant A confers nothing in tenant B.
func (p *Principal) RolesIn(tenant store.Tenant) RoleSet {
	if p == nil {
		return nil
	}
	return slices.Clone(p.scopes[tenant])
}

// Can answers the permission question for one tenant. It is the only
// permission check on this surface.
func (p *Principal) Can(tenant store.Tenant, perm Permission) bool {
	return p.RolesIn(tenant).Can(perm)
}

// SoleTenant returns the one tenant this principal may act in, and false when
// it may act in none or in several.
//
// It exists for M18's "single-tenant deployments look untouched": a route with
// no tenant selector resolves to this when there is exactly one, so an operator
// who never uses tenancy never types a tenant. A principal scoped to several
// tenants must select, because a default there would silently pick one.
func (p *Principal) SoleTenant() (store.Tenant, bool) {
	if p == nil || len(p.scopes) != 1 {
		return "", false
	}
	for t := range p.scopes {
		return t, true
	}
	return "", false
}

// ScopedTenants returns the tenants in scope, sorted. It exists for the
// `/session` response and for logs — the two places a human needs to see the
// set — and nothing decides access with it.
func (p *Principal) ScopedTenants() []store.Tenant {
	if p == nil {
		return nil
	}
	out := slices.Collect(maps.Keys(p.scopes))
	slices.Sort(out)
	return out
}

// principalFromRow builds a Principal from a stored row, dropping any role code
// this build does not define.
//
// Dropping rather than failing is deliberate: a binding written by a newer
// release must not lock an older one out of every route it could still serve.
// What it must never do is grant something, and [ParseRole] is what guarantees
// that.
func principalFromRow(row store.NorthPrincipal) *Principal {
	p := &Principal{
		ID:             row.ID,
		Kind:           row.Kind,
		Subject:        row.SubjectID,
		DisplayName:    row.DisplayName,
		Source:         row.Source,
		BreakGlass:     row.BreakGlass,
		MappingVersion: row.MappingVersion,
		Attributes:     map[string]string{},
		scopes:         map[store.Tenant]RoleSet{},
	}
	for tenant, codes := range row.Scopes {
		if tenant == "" {
			continue
		}
		set := make(RoleSet, 0, len(codes))
		for _, c := range codes {
			if r, err := ParseRole(c); err == nil {
				set = append(set, r)
			}
		}
		slices.Sort(set)
		p.scopes[store.Tenant(tenant)] = slices.Compact(set)
	}
	return p
}

// scopesToRow renders a scope set for storage.
func scopesToRow(scopes map[store.Tenant]RoleSet) map[string][]string {
	out := make(map[string][]string, len(scopes))
	for t, rs := range scopes {
		out[string(t)] = rs.Strings()
	}
	return out
}
