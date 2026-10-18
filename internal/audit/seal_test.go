// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Signed retention (0024). Every test here attacks with what a writer of the
// database alone has — SQL — and asserts the verifier says so, or does what a
// lawful pass does and asserts it verifies.

// sealed is a store whose tenant A has a stream of n records, and the signer
// and verifier a deployment with a key-encryption key runs.
type sealed struct {
	st       *store.Store
	in       *audit.Ingester
	emitter  *audit.Emitter
	keys     *audit.RetentionKeys
	verifier *audit.Verifier
	stream   string
}

func newSealed(t *testing.T, n int) sealed {
	t.Helper()
	st := storetest.New(t)
	in := newIngester(t, st)
	keys := retentionKeys(t, st)
	emitter, err := audit.NewEmitter(in, audit.WithRetentionSigner(keys))
	if err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		mustIngest(t.Context(), t, in, tenantA, proxyA, records(n, "sess-seal")...)
	}
	return sealed{
		st: st, in: in, emitter: emitter, keys: keys,
		verifier: audit.NewVerifier(st, audit.WithRetentionKeys(keys), audit.WithCaptureVerification(true)),
		stream:   audit.StreamFor(proxyA),
	}
}

func (s sealed) verify(t *testing.T, tenant store.Tenant) audit.StreamResult {
	t.Helper()
	result, err := s.verifier.VerifyStream(t.Context(), tenant, s.stream)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return result
}

func (s sealed) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.st.Pool().Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (s sealed) hashAt(t *testing.T, tenant store.Tenant, seq int64) string {
	t.Helper()
	var hash string
	if err := s.st.Pool().QueryRow(t.Context(),
		`SELECT hash FROM audit_records WHERE tenant = $1 AND stream = $2 AND chain_seq = $3`,
		tenant, s.stream, seq).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

func wantSealBreak(t *testing.T, result audit.StreamResult, stream, detail string) {
	t.Helper()
	if result.Break == nil {
		t.Fatalf("stream %s verified: %+v; want a %s break", stream, result, audit.BreakRetentionMarker)
	}
	if result.Break.Kind != audit.BreakRetentionMarker || result.Break.Stream != stream {
		t.Fatalf("break = %s; want a %s break naming %s", result.Break, audit.BreakRetentionMarker, stream)
	}
	if !strings.Contains(result.Break.Error(), detail) {
		t.Errorf("break = %s; want it to say %q", result.Break, detail)
	}
}

// THE ATTACK. A writer of the database deletes a stream's oldest records and
// writes the marker a lawful pass would have written — the right sequence and
// the right hash, both readable beforehand — and before 0024 the chain
// verified. Now it is a break, of its own kind, naming the stream.
func TestADeletedPrefixWithAForgedMarkerIsABreak(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 8)
	hash := s.hashAt(t, tenantA, 5)
	s.exec(t, `DELETE FROM audit_records WHERE tenant = $1 AND stream = $2 AND chain_seq <= 5`, tenantA, s.stream)
	s.exec(t, `INSERT INTO audit_retention (tenant, stream, through_seq, through_hash) VALUES ($1, $2, 5, $3)`,
		tenantA, s.stream, hash)
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "carries no signature")

	// A signature made with a key of the writer's own, under a name the key
	// store does not hold, or under the name of a key of another kind.
	_, attacker, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged := base64.StdEncoding.EncodeToString(ed25519.Sign(attacker, audit.RetentionMessage(tenantA, s.stream, 5, hash)))
	for name, detail := range map[string]string{
		"audit-retention:20990101T000000Z-00000000": "does not hold",
		"ssh-ca:20260101-abc":                       "not a retention key",
	} {
		s.exec(t, `UPDATE audit_retention SET key_name = $3, signature = $4 WHERE tenant = $1 AND stream = $2`,
			tenantA, s.stream, name, forged)
		wantSealBreak(t, s.verify(t, tenantA), s.stream, detail)
	}
}

// The same forgery after the writer replaces the public half the database holds
// with its own: still a break, because the seal is checked against the key
// store's signer — the private half, sealed under a key the database never sees
// — and never against a stored public key.
func TestReplacingTheStoredPublicHalfForgesNothing(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 8)
	ctx := t.Context()
	ref, err := s.keys.Current(ctx, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	attackerPub, attacker, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkix, err := x509.MarshalPKIXPublicKey(attackerPub)
	if err != nil {
		t.Fatal(err)
	}
	s.exec(t, `UPDATE software_keys SET public_key = $3 WHERE tenant = $1 AND name = $2`, tenantA, ref.Name, pkix)

	hash := s.hashAt(t, tenantA, 5)
	s.exec(t, `DELETE FROM audit_records WHERE tenant = $1 AND stream = $2 AND chain_seq <= 5`, tenantA, s.stream)
	s.exec(t, `INSERT INTO audit_retention (tenant, stream, through_seq, through_hash, key_name, signature)
		VALUES ($1, $2, 5, $3, $4, $5)`, tenantA, s.stream, hash, ref.Name,
		base64.StdEncoding.EncodeToString(ed25519.Sign(attacker, audit.RetentionMessage(tenantA, s.stream, 5, hash))))
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "does not check under key "+ref.Name)
}

// A lawful pass verifies, says where it resumes, names its key, and records
// the key and the signature on the control chain beside what it deleted.
func TestALawfulPassIsSealedAndVerifies(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 8)
	ctx := t.Context()
	out, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 5}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if !audit.IsRetentionKey(out.KeyName) || out.Signature == "" {
		t.Fatalf("pass = %+v; want it sealed by a retention key", out)
	}
	if result := s.verify(t, tenantA); result.Break != nil || result.RetainedThrough != 5 || result.Records != 3 {
		t.Fatalf("after a lawful pass: %+v", result)
	}
	recs, err := s.st.Audit().Query(ctx, tenantA, store.AuditQuery{Event: audit.EventAuditRetained})
	if err != nil || len(recs) != 1 {
		t.Fatalf("records of the pass: %d, %v", len(recs), err)
	}
	for key, want := range map[string]string{
		audit.AttrOperatorPrefix + "key":       out.KeyName,
		audit.AttrOperatorPrefix + "signature": out.Signature,
	} {
		if !strings.Contains(recs[0].Body, `"`+key+`":"`+want+`"`) {
			t.Errorf("the record of the pass does not carry %s = %s: %s", key, want, recs[0].Body)
		}
	}
	// The whole tenant verifies, the pass's own record on the control stream
	// included.
	if result, err := s.verifier.VerifyTenant(ctx, tenantA); err != nil || !result.OK() {
		t.Fatalf("the tenant after a lawful pass: %v %+v", err, result.Streams)
	}
}

// Every field of the statement is inside the signed bytes, and each change to
// one changes them: a seal over one marker never checks as another's.
func TestEveryFieldOfAMarkerIsSigned(t *testing.T) {
	t.Parallel()
	base := audit.RetentionMessage("acme", "proxy:p1", 5, "sha256:aa")
	for name, other := range map[string][]byte{
		"tenant":       audit.RetentionMessage("globex", "proxy:p1", 5, "sha256:aa"),
		"stream":       audit.RetentionMessage("acme", "proxy:p2", 5, "sha256:aa"),
		"through_seq":  audit.RetentionMessage("acme", "proxy:p1", 6, "sha256:aa"),
		"through_hash": audit.RetentionMessage("acme", "proxy:p1", 5, "sha256:ab"),
		// Length prefixes: moving a byte between fields is a change too.
		"boundary": audit.RetentionMessage("acm", "eproxy:p1", 5, "sha256:aa"),
	} {
		if bytes.Equal(base, other) {
			t.Errorf("changing the %s leaves the signed bytes unchanged", name)
		}
	}
	// The encoding is pinned: a change to it is a new version, never an edit.
	if want := "19:hoplock.retention/14:acme8:proxy:p11:59:sha256:aa"; string(base) != want {
		t.Errorf("signed bytes = %q, want %q", base, want)
	}
	if want := "27:hoplock.capture-retention/14:acme27:2026-10-18T12:00:00.000001Z"; string(audit.CaptureRetentionMessage("acme",
		time.Date(2026, 10, 18, 12, 0, 0, 1999, time.UTC))) != want {
		t.Errorf("capture signed bytes = %q, want %q", audit.CaptureRetentionMessage("acme",
			time.Date(2026, 10, 18, 12, 0, 0, 1999, time.UTC)), want)
	}
}

// A marker signed before a rotation still verifies after it, because each
// marker names the generation that signed it. Destroying that generation makes
// its markers unverifiable, loudly: a break naming the key, never a pass.
func TestAMarkerSignedBeforeARotationStillVerifies(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 8)
	ctx := t.Context()
	other := audit.StreamFor("proxy-2")
	mustIngest(ctx, t, s.in, tenantA, "proxy-2", records(4, "sess-other")...)

	first, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 3}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := s.emitter.RotateRetentionKey(ctx, tenantA, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Ref.Name == first.KeyName {
		t.Fatalf("rotation kept the key name %s", first.KeyName)
	}
	second, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: other, ThroughSeq: 2}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	if second.KeyName != rotated.Ref.Name {
		t.Errorf("a pass after the rotation was signed by %s, want the new key %s", second.KeyName, rotated.Ref.Name)
	}
	result, err := s.verifier.VerifyTenant(ctx, tenantA)
	if err != nil || !result.OK() {
		t.Fatalf("after a rotation: %v %+v", err, result.Streams)
	}
	// A pass moving the older marker is signed by the new key, and the older
	// marker, still the old key's, checked first.
	if again, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 5}, retentionActor); err != nil || again.KeyName != rotated.Ref.Name {
		t.Fatalf("moving a marker signed before the rotation: %+v, %v", again, err)
	}

	// A rotation on a server whose clock is behind the newest key's still
	// makes a key that sorts after it, so the rotation takes.
	ahead := "audit-retention:20991231T000000.000000000Z-00000000"
	ks := softwareKeyStore(t, s.st)
	if _, err := ks.Generate(ctx, ext.KeyRef{Tenant: ext.Tenant(tenantB), Name: ahead}, ext.KeyAlgorithmEd25519); err != nil {
		t.Fatal(err)
	}
	if behind, err := s.keys.Rotate(ctx, tenantB); err != nil || behind.Ref.Name <= ahead {
		t.Errorf("a rotation behind the newest key made %s, %v; want a name after %s", behind.Ref.Name, err, ahead)
	}

	// Destroy the new key: the markers it signed break, by name.
	if err := ks.Destroy(ctx, ext.KeyRef{Tenant: ext.Tenant(tenantA), Name: rotated.Ref.Name}); err != nil {
		t.Fatal(err)
	}
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "does not hold")
}

// Without a key store — no key-encryption key and no ext.KeyStore — both
// retention passes are refused before anything is deleted, and the marker does
// not move.
func TestRetentionWithoutAKeyStoreIsRefusedAndDeletesNothing(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 6)
	ctx := t.Context()
	unsigned, err := audit.NewEmitter(s.in)
	if err != nil {
		t.Fatal(err)
	}
	rec := record("rec-keyless", "sess-keyless")
	rec.Kind = "stream"
	rec.Timestamp = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	rec.Payload = base64.StdEncoding.EncodeToString([]byte("id\r\n"))
	mustIngest(ctx, t, s.in, tenantA, proxyA, rec)

	if _, err := unsigned.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 3}, retentionActor); !errors.Is(err, audit.ErrRetentionKeyNotConfigured) {
		t.Errorf("a prefix pass with no key store answered %v", err)
	}
	if _, err := unsigned.RetainCaptures(ctx, tenantA, time.Now(), retentionActor); !errors.Is(err, audit.ErrRetentionKeyNotConfigured) {
		t.Errorf("a capture pass with no key store answered %v", err)
	}
	if chain, _ := s.st.Audit().Chain(ctx, tenantA, s.stream, 0, 100); len(chain) != 7 {
		t.Errorf("%d of 7 records left after refused passes", len(chain))
	}
	if _, err := s.st.Audit().Retention(ctx, tenantA, s.stream); !store.IsNotFound(err) {
		t.Errorf("the marker moved: %v", err)
	}
	if b, err := s.st.Audit().Capture(ctx, tenantA, rec.RecordID); err != nil || len(b) == 0 {
		t.Errorf("the capture after refused passes: %d bytes, %v", len(b), err)
	}
	// And a verifier with no key store trusts no marker it meets.
	if _, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 3}, retentionActor); err != nil {
		t.Fatal(err)
	}
	result, err := audit.NewVerifier(s.st).VerifyStream(ctx, tenantA, s.stream)
	if err != nil {
		t.Fatal(err)
	}
	wantSealBreak(t, result, s.stream, "no key store is configured")
}

// Tenant A's key neither signs nor verifies tenant B's markers (M18). A pass
// in A signs with a key of A's; B's marker copied into A breaks; and B's key
// material copied into A's key store — the software custodian's ciphertext
// carries no binding to its row — checks nothing of A's, because the tenant is
// inside the signed bytes.
func TestATenantsKeyNeitherSignsNorVerifiesAnothers(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 8)
	ctx := t.Context()
	mustIngest(ctx, t, s.in, tenantB, proxyA, records(8, "sess-seal")...)

	a, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 5}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.emitter.RetainPrefix(ctx, tenantB, audit.PrefixPass{Stream: s.stream, ThroughSeq: 5}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	ks := softwareKeyStore(t, s.st)
	if _, err := ks.Describe(ctx, ext.KeyRef{Tenant: ext.Tenant(tenantB), Name: a.KeyName}); !ext.IsNotFound(err) {
		t.Errorf("tenant B holds tenant A's key %s: %v", a.KeyName, err)
	}

	// B's marker, whole, in A's row: A's key store holds no key by B's name.
	s.exec(t, `UPDATE audit_retention SET through_hash = $3, key_name = $4, signature = $5 WHERE tenant = $1 AND stream = $2`,
		tenantA, s.stream, b.ThroughHash, b.KeyName, b.Signature)
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "does not hold")

	// And with B's key material copied into A's key store under that name.
	s.exec(t, `INSERT INTO software_keys (tenant, name, algorithm, public_key, private_key)
		SELECT $1, name, algorithm, public_key, private_key FROM software_keys WHERE tenant = $2 AND name = $3`,
		tenantA, tenantB, b.KeyName)
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "does not check under key "+b.KeyName)

	// B still verifies on its own.
	if result := s.verify(t, tenantB); result.Break != nil {
		t.Errorf("tenant B after A was tampered with: %s", result.Break)
	}
}

// A capture emptied and marked retained by a writer of the database is a break:
// with no signed capture pass at all, and after one, when its record — by the
// timestamp inside the hashed body, never the column a writer could move — was
// made after the pass's watermark. A watermark row the writer forges breaks too.
func TestAForgedCaptureRetentionIsABreak(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 0)
	ctx := t.Context()
	old, recent := captureRecord("rec-old", -72*time.Hour), captureRecord("rec-recent", -2*time.Hour)
	mustIngest(ctx, t, s.in, tenantA, proxyA, old, recent)
	forgeRetained := func(id string) {
		s.exec(t, `UPDATE audit_captures SET bytes = ''::bytea, retained_at = now() WHERE tenant = $1 AND record_id = $2`, tenantA, id)
	}

	forgeRetained(old.RecordID)
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "no signed capture pass has reached this tenant")

	// A lawful pass reaching back 48 hours: the old capture is now lawfully
	// gone, and the recent one, forged, is not — even with its derived
	// recorded_at moved back past the watermark.
	s.exec(t, `UPDATE audit_captures SET retained_at = NULL WHERE tenant = $1 AND record_id = $2`, tenantA, old.RecordID)
	if _, err := s.emitter.RetainCaptures(ctx, tenantA, time.Now().Add(-48*time.Hour), retentionActor); err != nil {
		t.Fatal(err)
	}
	if result := s.verify(t, tenantA); result.Break != nil || result.CapturesRetained != 1 {
		t.Fatalf("after a lawful capture pass: %+v", result)
	}
	forgeRetained(recent.RecordID)
	s.exec(t, `UPDATE audit_records SET recorded_at = recorded_at - interval '30 days' WHERE tenant = $1 AND record_id = $2`, tenantA, recent.RecordID)
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "which no signed capture pass reached")

	// A watermark the writer moves forward without a signature to match.
	s.exec(t, `UPDATE audit_capture_retention SET retained_before = now() WHERE tenant = $1`, tenantA)
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "the capture retention watermark does not verify")
}

// A pass refuses to sign over a start nobody checked: a marker with no seal
// that holds, a prefix deleted with no marker, captures marked retained outside
// any signed pass. A new seal there would launder what made it so. Nothing is
// deleted.
func TestAPassRefusesToSignOverAStartThatDoesNotVerify(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("an unsigned marker", func(t *testing.T) {
		t.Parallel()
		s := newSealed(t, 8)
		if _, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 2}, retentionActor); err != nil {
			t.Fatal(err)
		}
		s.exec(t, `UPDATE audit_retention SET signature = '' WHERE tenant = $1 AND stream = $2`, tenantA, s.stream)
		_, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 5}, retentionActor)
		if !audit.IsRetentionUnverified(err) {
			t.Fatalf("a pass over an unsigned marker answered %v", err)
		}
		if chain, _ := s.st.Audit().Chain(ctx, tenantA, s.stream, 0, 100); len(chain) != 6 {
			t.Errorf("%d of 6 records left after a refused pass", len(chain))
		}
	})
	t.Run("a prefix deleted with no marker", func(t *testing.T) {
		t.Parallel()
		s := newSealed(t, 8)
		s.exec(t, `DELETE FROM audit_records WHERE tenant = $1 AND stream = $2 AND chain_seq <= 2`, tenantA, s.stream)
		if _, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 5}, retentionActor); !audit.IsRetentionUnverified(err) {
			t.Fatalf("a pass over a prefix deleted with no marker answered %v", err)
		}
	})
	t.Run("records deleted past a signed marker", func(t *testing.T) {
		t.Parallel()
		s := newSealed(t, 8)
		if _, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 2}, retentionActor); err != nil {
			t.Fatal(err)
		}
		s.exec(t, `DELETE FROM audit_records WHERE tenant = $1 AND stream = $2 AND chain_seq = 3`, tenantA, s.stream)
		if _, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 5}, retentionActor); !audit.IsRetentionUnverified(err) {
			t.Fatalf("a pass over records deleted past the marker answered %v", err)
		}
	})
	t.Run("captures retained outside any signed pass", func(t *testing.T) {
		t.Parallel()
		s := newSealed(t, 0)
		rec := captureRecord("rec-forged", -72*time.Hour)
		mustIngest(ctx, t, s.in, tenantA, proxyA, rec, captureRecord("rec-kept", -60*time.Hour))
		s.exec(t, `UPDATE audit_captures SET bytes = ''::bytea, retained_at = now() WHERE tenant = $1 AND record_id = $2`, tenantA, rec.RecordID)
		if _, err := s.emitter.RetainCaptures(ctx, tenantA, time.Now().Add(-24*time.Hour), retentionActor); !audit.IsRetentionUnverified(err) {
			t.Fatalf("a capture pass over a forged retention answered %v", err)
		}
		if b, err := s.st.Audit().Capture(ctx, tenantA, "rec-kept"); err != nil || len(b) == 0 {
			t.Errorf("a refused capture pass emptied a capture: %d bytes, %v", len(b), err)
		}
	})
}

// A marker written before 0024 is a break until an operator adopts it, and
// adoption signs only the marker the operator names, only over a stream that
// verifies from it, and is recorded.
func TestAnUnsignedMarkerIsAdoptedOnlyAsVerified(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 8)
	ctx := t.Context()
	pass, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 4}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	// What v0.6.0 to v0.10.0 wrote.
	s.exec(t, `UPDATE audit_retention SET key_name = '', signature = '' WHERE tenant = $1 AND stream = $2`, tenantA, s.stream)
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "written before v0.11.0")

	if _, err := s.emitter.AdoptRetention(ctx, tenantA, s.stream, 3, pass.ThroughHash, retentionActor); err == nil {
		t.Error("adopting a marker by the wrong sequence succeeded")
	}
	if _, err := s.emitter.AdoptRetention(ctx, tenantA, s.stream, 4, "sha256:00", retentionActor); err == nil {
		t.Error("adopting a marker by the wrong hash succeeded")
	}
	// Over a broken stream: a record after the marker removed.
	s.exec(t, `DELETE FROM audit_records WHERE tenant = $1 AND stream = $2 AND chain_seq = 6`, tenantA, s.stream)
	if _, err := s.emitter.AdoptRetention(ctx, tenantA, s.stream, 4, pass.ThroughHash, retentionActor); err == nil {
		t.Fatal("adopting a marker over a broken stream succeeded")
	}
	mark, err := s.st.Audit().Retention(ctx, tenantA, s.stream)
	if err != nil || mark.Signature != "" {
		t.Fatalf("a refused adoption sealed the marker: %+v, %v", mark, err)
	}

	clean := newSealed(t, 8)
	pass, err = clean.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: clean.stream, ThroughSeq: 4}, retentionActor)
	if err != nil {
		t.Fatal(err)
	}
	clean.exec(t, `UPDATE audit_retention SET key_name = '', signature = '' WHERE tenant = $1 AND stream = $2`, tenantA, clean.stream)
	adopted, err := clean.emitter.AdoptRetention(ctx, tenantA, clean.stream, 4, pass.ThroughHash, retentionActor)
	if err != nil {
		t.Fatalf("adopting a verified marker: %v", err)
	}
	if !audit.IsRetentionKey(adopted.KeyName) || adopted.Signature == "" {
		t.Errorf("adopted marker = %+v", adopted)
	}
	if result := clean.verify(t, tenantA); result.Break != nil || result.RetainedThrough != 4 {
		t.Errorf("after adoption: %+v", result)
	}
	if recs, _ := clean.st.Audit().Query(ctx, tenantA, store.AuditQuery{Event: audit.EventAuditRetentionAdopted}); len(recs) != 1 {
		t.Errorf("%d records of the adoption, want 1", len(recs))
	}
	if _, err := clean.emitter.AdoptRetention(ctx, tenantA, clean.stream, 4, pass.ThroughHash, retentionActor); err == nil {
		t.Error("adopting a marker that is already signed succeeded")
	}
}

// Captures emptied before 0024 are adopted under an instant that covers every
// one of them and nothing ahead of the clock.
func TestCapturesRetainedBeforeSealingAreAdoptedUnderAnInstantThatCoversThem(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 0)
	ctx := t.Context()
	rec := captureRecord("rec-legacy", -72*time.Hour)
	mustIngest(ctx, t, s.in, tenantA, proxyA, rec)
	s.exec(t, `UPDATE audit_captures SET bytes = ''::bytea, retained_at = now() WHERE tenant = $1 AND record_id = $2`, tenantA, rec.RecordID)
	wantSealBreak(t, s.verify(t, tenantA), s.stream, "no signed capture pass")

	if _, err := s.emitter.AdoptCaptureRetention(ctx, tenantA, time.Now().Add(-96*time.Hour), retentionActor); err == nil {
		t.Error("an instant before a retained capture was adopted")
	}
	if _, err := s.emitter.AdoptCaptureRetention(ctx, tenantA, time.Now().Add(time.Hour), retentionActor); err == nil {
		t.Error("an instant ahead of the clock was adopted")
	}
	if _, err := s.emitter.AdoptCaptureRetention(ctx, tenantA, time.Now().Add(-48*time.Hour), retentionActor); err != nil {
		t.Fatalf("adopting: %v", err)
	}
	if result := s.verify(t, tenantA); result.Break != nil || result.CapturesRetained != 1 {
		t.Errorf("after adoption: %+v", result)
	}
	if _, err := s.emitter.AdoptCaptureRetention(ctx, tenantA, time.Now().Add(-1*time.Hour), retentionActor); err == nil {
		t.Error("a second adoption over a watermark succeeded")
	}
}

// A key store that answers with a signer it cannot use is an outage, never a
// verdict: the verifier returns the error rather than a pass or a break.
func TestAKeyStoreThatCannotBeAskedIsAnOutage(t *testing.T) {
	t.Parallel()
	s := newSealed(t, 8)
	ctx := t.Context()
	if _, err := s.emitter.RetainPrefix(ctx, tenantA, audit.PrefixPass{Stream: s.stream, ThroughSeq: 3}, retentionActor); err != nil {
		t.Fatal(err)
	}
	broken := audit.NewVerifier(s.st, audit.WithRetentionKeys(audit.NewRetentionKeys(failingKeyStore{})))
	if _, err := broken.VerifyStream(ctx, tenantA, s.stream); err == nil {
		t.Error("a key store that failed produced a verdict")
	}
}

type failingKeyStore struct{ ext.KeyStore }

func (failingKeyStore) Signer(context.Context, ext.KeyRef) (crypto.Signer, error) {
	return nil, ext.Errorf(ext.PointKeyStore, "test", "Signer", ext.KindUnavailable, "the device is unreachable")
}

func captureRecord(id string, age time.Duration) contract.LogRecord {
	rec := record(id, "sess-capture")
	rec.Kind = "stream"
	rec.Timestamp = time.Now().Add(age).UTC().Format(time.RFC3339)
	rec.Payload = base64.StdEncoding.EncodeToString([]byte("whoami\r\n" + id))
	return rec
}
