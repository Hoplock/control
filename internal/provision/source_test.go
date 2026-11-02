// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/store"
)

// The directory source (0042): two directories feeding one tenant each keep
// their own ids, and everything 0040 keyed on an ExternalID — the link and the
// group, the ordering, the tombstones, the lock and a group's members — is
// keyed on (tenant, source, ExternalID).

// from re-labels a directory record with another source.
func from(src string, s ext.SyncedSubject) ext.SyncedSubject { s.Source = src; return s }

func groupFrom(src string, g ext.SyncedGroup) ext.SyncedGroup { g.Source = src; return g }

func refIn(src, externalID string) ext.DirectoryRef {
	return ext.DirectoryRef{Tenant: ext.Tenant(tenant), Source: src, ExternalID: externalID}
}

func TestTwoDirectoriesKeepTheirOwnIDsInOneTenant(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-sre", "SRE Team"))
	h.mustGroup(groupFrom(sourceB, h.group("grp-sre", "SRE Team")))
	h.mustUpsert(h.person("00u1", "alice", "grp-sre"))
	h.mustUpsert(from(sourceB, h.person("00u1", "bob", "grp-sre")))

	a, b := h.linkIn(source, "00u1"), h.linkIn(sourceB, "00u1")
	if a.SubjectID == b.SubjectID || !strings.HasPrefix(a.SubjectID, "dir-") || !strings.HasPrefix(b.SubjectID, "dir-") {
		t.Fatalf("one id in two directories is subjects %q and %q, want two dir- ids", a.SubjectID, b.SubjectID)
	}
	for src, link := range map[string]store.DirectoryLink{source: a, sourceB: b} {
		standing, err := h.store.Subjects().Standing(t.Context(), tenant, link.SubjectID)
		if err != nil || standing.Directory == nil || standing.Directory.Source != src || standing.Directory.ExternalID != "00u1" {
			t.Errorf("subject %s reads directory %+v (%v), want source %s", link.SubjectID, standing.Directory, err, src)
		}
	}
	if out := h.authorize(b.SubjectID, "sess-b"); out.Deny != nil {
		t.Fatalf("source b's person is denied before anything happened: %+v", out.Deny)
	}

	if err := h.sink.DeactivateSubject(t.Context(), refIn(source, "00u1"), "terminated"); err != nil {
		t.Fatal(err)
	}
	if out := h.authorize(a.SubjectID, "sess-a"); out.Deny == nil || out.Deny.Reason != "subject_deactivated" {
		t.Errorf("source a's deactivated person = %+v, want subject_deactivated", out.Deny)
	}
	if out := h.authorize(b.SubjectID, "sess-b2"); out.Deny != nil {
		t.Errorf("deactivating (a, 00u1) denied (b, 00u1): %+v", out.Deny)
	}
	if h.linkIn(sourceB, "00u1").Deactivated() {
		t.Error("source b's link carries source a's deactivation")
	}
}

func TestASubjectsGroupsResolveWithinItsOwnSource(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("g1", "SRE Team"))
	h.mustGroup(groupFrom(sourceB, h.group("g1", "DBAs")))
	h.mustUpsert(h.person("u-a", "alice", "g1"))
	h.mustUpsert(from(sourceB, h.person("u-b", "bob", "g1")))

	if got := h.subject("u-a").Groups; !slices.Equal(got, []string{"sre"}) {
		t.Errorf("source a's subject takes %v from g1, want only sre: (b, g1) is DBAs", got)
	}
	bob, err := h.store.Subjects().Get(t.Context(), tenant, h.linkIn(sourceB, "u-b").SubjectID)
	if err != nil || !slices.Equal(bob.Groups, []string{"dba"}) {
		t.Errorf("source b's subject = %v (%v), want dba", bob.Groups, err)
	}

	// Renaming and deleting (b, g1) re-derive no subject of a.
	alice := h.subject("u-a")
	before := len(h.revoker.invalidated())
	h.mustGroup(groupFrom(sourceB, h.group("g1", "SRE Team")))
	if err := h.sink.DeleteGroup(t.Context(), refIn(sourceB, "g1")); err != nil {
		t.Fatal(err)
	}
	for _, rec := range append(h.records(tenant, audit.EventSyncGroupUpserted)[:1], h.records(tenant, audit.EventSyncGroupDeleted)...) {
		if field(rec, audit.AttrSyncSource) != sourceB || field(rec, "identity_sync.members_rederived") != "1" {
			t.Errorf("a change to (b, g1) recorded %v, want one member re-derived in source b", rec.Attributes)
		}
	}
	if got := h.subject("u-a"); !slices.Equal(got.Groups, alice.Groups) || !got.UpdatedAt.Equal(alice.UpdatedAt) {
		t.Errorf("a change to (b, g1) rewrote source a's subject: %+v", got)
	}
	for _, inv := range h.revoker.invalidated()[before:] {
		if inv.Subject == alice.ID || inv.All {
			t.Errorf("a change to (b, g1) dropped source a's cached decisions: %+v", inv)
		}
	}
	if got := h.subject("u-a").Groups; !slices.Equal(got, []string{"sre"}) {
		t.Errorf("after (b, g1) was deleted source a's subject has %v, want sre still", got)
	}
}

func TestOrderingAndTombstonesAreKeptPerSource(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{"a stale upsert for (a, x) changes nothing although (b, x) was written later", func(t *testing.T, h *harness) {
			stale := h.person("x", "alice")
			h.mustUpsert(h.person("x", "alice.v2"))
			h.mustUpsert(from(sourceB, h.person("x", "bob")))
			h.mustUpsert(stale)
			if got := h.linkIn(source, "x").Username; got != "alice.v2" {
				t.Errorf("(a, x) is %q, want the newer write's alice.v2", got)
			}
		}},
		{"(b, x) written later does not make a newer (a, x) stale", func(t *testing.T, h *harness) {
			h.mustUpsert(h.person("x", "alice"))
			older := h.person("x", "alice.v2")
			h.mustUpsert(from(sourceB, h.person("x", "bob")))
			h.mustUpsert(older)
			if got := h.linkIn(source, "x").Username; got != "alice.v2" {
				t.Errorf("(a, x) is %q: b's later time ordered a's write", got)
			}
		}},
		{"a deactivation kept for an unseen (a, x) leaves a later create of (b, x) active", func(t *testing.T, h *harness) {
			if err := h.sink.DeactivateSubject(t.Context(), refIn(source, "x"), "terminated"); err != nil {
				t.Fatal(err)
			}
			h.mustUpsert(h.person("x", "alice"))
			h.mustUpsert(from(sourceB, h.person("x", "bob")))
			if a := h.linkIn(source, "x"); !a.Deactivated() || !a.Provisioned {
				t.Errorf("(a, x) = %+v, want provisioned deactivated", a)
			}
			if b := h.linkIn(sourceB, "x"); b.Deactivated() || !b.Provisioned {
				t.Errorf("(b, x) = %+v, want provisioned active", b)
			}
		}},
		{"a tombstone for (a, g) does not stop an older upsert of (b, g)", func(t *testing.T, h *harness) {
			older := groupFrom(sourceB, h.group("g", "DBAs"))
			if err := h.sink.DeleteGroup(t.Context(), refIn(source, "g")); err != nil {
				t.Fatal(err)
			}
			h.mustGroup(older)
			g, err := h.store.DirectoryGroups().Get(t.Context(), tenant, sourceB, "g")
			if err != nil || g.Deleted() || g.Name != "DBAs" {
				t.Errorf("(b, g) = %+v (%v), want it live: (a, g)'s tombstone is a's", g, err)
			}
			if a, _ := h.store.DirectoryGroups().Get(t.Context(), tenant, source, "g"); !a.Deleted() {
				t.Errorf("(a, g) = %+v, want its tombstone kept", a)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newHarness(t)) })
	}
}

func TestALoginNameIsUniqueAcrossSources(t *testing.T) {
	h := newHarness(t)
	h.mustUpsert(h.person("x", "alice"))
	err := h.sink.UpsertSubject(t.Context(), from(sourceB, h.person("y", "alice")))
	if kindOf(err) != ext.KindConflict {
		t.Fatalf("(b, y) presenting (a, x)'s name: %v, want KindConflict", err)
	}
	if _, err := h.store.DirectoryLinks().Get(t.Context(), tenant, sourceB, "y"); !store.IsNotFound(err) {
		t.Errorf("the refused upsert left a link: %v", err)
	}
	recs := h.records(tenant, audit.EventSyncRefused)
	if len(recs) != 1 || field(recs[0], audit.AttrSyncSource) != sourceB || field(recs[0], audit.AttrSyncExternalID) != "y" ||
		field(recs[0], "identity_sync.conflicting_subject") != h.link("x").SubjectID {
		t.Errorf("the refusal recorded %v", recs)
	}
}

func TestASourceIsRequiredAndHeldToItsPattern(t *testing.T) {
	h := newHarness(t)
	calls := map[string]func(src string) error{
		"UpsertSubject": func(src string) error {
			// One login name per source: a name is unique across them.
			return h.sink.UpsertSubject(t.Context(), from(src, h.person("u-1", "alice-"+src)))
		},
		"UpsertGroup": func(src string) error {
			return h.sink.UpsertGroup(t.Context(), groupFrom(src, h.group("g1", "SRE Team")))
		},
		"DeactivateSubject": func(src string) error {
			return h.sink.DeactivateSubject(t.Context(), refIn(src, "u-1"), "terminated")
		},
		"DeleteGroup": func(src string) error { return h.sink.DeleteGroup(t.Context(), refIn(src, "g1")) },
	}
	bad := map[string]string{
		"absent":           "",
		"upper-case":       "Okta",
		"64 characters":    strings.Repeat("a", 64),
		"starting with -":  "-okta",
		"with a dot":       "okta.a",
		"with a space":     "okta a",
		"with an under_sc": "okta_a",
	}
	refused := 0
	for method, call := range calls {
		for why, src := range bad {
			err := call(src)
			if kindOf(err) != ext.KindInvalid || !strings.Contains(err.Error(), "field source") {
				t.Errorf("%s with a source %s (%q): %v, want KindInvalid naming source", method, why, src, err)
			}
			refused++
		}
		// The longest source there is, and one that is all digits, are fine.
		for _, src := range []string{strings.Repeat("a", 63), "0123"} {
			if err := call(src); err != nil {
				t.Errorf("%s with the source %q: %v", method, src, err)
			}
		}
	}
	recs := h.records(tenant, audit.EventSyncRefused)
	if len(recs) != refused {
		t.Errorf("%d refusal records for %d refused calls", len(recs), refused)
	}
	// A refusal carries the source the call named, whatever was wrong with it.
	var withSource int
	for _, rec := range recs {
		if field(rec, "identity_sync.field") != "source" {
			t.Errorf("a refusal names field %q", field(rec, "identity_sync.field"))
		}
		if field(rec, audit.AttrSyncSource) == "Okta" {
			withSource++
		}
	}
	if withSource != len(calls) {
		t.Errorf("%d refusals of the source Okta carry it, want %d: a refusal carries the source the call named", withSource, len(calls))
	}
}

func TestEverySyncRecordCarriesTheSource(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("g1", "SRE Team"))
	h.mustUpsert(h.person("u-1", "alice", "g1"))
	h.mustUpsert(h.person("u-1", "alice.smith", "g1"))
	inactive := h.person("u-1", "alice.smith", "g1")
	inactive.Active = false
	h.mustUpsert(inactive)
	// A re-activation is later than Control's instant of the deactivation.
	active := h.person("u-1", "alice.smith", "g1")
	active.UpdatedAt = time.Now().Add(time.Hour)
	h.mustUpsert(active)
	if err := h.sink.DeleteGroup(t.Context(), ref(tenant, "g1")); err != nil {
		t.Fatal(err)
	}
	if err := h.sink.UpsertSubject(t.Context(), h.person("u-2", "alice.smith")); kindOf(err) != ext.KindConflict {
		t.Fatalf("a taken name: %v", err)
	}
	for _, event := range []string{
		audit.EventSyncSubjectProvisioned, audit.EventSyncSubjectUpdated, audit.EventSyncSubjectDeactivated,
		audit.EventSyncSubjectReactivated, audit.EventSyncGroupUpserted, audit.EventSyncGroupDeleted,
		audit.EventSyncRefused,
	} {
		recs := h.records(tenant, event)
		if len(recs) == 0 {
			t.Errorf("no %s record was written", event)
		}
		for _, rec := range recs {
			if field(rec, audit.AttrSyncSource) != source || field(rec, audit.AttrSyncProvider) != provider || field(rec, audit.AttrSyncExternalID) == "" {
				t.Errorf("%s carries %v, want provider, source and external_id", event, rec.Attributes)
			}
		}
	}
	h.verify()
}
