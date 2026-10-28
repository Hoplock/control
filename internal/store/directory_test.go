// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

func directoryLink(externalID, subjectID string) store.DirectoryLink {
	return store.DirectoryLink{
		ExternalID: externalID, SubjectID: subjectID, Provider: "hoplock/scim",
		LinkedAt: time.Now().UTC().Truncate(time.Microsecond), Provisioned: true,
		UpdatedAt: time.Now().UTC().Truncate(time.Microsecond), Username: "alice",
		Attributes: map[string]string{"department": "platform"}, GroupIDs: []string{"grp-1", "grp-2"},
	}
}

// A link round-trips, is found by subject, and never moves to another subject.
func TestADirectoryLinkRoundTripsAndNeverMoves(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	l := directoryLink("u-1", "dir-a")
	if err := st.DirectoryLinks().Put(ctx, tenantA, l); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := st.DirectoryLinks().Get(ctx, tenantA, "u-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SubjectID != "dir-a" || !got.UpdatedAt.Equal(l.UpdatedAt) || got.Attributes["department"] != "platform" ||
		!slices.Equal(got.GroupIDs, l.GroupIDs) || got.Deactivated() {
		t.Errorf("round trip = %+v", got)
	}
	if by, err := st.DirectoryLinks().BySubject(ctx, tenantA, "dir-a"); err != nil || by.ExternalID != "u-1" {
		t.Errorf("BySubject = %+v, %v", by, err)
	}
	moved := l
	moved.SubjectID = "dir-b"
	if err := st.DirectoryLinks().Put(ctx, tenantA, moved); !store.IsConflict(err) {
		t.Errorf("re-pointing a link: %v, want a conflict", err)
	}
	// Another tenant has no such link, and can hold its own under the id.
	if _, err := st.DirectoryLinks().Get(ctx, tenantB, "u-1"); !store.IsNotFound(err) {
		t.Errorf("tenant B reads tenant A's link: %v", err)
	}
	if err := st.DirectoryLinks().Put(ctx, tenantB, directoryLink("u-1", "dir-b")); err != nil {
		t.Errorf("tenant B's own link under the same id: %v", err)
	}
}

// The decision path's read carries the directory's link and its deactivation
// in the statement that reads the row and the lock, and a deactivation comes
// back for an id with no row.
func TestSubjectStandingCarriesTheDeactivation(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if err := st.Subjects().Upsert(ctx, tenantA, storetest.Subject("dir-a", "sre")); err != nil {
		t.Fatal(err)
	}
	l := directoryLink("u-1", "dir-a")
	if err := st.DirectoryLinks().Put(ctx, tenantA, l); err != nil {
		t.Fatal(err)
	}
	got, err := st.Subjects().Standing(ctx, tenantA, "dir-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Directory == nil || got.Directory.ExternalID != "u-1" || got.Directory.Provider != "hoplock/scim" || got.Deactivated() {
		t.Errorf("Standing of an active directory subject = %+v", got.Directory)
	}

	l.DeactivatedAt, l.DeactivationReason = time.Now().UTC().Truncate(time.Microsecond), "terminated"
	if err := st.DirectoryLinks().Put(ctx, tenantA, l); err != nil {
		t.Fatal(err)
	}
	got, err = st.Subjects().Standing(ctx, tenantA, "dir-a")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Deactivated() || got.Directory.DeactivationReason != "terminated" || !got.Directory.DeactivatedAt.Equal(l.DeactivatedAt) {
		t.Errorf("Standing of a deactivated subject = %+v", got.Directory)
	}
	if got.Directory.DeactivatedBy().Principal != "identity-sync:hoplock/scim" {
		t.Errorf("deactivated by %+v", got.Directory.DeactivatedBy())
	}

	// A deactivation kept ahead of the subject's create.
	early := store.DirectoryLink{
		ExternalID: "u-2", SubjectID: "dir-early", Provider: "hoplock/scim", LinkedAt: time.Now().UTC(),
		DeactivatedAt: time.Now().UTC(), DeactivationReason: "offboarded",
	}
	if err := st.DirectoryLinks().Put(ctx, tenantA, early); err != nil {
		t.Fatal(err)
	}
	got, err = st.Subjects().Standing(ctx, tenantA, "dir-early")
	if err != nil || got.Known || !got.Deactivated() {
		t.Errorf("Standing of a deactivation with no row = %+v, %v", got, err)
	}
	// A subject the directory does not manage has no link.
	if err := st.Subjects().Upsert(ctx, tenantA, storetest.Subject("local")); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Subjects().Standing(ctx, tenantA, "local"); got.Directory != nil {
		t.Errorf("a local subject reads as directory-managed: %+v", got.Directory)
	}
	// Tenant B never sees tenant A's deactivation.
	if got, _ := st.Subjects().Standing(ctx, tenantB, "dir-a"); got.Directory != nil {
		t.Errorf("tenant B reads tenant A's link: %+v", got.Directory)
	}
}

// A group's members are found by containment, and only provisioned ones; a
// deleted group resolves to no name.
func TestDirectoryGroupsAndTheirMembers(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	for _, l := range []store.DirectoryLink{directoryLink("u-1", "dir-a"), directoryLink("u-2", "dir-b")} {
		if err := st.DirectoryLinks().Put(ctx, tenantA, l); err != nil {
			t.Fatal(err)
		}
	}
	unprovisioned := directoryLink("u-3", "dir-c")
	unprovisioned.Provisioned = false
	if err := st.DirectoryLinks().Put(ctx, tenantA, unprovisioned); err != nil {
		t.Fatal(err)
	}
	members, err := st.DirectoryLinks().Members(ctx, tenantA, "grp-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].SubjectID != "dir-a" || members[1].SubjectID != "dir-b" {
		t.Errorf("Members = %+v", members)
	}
	if members, _ := st.DirectoryLinks().Members(ctx, tenantB, "grp-1"); len(members) != 0 {
		t.Errorf("tenant B sees %d of tenant A's members", len(members))
	}

	for _, g := range []store.DirectoryGroup{
		{ExternalID: "grp-1", Provider: "hoplock/scim", Name: "SRE Team", UpdatedAt: time.Now()},
		{ExternalID: "grp-2", Provider: "hoplock/scim", Name: "DBAs", UpdatedAt: time.Now(), DeletedAt: time.Now()},
	} {
		if err := st.DirectoryGroups().Put(ctx, tenantA, g); err != nil {
			t.Fatal(err)
		}
	}
	names, err := st.DirectoryGroups().Names(ctx, tenantA, []string{"grp-1", "grp-2", "grp-none"})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names["grp-1"] != "SRE Team" {
		t.Errorf("Names = %v, want only the live group", names)
	}
	if g, err := st.DirectoryGroups().Get(ctx, tenantA, "grp-2"); err != nil || !g.Deleted() {
		t.Errorf("the tombstone = %+v, %v", g, err)
	}
}

// A deactivation's credential revocation ends the subject's north-bound
// credentials and outstanding certificates, and nobody else's.
func TestRevokingBySubjectEndsOnlyThatSubjectsCredentials(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	now := time.Now().UTC()

	for _, p := range []store.NorthPrincipal{
		{ID: "s-1", Kind: store.PrincipalSession, SubjectID: "alice", SessionDigest: "d1"},
		{ID: "s-2", Kind: store.PrincipalSession, SubjectID: "bob", SessionDigest: "d2"},
		{ID: "t-1", Kind: store.PrincipalToken, TokenDigest: "d3"},
	} {
		if err := st.NorthPrincipals().Put(ctx, tenantA, p); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.NorthPrincipals().RevokeBySubject(ctx, tenantA, "alice", now)
	if err != nil || n != 1 {
		t.Errorf("RevokeBySubject = %d, %v", n, err)
	}
	if n, _ := st.NorthPrincipals().RevokeBySubject(ctx, tenantA, "alice", now); n != 0 {
		t.Errorf("a second revocation revoked %d", n)
	}
	if _, err := st.NorthPrincipals().RevokeBySubject(ctx, tenantA, "", now); !store.IsInvalid(err) {
		t.Errorf("revoking the credentials of nobody: %v, want a refusal", err)
	}
	for id, revoked := range map[string]bool{"s-1": true, "s-2": false, "t-1": false} {
		p, _ := st.NorthPrincipals().Get(ctx, tenantA, id)
		if got := !p.RevokedAt.IsZero(); got != revoked {
			t.Errorf("%s revoked = %v, want %v", id, got, revoked)
		}
	}

	if err := st.CAKeys().Insert(ctx, tenantA, store.CAKey{
		KeyID: "ca-1", Algorithm: "ed25519", PublicKey: []byte("pub"), PrivateRef: "software:ca-1", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	for serial, c := range map[int64]store.SSHCertificate{
		1: {SubjectID: "alice", ValidBefore: now.Add(time.Hour)},
		2: {SubjectID: "alice", ValidBefore: now.Add(-time.Minute)},
		3: {SubjectID: "bob", ValidBefore: now.Add(time.Hour)},
	} {
		c.Serial, c.KeyID, c.CAKeyID, c.ValidAfter, c.Certificate = serial, "k", "ca-1", now.Add(-time.Hour), "cert"
		if err := st.SSHCertificates().Insert(ctx, tenantA, c); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := st.SSHCertificates().RevokeBySubject(ctx, tenantA, "alice", now, "deactivated"); err != nil || n != 1 {
		t.Errorf("certificates revoked = %d, %v; want only the outstanding one", n, err)
	}
	if c, _ := st.SSHCertificates().Get(ctx, tenantA, 3); !c.RevokedAt.IsZero() {
		t.Error("bob's certificate was revoked with alice's")
	}
}

// The directory's locks are transaction-scoped and refused outside one.
func TestTheDirectoryLocksAreTakenInsideATransaction(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	for name, lock := range map[string]func(*store.Store) error{
		"links":     func(s *store.Store) error { return s.DirectoryLinks().Lock(ctx, tenantA, "u-1") },
		"groups":    func(s *store.Store) error { return s.DirectoryGroups().Lock(ctx, tenantA) },
		"share":     func(s *store.Store) error { return s.DirectoryGroups().Share(ctx, tenantA) },
		"principal": func(s *store.Store) error { return s.Subjects().LockPrincipal(ctx, tenantA, "alice") },
	} {
		if err := lock(st); !store.IsInvalid(err) {
			t.Errorf("%s outside a transaction: %v, want a refusal", name, err)
		}
		if err := st.InTx(ctx, func(_ context.Context, tx *store.Store) error { return lock(tx) }); err != nil {
			t.Errorf("%s inside a transaction: %v", name, err)
		}
	}
	// Two shares stand together; an exclusive lock waits for both.
	if err := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.DirectoryGroups().Share(ctx, tenantA); err != nil {
			return err
		}
		return st.InTx(ctx, func(ctx context.Context, other *store.Store) error {
			return other.DirectoryGroups().Share(ctx, tenantA)
		})
	}); err != nil {
		t.Errorf("two shared locks: %v", err)
	}
}

// A subject other than the one named presents a login name.
func TestPresentedByAnother(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	if err := st.Subjects().Upsert(ctx, tenantA, storetest.Subject("alice")); err != nil {
		t.Fatal(err)
	}
	if s, err := st.Subjects().PresentedByAnother(ctx, tenantA, "alice", "dir-x"); err != nil || s.ID != "alice" {
		t.Errorf("PresentedByAnother = %+v, %v", s, err)
	}
	if _, err := st.Subjects().PresentedByAnother(ctx, tenantA, "alice", "alice"); !store.IsNotFound(err) {
		t.Errorf("the subject itself counted as another: %v", err)
	}
	if _, err := st.Subjects().PresentedByAnother(ctx, tenantB, "alice", "dir-x"); !store.IsNotFound(err) {
		t.Errorf("tenant B sees tenant A's login: %v", err)
	}
}
