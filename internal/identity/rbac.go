// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hoplock/control/internal/store"
)

// RBAC: the role set, the permission set, and the one place a permission check
// is answered.
//
// THE ROLE SET IS FIXED AND LIVES IN CODE. There is no `roles` table, and that
// is a decision rather than an omission: a role whose permissions are rows is
// a role whose permissions can be widened by an UPDATE, and "admin means these
// permissions" becomes a fact about somebody's database rather than about the
// product. A deployment that needs a different set needs a different release,
// which is the point — and it is what lets this file be the documentation.
//
// What IS data is who holds which role, per tenant
// (`identity_role_bindings`). A role granted in tenant A confers nothing in
// tenant B, including to an administrator, and that is the binding's primary
// key rather than a filter somebody remembers to apply (M18).

// Permission is one thing a caller may do. Closed set, so a switch over it is
// checked for exhaustiveness (M13) and so that a route cannot be registered
// demanding a permission nobody granted.
type Permission string

const (
	// PermPolicyRead is reading bundles, rules and simulations.
	PermPolicyRead Permission = "policy:read"
	// PermPolicyWrite is authoring a bundle.
	PermPolicyWrite Permission = "policy:write"
	// PermPolicyPublish is making a bundle the active one — a separate
	// permission from authoring it, because the review step is the control.
	PermPolicyPublish Permission = "policy:publish"

	// PermGrantRead is reading just-in-time grants and requests (M10).
	PermGrantRead Permission = "grant:read"
	// PermGrantWrite is raising and withdrawing them.
	PermGrantWrite Permission = "grant:write"
	// PermGrantApprove is approving one. Separate from writing for the same
	// reason publishing is separate from authoring.
	PermGrantApprove Permission = "grant:approve"

	// PermFleetRead is reading proxies, zones and the graph (M6).
	PermFleetRead Permission = "fleet:read"
	// PermFleetWrite is enrolling, approving and revoking a proxy.
	PermFleetWrite Permission = "fleet:write"

	// PermIdentityRead is reading subjects, groups, connectors and the
	// claim mapping.
	PermIdentityRead Permission = "identity:read"
	// PermIdentityWrite is changing any of them. It is an ADMIN
	// permission: the claim mapping decides which IdP claims become policy
	// attributes, so whoever can write it can grant themselves a group.
	PermIdentityWrite Permission = "identity:write"

	// PermCARead is reading the certificate authority's public half and the
	// certificates it has issued.
	PermCARead Permission = "ca:read"
	// PermCARotate is rotating the CA key. Separate from identity:write
	// because a rotation can invalidate every outstanding certificate in
	// the tenant, which is an availability decision as much as a security
	// one.
	PermCARotate Permission = "ca:rotate"

	// PermAuditRead is reading the audit store (M8).
	PermAuditRead Permission = "audit:read"
	// PermDecisionRead is resolving a decision or session id into the whole
	// story (M4).
	PermDecisionRead Permission = "decision:read"
)

// AllPermissions is every permission, in a stable order. A test asserts each
// one is held by at least one role: a permission no role grants is a route
// nobody can reach, which is a configuration bug that looks like a bug report.
var AllPermissions = []Permission{
	PermPolicyRead, PermPolicyWrite, PermPolicyPublish,
	PermGrantRead, PermGrantWrite, PermGrantApprove,
	PermFleetRead, PermFleetWrite,
	PermIdentityRead, PermIdentityWrite,
	PermCARead, PermCARotate,
	PermAuditRead, PermDecisionRead,
}

// Role is a named permission set. Closed set (M13).
type Role string

const (
	// RoleAuditor can read everything and change nothing. It is the role a
	// compliance function holds, and it is deliberately total across the
	// reads: an auditor who has to ask an engineer for a record is not an
	// auditor.
	RoleAuditor Role = "auditor"
	// RolePolicyAuthor writes and publishes policy.
	RolePolicyAuthor Role = "policy-author"
	// RoleGrantAdmin runs the just-in-time access desk (M10).
	RoleGrantAdmin Role = "grant-admin"
	// RoleFleetAdmin enrolls and retires proxies (M6).
	RoleFleetAdmin Role = "fleet-admin"
	// RoleAdmin holds every permission, including the two that can grant
	// permissions: identity:write and ca:rotate.
	RoleAdmin Role = "admin"
)

// AllRoles is every role, in the order an operator would read them: least
// privilege first.
var AllRoles = []Role{RoleAuditor, RolePolicyAuthor, RoleGrantAdmin, RoleFleetAdmin, RoleAdmin}

// readOnly is what every role can do, because a role that can change a thing
// it cannot read is a role that changes things blind.
var readOnly = []Permission{
	PermPolicyRead, PermGrantRead, PermFleetRead,
	PermIdentityRead, PermCARead, PermAuditRead, PermDecisionRead,
}

// rolePermissions is the whole of RBAC's vocabulary. Read it as the product's
// documentation of what each role means.
var rolePermissions = map[Role][]Permission{
	RoleAuditor:      readOnly,
	RolePolicyAuthor: append(slices.Clone(readOnly), PermPolicyWrite, PermPolicyPublish),
	RoleGrantAdmin:   append(slices.Clone(readOnly), PermGrantWrite, PermGrantApprove),
	RoleFleetAdmin:   append(slices.Clone(readOnly), PermFleetWrite),
	RoleAdmin:        AllPermissions,
}

// ParseRole turns a stored code into a Role. An unknown code is refused rather
// than coerced: a binding naming a role this build does not have is a
// configuration error, and silently dropping it would grant less than an
// operator thinks while silently mapping it would grant more.
func ParseRole(code string) (Role, error) {
	r := Role(strings.TrimSpace(code))
	if _, ok := rolePermissions[r]; !ok {
		return "", fmt.Errorf("identity: %q is not a role this build defines", code)
	}
	return r, nil
}

// Permissions returns what a role may do, sorted and de-duplicated.
func (r Role) Permissions() []Permission {
	perms := slices.Clone(rolePermissions[r])
	slices.Sort(perms)
	return slices.Compact(perms)
}

// Can reports whether a role holds a permission.
func (r Role) Can(p Permission) bool { return slices.Contains(rolePermissions[r], p) }

// RoleSet is the set of roles a principal holds IN ONE TENANT.
//
// It is per tenant and only per tenant. There is no "global" role set and
// there is no way to express one, because M18's rule — a role granted in
// tenant A confers nothing in tenant B — is only true if there is nowhere to
// write a role that is not against a tenant.
type RoleSet []Role

// Can reports whether any role in the set holds the permission. This is THE
// permission check: the north-bound middleware calls it, the console reaches
// it through the API, and nothing else decides.
func (rs RoleSet) Can(p Permission) bool {
	for _, r := range rs {
		if r.Can(p) {
			return true
		}
	}
	return false
}

// Permissions returns the union of the set's permissions, sorted. The console
// renders it so a UI can hide what a caller cannot do — which is a courtesy,
// never the control: the control is [RoleSet.Can] on the server.
func (rs RoleSet) Permissions() []Permission {
	var out []Permission
	for _, r := range rs {
		out = append(out, rolePermissions[r]...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Strings renders the set as stable codes, sorted, for storage and for logs.
func (rs RoleSet) Strings() []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, string(r))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ParseRoleSet turns stored codes into a RoleSet, refusing any unknown one.
func ParseRoleSet(codes []string) (RoleSet, error) {
	out := make(RoleSet, 0, len(codes))
	for _, c := range codes {
		r, err := ParseRole(c)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// RoleResolver answers "which roles does this subject hold in this tenant".
//
// It is an interface so the north-bound middleware can be tested without a
// database, and so that the answer has one source: the bindings table, read
// with the groups the caller has ALREADY resolved. Resolving groups a second
// time here would answer differently for a federated login than for a local
// one, which is exactly the asymmetry M7 forbids.
type RoleResolver interface {
	RolesFor(ctx context.Context, tenant store.Tenant, subjectID string, groups []string) (RoleSet, error)
}

// StoreRoles resolves roles out of the bindings table.
type StoreRoles struct{ bindings store.RoleBindingRepository }

// NewStoreRoles builds a resolver over a store.
func NewStoreRoles(st *store.Store) *StoreRoles { return &StoreRoles{bindings: st.RoleBindings()} }

// RolesFor returns the roles held directly and through the given groups.
//
// An unknown role code in the table is dropped and is NOT an error: a binding
// written by a newer release must not lock an older one out of every route it
// could still serve. It is logged by the caller that cares; what it must never
// do is grant something.
func (s *StoreRoles) RolesFor(ctx context.Context, tenant store.Tenant, subjectID string, groups []string) (RoleSet, error) {
	codes, err := s.bindings.RolesFor(ctx, tenant, subjectID, groups)
	if err != nil {
		return nil, err
	}
	out := make(RoleSet, 0, len(codes))
	for _, c := range codes {
		if r, err := ParseRole(c); err == nil {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
