// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/provision"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The directory source (0042): every directory identity and group is keyed by
// (tenant, source, external id), the sources a tenant holds are readable for a
// connector's tie, and rows written before the source existed are keyed under
// `default`.

// What a tie is checked against: any link or group of the source, in the
// tenant asked about, and the tenant's sources named in order and bounded.
func TestTheSourceReadsNameWhatTheTenantHolds(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if got, err := st.DirectoryLinks().Sources(ctx, tenantA); err != nil || len(got) != 0 {
		t.Fatalf("Sources of an empty tenant = %v, %v", got, err)
	}
	if err := st.DirectoryLinks().Put(ctx, tenantA, directoryLink("u-1", "dir-a")); err != nil {
		t.Fatal(err)
	}
	// A source that holds only a group, even a tombstone, is a source the
	// tenant has.
	if err := st.DirectoryGroups().Put(ctx, tenantA, store.DirectoryGroup{
		Source: sourceB, ExternalID: "grp-1", Provider: "hoplock/scim", DeletedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tenant store.Tenant
		source string
		want   bool
	}{
		{tenantA, sourceA, true},
		{tenantA, sourceB, true},
		{tenantA, "okta", false},
		{tenantB, sourceA, false},
	} {
		if got, err := st.DirectoryLinks().HasSource(ctx, tc.tenant, tc.source); err != nil || got != tc.want {
			t.Errorf("HasSource(%s, %s) = %v, %v; want %v", tc.tenant, tc.source, got, err, tc.want)
		}
	}
	if got, err := st.DirectoryLinks().Sources(ctx, tenantA); err != nil || !slices.Equal(got, []string{sourceB, sourceA}) {
		t.Errorf("Sources = %v, %v; want both, in order", got, err)
	}
	if got, _ := st.DirectoryLinks().Sources(ctx, tenantB); len(got) != 0 {
		t.Errorf("tenant B reads tenant A's sources: %v", got)
	}

	for i := range store.MaxListedSources + 5 {
		l := directoryLink("u-1", fmt.Sprintf("dir-many-%02d", i))
		l.Source = fmt.Sprintf("s-%02d", i)
		if err := st.DirectoryLinks().Put(ctx, tenantB, l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.DirectoryLinks().Sources(ctx, tenantB)
	if err != nil || len(got) != store.MaxListedSources || got[0] != "s-00" || !slices.IsSorted(got) {
		t.Errorf("Sources of a tenant with %d = %v, %v; want the first %d in order",
			store.MaxListedSources+5, got, err, store.MaxListedSources)
	}
}

// A group's members are the links of its own source: another directory's
// person whose group ids name the same id is not one, and a group's name
// resolves within its own source. The read is served by the GIN index on
// group_ids.
func TestAGroupsMembersAreReadWithinItsSource(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	mine := directoryLink("u-1", "dir-a")
	yours := directoryLink("u-1", "dir-b")
	yours.Source = sourceB
	for _, l := range []store.DirectoryLink{mine, yours} {
		if err := st.DirectoryLinks().Put(ctx, tenantA, l); err != nil {
			t.Fatal(err)
		}
	}
	for source, want := range map[string]string{sourceA: "dir-a", sourceB: "dir-b"} {
		members, err := st.DirectoryLinks().Members(ctx, tenantA, source, "grp-1")
		if err != nil || len(members) != 1 || members[0].SubjectID != want || members[0].Source != source {
			t.Errorf("Members(%s, grp-1) = %+v, %v; want only %s", source, members, err, want)
		}
	}
	for source, name := range map[string]string{sourceA: "SRE Team", sourceB: "Contractors"} {
		if err := st.DirectoryGroups().Put(ctx, tenantA, store.DirectoryGroup{
			Source: source, ExternalID: "grp-1", Provider: "hoplock/scim", Name: name, UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for source, want := range map[string]string{sourceA: "SRE Team", sourceB: "Contractors"} {
		names, err := st.DirectoryGroups().Names(ctx, tenantA, source, []string{"grp-1"})
		if err != nil || len(names) != 1 || names["grp-1"] != want {
			t.Errorf("Names(%s, grp-1) = %v, %v; want %q", source, names, err, want)
		}
		if g, err := st.DirectoryGroups().Get(ctx, tenantA, source, "grp-1"); err != nil || g.Name != want {
			t.Errorf("Get(%s, grp-1) = %+v, %v", source, g, err)
		}
	}

	plan := planWithOnly(t, st, "directory_links", "directory_links_group_ids_idx", "", store.DirectoryMembersSQL())
	t.Logf("the members read, with the group ids index the only one left:\n%s", plan)
	if !strings.Contains(plan, "directory_links_group_ids_idx") || strings.Contains(plan, "Seq Scan") {
		t.Errorf("the members read is not served by the group ids index:\n%s", plan)
	}
}

// 0023 applies forward over the rows 0040 wrote: each is keyed under the
// source `default`, a tied connector resolves in `default`, a sync rebuilt to
// send `default` updates what it wrote and creates nothing, and the column
// keeps no default for a write that names no source.
func TestRowsFromBeforeTheSourceAreKeyedUnderDefault(t *testing.T) {
	t.Parallel()
	dsn := storetest.DSN(t)
	ctx := t.Context()
	st, err := store.Open(ctx, storetest.DSNForSchema(dsn, storetest.NewSchema(t, dsn)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)

	if err := store.MigrateThrough(ctx, st, 22); err != nil {
		t.Fatalf("migrate through 0022: %v", err)
	}
	// What 0040's sink and connector-put left behind: a provisioned link and
	// its subject, a deactivation kept ahead of its create, a group, a
	// tombstone, a tied connector and an untied one.
	updated := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if err := st.Subjects().Upsert(ctx, tenantA, store.Subject{
		ID: "dir-old", Source: "hoplock/scim", DisplayName: "Alice", Principals: []string{"alice"},
	}); err != nil {
		t.Fatalf("subject: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO directory_links (tenant, external_id, subject_id, provider, linked_at, provisioned,
			directory_updated_at, username, display_name)
		 VALUES ('tenant-a', 'u-1', 'dir-old', 'hoplock/scim', now(), true, '` + updated.Format(time.RFC3339Nano) + `', 'alice', 'Alice')`,
		`INSERT INTO directory_links (tenant, external_id, subject_id, provider, linked_at, deactivated_at, deactivation_reason)
		 VALUES ('tenant-a', 'u-2', 'dir-early', 'hoplock/scim', now(), now(), 'offboarded')`,
		`INSERT INTO directory_groups (tenant, external_id, provider, name, directory_updated_at)
		 VALUES ('tenant-a', 'grp-1', 'hoplock/scim', 'SRE Team', now())`,
		`INSERT INTO directory_groups (tenant, external_id, provider, name, deleted_at)
		 VALUES ('tenant-a', 'grp-2', 'hoplock/scim', 'DBAs', now())`,
		`INSERT INTO idp_connectors (tenant, connector, kind, config, directory_claim)
		 VALUES ('tenant-a', 'okta', 'oidc', '{}', 'oid')`,
		`INSERT INTO idp_connectors (tenant, connector, kind, config)
		 VALUES ('tenant-a', 'github', 'oidc', '{}')`,
	} {
		if _, err := st.Pool().Exec(ctx, stmt); err != nil {
			t.Fatalf("seed a 0021 row: %v\n%s", err, stmt)
		}
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	link, err := st.DirectoryLinks().Get(ctx, tenantA, "default", "u-1")
	if err != nil || link.Source != "default" || link.SubjectID != "dir-old" || !link.UpdatedAt.Equal(updated) {
		t.Errorf("the link reads %+v (%v), want it whole under default", link, err)
	}
	early, err := st.DirectoryLinks().Get(ctx, tenantA, "default", "u-2")
	if err != nil || !early.Deactivated() || early.Provisioned || early.DeactivationReason != "offboarded" {
		t.Errorf("the kept deactivation reads %+v (%v)", early, err)
	}
	if standing, err := st.Subjects().Standing(ctx, tenantA, "dir-early"); err != nil || !standing.Deactivated() ||
		standing.Directory.Source != "default" {
		t.Errorf("the decision path's read of the kept deactivation = %+v, %v", standing.Directory, err)
	}
	if g, err := st.DirectoryGroups().Get(ctx, tenantA, "default", "grp-1"); err != nil || g.Name != "SRE Team" || g.Deleted() {
		t.Errorf("the group reads %+v (%v)", g, err)
	}
	if g, err := st.DirectoryGroups().Get(ctx, tenantA, "default", "grp-2"); err != nil || !g.Deleted() {
		t.Errorf("the tombstone reads %+v (%v)", g, err)
	}

	// The tied connector resolves where its links now are: the lookup a
	// login through it makes is (tenant, directory_source, claim value).
	tied, err := st.Connectors().Get(ctx, tenantA, "okta")
	if err != nil || tied.DirectoryClaim != "oid" || tied.DirectorySource != "default" {
		t.Fatalf("the tied connector reads %+v (%v), want it tied to default", tied, err)
	}
	if l, err := st.DirectoryLinks().Get(ctx, tenantA, tied.DirectorySource, "u-1"); err != nil || l.SubjectID != "dir-old" {
		t.Errorf("a login through the tied connector finds %+v (%v)", l, err)
	}
	if untied, err := st.Connectors().Get(ctx, tenantA, "github"); err != nil || untied.DirectorySource != "" {
		t.Errorf("the untied connector reads %+v (%v), want it untied", untied, err)
	}

	// A connector has both or neither, and a write naming no source fails
	// rather than landing in `default`.
	for name, stmt := range map[string]string{
		"a claim with no source": `UPDATE idp_connectors SET directory_source = '' WHERE connector = 'okta'`,
		"a source with no claim": `UPDATE idp_connectors SET directory_source = 'okta-a' WHERE connector = 'github'`,
		"a link with no source": `INSERT INTO directory_links (tenant, external_id, subject_id, provider, linked_at)
			VALUES ('tenant-a', 'u-9', 'dir-nine', 'hoplock/scim', now())`,
	} {
		if _, err := st.Pool().Exec(ctx, stmt); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// A sync rebuilt against this release and sending `default` carries on:
	// its next write updates the person it provisioned and creates nothing.
	rec := &migrationRecorder{}
	sink, err := provision.New(provision.Options{
		Provider: "hoplock/scim", Store: st, Grants: refusingGrants{}, Revoker: quietRevoker{}, Recorder: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.UpsertSubject(ctx, ext.SyncedSubject{
		Tenant: ext.Tenant(tenantA), Source: "default", ExternalID: "u-1", Username: "alice.smith",
		Active: true, UpdatedAt: updated.Add(time.Minute),
	}); err != nil {
		t.Fatalf("an upsert under default: %v", err)
	}
	after, err := st.DirectoryLinks().Get(ctx, tenantA, "default", "u-1")
	if err != nil || after.SubjectID != "dir-old" || after.Username != "alice.smith" {
		t.Errorf("after the upsert the link reads %+v (%v), want dir-old renamed", after, err)
	}
	var links, subjects int
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM directory_links`).Scan(&links)
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM subjects`).Scan(&subjects)
	if links != 2 || subjects != 1 {
		t.Errorf("%d links and %d subjects after the upsert, want 2 and 1: it created something", links, subjects)
	}
	if !slices.Equal(rec.events, []string{audit.EventSyncSubjectUpdated}) {
		t.Errorf("the upsert recorded %v, want one update", rec.events)
	}
}

// migrationRecorder, refusingGrants and quietRevoker are the sink's
// collaborators reduced to what an upsert of an active subject touches.
type migrationRecorder struct{ events []string }

func (r *migrationRecorder) SyncEvent(_ context.Context, _ *store.Store, _ store.Tenant, act audit.SyncAct) error {
	r.events = append(r.events, act.Event)
	return nil
}

func (r *migrationRecorder) OperatorEvent(context.Context, *store.Store, store.Tenant, audit.OperatorAct) error {
	return nil
}

type refusingGrants struct{}

func (refusingGrants) Revoke(context.Context, store.Tenant, access.Actor, string, string) (access.Revocation, error) {
	return access.Revocation{}, fmt.Errorf("no grant is revoked by an upsert of an active subject")
}

func (refusingGrants) Cancel(context.Context, store.Tenant, access.Actor, string, string) (access.Created, error) {
	return access.Created{}, fmt.Errorf("no request is withdrawn by an upsert of an active subject")
}

type quietRevoker struct{}

func (quietRevoker) Kill(context.Context, store.Tenant, revoke.Audience, revoke.Kill) (revoke.Receipt, error) {
	return revoke.Receipt{}, nil
}

func (quietRevoker) Invalidate(context.Context, store.Tenant, revoke.Audience, revoke.Invalidation) (revoke.Receipt, error) {
	return revoke.Receipt{}, nil
}
