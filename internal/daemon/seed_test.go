// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// A seeded API token is an issued one (0021): it is stored as the digest the
// north-bound middleware looks it up by — never its secret — and it holds the
// roles the seed names, in the seeded tenant, and nothing else anywhere.
func TestASeededAPITokenIsAnIssuedOneHoldingOnlyItsRoles(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	const tenant = store.Tenant("acme")
	doc := &seedDocument{APITokens: []seedAPIToken{{
		ID: "tok-ci", DisplayName: "CI", Secret: "seeded-secret-value", Roles: []string{"auditor"},
	}}}
	if err := doc.apply(ctx, st, tenant, io.Discard); err != nil {
		t.Fatal(err)
	}

	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	p, ok, err := federation.Authenticate(ctx, "ht.acme.seeded-secret-value")
	if err != nil || !ok {
		t.Fatalf("the seeded token does not authenticate as an issued one would: ok=%v err=%v", ok, err)
	}
	if p.ID != "tok-ci" || !slices.Equal(p.RolesIn(tenant), identity.RoleSet{identity.RoleAuditor}) {
		t.Errorf("principal %s holds %v in %s, want tok-ci holding auditor alone", p.ID, p.RolesIn(tenant), tenant)
	}
	if p.Can(tenant, identity.PermAuditRetain) || p.MayActIn("globex") {
		t.Error("the seeded token reaches beyond the roles and tenant it names")
	}

	rows, err := st.Pool().Query(ctx, `SELECT token_digest FROM north_principals WHERE tenant = $1 AND principal_id = $2`, tenant, "tok-ci")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(digest, "seeded-secret-value") || digest != (identity.Credential{Secret: "seeded-secret-value"}).Digest() {
			t.Errorf("stored %q, want the credential's digest and never its secret", digest)
		}
	}

	for _, bad := range []seedAPIToken{
		{ID: "tok-none", Secret: "s"},
		{ID: "tok-unknown", Secret: "s", Roles: []string{"superuser"}},
	} {
		if err := (&seedDocument{APITokens: []seedAPIToken{bad}}).apply(ctx, st, tenant, io.Discard); err == nil {
			t.Errorf("a token naming roles %v was seeded", bad.Roles)
		}
	}
}
