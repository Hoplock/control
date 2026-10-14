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
	// PermGrantRequest is asking the registered approval workflow for a
	// grant for YOURSELF (M10, 0016). It reaches only the self-scoped routes,
	// whose holder is the caller and never a name in the request, so it can
	// neither grant anything nor touch anybody else's request: a workflow
	// decides, and with none registered the request is refused.
	PermGrantRequest Permission = "grant:request"

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

	// PermTargetWrite is creating, editing, relabelling and removing
	// targets. A label is a policy input, so editing one changes decisions:
	// it is held by the roles that own the estate (fleet-admin) and its
	// policy (policy-author), and a relabel is diffable and simulable before
	// it is applied (0019). Reading targets is fleet:read.
	PermTargetWrite Permission = "target:write"

	// PermAuditRead is reading the audit store (M8).
	PermAuditRead Permission = "audit:read"
	// PermAuditRetain is running a retention pass: deleting a stream's oldest
	// records, or the bytes of old session captures (0019, PLAN §7). It is
	// the admin's alone — the one permission that removes evidence — and the
	// pass is itself an audit record.
	PermAuditRetain Permission = "audit:retain"
	// PermSessionRevoke is publishing an operator action on the revocation
	// stream (0009): ending sessions, invalidating cached decisions, a
	// resync, withdrawing a host-key decision. The grant desk holds it —
	// it already ends sessions by revoking grants — and the admin.
	PermSessionRevoke Permission = "session:revoke"
	// PermDecisionRead is resolving a decision or session id into the whole
	// story (M4).
	PermDecisionRead Permission = "decision:read"
	// PermAccessContextPush is asserting a window to an external-context
	// integration's push receiver (M16, 0013). It is the one thing the
	// integration role can do, and what it can reach is decided again per
	// push: the integration's scope binding must name this very credential.
	PermAccessContextPush Permission = "access-context:push"
	// PermAccessContextRead is reading the external access-context scope
	// bindings and the providers they may name (M16, 0019). A binding says
	// who may open what, so every role that reads the tenant reads it.
	PermAccessContextRead Permission = "access-context:read"
	// PermAccessContextWrite is writing a binding. A binding is a standing
	// pre-approval for an external system to open access, which is larger
	// than grant:write, so it is the admin's alone (PLAN §2).
	PermAccessContextWrite Permission = "access-context:write"
	// PermExtensionRead is reading which extensions this deployment runs
	// (M15). It is read-only because there is nothing to change: the
	// registry is sealed before the server starts.
	PermExtensionRead Permission = "extension:read"

	// PermReportWrite is changing what reports are produced: their
	// schedules, their recipients and the campaigns they belong to. Control
	// serves no route that needs it; a host's reporting routes name it (M15),
	// because a host names a permission and never declares one. Reading a
	// report is PermAuditRead: a report is the audit store, summarised.
	PermReportWrite Permission = "report:write"
	// PermLicenseRead is reading a deployment's licence and entitlement
	// state. Control has no licence (M14) and serves no route that needs
	// it; it exists so that a host's licence route names a permission this
	// file defines rather than one it invents.
	PermLicenseRead Permission = "license:read"
)

// AllPermissions is every permission, in a stable order. A test asserts each
// one is held by at least one role: a permission no role grants is a route
// nobody can reach, which is a configuration bug that looks like a bug report.
var AllPermissions = []Permission{
	PermPolicyRead, PermPolicyWrite, PermPolicyPublish,
	PermGrantRead, PermGrantWrite, PermGrantApprove, PermGrantRequest,
	PermFleetRead, PermFleetWrite, PermTargetWrite,
	PermIdentityRead, PermIdentityWrite,
	PermCARead, PermCARotate,
	PermAuditRead, PermAuditRetain, PermDecisionRead, PermSessionRevoke,
	PermAccessContextPush, PermAccessContextRead, PermAccessContextWrite,
	PermExtensionRead,
	PermReportWrite, PermLicenseRead,
}

// Role is a named permission set. Closed set (M13).
type Role string

const (
	// RoleIntegration is an external system's credential (M16): a scanner's
	// or a ticketing system's token. It may push a window assertion and do
	// nothing else — it reads NOTHING, which makes it the one role without
	// the read set below. That is deliberate: the credential sits in
	// somebody else's software, and every grant, decision and audit record
	// in the tenant is not that software's to read. What it may assert is
	// bounded per push by the integration's scope binding, which must name
	// this credential by its principal id.
	RoleIntegration Role = "integration"
	// RoleRequester is a person who may ask for access for themselves and
	// do nothing else (M10, 0016): raise a request the registered approval
	// workflow decides, read their own requests, and withdraw them. It holds
	// none of the read set below, and its comment says why.
	RoleRequester Role = "requester"
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
	// permissions — identity:write and ca:rotate — and report:write, which
	// no narrower role's meaning covers: the auditor changes nothing, and
	// what a compliance report covers is not a policy, a grant or a proxy.
	RoleAdmin Role = "admin"
)

// AllRoles is every role, in the order an operator would read them: least
// privilege first.
var AllRoles = []Role{RoleIntegration, RoleRequester, RoleAuditor, RolePolicyAuthor, RoleGrantAdmin, RoleFleetAdmin, RoleAdmin}

// readOnly is what every role that administers or audits a tenant can do,
// because a role that can change a thing it cannot read is a role that changes
// things blind. Two roles hold none of it, for different reasons:
//
//   - integration is somebody else's software, and it reads nothing on
//     purpose: every grant, decision and audit record in the tenant is not
//     that software's to read.
//   - requester changes only its own requests, and it reads exactly those —
//     through the self-scoped routes and nowhere else — so it never changes
//     anything blind. Reading the tenant's audit, decisions and identities is
//     not needed to ask for access, and a role every developer holds must not
//     carry it.
//
// license:read is here because it is not data about anyone: which features a
// deployment is entitled to is something every administrator and auditor may
// know.
var readOnly = []Permission{
	PermPolicyRead, PermGrantRead, PermFleetRead,
	PermIdentityRead, PermCARead, PermAuditRead, PermDecisionRead,
	PermAccessContextRead, PermExtensionRead,
	PermLicenseRead,
}

// rolePermissions is the whole of RBAC's vocabulary. Read it as the product's
// documentation of what each role means.
var rolePermissions = map[Role][]Permission{
	RoleIntegration:  {PermAccessContextPush},
	RoleRequester:    {PermGrantRequest},
	RoleAuditor:      readOnly,
	RolePolicyAuthor: append(slices.Clone(readOnly), PermPolicyWrite, PermPolicyPublish, PermTargetWrite),
	RoleGrantAdmin:   append(slices.Clone(readOnly), PermGrantWrite, PermGrantApprove, PermSessionRevoke),
	RoleFleetAdmin:   append(slices.Clone(readOnly), PermFleetWrite, PermTargetWrite),
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
