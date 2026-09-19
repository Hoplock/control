// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"slices"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

// tenantA is testTenant under the name the two-tenant tests read better with.
const (
	tenantA = testTenant
	tenantB = store.Tenant("tenant-b")
)

// THE WORST THING THIS GRAPH CAN DO (M18). A cross-tenant hop is not an
// information leak to filter out of a response — it is one customer's session
// traversing another customer's infrastructure.
//
// The graph below is built so that the ONLY route from tenant A's entry proxy to
// a reachable zone passes through tenant B's proxy. The answer must be the
// explicit no-path outcome — an outage — and never a path, and never a deny.
func TestNoPathCrossesATenant(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	// Tenant A: an entry proxy that declares it can reach `enclave`, and
	// nothing of its own in that zone.
	enrolled(t, r, tenantA, "p-edge", "edge", []fleet.Edge{
		{ToZone: "enclave", Connection: contract.HopConnectionDial, Address: "enclave-gw:22", Cost: 1},
	}, fleet.Capabilities{})

	// Tenant B: the only proxy anywhere that actually serves `enclave`. Same
	// zone name, same shape, different customer.
	enrolled(t, r, tenantB, "p-enclave", "enclave", nil, fleet.Capabilities{})

	_, err := r.Path(ctx, tenantA, fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{
		Zone: "enclave", Hostname: "db-1.enclave",
	})
	np, ok := fleet.NoPath(err)
	if !ok {
		t.Fatalf("err = %v, want the explicit no-path outcome", err)
	}
	if np.Reason != fleet.NoPathNoLiveRoute {
		t.Errorf("reason = %q, want %q", np.Reason, fleet.NoPathNoLiveRoute)
	}

	// And tenant A's graph cannot even see the other tenant's proxy: the edge
	// does not exist rather than being pruned late.
	g, err := r.Graph(ctx, tenantA)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if _, found := g.Node("p-enclave"); found {
		t.Error("tenant A's graph contains tenant B's proxy")
	}
	if got := g.LiveProxiesInZone("enclave"); len(got) != 0 {
		t.Errorf("tenant A sees %v in zone enclave, want none", got)
	}
}

// Two tenants using identical zone names, proxy ids and target labels resolve
// independently, and neither can observe the other's proxies in the fleet view.
//
// Zone names are deliberately NOT globally unique: making them so to simplify an
// internal lookup would be a customer-visible constraint invented for our
// convenience.
func TestTwoTenantsWithIdenticalNamesResolveIndependently(t *testing.T) {
	t.Parallel()
	st, r, _ := newRegistry(t)
	ctx := t.Context()

	// Both tenants run a `p-edge` in `edge` and a `p-inner` in `prod`, with the
	// same edge, and a target called `db-1.prod` labelled env=prod.
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		enrolled(t, r, tenant, "p-edge", "edge", []fleet.Edge{
			{ToZone: "prod", Connection: contract.HopConnectionDial, Address: "prod-gw:22", Cost: 1},
		}, fleet.Capabilities{})
		enrolled(t, r, tenant, "p-inner", "prod", nil, fleet.Capabilities{})
		if err := st.Targets().Upsert(ctx, tenant, store.Target{
			ID: "db-1", Hostname: "db-1.prod", Zone: "prod",
			Labels: map[string]string{"env": "prod"},
		}); err != nil {
			t.Fatalf("seed target for %s: %v", tenant, err)
		}
	}

	// Each resolves its own route, and each sees exactly two proxies.
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		hops, err := r.Path(ctx, tenant, fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "prod"})
		if err != nil {
			t.Fatalf("%s: Path: %v", tenant, err)
		}
		if got, want := hopIDs(hops), []string{"p-inner"}; !slices.Equal(got, want) {
			t.Errorf("%s: path = %v, want %v", tenant, got, want)
		}

		health, err := r.Health(ctx, tenant)
		if err != nil {
			t.Fatalf("%s: Health: %v", tenant, err)
		}
		if len(health) != 2 {
			t.Errorf("%s: fleet view has %d row(s), want 2 — it must not see the other tenant's proxies",
				tenant, len(health))
		}
	}

	// Taking one tenant's inner proxy out of routing does not touch the other's.
	p, err := st.Proxies().Get(ctx, tenantB, "p-inner")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.State = store.EnrollmentRevoked
	if err := st.Proxies().Upsert(ctx, tenantB, p); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if _, err := r.Path(ctx, tenantA, fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "prod"}); err != nil {
		t.Errorf("tenant A's route broke when tenant B revoked a proxy: %v", err)
	}
	if _, err := r.Path(ctx, tenantB, fleet.EntryPoint{ProxyID: "p-edge"}, fleet.Destination{Zone: "prod"}); !fleet.IsNoPath(err) {
		t.Errorf("tenant B's route survived revoking its own proxy: %v", err)
	}
}

// An enrollment token resolves the tenant, and it resolves exactly one.
//
// The tenant in the credential is a SELECTOR within what the credential is good
// for, never a widening of it: a token minted for one tenant cannot enroll into
// another, because the secret is verified against the grant stored under the
// tenant the token names and no such grant exists there.
func TestAnEnrollmentTokenCannotReachAnotherTenant(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	token, err := r.IssueGrant(ctx, tenantA, fleet.EnrollmentGrant{
		ProxyID: "p-edge", GrantedZones: []fleet.Zone{"edge"},
	}, time.Time{})
	if err != nil {
		t.Fatalf("IssueGrant: %v", err)
	}

	// Tenant B issues its own grant for the same proxy id, in the same zone.
	if _, err := r.IssueGrant(ctx, tenantB, fleet.EnrollmentGrant{
		ProxyID: "p-edge", GrantedZones: []fleet.Zone{"edge"},
	}, time.Time{}); err != nil {
		t.Fatalf("IssueGrant for tenant B: %v", err)
	}

	// Tenant A's secret, relabelled as tenant B's. The prefix is not authority:
	// the secret has to match the grant stored under the tenant it names.
	forged := fleet.EnrollmentToken{Tenant: tenantB, Secret: token.Secret}
	if _, err := r.Enroll(ctx, fleet.EnrollmentRequest{
		Token: forged.String(), ProxyID: "p-edge", Zone: "edge", PublicKey: []byte("k"),
	}); !errorsIs(err, fleet.ErrEnrollmentRejected) {
		t.Fatalf("a relabelled token enrolled into another tenant: %v", err)
	}

	// The real token still works, in its own tenant.
	got, err := r.Enroll(ctx, fleet.EnrollmentRequest{
		Token: token.String(), ProxyID: "p-edge", Zone: "edge", PublicKey: []byte("k"),
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if got.Tenant != tenantA {
		t.Errorf("resolved tenant = %q, want %q", got.Tenant, tenantA)
	}
}

// A tenant name that could make a token ambiguous is refused at issuance: an
// ambiguous credential is one that can be made to resolve to the wrong tenant.
func TestATenantNameCannotMakeATokenAmbiguous(t *testing.T) {
	t.Parallel()
	if _, err := fleet.MintEnrollmentToken("tenant.with.dots"); err == nil {
		t.Fatal("a tenant containing the separator was accepted")
	}
	if _, err := fleet.MintEnrollmentToken(""); err == nil {
		t.Fatal("an empty tenant was accepted")
	}
}

// A capability record belongs to one tenant, so two tenants observing the same
// hostname differently do not overwrite each other.
func TestTargetCapabilitiesAreScopedToATenant(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t)
	ctx := t.Context()

	key := fleet.TargetCapabilityKey{Hostname: "db-1.prod", Platform: "linux"}
	if _, err := r.ReportTargetCapabilities(ctx, tenantA, fleet.TargetCapabilities{
		Key:        key,
		Execution:  []contract.ExecutionRung{contract.ExecutionAccountConfined},
		ObservedAt: c.now(),
		ReportedBy: "p-a",
	}); err != nil {
		t.Fatalf("report for tenant A: %v", err)
	}

	a, err := r.TargetRungs(ctx, tenantA, key)
	if err != nil {
		t.Fatalf("TargetRungs A: %v", err)
	}
	if !a.AllowsExecution(contract.ExecutionAccountConfined) {
		t.Error("tenant A's own observation did not apply")
	}

	b, err := r.TargetRungs(ctx, tenantB, key)
	if err != nil {
		t.Fatalf("TargetRungs B: %v", err)
	}
	if b.Observed {
		t.Error("tenant B saw tenant A's observation")
	}
	if b.AllowsExecution(contract.ExecutionAccountConfined) {
		t.Error("tenant B inherited an applied rung from tenant A's record")
	}
}
