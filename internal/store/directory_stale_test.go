// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The claim mapping recalculation's reads (0043): a tenant's directory
// subjects whose row names another mapping version, a batch at a time in
// subject order, and their count.

// staleFixture writes a provisioned link and its subject row naming a mapping
// version.
func staleFixture(t *testing.T, st *store.Store, tenant store.Tenant, externalID, subjectID string, version int) {
	t.Helper()
	l := directoryLink(externalID, subjectID)
	l.Username = subjectID
	if err := st.DirectoryLinks().Put(t.Context(), tenant, l); err != nil {
		t.Fatal(err)
	}
	if err := st.Subjects().Upsert(t.Context(), tenant, store.Subject{
		ID: subjectID, Source: "hoplock/scim", DisplayName: subjectID, MappingVersion: version,
	}); err != nil {
		t.Fatal(err)
	}
}

func subjectIDs(links []store.DirectoryLink) []string {
	out := []string{}
	for _, l := range links {
		out = append(out, l.SubjectID)
	}
	return out
}

func TestTheStaleReadsFindSubjectsOnAnotherMappingVersion(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	for i, version := range []int{2, 1, 2, 0, 1} {
		staleFixture(t, st, tenantA, fmt.Sprintf("u-%d", i), fmt.Sprintf("dir-%d", i), version)
	}
	// Another tenant's stale subject is never read.
	staleFixture(t, st, tenantB, "u-x", "dir-x", 1)
	// A deactivation kept for an id never provisioned has no subject.
	kept := directoryLink("u-kept", "dir-kept")
	kept.Provisioned = false
	if err := st.DirectoryLinks().Put(ctx, tenantA, kept); err != nil {
		t.Fatal(err)
	}
	// A subject that is current and owes an invalidation is read; one that
	// owes a deactivation's is the sink's.
	staleFixture(t, st, tenantA, "u-owed", "dir-owed", 2)
	owed := directoryLink("u-owed", "dir-owed")
	owed.Username, owed.InvalidateOwed = "dir-owed", true
	if err := st.DirectoryLinks().Put(ctx, tenantA, owed); err != nil {
		t.Fatal(err)
	}
	staleFixture(t, st, tenantA, "u-deact", "dir-deact", 2)
	deact := directoryLink("u-deact", "dir-deact")
	deact.Username, deact.InvalidateOwed, deact.DeactivationOwed = "dir-deact", true, true
	if err := st.DirectoryLinks().Put(ctx, tenantA, deact); err != nil {
		t.Fatal(err)
	}

	all, err := st.DirectoryLinks().Stale(ctx, tenantA, 2, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := subjectIDs(all), []string{"dir-1", "dir-3", "dir-4", "dir-owed"}; !slices.Equal(got, want) {
		t.Errorf("stale against 2: %v, want %v", got, want)
	}
	first, err := st.DirectoryLinks().Stale(ctx, tenantA, 2, "", 2)
	if err != nil || !slices.Equal(subjectIDs(first), []string{"dir-1", "dir-3"}) {
		t.Errorf("the first batch of 2: %v, %v", subjectIDs(first), err)
	}
	rest, err := st.DirectoryLinks().Stale(ctx, tenantA, 2, "dir-3", 2)
	if err != nil || !slices.Equal(subjectIDs(rest), []string{"dir-4", "dir-owed"}) {
		t.Errorf("the batch after dir-3: %v, %v", subjectIDs(rest), err)
	}
	if _, err := st.DirectoryLinks().Stale(ctx, tenantA, 2, "", 0); err == nil {
		t.Error("a batch of 0 was accepted")
	}

	for version, want := range map[int]int64{2: 3, 1: 5, 0: 6, 9: 7} {
		n, err := st.DirectoryLinks().StaleCount(ctx, tenantA, version)
		if err != nil || n != want {
			t.Errorf("StaleCount(%d) = %d, %v; want %d", version, n, err, want)
		}
	}
	if n, err := st.DirectoryLinks().StaleCount(ctx, tenantB, 1); err != nil || n != 0 {
		t.Errorf("tenant B against its own version: %d, %v", n, err)
	}
}

// Each read is served by an index: the range on (tenant, subject_id) by
// directory_links_subject_key, and each row's subject by the subjects primary
// key. No migration is needed for them.
func TestTheStaleReadsAreServedByIndexes(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	// Ten tenants of 2 000 directory subjects each, analysed, so the plan is
	// the one a populated deployment gets rather than an empty table's.
	const seed = `
		INSERT INTO subjects (tenant, subject_id, source, display_name, mapping_version)
		SELECT 't' || (i % 10), 'dir-' || i, 'hoplock/scim', 'p' || i, i % 3
		FROM generate_series(1, 20000) AS i;
		INSERT INTO directory_links (tenant, source, external_id, subject_id, provider, linked_at, provisioned)
		SELECT 't' || (i % 10), 'okta', 'u-' || i, 'dir-' || i, 'hoplock/scim', now(), true
		FROM generate_series(1, 20000) AS i;
		ANALYZE subjects;
		ANALYZE directory_links`
	for name, sql := range map[string]string{
		"batch": store.DirectoryStaleSQL(),
		"count": store.DirectoryStaleCountSQL(),
	} {
		plan := planWithOnly(t, st, "directory_links", "directory_links_subject_key", seed, sql)
		t.Logf("the %s read, over 20 000 analysed rows:\n%s", name, plan)
		if !strings.Contains(plan, "directory_links_subject_key") || !strings.Contains(plan, "subjects_pkey") ||
			strings.Contains(plan, "Seq Scan") {
			t.Errorf("the %s read is not served by indexes:\n%s", name, plan)
		}
	}
}
