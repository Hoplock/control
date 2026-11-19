// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package credential_test

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/credential/credentialtest"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/store"
)

// CA CUSTODY THROUGH THE KEY STORE (0062).
//
// The CA has one key store — the one the deployment resolved — and every key it
// makes and every signer it uses comes from there. These tests hold the four
// answers that follow from that: a registered store holds the keys; a key the
// store does not hold is refused and nothing else signs; a store that cannot be
// asked is an outage told apart from an absent key; and `Rotate` is the way out.

const hsmCustodian = "pkcs11:slot-test"

// hsmCA is a CA over an in-memory key store standing in for a host's HSM, on
// the fixture's database and clock.
func hsmCA(t *testing.T, f *caFixture, keys *credentialtest.KeyStore) *credential.CA {
	t.Helper()
	ca, err := credential.New(f.st, keys, credential.WithClock(f.clock),
		credential.WithKeyStoreName("registered ext.KeyStore "+credentialtest.Provider))
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	return ca
}

func softwareKeyCount(t *testing.T, st *store.Store, tenant store.Tenant) int {
	t.Helper()
	rows, err := st.SoftwareKeys().List(t.Context(), tenant)
	if err != nil {
		t.Fatalf("software keys: %v", err)
	}
	return len(rows)
}

func certificateCount(t *testing.T, st *store.Store, tenant store.Tenant) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM ssh_certificates WHERE tenant = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count certificates: %v", err)
	}
	return n
}

func issueOne(ctx context.Context, t *testing.T, ca *credential.CA, tenant store.Tenant) (credential.Issued, error) {
	t.Helper()
	public, _ := proxyKey(t)
	return ca.Issue(ctx, tenant, credential.IssueRequest{
		SubjectID: "alice", Principals: []string{"deploy"}, Target: "db-1.prod", PublicKey: public,
	})
}

// A CA over a registered key store makes its keys there, signs with them, and
// names the custodian the store states — and Control's software custodian
// holds nothing.
func TestARegisteredKeyStoreHoldsTheCAsKeys(t *testing.T) {
	f := newCA(t)
	ctx := t.Context()
	keys := credentialtest.New(hsmCustodian)
	ca := hsmCA(t, f, keys)

	info, err := ca.Ensure(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if refs := keys.Refs("tenant-a"); len(refs) != 1 || refs[0] != credential.CAKeyName+":"+info.ActiveKeyID {
		t.Fatalf("the registered store holds %v; want the CA key %s", refs, info.ActiveKeyID)
	}
	if n := softwareKeyCount(t, f.st, "tenant-a"); n != 0 {
		t.Fatalf("software_keys gained %d rows for a CA whose keys live in a registered store", n)
	}
	if info.Custodian != hsmCustodian {
		t.Errorf("custodian %q, want the key store's own %q", info.Custodian, hsmCustodian)
	}

	issued, err := issueOne(ctx, t, ca, "tenant-a")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := ca.Verify(ctx, "tenant-a", issued.Certificate, credential.VerifyRequest{Principal: "deploy"}); err != nil {
		t.Fatalf("a certificate the registered store signed does not verify: %v", err)
	}
}

// The custodian is asked of the key store, never read off the key's name: a
// stored ref that looks like it names a custodian says nothing.
func TestTheCustodianIsTheKeyStoresOwnWord(t *testing.T) {
	f := newCA(t)
	ctx := t.Context()
	for _, custodian := range []string{"aws-kms:arn:aws:kms:eu-west-1:000000000000:key/test", ""} {
		tenant := store.Tenant("tenant-" + fmt.Sprint(len(custodian)))
		ca := hsmCA(t, f, credentialtest.New(custodian))
		info, err := ca.Ensure(ctx, tenant)
		if err != nil {
			t.Fatalf("ensure: %v", err)
		}
		if info.Custodian != custodian {
			t.Errorf("custodian %q, want the store's %q", info.Custodian, custodian)
		}
	}
}

// A software-held key, and then custody moved to a registered store that does
// not hold it: the case 0062 meets only in a development database, or when an
// operator changes custody by hand.
func strandedKey(t *testing.T) (*caFixture, *credentialtest.KeyStore, *credential.CA, credential.Info) {
	t.Helper()
	f := newCA(t)
	before, err := f.ca.Ensure(t.Context(), "tenant-a")
	if err != nil {
		t.Fatalf("ensure in the software custodian: %v", err)
	}
	keys := credentialtest.New(hsmCustodian)
	return f, keys, hsmCA(t, f, keys), before
}

// A key the resolved store does not hold is refused, naming the key and the
// store, and NOTHING SIGNS IT — not the software custodian that does hold it,
// and not anything else. No serial is spent and no certificate is recorded.
func TestAKeyTheStoreDoesNotHoldIsRefusedAndNothingElseSigns(t *testing.T) {
	f, keys, ca, before := strandedKey(t)
	ctx := t.Context()

	_, err := issueOne(ctx, t, ca, "tenant-a")
	var custody *credential.CustodyError
	if !errors.As(err, &custody) || !errors.Is(err, credential.ErrKeyNotHeld) {
		t.Fatalf("issuance with a key the store does not hold: %v; want a CustodyError, not held", err)
	}
	if errors.Is(err, credential.ErrKeyStoreUnavailable) || custody.Unreachable {
		t.Errorf("an absent key was reported as an unreachable store: %v", err)
	}
	if custody.KeyID != before.ActiveKeyID || custody.Ref != credential.CAKeyName+":"+before.ActiveKeyID ||
		custody.KeyStore != "registered ext.KeyStore "+credentialtest.Provider || custody.Tenant != "tenant-a" {
		t.Errorf("the refusal names %+v; want key %s and the registered store", custody, before.ActiveKeyID)
	}
	for _, want := range []string{before.ActiveKeyID, credentialtest.Provider, "does not hold"} {
		if !strings.Contains(custody.Message(), want) {
			t.Errorf("the message %q does not say %q", custody.Message(), want)
		}
	}
	if n := certificateCount(t, f.st, "tenant-a"); n != 0 {
		t.Fatalf("%d certificates were recorded for an issuance that was refused", n)
	}
	if keys.Len() != 0 {
		t.Errorf("a refused issuance made a key: the store holds %d", keys.Len())
	}

	// Describe will not name a custodian it cannot confirm; Ensure does not
	// paper over the gap by making a new key, because targets must trust a
	// new key before it signs.
	if _, err := ca.Describe(ctx, "tenant-a"); !errors.Is(err, credential.ErrKeyNotHeld) {
		t.Errorf("describe: %v; want not held", err)
	}
	if _, err := ca.Ensure(ctx, "tenant-a"); !errors.Is(err, credential.ErrKeyNotHeld) {
		t.Errorf("ensure: %v; want not held", err)
	}
	if keys.Len() != 0 {
		t.Errorf("Ensure made a key over a stranded one: the store holds %d", keys.Len())
	}

	// The custody listing names every key's state, the stranded one included.
	rows, err := ca.Custody(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("custody: %v", err)
	}
	if len(rows) != 1 || !rows[0].Active || !errors.Is(rows[0].Err, credential.ErrKeyNotHeld) || rows[0].Custodian != "" {
		t.Errorf("custody rows %+v; want the active key, not held, with no custodian", rows)
	}
}

// A store holding some other key under the active key's name does not hold the
// active key: a signature from it would chain to no key any target trusts.
func TestAStoreHoldingAnotherKeyUnderTheNameDoesNotHoldIt(t *testing.T) {
	f, keys, ca, before := strandedKey(t)
	keys.Put(ext.KeyRef{Tenant: "tenant-a", Name: credential.CAKeyName + ":" + before.ActiveKeyID})

	if _, err := issueOne(t.Context(), t, ca, "tenant-a"); !errors.Is(err, credential.ErrKeyNotHeld) {
		t.Fatalf("issuance with another key under the name: %v; want not held", err)
	}
	if _, err := ca.Describe(t.Context(), "tenant-a"); !errors.Is(err, credential.ErrKeyNotHeld) {
		t.Errorf("describe with another key under the name: %v; want not held", err)
	}
	if n := certificateCount(t, f.st, "tenant-a"); n != 0 {
		t.Fatalf("%d certificates were recorded", n)
	}
}

// A store that cannot be asked is an outage, and never read as the key being
// absent: "rotate" is the answer to one and the wrong answer to the other.
func TestAStoreThatCannotBeAskedIsAnOutageNotAnAbsentKey(t *testing.T) {
	f := newCA(t)
	ctx := t.Context()
	keys := credentialtest.New(hsmCustodian)
	ca := hsmCA(t, f, keys)
	if _, err := ca.Ensure(ctx, "tenant-a"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	keys.SetDown(true)

	_, err := issueOne(ctx, t, ca, "tenant-a")
	var custody *credential.CustodyError
	if !errors.As(err, &custody) || !custody.Unreachable || !errors.Is(err, credential.ErrKeyStoreUnavailable) {
		t.Fatalf("issuance with the store down: %v; want unreachable", err)
	}
	if errors.Is(err, credential.ErrKeyNotHeld) {
		t.Errorf("an unreachable store was reported as an absent key: %v", err)
	}
	if !strings.Contains(custody.Message(), "could not be asked") || strings.Contains(custody.Message(), "does not hold") {
		t.Errorf("the message does not tell an outage from an absent key: %q", custody.Message())
	}
	if _, err := ca.Describe(ctx, "tenant-a"); !errors.Is(err, credential.ErrKeyStoreUnavailable) {
		t.Errorf("describe: %v; want unreachable", err)
	}
	if _, err := ca.Rotate(ctx, "tenant-a", credential.RotateRequest{}); !errors.Is(err, credential.ErrKeyStoreUnavailable) {
		t.Errorf("rotate: %v; want unreachable", err)
	}
	if n := certificateCount(t, f.st, "tenant-a"); n != 0 {
		t.Fatalf("%d certificates were recorded", n)
	}

	// A deadline is an outage too, whatever the store's own words.
	keys.SetDown(false)
	slowCA, err := credential.New(f.st, deadlineStore{KeyStore: keys}, credential.WithClock(f.clock))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issueOne(ctx, t, slowCA, "tenant-a"); !errors.Is(err, credential.ErrKeyStoreUnavailable) {
		t.Errorf("a store past its deadline: %v; want unreachable", err)
	}
}

// deadlineStore answers a signer request as a store whose own call ran out of
// time does: with the deadline, in its own words, and no ext kind.
type deadlineStore struct{ *credentialtest.KeyStore }

func (deadlineStore) Signer(context.Context, ext.KeyRef) (crypto.Signer, error) {
	return nil, fmt.Errorf("hsm session: %w", context.DeadlineExceeded)
}

// ROTATION IS THE WAY OUT, and it needs nothing from the stranded key's private
// half. A routine rotation generates the new key in the resolved store and keeps
// the old one in the trust bundle for the overlap, so what it signed keeps
// verifying; a compromise rotation removes it and revokes what it signed.
func TestRotationIsTheWayOutOfAStrandedKey(t *testing.T) {
	for _, compromise := range []bool{false, true} {
		t.Run(fmt.Sprintf("compromise=%v", compromise), func(t *testing.T) {
			f := newCA(t)
			ctx := t.Context()
			before, err := f.ca.Ensure(ctx, "tenant-a")
			if err != nil {
				t.Fatal(err)
			}
			old, err := issueOne(ctx, t, f.ca, "tenant-a")
			if err != nil {
				t.Fatalf("issue under the software custodian: %v", err)
			}
			keys := credentialtest.New(hsmCustodian)
			ca := hsmCA(t, f, keys)

			result, err := ca.Rotate(ctx, "tenant-a", credential.RotateRequest{Compromise: compromise})
			if err != nil {
				t.Fatalf("rotate a stranded key: %v", err)
			}
			if result.PreviousKeyID != before.ActiveKeyID {
				t.Fatalf("rotated from %s, want %s", result.PreviousKeyID, before.ActiveKeyID)
			}
			if refs := keys.Refs("tenant-a"); len(refs) != 1 || refs[0] != credential.CAKeyName+":"+result.NewKeyID {
				t.Fatalf("the new key is not in the resolved store: it holds %v", refs)
			}
			if n := softwareKeyCount(t, f.st, "tenant-a"); n != 1 {
				t.Errorf("software_keys holds %d rows; the rotation must add none", n)
			}
			if result.Info.Custodian != hsmCustodian {
				t.Errorf("custodian after rotation %q, want %q", result.Info.Custodian, hsmCustodian)
			}

			inBundle := false
			for _, k := range result.Info.TrustBundle {
				if k.KeyID == before.ActiveKeyID {
					inBundle = true
				}
			}
			verr := ca.Verify(ctx, "tenant-a", old.Certificate, credential.VerifyRequest{Principal: "deploy"})
			if compromise {
				if inBundle || result.RevokedCertificates != 1 {
					t.Errorf("compromise: old key in bundle %v, revoked %d; want gone and 1", inBundle, result.RevokedCertificates)
				}
				if verr == nil {
					t.Error("a certificate the compromised key signed still verifies")
				}
			} else {
				if !inBundle || result.RevokedCertificates != 0 {
					t.Errorf("routine: old key in bundle %v, revoked %d; want kept and 0", inBundle, result.RevokedCertificates)
				}
				if verr != nil {
					t.Errorf("a certificate the retired key signed no longer verifies in the overlap: %v", verr)
				}
			}

			issued, err := issueOne(ctx, t, ca, "tenant-a")
			if err != nil {
				t.Fatalf("issue after the rotation: %v", err)
			}
			if issued.CAKeyID != result.NewKeyID {
				t.Errorf("signed by %s, want the new key %s", issued.CAKeyID, result.NewKeyID)
			}

			// The retired key is still listed, as one the store does not
			// hold: true, and not a reason to fail.
			rows, err := ca.Custody(ctx, "tenant-a")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				switch {
				case r.Active && (r.Err != nil || r.Custodian != hsmCustodian):
					t.Errorf("the active key's custody: %+v", r)
				case !r.Active && !errors.Is(r.Err, credential.ErrKeyNotHeld):
					t.Errorf("the retired key's custody: %+v; want not held", r)
				}
			}
		})
	}
}

// Control's software custodian, with nothing registered, is what it always was.
func TestTheSoftwareCustodianAloneIsUnchanged(t *testing.T) {
	f := newCA(t)
	ctx := t.Context()
	info, err := f.ca.Ensure(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if info.Custodian != extdefault.CustodianSoftware {
		t.Errorf("custodian %q, want %q", info.Custodian, extdefault.CustodianSoftware)
	}
	if n := softwareKeyCount(t, f.st, "tenant-a"); n != 1 {
		t.Errorf("software_keys holds %d rows, want the CA key", n)
	}
	if _, err := issueOne(ctx, t, f.ca, "tenant-a"); err != nil {
		t.Fatalf("issue: %v", err)
	}
}
