// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/provision"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/standing"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// An upsert provisions a subject joined on ExternalID, never on the username:
// a rename keeps the subject, replaces its one login name, and leaves the old
// name resolving to nobody.
func TestAnUpsertProvisionsJoinedOnExternalIDAndARenameKeepsTheSubject(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))
	h.mustUpsert(h.person("u-1", "alice", "grp-1"))

	link := h.link("u-1")
	if !strings.HasPrefix(link.SubjectID, provision.SubjectIDPrefix) || strings.Contains(link.SubjectID, ":") {
		t.Fatalf("subject id %q is not one Control allocated", link.SubjectID)
	}
	alice := h.subject("u-1")
	if !slices.Equal(alice.Principals, []string{"alice"}) || alice.Source != provider || alice.BreakGlass {
		t.Errorf("provisioned subject = %+v", alice)
	}
	if !slices.Equal(alice.Groups, []string{"sre"}) {
		t.Errorf("groups = %v, want the mapped sre", alice.Groups)
	}
	if out := h.authorize(alice.ID, "sess-1"); out.Deny != nil {
		t.Fatalf("a provisioned sre was denied: %+v", out.Deny)
	}

	h.mustUpsert(h.person("u-1", "alice.smith", "grp-1"))
	renamed := h.subject("u-1")
	if renamed.ID != alice.ID {
		t.Fatalf("a rename moved the person from %s to %s", alice.ID, renamed.ID)
	}
	if !slices.Equal(renamed.Principals, []string{"alice.smith"}) {
		t.Errorf("principals after a rename = %v, want only the new name", renamed.Principals)
	}
	if _, err := h.store.Subjects().GetByPrincipal(t.Context(), tenant, "alice"); !store.IsNotFound(err) {
		t.Errorf("the old name still resolves: %v", err)
	}
	if got := len(h.records(tenant, audit.EventSyncSubjectProvisioned)); got != 1 {
		t.Errorf("%d provisioned records, want one", got)
	}
	updated := h.records(tenant, audit.EventSyncSubjectUpdated)
	if len(updated) != 1 {
		t.Fatalf("%d updated records, want one", len(updated))
	}
	rec := updated[0]
	if field(rec, audit.AttrPrincipalID) != provision.PrincipalPrefix+provider || field(rec, audit.AttrSyncProvider) != provider ||
		field(rec, audit.AttrSyncExternalID) != "u-1" || rec.Subject != alice.ID {
		t.Errorf("the update record names %v", rec.Attributes)
	}
	if field(rec, "identity_sync.principals_before") != `["alice"]` || field(rec, "identity_sync.principals_after") != `["alice.smith"]` {
		t.Errorf("the record's principals = %q -> %q", field(rec, "identity_sync.principals_before"), field(rec, "identity_sync.principals_after"))
	}
	h.verify()
}

// The same upsert twice applies once and writes one record, and an upsert older
// than the last applied one changes nothing.
func TestAnUpsertIsIdempotentAndOrderedByUpdatedAt(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))
	h.mustGroup(h.group("grp-2", "DBAs"))

	first := h.person("u-1", "alice", "grp-1")
	h.mustUpsert(first)
	h.mustUpsert(first)
	if got := h.syncRecords(tenant); got != 3 {
		t.Errorf("%d sync records after two groups and the same upsert twice, want 3", got)
	}

	newer := h.person("u-1", "alice", "grp-2")
	h.mustUpsert(newer)
	stale := first
	stale.Groups = []string{"grp-1"}
	stale.Username = "mallory"
	h.mustUpsert(stale)
	s := h.subject("u-1")
	if !slices.Equal(s.Groups, []string{"dba"}) || !slices.Equal(s.Principals, []string{"alice"}) {
		t.Errorf("after a stale upsert the subject is %+v, want the newer write's", s)
	}
	if got := len(h.records(tenant, audit.EventSyncSubjectUpdated)); got != 1 {
		t.Errorf("%d update records, want one for the newer write and none for the stale one", got)
	}
	// The directory's time is kept to the database's precision, so a replay
	// with nanoseconds is still the same write.
	nanos := h.person("u-1", "alice", "grp-2")
	nanos.UpdatedAt = nanos.UpdatedAt.Add(123)
	h.mustUpsert(nanos)
	h.mustUpsert(nanos)
	if got := len(h.records(tenant, audit.EventSyncSubjectUpdated)); got != 2 {
		t.Errorf("%d update records after a sub-microsecond replay, want 2", got)
	}
}

// A hundred concurrent upserts across fresh ids each apply once.
func TestAHundredConcurrentUpsertsAcrossFreshIDsEachApplyOnce(t *testing.T) {
	h := newHarness(t)
	const n = 120
	at := time.Now().UTC().Truncate(time.Second)
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := range n {
		s := ext.SyncedSubject{
			Tenant: ext.Tenant(tenant), ExternalID: fmt.Sprintf("u-%03d", i), Username: fmt.Sprintf("user%03d", i),
			Active: true, UpdatedAt: at,
		}
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- h.sink.UpsertSubject(t.Context(), s)
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent upsert failed: %v", err)
		}
	}
	if got := len(h.records(tenant, audit.EventSyncSubjectProvisioned)); got != n {
		t.Errorf("%d provisioned records for %d people each sent twice, want %d", got, n, n)
	}
	var subjects int
	if err := h.store.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM subjects WHERE tenant = $1 AND source = $2`, string(tenant), provider).Scan(&subjects); err != nil {
		t.Fatal(err)
	}
	if subjects != n {
		t.Errorf("%d subjects, want %d", subjects, n)
	}
}

// Only what the tenant's mapping names becomes an attribute or a group, on the
// subject row and in a decision; a reserved name is stored nowhere; and a rename
// narrows access rather than widening it.
func TestOnlyWhatTheMappingNamesReachesTheSubjectOrADecision(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))
	h.mustGroup(h.group("grp-x", "Domain Admins"))
	p := h.person("u-1", "alice", "grp-1", "grp-x")
	p.Attributes = map[string]string{
		"department":         "platform",
		"role":               "admin",
		"chain_hop_proxy_id": "proxy-1",
		"hoplock.trust":      "yes",
	}
	h.mustUpsert(p)

	s := h.subject("u-1")
	if !slices.Equal(s.Groups, []string{"sre"}) {
		t.Errorf("groups = %v, want only the mapped sre", s.Groups)
	}
	want := map[string]string{"department": "platform", "email": "alice@example.com"}
	if len(s.Claims) != len(want) || s.Claims["department"] != "platform" || s.Claims["email"] != "alice@example.com" {
		t.Errorf("claims = %v, want %v", s.Claims, want)
	}
	if s.MappingVersion == 0 {
		t.Error("the subject does not record the mapping version that produced it")
	}
	link := h.link("u-1")
	for _, reserved := range []string{"chain_hop_proxy_id", "hoplock.trust"} {
		if _, ok := link.Attributes[reserved]; ok {
			t.Errorf("%s was stored on the link", reserved)
		}
	}
	var stored int
	if err := h.store.Pool().QueryRow(t.Context(), `
		SELECT count(*) FROM subjects WHERE claims ? 'chain_hop_proxy_id' OR claims::text LIKE '%hoplock.%'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Errorf("%d subject rows carry a reserved name", stored)
	}

	// The decision sees the mapped group and allows; a rename of the group the
	// mapping names takes it away until the mapping names the new name.
	if out := h.authorize(s.ID, "sess-1"); out.Deny != nil {
		t.Fatalf("denied with the mapped group: %+v", out.Deny)
	}
	h.mustGroup(h.group("grp-1", "Site Reliability"))
	if s := h.subject("u-1"); len(s.Groups) != 0 {
		t.Errorf("after a rename the mapping does not name, groups = %v", s.Groups)
	}
	if out := h.authorize(s.ID, "sess-2"); out.Deny == nil {
		t.Error("a renamed directory group still granted the policy group")
	}
	if !slices.ContainsFunc(h.revoker.invalidated(), func(i revoke.Invalidation) bool { return i.Subject == s.ID }) {
		t.Error("a rename that narrowed a subject's groups dropped no cached decision")
	}
}

// DeactivateSubject on a subject that holds everything leaves all of it
// revoked: the grant and the session it backed, the pending request, the
// north-bound session and API token, and the outstanding certificate. The
// session is killed with Control's text, the next authorize is a deny with
// `subject_deactivated` whatever policy allows, and the audit history verifies.
func TestADeactivationRevokesEverythingTheSubjectHolds(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))
	h.mustUpsert(h.person("u-1", "alice", "grp-1"))
	id := h.link("u-1").SubjectID
	ctx := t.Context()

	h.grantFor("g-1", id, "sess-live")
	storetest.Seed(t, h.store, storetest.Fixture{Tenant: tenant, GrantRequests: []store.GrantRequest{storetest.GrantRequest("r-1", id, time.Hour)}})
	for _, p := range []store.NorthPrincipal{
		{ID: "sess-n1", Kind: store.PrincipalSession, SubjectID: id, SessionDigest: "digest-session", ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "tok-n1", Kind: store.PrincipalToken, SubjectID: id, TokenDigest: "digest-token"},
		{ID: "tok-machine", Kind: store.PrincipalToken, TokenDigest: "digest-machine"},
	} {
		if err := h.store.NorthPrincipals().Put(ctx, tenant, p); err != nil {
			t.Fatalf("principal %s: %v", p.ID, err)
		}
	}
	if err := h.store.CAKeys().Insert(ctx, tenant, store.CAKey{
		KeyID: "ca-1", Algorithm: "ed25519", PublicKey: []byte("pub"), PrivateRef: "software:ca-1", Active: true,
	}); err != nil {
		t.Fatalf("ca key: %v", err)
	}
	if err := h.store.SSHCertificates().Insert(ctx, tenant, store.SSHCertificate{
		Serial: 7, KeyID: "k", CAKeyID: "ca-1", SubjectID: id, Principals: []string{"alice"},
		ValidAfter: time.Now().Add(-time.Minute), ValidBefore: time.Now().Add(time.Hour), Certificate: "ssh-ed25519-cert-v01@openssh.com AAAA",
	}); err != nil {
		t.Fatalf("certificate: %v", err)
	}

	if err := h.sink.DeactivateSubject(ctx, ext.Tenant(tenant), "u-1", "terminated"); err != nil {
		t.Fatalf("DeactivateSubject: %v", err)
	}

	g, err := h.store.Grants().Get(ctx, tenant, "g-1")
	if err != nil || g.RevokedAt.IsZero() || g.RevokedBy.Principal != provision.PrincipalPrefix+provider || g.RevokedBy.Subject != "" {
		t.Errorf("the grant after a deactivation = %+v, %v", g, err)
	}
	if g.RevokeReason != provision.DeactivatedByDirectory {
		t.Errorf("the grant's holder reason = %q", g.RevokeReason)
	}
	req, err := h.store.GrantRequests().Get(ctx, tenant, "r-1")
	if err != nil || req.State != store.GrantRequestCancelled {
		t.Errorf("the pending request after a deactivation = %+v, %v", req, err)
	}
	for _, pid := range []string{"sess-n1", "tok-n1"} {
		if p, err := h.store.NorthPrincipals().Get(ctx, tenant, pid); err != nil || p.RevokedAt.IsZero() {
			t.Errorf("north-bound credential %s after a deactivation = %+v, %v", pid, p, err)
		}
	}
	if p, _ := h.store.NorthPrincipals().Get(ctx, tenant, "tok-machine"); !p.RevokedAt.IsZero() {
		t.Error("a machine token with no subject was revoked with the person")
	}
	if c, err := h.store.SSHCertificates().Get(ctx, tenant, 7); err != nil || c.RevokedAt.IsZero() {
		t.Errorf("the certificate after a deactivation = %+v, %v", c, err)
	}

	var subjectKill, sessionKill bool
	for _, k := range h.revoker.killed() {
		if k.Subject == id && k.Reason == provision.DeactivatedByDirectory {
			subjectKill = true
		}
		if slices.Contains(k.SessionIDs, "sess-live") && k.Reason == provision.DeactivatedByDirectory {
			sessionKill = true
		}
		if k.Reason == "terminated" {
			t.Errorf("the holder was shown the directory's code: %+v", k)
		}
	}
	if !subjectKill || !sessionKill {
		t.Errorf("kills = %+v, want the subject's and the grant's session, with Control's text", h.revoker.killed())
	}

	out := h.authorize(id, "sess-after")
	if out.Deny == nil || out.Deny.Reason != "subject_deactivated" {
		t.Fatalf("authorize after a deactivation = %+v, want a subject_deactivated deny", out)
	}
	recs := h.records(tenant, audit.EventSyncSubjectDeactivated)
	if len(recs) != 1 || field(recs[0], "identity_sync.reason_code") != "terminated" ||
		field(recs[0], "identity_sync.credentials_revoked") != "2" || field(recs[0], "identity_sync.certificates_revoked") != "1" {
		t.Errorf("deactivation records = %+v", recs)
	}
	if got := len(h.records(tenant, audit.EventRevocationPublished)); got < 2 {
		t.Errorf("%d revocation records, want the invalidation's and the kill's", got)
	}
	// History is attributable: the subject row is still there.
	if _, err := h.store.Subjects().Get(ctx, tenant, id); err != nil {
		t.Errorf("a deactivation deleted the subject: %v", err)
	}
	h.verify()
}

// With the stream refusing, a deactivation is KindUnavailable and the deny
// already holds; once it recovers, the same call delivers the kill, writes no
// second deactivation record, and returns nil.
func TestADeactivationTheStreamRefusedIsFinishedByTheSameCall(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))
	h.mustUpsert(h.person("u-1", "alice", "grp-1"))
	id := h.link("u-1").SubjectID

	h.revoker.fail.Store(true)
	err := h.sink.DeactivateSubject(t.Context(), ext.Tenant(tenant), "u-1", "terminated")
	if kindOf(err) != ext.KindUnavailable {
		t.Fatalf("DeactivateSubject with the stream down: %v, want KindUnavailable", err)
	}
	if out := h.authorize(id, "sess-1"); out.Deny == nil || out.Deny.Reason != "subject_deactivated" {
		t.Fatalf("the deny does not hold while the stream is down: %+v", out)
	}
	if !h.link("u-1").DeactivationOwed {
		t.Error("the link does not remember what it owes")
	}

	h.revoker.fail.Store(false)
	if err := h.sink.DeactivateSubject(t.Context(), ext.Tenant(tenant), "u-1", "terminated"); err != nil {
		t.Fatalf("the retry: %v", err)
	}
	if !killedSubject(h, id) {
		t.Error("the retry did not deliver the kill")
	}
	if got := len(h.records(tenant, audit.EventSyncSubjectDeactivated)); got != 1 {
		t.Errorf("%d deactivation records, want one", got)
	}
	if h.link("u-1").DeactivationOwed {
		t.Error("the link still owes the deactivation after it was delivered")
	}
}

func killedSubject(h *harness, id string) bool {
	for _, k := range h.revoker.killed() {
		if k.Subject == id {
			return true
		}
	}
	return false
}

// A deactivation for an unknown id is kept, and the create that follows
// provisions the subject deactivated. A re-delivered upsert written before a
// deactivation does not re-activate; a later `Active: true` does, and restores
// no revoked grant.
func TestOrderIsDecidedWithoutTrustingEitherClockToWidenAccess(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))

	created := h.person("u-1", "alice", "grp-1") // written before the deactivation
	if err := h.sink.DeactivateSubject(t.Context(), ext.Tenant(tenant), "u-1", "offboarded"); err != nil {
		t.Fatalf("deactivating an unknown id: %v", err)
	}
	link := h.link("u-1")
	if link.Provisioned || !link.Deactivated() {
		t.Fatalf("the kept deactivation = %+v", link)
	}
	h.mustUpsert(created)
	id := h.link("u-1").SubjectID
	if id != link.SubjectID {
		t.Fatalf("the create provisioned %s, not the id the deactivation kept (%s)", id, link.SubjectID)
	}
	if out := h.authorize(id, "sess-1"); out.Deny == nil || out.Deny.Reason != "subject_deactivated" {
		t.Fatalf("the late create re-admitted the person: %+v", out)
	}

	// A re-delivery of an earlier write changes nothing.
	h.mustUpsert(created)
	if !h.link("u-1").Deactivated() {
		t.Fatal("a re-delivered upsert written before the deactivation re-activated the person")
	}
	// A write later than the last applied one, but stamped by the directory
	// before Control applied the deactivation, is applied and does not lift
	// it: a directory clock behind Control's delays a re-activation.
	between := h.person("u-1", "alice.b", "grp-1")
	h.mustUpsert(between)
	if l := h.link("u-1"); !l.Deactivated() || l.Username != "alice.b" {
		t.Fatalf("an upsert written before the deactivation = %+v, want it applied and the deactivation standing", l)
	}

	// A grant revoked by the deactivation stays revoked after a re-activation.
	h.grantFor("g-1", id)
	if err := h.sink.DeactivateSubject(t.Context(), ext.Tenant(tenant), "u-1", "offboarded"); err != nil {
		t.Fatalf("deactivating again: %v", err)
	}
	if got := len(h.records(tenant, audit.EventSyncSubjectDeactivated)); got != 1 {
		t.Errorf("%d deactivation records after a repeat, want one", got)
	}

	// A write later than both the last applied one and Control's deactivation.
	later := h.person("u-1", "alice", "grp-1")
	later.UpdatedAt = time.Now().Add(time.Minute)
	h.mustUpsert(later)
	if h.link("u-1").Deactivated() {
		t.Fatal("a later Active: true did not re-activate")
	}
	if out := h.authorize(id, "sess-2"); out.Deny != nil {
		t.Errorf("a re-activated sre was denied: %+v", out.Deny)
	}
	if g, _ := h.store.Grants().Get(t.Context(), tenant, "g-1"); g.RevokedAt.IsZero() {
		t.Error("a re-activation restored a revoked grant")
	}
	if got := len(h.records(tenant, audit.EventSyncSubjectReactivated)); got != 1 {
		t.Errorf("%d re-activation records, want one", got)
	}
}

// `Active: false` on an upsert is a deactivation.
func TestAnInactiveUpsertIsADeactivation(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))
	h.mustUpsert(h.person("u-1", "alice", "grp-1"))
	id := h.link("u-1").SubjectID
	h.grantFor("g-1", id)

	off := h.person("u-1", "alice", "grp-1")
	off.Active = false
	h.mustUpsert(off)
	if out := h.authorize(id, "sess-1"); out.Deny == nil || out.Deny.Reason != "subject_deactivated" {
		t.Fatalf("authorize after Active: false = %+v", out)
	}
	if g, _ := h.store.Grants().Get(t.Context(), tenant, "g-1"); g.RevokedAt.IsZero() {
		t.Error("Active: false revoked no grant")
	}
	recs := h.records(tenant, audit.EventSyncSubjectDeactivated)
	if len(recs) != 1 || field(recs[0], "identity_sync.reason_code") != provision.ReasonDirectoryInactive {
		t.Errorf("deactivation records = %+v", recs)
	}
}

// A locked and deactivated subject stays denied after either is lifted alone:
// an operator's unlock does not re-admit a person the directory de-provisioned,
// and the directory's re-activation does not lift a lock.
func TestALockAndADeactivationAreTwoStandingDenials(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))
	h.mustUpsert(h.person("u-1", "alice", "grp-1"))
	id := h.link("u-1").SubjectID
	operator := standing.Actor{Principal: "p-soc"}

	if _, err := h.standing.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: id, Reason: "Investigating."}); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if err := h.sink.DeactivateSubject(t.Context(), ext.Tenant(tenant), "u-1", "terminated"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	out := h.authorize(id, "sess-1")
	if out.Deny == nil || out.Deny.Reason != "subject_locked" {
		t.Fatalf("locked and deactivated = %+v", out)
	}
	rec, err := h.store.Decisions().Get(t.Context(), tenant, out.Deny.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rec.Inputs), `"subject_locked"`) || !strings.Contains(string(rec.Inputs), `"subject_deactivated"`) {
		t.Errorf("the record does not hold both denials: %s", rec.Inputs)
	}

	// The operator lifts the lock: still deactivated.
	err = h.store.InTx(t.Context(), func(ctx context.Context, tx *store.Store) error {
		_, err := h.standing.LiftLock(ctx, tx, tenant, operator, standing.LiftRequest{ID: id})
		return err
	})
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if out := h.authorize(id, "sess-2"); out.Deny == nil || out.Deny.Reason != "subject_deactivated" {
		t.Errorf("an unlock re-admitted a deactivated person: %+v", out)
	}

	// Lock again; the directory re-activates: still locked.
	if _, err := h.standing.Lock(t.Context(), tenant, operator, standing.LockRequest{SubjectID: id, Reason: "Investigating."}); err != nil {
		t.Fatalf("lock: %v", err)
	}
	back := h.person("u-1", "alice", "grp-1")
	back.UpdatedAt = time.Now().Add(time.Minute)
	h.mustUpsert(back)
	if out := h.authorize(id, "sess-3"); out.Deny == nil || out.Deny.Reason != "subject_locked" {
		t.Errorf("a re-activation lifted a lock: %+v", out)
	}
}

// DeleteGroup removes the derived group from every member and invalidates
// their cached decisions; an older UpsertGroup after it changes nothing.
func TestDeletingAGroupStopsItsMembershipApplying(t *testing.T) {
	h := newHarness(t)
	older := h.group("grp-1", "SRE Team")
	h.mustGroup(older)
	h.mustUpsert(h.person("u-1", "alice", "grp-1"))
	h.mustUpsert(h.person("u-2", "bob", "grp-1"))
	alice, bob := h.link("u-1").SubjectID, h.link("u-2").SubjectID
	before := len(h.revoker.invalidated())

	if err := h.sink.DeleteGroup(t.Context(), ext.Tenant(tenant), "grp-1"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	for _, x := range []string{"u-1", "u-2"} {
		if s := h.subject(x); len(s.Groups) != 0 {
			t.Errorf("%s keeps %v after the group was deleted", x, s.Groups)
		}
	}
	got := map[string]bool{}
	for _, inv := range h.revoker.invalidated()[before:] {
		got[inv.Subject] = true
	}
	if !got[alice] || !got[bob] {
		t.Errorf("invalidations after the delete = %v, want both members", got)
	}
	recs := h.records(tenant, audit.EventSyncGroupDeleted)
	if len(recs) != 1 || field(recs[0], "identity_sync.members_rederived") != "2" {
		t.Errorf("group_deleted records = %+v", recs)
	}

	// The upsert written before the delete, delivered after it.
	h.mustGroup(older)
	if s := h.subject("u-1"); len(s.Groups) != 0 {
		t.Errorf("an older upsert brought the group back: %v", s.Groups)
	}
	if got := len(h.records(tenant, audit.EventSyncGroupUpserted)); got != 1 {
		t.Errorf("%d group_upserted records, want only the first", got)
	}
	// A delete of a group already deleted changes nothing.
	if err := h.sink.DeleteGroup(t.Context(), ext.Tenant(tenant), "grp-1"); err != nil {
		t.Fatal(err)
	}
	if got := len(h.records(tenant, audit.EventSyncGroupDeleted)); got != 1 {
		t.Errorf("%d group_deleted records after a repeat, want one", got)
	}
	// Deleting an id never seen keeps a tombstone, and an older upsert of it
	// changes nothing either.
	unseen := h.group("grp-9", "DBAs")
	if err := h.sink.DeleteGroup(t.Context(), ext.Tenant(tenant), "grp-9"); err != nil {
		t.Fatal(err)
	}
	h.mustGroup(unseen)
	if g, _ := h.store.DirectoryGroups().Get(t.Context(), tenant, "grp-9"); !g.Deleted() {
		t.Errorf("an upsert older than the tombstone lifted it: %+v", g)
	}
}

// A group created after its members were provisioned reaches them: the group's
// first name is a change of name.
func TestAGroupThatArrivesAfterItsMembersReachesThem(t *testing.T) {
	h := newHarness(t)
	h.mustUpsert(h.person("u-1", "alice", "grp-1"))
	if s := h.subject("u-1"); len(s.Groups) != 0 {
		t.Fatalf("an unnamed group mapped to %v", s.Groups)
	}
	h.mustGroup(h.group("grp-1", "SRE Team"))
	if s := h.subject("u-1"); !slices.Equal(s.Groups, []string{"sre"}) {
		t.Errorf("groups once the group arrived = %v", s.Groups)
	}
}

// A username another subject presents is KindConflict, recorded, and applied
// to nothing. An existing subject is never adopted.
func TestAUsernameAnotherSubjectPresentsIsAConflict(t *testing.T) {
	h := newHarness(t)
	storetest.Seed(t, h.store, storetest.Fixture{Tenant: tenant, Subjects: []store.Subject{storetest.Subject("alice")}})

	err := h.sink.UpsertSubject(t.Context(), h.person("u-1", "alice"))
	if kindOf(err) != ext.KindConflict {
		t.Fatalf("a username another subject presents: %v, want KindConflict", err)
	}
	if strings.Contains(err.Error(), "alice@") || strings.Contains(err.Error(), "subject alice") {
		t.Errorf("the error names the other subject: %v", err)
	}
	if _, err := h.store.DirectoryLinks().Get(t.Context(), tenant, "u-1"); !store.IsNotFound(err) {
		t.Errorf("a refused upsert left a link: %v", err)
	}
	recs := h.records(tenant, audit.EventSyncRefused)
	if len(recs) != 1 || field(recs[0], "identity_sync.refusal") != "conflict" ||
		field(recs[0], "identity_sync.field") != "username" || field(recs[0], "identity_sync.conflicting_subject") != "alice" {
		t.Errorf("refusal records = %+v", recs)
	}

	// Two directory identities cannot share a name either.
	h.mustUpsert(h.person("u-2", "bob"))
	if err := h.sink.UpsertSubject(t.Context(), h.person("u-3", "bob")); kindOf(err) != ext.KindConflict {
		t.Errorf("a second directory identity named bob: %v, want KindConflict", err)
	}
}

// Each malformed call is KindInvalid naming the field, is recorded, and has no
// effect.
func TestMalformedCallsAreInvalidRecordedAndHaveNoEffect(t *testing.T) {
	h := newHarness(t)
	long := strings.Repeat("x", 300)
	ok := h.person("u-ok", "carol")
	cases := []struct {
		name  string
		call  func() error
		field string
	}{
		{"no tenant", func() error { s := ok; s.Tenant = ""; return h.sink.UpsertSubject(t.Context(), s) }, "tenant"},
		{"no external id", func() error { s := ok; s.ExternalID = ""; return h.sink.UpsertSubject(t.Context(), s) }, "external_id"},
		{"a long external id", func() error { s := ok; s.ExternalID = long; return h.sink.UpsertSubject(t.Context(), s) }, "external_id"},
		{"a two-line username", func() error { s := ok; s.Username = "carol\nroot"; return h.sink.UpsertSubject(t.Context(), s) }, "username"},
		{"no updated_at", func() error { s := ok; s.UpdatedAt = time.Time{}; return h.sink.UpsertSubject(t.Context(), s) }, "updated_at"},
		{"a bad group id", func() error { s := ok; s.Groups = []string{" padded"}; return h.sink.UpsertSubject(t.Context(), s) }, "groups"},
		{"a bad attribute", func() error {
			s := ok
			s.Attributes = map[string]string{"department": "a\x00b"}
			return h.sink.UpsertSubject(t.Context(), s)
		}, "attributes"},
		{"a long reason code", func() error {
			return h.sink.DeactivateSubject(t.Context(), ext.Tenant(tenant), "u-ok", strings.Repeat("r", 200))
		}, "reason_code"},
		{"a nameless group", func() error { g := h.group("grp-1", ""); return h.sink.UpsertGroup(t.Context(), g) }, "name"},
		{"a group with no id", func() error { return h.sink.DeleteGroup(t.Context(), ext.Tenant(tenant), "") }, "external_id"},
	}
	for _, c := range cases {
		err := c.call()
		if kindOf(err) != ext.KindInvalid || !strings.Contains(err.Error(), "field "+c.field) {
			t.Errorf("%s: %v, want KindInvalid naming %s", c.name, err, c.field)
		}
	}
	recs := h.records(tenant, audit.EventSyncRefused)
	if len(recs) != len(cases) {
		t.Errorf("%d refusal records for %d malformed calls", len(recs), len(cases))
	}
	var links, groups int
	_ = h.store.Pool().QueryRow(t.Context(), `SELECT count(*) FROM directory_links`).Scan(&links)
	_ = h.store.Pool().QueryRow(t.Context(), `SELECT count(*) FROM directory_groups`).Scan(&groups)
	if links != 0 || groups != 0 {
		t.Errorf("malformed calls left %d links and %d groups", links, groups)
	}
}

// Two tenants: a call in one reaches nothing in the other.
func TestASyncCallInOneTenantReachesNothingInAnother(t *testing.T) {
	h := newHarness(t)
	h.mustGroup(h.group("grp-1", "SRE Team"))
	theirs := h.person("u-1", "alice", "grp-1")
	theirs.Tenant = ext.Tenant(other)
	if err := h.sink.UpsertSubject(t.Context(), theirs); err != nil {
		t.Fatal(err)
	}
	// The same external id in this tenant is a different person.
	h.mustUpsert(h.person("u-1", "alice", "grp-1"))
	mine, err := h.store.DirectoryLinks().Get(t.Context(), tenant, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	yours, err := h.store.DirectoryLinks().Get(t.Context(), other, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if mine.SubjectID == yours.SubjectID {
		t.Error("one external id in two tenants is one subject")
	}
	// The group named in this tenant is not the other's.
	if s, _ := h.store.Subjects().Get(t.Context(), other, yours.SubjectID); len(s.Groups) != 0 {
		t.Errorf("the other tenant's subject took this tenant's group: %v", s.Groups)
	}
	if err := h.sink.DeactivateSubject(t.Context(), ext.Tenant(tenant), "u-1", "terminated"); err != nil {
		t.Fatal(err)
	}
	if l, _ := h.store.DirectoryLinks().Get(t.Context(), other, "u-1"); l.Deactivated() {
		t.Error("a deactivation in one tenant deactivated the other's person")
	}
	if got := len(h.records(other, audit.EventSyncSubjectDeactivated)); got != 0 {
		t.Errorf("%d deactivation records in the other tenant", got)
	}
}

// A login through a connector tied to the directory is covered in
// internal/identity; the sink's half is that the claim mapping's version is
// recorded and that a provisioned subject is never break-glass.
func TestAProvisionedSubjectIsNeverBreakGlassAndRecordsItsMapping(t *testing.T) {
	h := newHarness(t)
	h.mustUpsert(h.person("u-1", "alice"))
	s := h.subject("u-1")
	m, err := identity.ActiveMapping(t.Context(), h.store, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if s.BreakGlass || s.MappingVersion != m.Version {
		t.Errorf("subject = %+v, want mapping version %d and no break-glass", s, m.Version)
	}
}
