// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

const southTenant = store.Tenant("acme")

// The generated fingerprint column is the whole of "is this key one of ours"
// (proxy D11), so what it computes has to be OpenSSH's fingerprint and not
// something merely stable. A column that agreed with itself and with nothing
// else would answer 401 to every chain leg in the fleet.
func TestProxyKeyFingerprintIsTheOpenSSHForm(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	blob := []byte("ssh-ed25519 wire encoding, near enough for a fingerprint")
	sum := sha256.Sum256(blob)
	want := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])

	if err := st.Proxies().Upsert(ctx, southTenant, store.Proxy{
		ID: "proxy-1", Zone: "edge", PublicKey: blob, State: store.EnrollmentEnrolled,
	}); err != nil {
		t.Fatalf("seed proxy: %v", err)
	}

	got, err := st.Proxies().GetByKeyFingerprint(ctx, southTenant, want)
	if err != nil {
		t.Fatalf("GetByKeyFingerprint(%s): %v", want, err)
	}
	if got.ID != "proxy-1" {
		t.Errorf("fingerprint %s resolved to %q, want proxy-1", want, got.ID)
	}
}

// A proxy with no key material must not share a fingerprint with every other
// keyless proxy: an offered key that hashed to the empty string's digest would
// otherwise authenticate a chain leg for all of them at once.
func TestAKeylessProxyHasNoFingerprint(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	if err := st.Proxies().Upsert(ctx, southTenant, store.Proxy{
		ID: "keyless", Zone: "edge", PublicKey: nil, State: store.EnrollmentEnrolled,
	}); err != nil {
		t.Fatalf("seed proxy: %v", err)
	}

	empty := sha256.Sum256(nil)
	fingerprintOfNothing := "SHA256:" + base64.RawStdEncoding.EncodeToString(empty[:])

	_, err := st.Proxies().GetByKeyFingerprint(ctx, southTenant, fingerprintOfNothing)
	if !store.IsNotFound(err) {
		t.Fatalf("GetByKeyFingerprint(fingerprint of no bytes) = %v, want not found", err)
	}
}

// `known` decides the wire answer, and it comes from the write rather than
// from a preceding read so two proxies reporting one new key agree.
func TestHostKeyRecordReportsWhetherTheKeyWasAlreadyHeld(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	key := store.TargetHostKey{
		Hostname: "db-1.example.com", Port: 22, Fingerprint: "SHA256:aaa",
		KeyType: "ssh-ed25519", Decision: store.HostKeyAccepted,
		LastSeenAt: time.Now().UTC(), FirstReportedBy: "proxy-1", LastReportedBy: "proxy-1",
	}

	first, known, err := st.TargetHostKeys().Record(ctx, southTenant, key)
	if err != nil {
		t.Fatalf("first Record: %v", err)
	}
	if known {
		t.Error("a key the server has never seen came back known: true")
	}

	key.LastReportedBy = "proxy-2"
	key.LastSeenAt = key.LastSeenAt.Add(time.Minute)
	second, known, err := st.TargetHostKeys().Record(ctx, southTenant, key)
	if err != nil {
		t.Fatalf("second Record: %v", err)
	}
	if !known {
		t.Error("a key recorded a moment ago came back known: false")
	}
	if !second.FirstSeenAt.Equal(first.FirstSeenAt) {
		t.Errorf("first_seen_at moved from %s to %s; a later sighting is not a first one",
			first.FirstSeenAt, second.FirstSeenAt)
	}
	if second.FirstReportedBy != "proxy-1" || second.LastReportedBy != "proxy-2" {
		t.Errorf("reporters = %q / %q, want proxy-1 / proxy-2",
			second.FirstReportedBy, second.LastReportedBy)
	}
}

// A changed key is a question about the SET a target has presented, which is
// what ListForTarget answers.
func TestHostKeysForOneTargetAccumulate(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	for _, fp := range []string{"SHA256:old", "SHA256:new"} {
		if _, _, err := st.TargetHostKeys().Record(ctx, southTenant, store.TargetHostKey{
			Hostname: "web-1.example.com", Port: 22, Fingerprint: fp,
			Decision: store.HostKeyAccepted, LastSeenAt: now, LastReportedBy: "proxy-1",
		}); err != nil {
			t.Fatalf("record %s: %v", fp, err)
		}
		now = now.Add(time.Second)
	}

	got, err := st.TargetHostKeys().ListForTarget(ctx, southTenant, "web-1.example.com", 22)
	if err != nil {
		t.Fatalf("ListForTarget: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListForTarget returned %d keys, want 2 — a rotated key must not replace the record of the old one", len(got))
	}
}

// Single use is a database predicate rather than a Go check, because two polls
// can arrive together and only one of them may resolve the challenge.
func TestAChallengeResolvesExactlyOnce(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	seedSubjectFor(t, st, "alice@example.com")
	if err := st.MFA().CreateChallenge(ctx, southTenant, store.MFAChallenge{
		Token: "tok-1", SubjectID: "alice@example.com", Login: "alice",
		Provider: "scripted", PollAfterMS: 100, State: store.MFAChallengePending,
		IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}

	if err := st.MFA().ResolveChallenge(ctx, southTenant, "tok-1", store.MFAChallengeApproved, now); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	err := st.MFA().ResolveChallenge(ctx, southTenant, "tok-1", store.MFAChallengeDenied, now)
	if !store.IsConflict(err) {
		t.Fatalf("second resolve = %v, want a conflict: an approved challenge replayed is one approval becoming many", err)
	}

	got, err := st.MFA().GetChallenge(ctx, southTenant, "tok-1")
	if err != nil {
		t.Fatalf("GetChallenge: %v", err)
	}
	if got.State != store.MFAChallengeApproved {
		t.Errorf("state = %q, want it to keep the first resolution", got.State)
	}
}

// Polling counts, which is what the poll budget and the deterministic provider
// both read.
func TestPollingAChallengeCountsAndStamps(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	seedSubjectFor(t, st, "alice@example.com")
	if err := st.MFA().CreateChallenge(ctx, southTenant, store.MFAChallenge{
		Token: "tok-2", SubjectID: "alice@example.com", Login: "alice",
		Provider: "scripted", PollAfterMS: 10, State: store.MFAChallengePending,
		IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}

	for want := 1; want <= 3; want++ {
		got, err := st.MFA().PollChallenge(ctx, southTenant, "tok-2", now.Add(time.Duration(want)*time.Second))
		if err != nil {
			t.Fatalf("poll %d: %v", want, err)
		}
		if got.Polls != want {
			t.Fatalf("poll %d reported polls = %d", want, got.Polls)
		}
	}

	_, err := st.MFA().PollChallenge(ctx, southTenant, "never-issued", now)
	if !store.IsNotFound(err) {
		t.Fatalf("polling an unissued token = %v, want not found", err)
	}
}

// The lease table is an audit record and nothing allocates from it, so what it
// has to do is survive and be findable by lease id.
func TestUIDLeasesAreRecordedAndFindable(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Now().UTC()
	for i, l := range []store.UIDLease{
		{LeaseID: "lease-a", TargetID: "host:22", ProxyID: "proxy-1", From: 2000000, To: 2004096, GrantedAt: now},
		{LeaseID: "lease-b", TargetID: "host:22", ProxyID: "proxy-2", From: 2004096, To: 2008192, GrantedAt: now},
	} {
		if err := st.UIDLeases().Record(ctx, southTenant, l); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	got, err := st.UIDLeases().Get(ctx, southTenant, "lease-b")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ProxyID != "proxy-2" {
		t.Errorf("lease-b proxy = %q, want proxy-2: an incident resolves a uid back to a proxy through this row", got.ProxyID)
	}

	all, err := st.UIDLeases().ListForTarget(ctx, southTenant, "host:22")
	if err != nil {
		t.Fatalf("ListForTarget: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListForTarget returned %d, want 2", len(all))
	}

	// An empty block is refused by the schema, because the answer at the top
	// of a range is 409 and never a 200 carrying one.
	err = st.UIDLeases().Record(ctx, southTenant, store.UIDLease{
		LeaseID: "lease-empty", TargetID: "host:22", ProxyID: "proxy-1",
		From: 3000, To: 3000, GrantedAt: now,
	})
	if err == nil {
		t.Fatal("recording an empty block succeeded")
	}
}

// The token lookup is the middleware's, so absence, expiry and revocation each
// have to be distinguishable from a database failure.
func TestProxyTokenLookup(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	hash := sha256.Sum256([]byte("a-secret"))
	now := time.Now().UTC()
	if err := st.ProxyTokens().Insert(ctx, southTenant, store.ProxyAPIToken{
		TokenID: "tok-1", ProxyID: "proxy-1", TokenHash: hash[:], Label: "test", IssuedAt: now,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := st.ProxyTokens().GetByHash(ctx, southTenant, hash[:])
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if got.ProxyID != "proxy-1" || !got.Usable(now) {
		t.Errorf("token = %+v, want a usable token for proxy-1", got)
	}

	other := sha256.Sum256([]byte("not-the-secret"))
	if _, err := st.ProxyTokens().GetByHash(ctx, southTenant, other[:]); !store.IsNotFound(err) {
		t.Errorf("GetByHash(unknown) = %v, want not found", err)
	}

	if err := st.ProxyTokens().Revoke(ctx, southTenant, "tok-1", now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, err = st.ProxyTokens().GetByHash(ctx, southTenant, hash[:])
	if err != nil {
		t.Fatalf("GetByHash after revoke: %v", err)
	}
	if got.Usable(now.Add(time.Second)) {
		t.Error("a revoked token is still usable")
	}
}

// A revoked key and an expired certificate are stored as what they are, so the
// auth path can tell them apart from an unknown key.
func TestSubjectKeyWindowsAndRevocationRoundTrip(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	seedSubjectFor(t, st, "alice@example.com")
	from := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	to := from.Add(2 * time.Hour)

	if err := st.SubjectKeys().Put(ctx, southTenant, store.SubjectKey{
		Fingerprint: "SHA256:cert", SubjectID: "alice@example.com", KeyType: "ssh-ed25519",
		IsCertificate: true, ValidFrom: from, ValidTo: to,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := st.SubjectKeys().GetByFingerprint(ctx, southTenant, "SHA256:cert")
	if err != nil {
		t.Fatalf("GetByFingerprint: %v", err)
	}
	if !got.ValidFrom.Equal(from) || !got.ValidTo.Equal(to) || !got.IsCertificate {
		t.Errorf("key = %+v, want the stored window back", got)
	}
	if !got.RevokedAt.IsZero() {
		t.Errorf("a key nobody revoked came back revoked at %s", got.RevokedAt)
	}

	at := time.Now().UTC().Truncate(time.Second)
	if err := st.SubjectKeys().Revoke(ctx, southTenant, "SHA256:cert", at); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// A second revocation keeps the first instant: the question an auditor
	// asks is when the key stopped working.
	if err := st.SubjectKeys().Revoke(ctx, southTenant, "SHA256:cert", at.Add(time.Hour)); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	got, err = st.SubjectKeys().GetByFingerprint(ctx, southTenant, "SHA256:cert")
	if err != nil {
		t.Fatalf("GetByFingerprint after revoke: %v", err)
	}
	if !got.RevokedAt.Equal(at) {
		t.Errorf("revoked_at = %s, want the first revocation at %s", got.RevokedAt, at)
	}
}

func seedSubjectFor(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.Subjects().Upsert(t.Context(), southTenant, store.Subject{
		ID: id, Source: "local", Principals: []string{"alice"},
	}); err != nil {
		t.Fatalf("seed subject %s: %v", id, err)
	}
}
