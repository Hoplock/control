// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// testKEK is a fixed key-encryption key. It is a TEST key and it is in a test
// file; nothing in this repository ships one.
var testKEK = bytes.Repeat([]byte{0x2b}, extdefault.KeyEncryptionKeySize)

type caFixture struct {
	st    *store.Store
	ca    *credential.CA
	now   time.Time
	clock func() time.Time
}

func newCA(t *testing.T, opts ...credential.Option) *caFixture {
	t.Helper()
	st := storetest.New(t)
	keys, err := extdefault.NewSoftwareKeyStore(st, testKEK)
	if err != nil {
		t.Fatalf("key store: %v", err)
	}

	f := &caFixture{st: st, now: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)}
	f.clock = func() time.Time { return f.now }

	ca, err := credential.New(st, keys, append([]credential.Option{
		credential.WithClock(f.clock),
	}, opts...)...)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	f.ca = ca
	return f
}

// proxyKey stands in for the key pair the PROXY generates per session. This
// server never sees the private half in production, and the test holds it only
// to prove the certificate is usable.
func proxyKey(t *testing.T) ([]byte, ssh.Signer) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("proxy key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("proxy signer: %v", err)
	}
	wire, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("proxy public key: %v", err)
	}
	return wire.Marshal(), signer
}

func TestACertificateIsScopedToPrincipalTargetAndWindow(t *testing.T) {
	f := newCA(t)
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	public, _ := proxyKey(t)

	issued, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
		SubjectID:     "alice",
		Principals:    []string{"deploy"},
		Target:        "db-1.prod",
		TargetPort:    22,
		SessionID:     "sess-123",
		SourceAddress: "10.0.0.7",
		PublicKey:     public,
		ValidFor:      5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	cert := parseCertificate(t, issued.Certificate)
	if !slices.Equal(cert.ValidPrincipals, []string{"deploy"}) {
		t.Errorf("principals: %v", cert.ValidPrincipals)
	}
	if cert.CertType != ssh.UserCert {
		t.Errorf("cert type: %d", cert.CertType)
	}
	if got := time.Unix(int64(cert.ValidBefore), 0).UTC(); !got.Equal(f.now.Add(5 * time.Minute)) {
		t.Errorf("valid before: %s", got)
	}
	// The window starts a minute back to absorb clock skew, and the backdating
	// is deliberately small: it is also a minute of extra life.
	if got := time.Unix(int64(cert.ValidAfter), 0).UTC(); !got.Equal(f.now.Add(-time.Minute)) {
		t.Errorf("valid after: %s", got)
	}
	// The target's OWN auth log has to name the person and the session, because
	// SSH has no hostname field this server could scope with.
	for _, want := range []string{"tenant-a", "alice", "db-1.prod", "sess-123"} {
		if !strings.Contains(cert.KeyId, want) {
			t.Errorf("the key id does not name %q: %q", want, cert.KeyId)
		}
	}
	if got := cert.CriticalOptions["source-address"]; got != "10.0.0.7" {
		t.Errorf("source-address: %q", got)
	}

	// ONE EXTENSION. Agent forwarding, port forwarding, X11 and user-rc are
	// each a way for a session to become something other than a session.
	if len(cert.Extensions) != 1 {
		t.Errorf("extensions: %v", cert.Extensions)
	}
	if _, ok := cert.Extensions["permit-pty"]; !ok {
		t.Errorf("extensions: %v", cert.Extensions)
	}

	// The row is the auditable record of the scope SSH cannot enforce.
	row, err := f.st.SSHCertificates().Get(ctx, "tenant-a", issued.Serial)
	if err != nil {
		t.Fatalf("stored certificate: %v", err)
	}
	if row.Target != "db-1.prod" || row.SessionID != "sess-123" || row.SubjectID != "alice" {
		t.Errorf("stored scope: %+v", row)
	}
}

func TestAnIssuedCertificateVerifiesAndAnExpiredOneDoesNot(t *testing.T) {
	f := newCA(t)
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	public, _ := proxyKey(t)

	issued, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
		SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1.prod",
		PublicKey: public, ValidFor: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if err := f.ca.Verify(ctx, "tenant-a", issued.Certificate, credential.VerifyRequest{
		Principal: "deploy",
	}); err != nil {
		t.Fatalf("a freshly issued certificate did not verify: %v", err)
	}

	// Past the window.
	if err := f.ca.Verify(ctx, "tenant-a", issued.Certificate, credential.VerifyRequest{
		Principal: "deploy", At: f.now.Add(10 * time.Minute),
	}); !errors.Is(err, credential.ErrCertificateExpired) {
		t.Fatalf("an expired certificate: want ErrCertificateExpired, got %v", err)
	}
	// And before it.
	if err := f.ca.Verify(ctx, "tenant-a", issued.Certificate, credential.VerifyRequest{
		Principal: "deploy", At: f.now.Add(-10 * time.Minute),
	}); !errors.Is(err, credential.ErrCertificateExpired) {
		t.Fatalf("a not-yet-valid certificate: want ErrCertificateExpired, got %v", err)
	}
	// And for a login it does not carry.
	if err := f.ca.Verify(ctx, "tenant-a", issued.Certificate, credential.VerifyRequest{
		Principal: "root",
	}); !errors.Is(err, credential.ErrPrincipalNotPermitted) {
		t.Fatalf("a wrong principal: want ErrPrincipalNotPermitted, got %v", err)
	}
}

func TestARevokedCertificateIsRefused(t *testing.T) {
	f := newCA(t)
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	public, _ := proxyKey(t)
	issued, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
		SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1.prod", PublicKey: public,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if err := f.ca.Revoke(ctx, "tenant-a", issued.Serial, "the session was killed"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := f.ca.Verify(ctx, "tenant-a", issued.Certificate, credential.VerifyRequest{
		Principal: "deploy",
	}); !errors.Is(err, credential.ErrCertificateRevoked) {
		t.Fatalf("want ErrCertificateRevoked, got %v", err)
	}
}

func TestARoutineRotationKeepsOutstandingCertificatesWorking(t *testing.T) {
	// "We rotate" without an answer for outstanding certificates is not a
	// rotation story. This is the answer for the routine case.
	f := newCA(t, credential.WithRotationOverlap(2*time.Hour))
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	public, _ := proxyKey(t)
	issued, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
		SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1.prod",
		PublicKey: public, ValidFor: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	result, err := f.ca.Rotate(ctx, "tenant-a", credential.RotateRequest{Comment: "quarterly"})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if result.PreviousKeyID == result.NewKeyID {
		t.Fatal("rotation reused the key id; a new generation is a new name")
	}
	if result.RevokedCertificates != 0 {
		t.Errorf("a routine rotation revoked %d certificates", result.RevokedCertificates)
	}

	// The retired key is still in the trust bundle, so a target that publishes
	// the bundle keeps accepting what the old key signed.
	if len(result.Info.TrustBundle) != 2 {
		t.Fatalf("trust bundle after rotation: %d entries, want the active key and the retired one",
			len(result.Info.TrustBundle))
	}
	if err := f.ca.Verify(ctx, "tenant-a", issued.Certificate, credential.VerifyRequest{
		Principal: "deploy", At: f.now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("a certificate signed before a routine rotation stopped verifying: %v", err)
	}

	// And a new certificate is signed by the NEW key.
	next, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
		SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1.prod", PublicKey: public,
	})
	if err != nil {
		t.Fatalf("issue after rotation: %v", err)
	}
	if next.CAKeyID != result.NewKeyID {
		t.Errorf("a certificate issued after rotation names %s, want %s", next.CAKeyID, result.NewKeyID)
	}

	// Past the overlap the retired key leaves the bundle, and by then nothing
	// it signed can still be inside its window.
	f.now = f.now.Add(3 * time.Hour)
	info, err := f.ca.Describe(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if len(info.TrustBundle) != 1 {
		t.Fatalf("trust bundle past the overlap: %d entries", len(info.TrustBundle))
	}
}

func TestACompromiseRotationRevokesEveryOutstandingCertificate(t *testing.T) {
	f := newCA(t)
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	public, _ := proxyKey(t)

	var issued []credential.Issued
	for range 3 {
		out, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
			SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1.prod",
			PublicKey: public, ValidFor: 30 * time.Minute,
		})
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		issued = append(issued, out)
	}

	result, err := f.ca.Rotate(ctx, "tenant-a", credential.RotateRequest{
		Comment: "the key may have leaked", Compromise: true,
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if result.RevokedCertificates != 3 {
		t.Errorf("a compromise rotation revoked %d of 3 certificates", result.RevokedCertificates)
	}
	// The retired key leaves the bundle AT ONCE.
	if len(result.Info.TrustBundle) != 1 {
		t.Errorf("trust bundle after a compromise rotation: %d entries", len(result.Info.TrustBundle))
	}
	for _, out := range issued {
		if err := f.ca.Verify(ctx, "tenant-a", out.Certificate, credential.VerifyRequest{
			Principal: "deploy",
		}); err == nil {
			t.Errorf("certificate %d still verifies after a compromise rotation", out.Serial)
		}
	}
	if outstanding, err := f.ca.OutstandingPage(ctx, "tenant-a", store.Page{Limit: store.DefaultPageLimit}); err != nil ||
		len(outstanding.Rows) != 0 || outstanding.More {
		t.Errorf("outstanding after a compromise rotation: %d more=%t (%v)", len(outstanding.Rows), outstanding.More, err)
	}
}

// The outstanding list is read a page at a time (M26), newest first, and
// "still usable" is judged at the CA's own instant when each page is read: a
// certificate that expires during a walk is not on the pages after it. Each
// tenant's walk reads that tenant's certificates alone (M18).
func TestOutstandingPagesAreJudgedAtTheCAsInstant(t *testing.T) {
	f := newCA(t)
	ctx := context.Background()
	public, _ := proxyKey(t)
	issue := func(tenant store.Tenant, validFor time.Duration) int64 {
		t.Helper()
		out, err := f.ca.Issue(ctx, tenant, credential.IssueRequest{
			SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1.prod",
			PublicKey: public, ValidFor: validFor,
		})
		if err != nil {
			t.Fatalf("issue in %s: %v", tenant, err)
		}
		f.now = f.now.Add(time.Minute)
		return out.Serial
	}
	serials := func(p store.Paged[store.SSHCertificate]) []int64 {
		var out []int64
		for _, c := range p.Rows {
			out = append(out, c.Serial)
		}
		return out
	}
	for _, tenant := range []store.Tenant{"tenant-a", "tenant-b"} {
		if _, err := f.ca.Ensure(ctx, tenant); err != nil {
			t.Fatalf("ensure %s: %v", tenant, err)
		}
	}
	short := issue("tenant-a", 10*time.Minute) // until five minutes after the walk starts
	other := issue("tenant-b", 30*time.Minute)
	second := issue("tenant-a", 30*time.Minute)
	third := issue("tenant-a", 30*time.Minute)
	newer := issue("tenant-b", 30*time.Minute)

	first, err := f.ca.OutstandingPage(ctx, "tenant-a", store.Page{Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if got := serials(first); !slices.Equal(got, []int64{third, second}) || !first.More {
		t.Fatalf("first page: %v more=%t, want [%d %d] and more", got, first.More, third, second)
	}
	last := first.Rows[len(first.Rows)-1]
	after := store.Page{Limit: 2, After: []any{last.IssuedAt, last.Serial}}

	rest, err := f.ca.OutstandingPage(ctx, "tenant-a", after)
	if err != nil || !slices.Equal(serials(rest), []int64{short}) || rest.More {
		t.Errorf("after %d, at once: %v more=%t (%v), want [%d]", second, serials(rest), rest.More, err, short)
	}
	f.now = f.now.Add(5 * time.Minute) // the short certificate's window closes
	rest, err = f.ca.OutstandingPage(ctx, "tenant-a", after)
	if err != nil || len(rest.Rows) != 0 || rest.More {
		t.Errorf("after %d, once %d expired: %v more=%t (%v), want an empty page that ends", second, short, serials(rest), rest.More, err)
	}

	// A position is not a scope: tenant-a's continues tenant-b's list from
	// the same instant, with tenant-b's certificates alone.
	b, err := f.ca.OutstandingPage(ctx, "tenant-b", after)
	if err != nil || !slices.Equal(serials(b), []int64{other}) || b.More {
		t.Errorf("tenant-b after tenant-a's position: %v more=%t (%v), want [%d]: the one issued before it", serials(b), b.More, err, other)
	}
	b, err = f.ca.OutstandingPage(ctx, "tenant-b", store.Page{Limit: 2})
	if err != nil || !slices.Equal(serials(b), []int64{newer, other}) || b.More {
		t.Errorf("tenant-b's own list: %v more=%t (%v), want [%d %d]", serials(b), b.More, err, newer, other)
	}
}

func TestOneTenantsTargetsDoNotTrustAnotherTenantsCA(t *testing.T) {
	// M18's fourth consequence, and the place it stops being intended and
	// becomes true.
	f := newCA(t)
	ctx := context.Background()
	for _, tenant := range []store.Tenant{"tenant-a", "tenant-b"} {
		if _, err := f.ca.Ensure(ctx, tenant); err != nil {
			t.Fatalf("ensure %s: %v", tenant, err)
		}
	}

	a, err := f.ca.Describe(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("describe a: %v", err)
	}
	b, err := f.ca.Describe(ctx, "tenant-b")
	if err != nil {
		t.Fatalf("describe b: %v", err)
	}
	if a.ActivePublicKey == b.ActivePublicKey {
		t.Fatal("two tenants share CA key material")
	}
	for _, k := range b.TrustBundle {
		if k.PublicKey == a.ActivePublicKey {
			t.Fatal("tenant-b's trust bundle carries tenant-a's CA key")
		}
	}

	public, _ := proxyKey(t)
	issued, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
		SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1.prod", PublicKey: public,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := f.ca.Verify(ctx, "tenant-b", issued.Certificate, credential.VerifyRequest{
		Principal: "deploy",
	}); !errors.Is(err, credential.ErrUntrustedCA) {
		t.Fatalf("tenant-a's certificate in tenant-b: want ErrUntrustedCA, got %v", err)
	}
}

func TestACertificateWithNoPrincipalsIsRefused(t *testing.T) {
	// One with none is valid for every account on every host that trusts the
	// CA.
	f := newCA(t)
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	public, _ := proxyKey(t)

	for name, req := range map[string]credential.IssueRequest{
		"no principals": {SubjectID: "alice", Target: "db-1", PublicKey: public},
		"no subject":    {Principals: []string{"deploy"}, Target: "db-1", PublicKey: public},
		"no target":     {SubjectID: "alice", Principals: []string{"deploy"}, PublicKey: public},
		"no key":        {SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1"},
	} {
		if _, err := f.ca.Issue(ctx, "tenant-a", req); err == nil {
			t.Errorf("%s: a certificate was issued anyway", name)
		}
	}
}

func TestARequestedLifetimeIsClampedDownAndNeverUp(t *testing.T) {
	f := newCA(t, credential.WithValidity(5*time.Minute), credential.WithMaxValidity(10*time.Minute))
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	public, _ := proxyKey(t)

	issued, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
		SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1",
		PublicKey: public, ValidFor: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if got := issued.ValidBefore; !got.Equal(f.now.Add(10 * time.Minute)) {
		t.Fatalf("a 24-hour request produced a window ending %s", got)
	}
}

// A route's lifetime_seconds is an UPPER BOUND (Hoplock/proxy#68): it shortens
// the CA's own validity and never lengthens it — including when the bound is
// above the default and below the ceiling, the case passing it as ValidFor
// would get wrong.
func TestAnUpperBoundShortensAndNeverLengthens(t *testing.T) {
	f := newCA(t, credential.WithValidity(5*time.Minute), credential.WithMaxValidity(time.Hour))
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	public, _ := proxyKey(t)
	for _, c := range []struct{ bound, want time.Duration }{
		{2 * time.Minute, 2 * time.Minute},
		{30 * time.Minute, 5 * time.Minute},
		{0, 5 * time.Minute},
	} {
		bound, want := c.bound, c.want
		issued, err := f.ca.Issue(ctx, "tenant-a", credential.IssueRequest{
			SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1",
			PublicKey: public, UpperBound: bound,
		})
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if got := issued.ValidBefore; !got.Equal(f.now.Add(want)) {
			t.Errorf("bound %s produced a window ending %s, want %s", bound, got, f.now.Add(want))
		}
	}
}

func TestSerialsAreMonotonicWithinATenantAndIndependentAcrossThem(t *testing.T) {
	// A serial that repeats makes a revocation list ambiguous.
	f := newCA(t)
	ctx := context.Background()
	public, _ := proxyKey(t)

	var serials []int64
	for _, tenant := range []store.Tenant{"tenant-a", "tenant-a", "tenant-b", "tenant-a"} {
		if _, err := f.ca.Ensure(ctx, tenant); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		issued, err := f.ca.Issue(ctx, tenant, credential.IssueRequest{
			SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1", PublicKey: public,
		})
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		serials = append(serials, issued.Serial)
	}
	if serials[0] != 1 || serials[1] != 2 || serials[3] != 3 {
		t.Errorf("tenant-a serials: %v", serials)
	}
	if serials[2] != 1 {
		t.Errorf("tenant-b's first serial is %d, so the counter is not per tenant", serials[2])
	}
}

func TestNoPrivateKeyIsEverStoredInTheClear(t *testing.T) {
	// The acceptance criterion: no private key appears in any stored row.
	f := newCA(t)
	ctx := context.Background()
	if _, err := f.ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	rows, err := f.st.SoftwareKeys().List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("software keys: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("software keys: %d rows", len(rows))
	}
	// A PKCS#8 DER private key starts with a SEQUENCE tag. Ciphertext under a
	// random nonce does not, and a second read of the same key gives different
	// bytes — which is what the per-call nonce buys.
	if rows[0].PrivateKey[0] == 0x30 {
		t.Fatal("the stored private key looks like plaintext DER")
	}
	if bytes.Contains(rows[0].PrivateKey, rows[0].PublicKey) {
		t.Fatal("the stored private key carries its public half in the clear")
	}

	// And the wrong key-encryption key is an OUTAGE that says what happened,
	// never a denial.
	wrong, err := extdefault.NewSoftwareKeyStore(f.st, bytes.Repeat([]byte{0x7f}, extdefault.KeyEncryptionKeySize))
	if err != nil {
		t.Fatalf("key store: %v", err)
	}
	other, err := credential.New(f.st, wrong, credential.WithClock(f.clock))
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	public, _ := proxyKey(t)
	_, err = other.Issue(ctx, "tenant-a", credential.IssueRequest{
		SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1", PublicKey: public,
	})
	if err == nil {
		t.Fatal("a CA with the wrong key-encryption key signed something")
	}
	if !strings.Contains(err.Error(), "key-encryption key") {
		t.Errorf("the error does not name the likeliest cause: %v", err)
	}
}

func TestASoftwareKeyStoreRefusesAKeyOfTheWrongLength(t *testing.T) {
	st := storetest.New(t)
	for _, size := range []int{0, 16, 31, 33} {
		if _, err := extdefault.NewSoftwareKeyStore(st, bytes.Repeat([]byte{1}, size)); err == nil {
			t.Errorf("a %d-byte key-encryption key was accepted", size)
		}
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	f := newCA(t)
	ctx := context.Background()
	first, err := f.ca.Ensure(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	second, err := f.ca.Ensure(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if first.ActiveKeyID != second.ActiveKeyID {
		t.Fatalf("a second Ensure created a second CA: %s / %s", first.ActiveKeyID, second.ActiveKeyID)
	}
	if first.Custodian != "software" {
		t.Errorf("custodian: %q — an auditor asking where the CA key is needs an answer", first.Custodian)
	}
}

func TestATenantWithNoAuthorityIsNamedRatherThanGuessedAt(t *testing.T) {
	f := newCA(t)
	if _, err := f.ca.Describe(context.Background(), "tenant-zzz"); !errors.Is(err, credential.ErrNoCA) {
		t.Fatalf("want ErrNoCA, got %v", err)
	}
	if _, err := f.ca.Rotate(context.Background(), "tenant-zzz", credential.RotateRequest{}); !errors.Is(err, credential.ErrNoCA) {
		t.Fatalf("want ErrNoCA, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// the seam
// ---------------------------------------------------------------------------

// THE TRIPWIRE, CHANGED RATHER THAN DELETED. 0011 left a test here that failed
// the day `brokered-certificate` reached the vendored contract. It has; and the
// refusal it guarded rendered the certificate, its serial and the CA bundle as
// ladder parameters, which is the shape upstream refused (Hoplock/proxy#68). So
// it now pins the shape upstream took: a rendered entry names the method,
// carries `username`, and carries no parameter outside `username`, `key_type`
// and `lifetime_seconds`. A proxy that implements the method refuses a
// parameter it does not know before it generates anything, so an artifact on
// the entry would fail every session closed.
func TestABrokeredCertificateEntryCarriesPolicyOnly(t *testing.T) {
	entry, err := credential.LadderEntry(model.CredentialEntry{
		Method:          model.CredentialBrokeredCertificate,
		Username:        model.Username{Source: model.UsernameLiteral, Value: "netadmin"},
		KeyType:         "ed25519",
		LifetimeSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Method != contract.TargetAuthBrokeredCertificate {
		t.Fatalf("method = %q, want %q", entry.Method, contract.TargetAuthBrokeredCertificate)
	}
	if entry.Params[contract.ParamUsername] != "netadmin" {
		t.Fatalf("username = %q, want netadmin", entry.Params[contract.ParamUsername])
	}
	// Named one by one, because these three are the shape upstream refused
	// and the likeliest to come back.
	for _, artifact := range []string{"certificate", "certificate_serial", "ca_public_keys"} {
		if _, ok := entry.Params[artifact]; ok {
			t.Errorf("a brokered-certificate entry carries %q; the certificate is minted per session at %s and is never a ladder parameter",
				artifact, contract.PathCredentialCertificate)
		}
	}
	policy := map[string]bool{
		contract.ParamUsername: true, contract.ParamKeyType: true, contract.ParamLifetimeSeconds: true,
	}
	for name := range entry.Params {
		if !policy[name] {
			t.Errorf("a brokered-certificate entry carries %q, outside the three policy names", name)
		}
	}
}

func TestALadderEntryStillRequiresAUsername(t *testing.T) {
	// `username` is required on every method the contract defines and is never
	// defaulted to the identity's login — a client-typed string the proxy must
	// not base an authorization decision on. The compiler refuses such an entry
	// first; the renderer refuses it too, as an outage rather than an entry.
	_, err := credential.LadderEntry(model.CredentialEntry{Method: model.CredentialBrokeredCertificate})
	if err == nil {
		t.Fatal("an entry with no username was rendered")
	}
}

func parseCertificate(t *testing.T, line string) *ssh.Certificate {
	t.Helper()
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		t.Fatalf("certificate does not parse: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatal("the issued value is a public key, not a certificate")
	}
	return cert
}
