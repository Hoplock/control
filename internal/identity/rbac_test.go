// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"slices"
	"testing"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

func TestEveryPermissionIsHeldBySomeRole(t *testing.T) {
	// A permission no role grants is a route nobody can reach, which ships as
	// a bug report rather than as a configuration mistake.
	for _, perm := range identity.AllPermissions {
		held := false
		for _, role := range identity.AllRoles {
			if role.Can(perm) {
				held = true
				break
			}
		}
		if !held {
			t.Errorf("no role grants %s, so no caller can reach a route that needs it", perm)
		}
	}
}

func TestEveryRoleCanReadWhatItCanChange(t *testing.T) {
	// A role that can change a thing it cannot read is a role that changes
	// things blind.
	//
	// A slice rather than a map keyed by Permission: `exhaustive` checks a
	// lookup table keyed by an enum for every member, and this table is
	// deliberately partial — only the write permissions have a read to pair
	// with (PLAN M13 says why that check is in the set at all).
	pairs := []struct{ write, read identity.Permission }{
		{identity.PermPolicyWrite, identity.PermPolicyRead},
		{identity.PermPolicyPublish, identity.PermPolicyRead},
		{identity.PermGrantWrite, identity.PermGrantRead},
		{identity.PermGrantApprove, identity.PermGrantRead},
		{identity.PermFleetWrite, identity.PermFleetRead},
		{identity.PermIdentityWrite, identity.PermIdentityRead},
		{identity.PermCARotate, identity.PermCARead},
	}
	for _, role := range identity.AllRoles {
		for _, pair := range pairs {
			if role.Can(pair.write) && !role.Can(pair.read) {
				t.Errorf("%s holds %s without %s", role, pair.write, pair.read)
			}
		}
	}
}

func TestTheAuditorChangesNothing(t *testing.T) {
	writes := []identity.Permission{
		identity.PermPolicyWrite, identity.PermPolicyPublish,
		identity.PermGrantWrite, identity.PermGrantApprove,
		identity.PermFleetWrite, identity.PermIdentityWrite, identity.PermCARotate,
	}
	for _, perm := range writes {
		if identity.RoleAuditor.Can(perm) {
			t.Errorf("the auditor holds %s; it is a read-only role", perm)
		}
	}
	if !identity.RoleAuditor.Can(identity.PermAuditRead) {
		t.Error("an auditor who has to ask an engineer for a record is not an auditor")
	}
}

func TestOnlyTheAdminHoldsThePermissionsThatGrantPermissions(t *testing.T) {
	// identity:write writes the claim mapping, which decides which IdP claims
	// become policy attributes: whoever holds it can grant themselves a group.
	// ca:rotate can invalidate every outstanding certificate in the tenant.
	for _, perm := range []identity.Permission{identity.PermIdentityWrite, identity.PermCARotate} {
		for _, role := range identity.AllRoles {
			if role == identity.RoleAdmin {
				continue
			}
			if role.Can(perm) {
				t.Errorf("%s holds %s, which is an administrator's", role, perm)
			}
		}
		if !identity.RoleAdmin.Can(perm) {
			t.Errorf("the admin does not hold %s", perm)
		}
	}
}

func TestAnUnknownRoleCodeIsRefusedRatherThanCoerced(t *testing.T) {
	// Silently dropping it would grant less than an operator thinks; silently
	// mapping it would grant more.
	if _, err := identity.ParseRole("superuser"); err == nil {
		t.Fatal("an unknown role code parsed")
	}
	if _, err := identity.ParseRoleSet([]string{"auditor", "superuser"}); err == nil {
		t.Fatal("a role set containing an unknown code parsed")
	}
	if got, err := identity.ParseRole("  admin "); err != nil || got != identity.RoleAdmin {
		t.Fatalf("a padded code did not parse: %v %v", got, err)
	}
}

func TestARoleGrantedInOneTenantConfersNothingInAnother(t *testing.T) {
	// Including to an administrator, which is the case M18 names.
	p := identity.NewPrincipal("tok-1", store.PrincipalToken, "tenant-a",
		map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAdmin}})

	if !p.MayActIn("tenant-a") {
		t.Fatal("the principal cannot act in its own tenant")
	}
	if p.MayActIn("tenant-b") {
		t.Fatal("the principal may act in a tenant it was never granted")
	}
	if !p.Can("tenant-a", identity.PermPolicyPublish) {
		t.Error("an admin cannot publish in its own tenant")
	}
	for _, perm := range identity.AllPermissions {
		if p.Can("tenant-b", perm) {
			t.Errorf("an admin in tenant-a holds %s in tenant-b", perm)
		}
	}
	if len(p.RolesIn("tenant-b")) != 0 {
		t.Error("the principal holds roles in a tenant it was not granted")
	}
}

func TestARestrictedRoleIsAlsoConfinedToItsTenant(t *testing.T) {
	p := identity.NewPrincipal("tok-2", store.PrincipalToken, "tenant-a",
		map[store.Tenant]identity.RoleSet{"tenant-a": {identity.RoleAuditor}})
	if p.Can("tenant-b", identity.PermAuditRead) {
		t.Fatal("an auditor in tenant-a can read tenant-b's audit store")
	}
}

func TestSoleTenantIsOnlyAnAnswerWhenThereIsExactlyOne(t *testing.T) {
	// It is what keeps single-tenant deployments untouched, and it must not
	// silently pick one for a principal scoped to several.
	one := identity.NewPrincipal("tok-3", store.PrincipalToken, "a",
		map[store.Tenant]identity.RoleSet{"a": {identity.RoleAuditor}})
	if got, ok := one.SoleTenant(); !ok || got != "a" {
		t.Fatalf("one tenant: got %q %v", got, ok)
	}

	two := identity.NewPrincipal("tok-4", store.PrincipalToken, "a",
		map[store.Tenant]identity.RoleSet{
			"a": {identity.RoleAuditor},
			"b": {identity.RoleAuditor},
		})
	if _, ok := two.SoleTenant(); ok {
		t.Fatal("a principal scoped to two tenants was given a default")
	}
	if got := two.ScopedTenants(); !slices.Equal(got, []store.Tenant{"a", "b"}) {
		t.Fatalf("scoped tenants: %v", got)
	}
}

func TestARoleSetUnionsItsPermissions(t *testing.T) {
	set := identity.RoleSet{identity.RolePolicyAuthor, identity.RoleFleetAdmin}
	if !set.Can(identity.PermPolicyPublish) || !set.Can(identity.PermFleetWrite) {
		t.Fatal("a role set does not union its permissions")
	}
	if set.Can(identity.PermIdentityWrite) {
		t.Fatal("a role set granted a permission neither role holds")
	}
	perms := set.Permissions()
	if !slices.IsSorted(perms) {
		t.Fatal("permissions are not sorted, so a console would render them in a different order each time")
	}
	if slices.Compact(slices.Clone(perms)) == nil || len(slices.Compact(slices.Clone(perms))) != len(perms) {
		t.Fatal("permissions are not de-duplicated")
	}
}

func TestANilPrincipalMayActNowhere(t *testing.T) {
	// The zero value has to be safe: a middleware bug that lost the principal
	// must refuse rather than grant.
	var p *identity.Principal
	if p.MayActIn("a") || p.Can("a", identity.PermAuditRead) {
		t.Fatal("a nil principal was granted something")
	}
	if _, ok := p.SoleTenant(); ok {
		t.Fatal("a nil principal resolved a tenant")
	}
}
