// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The instance id is generated once and read back by everyone after, however
// many nodes ask at once (M19: one identity per logical deployment).
func TestDeploymentIdentityIsOneAndStable(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ids = map[string]bool{}
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := st.Deployment().Identity(ctx)
			if err != nil {
				t.Errorf("Identity: %v", err)
				return
			}
			mu.Lock()
			ids[d.InstanceID] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(ids) != 1 {
		t.Fatalf("concurrent first reads produced %d instance ids, want 1: %v", len(ids), ids)
	}
	again, err := st.Deployment().Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ids[again.InstanceID] {
		t.Fatalf("a later read returned %q, not the id first generated", again.InstanceID)
	}
}

func TestDeploymentSupervisionRevocation(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	at := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

	d, err := st.Deployment().SetSupervisionRevoked(ctx, true, "ops", at)
	if err != nil {
		t.Fatal(err)
	}
	if !d.SupervisionRevokedAt.Equal(at) || d.SupervisionRevokedBy != "ops" {
		t.Fatalf("revoked = %v by %q", d.SupervisionRevokedAt, d.SupervisionRevokedBy)
	}
	// A second revocation keeps the first's time and author.
	d, err = st.Deployment().SetSupervisionRevoked(ctx, true, "someone-else", at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !d.SupervisionRevokedAt.Equal(at) || d.SupervisionRevokedBy != "ops" {
		t.Fatalf("a repeated revocation moved it: %v by %q", d.SupervisionRevokedAt, d.SupervisionRevokedBy)
	}
	d, err = st.Deployment().SetSupervisionRevoked(ctx, false, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !d.SupervisionRevokedAt.IsZero() {
		t.Fatalf("restore left it revoked at %v", d.SupervisionRevokedAt)
	}
}

func TestDeploymentNodesAndTenants(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	dep := st.Deployment()

	for i, id := range []string{"n1", "n2", "old"} {
		at := now
		if id == "old" {
			at = now.Add(-48 * time.Hour)
		}
		if err := dep.ReportNode(ctx, store.DeploymentNode{
			NodeID: id, Version: "v1", StartedAt: at.Add(-time.Minute), ReportedAt: at,
			Report: []byte(`{"i":` + string(rune('0'+i)) + `}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := dep.LeaveNode(ctx, "n2", now); err != nil {
		t.Fatal(err)
	}
	nodes, err := dep.Nodes(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].NodeID != "n1" || nodes[1].NodeID != "n2" {
		t.Fatalf("nodes = %+v, want n1 and n2 (old forgotten)", nodes)
	}
	if !nodes[1].LeftAt.Equal(now) || !nodes[0].LeftAt.IsZero() {
		t.Fatalf("left_at: n1 %v, n2 %v", nodes[0].LeftAt, nodes[1].LeftAt)
	}
	// Reporting again clears a leave: the node is back.
	if err := dep.ReportNode(ctx, store.DeploymentNode{NodeID: "n2", Version: "v2", StartedAt: now, ReportedAt: now}); err != nil {
		t.Fatal(err)
	}
	nodes, _ = dep.Nodes(ctx, time.Time{})
	if !nodes[1].LeftAt.IsZero() || nodes[1].Version != "v2" {
		t.Fatalf("n2 after rejoin = %+v", nodes[1])
	}

	for _, tn := range []store.Tenant{"b", "a", "b"} {
		if err := dep.TouchTenant(ctx, tn, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := dep.TouchTenant(ctx, "a", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	tenants, err := dep.Tenants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 2 || tenants[0].Tenant != "a" || !tenants[0].LastActive.Equal(now) {
		t.Fatalf("tenants = %+v (an older touch must not move last_active back)", tenants)
	}
}

// The feed keeps 0027's shape: positions in order, the newest N kept, and a
// floor naming what was pruned.
func TestSupervisionLogKeepsTheNewestAndRaisesTheFloor(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	dep := st.Deployment()
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

	var last store.SupervisionStream
	for i := range 5 {
		e, s, err := dep.AppendSupervision(ctx, store.SupervisionEntry{
			Kind: "node_joined", Body: []byte(`{}`), LoggedAt: now, Origin: "n1",
		}, 3)
		if err != nil {
			t.Fatal(err)
		}
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d took position %d", i, e.Seq)
		}
		last = s
	}
	if last.Head != 5 || last.Floor != 2 || last.Epoch == "" {
		t.Fatalf("stream = %+v, want head 5, floor 2", last)
	}
	got, err := dep.SupervisionAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Seq != 3 {
		t.Fatalf("after 0 = %d events from %d", len(got), got[0].Seq)
	}
	read, err := dep.SupervisionStream(ctx)
	if err != nil || !reflect.DeepEqual(read, last) {
		t.Fatalf("SupervisionStream = %+v, %v; want %+v", read, err, last)
	}
}

// The deployment repository is the one whose methods name no tenant, and it is
// held to that: none of its methods may take a store.Tenant except the one that
// records a tenant's NAME into the tenant set.
func TestDeploymentRepositoryReadsNoTenantsRows(t *testing.T) {
	t.Parallel()
	iface := reflect.TypeOf((*store.DeploymentRepository)(nil)).Elem()
	tenantType := reflect.TypeOf(store.Tenant(""))
	for i := range iface.NumMethod() {
		m := iface.Method(i)
		for j := range m.Type.NumIn() {
			if m.Type.In(j) == tenantType && m.Name != "TouchTenant" {
				t.Errorf("DeploymentRepository.%s takes a tenant: a tenant's rows are read through a repository that names it (M18)", m.Name)
			}
		}
	}
}
