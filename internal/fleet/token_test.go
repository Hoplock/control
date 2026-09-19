// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The token carries its tenant, and the tenant is a SELECTOR inside what the
// credential is good for rather than something the caller asserts (M18). A
// forged prefix fails the hash comparison in a tenant where no such token
// exists.
func TestAProxyTokenResolvesItsOwnTenantAndNothingElse(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)
	ctx := t.Context()

	token, tokenID, err := reg.IssueProxyToken(ctx, uidTenant, "proxy-1", "test", time.Time{})
	if err != nil {
		t.Fatalf("IssueProxyToken: %v", err)
	}
	if !strings.HasPrefix(token.String(), uidTenant.String()+".") {
		t.Fatalf("token %q does not carry its tenant", token.String())
	}

	caller, ok, err := reg.AuthenticateProxyToken(ctx, token.String())
	if err != nil {
		t.Fatalf("AuthenticateProxyToken: %v", err)
	}
	if !ok {
		t.Fatal("a token this server just issued was not recognised")
	}
	if caller.Tenant != uidTenant || caller.ProxyID != "proxy-1" || caller.TokenID != tokenID {
		t.Errorf("caller = %+v, want %s / proxy-1 / %s", caller, uidTenant, tokenID)
	}
	if !caller.Bound() {
		t.Error("a token issued to a named proxy is not bound to it")
	}

	// The same secret under a different tenant prefix is not a credential.
	forged := "other-tenant." + token.Secret
	if _, ok, err := reg.AuthenticateProxyToken(ctx, forged); err != nil || ok {
		t.Errorf("a forged tenant prefix was accepted (ok=%v, err=%v)", ok, err)
	}
}

// A refusal and an outage are different returns, which is what keeps M11
// structural here rather than conventional: a database failure can never
// arrive as `ok == false`.
func TestAnUnrecognisedTokenIsRefusedRatherThanErroring(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)

	for _, presented := range []string{
		"",
		"not-shaped-like-a-token",
		uidTenant.String() + ".not-a-real-secret",
		"." + "orphan-secret",
	} {
		caller, ok, err := reg.AuthenticateProxyToken(t.Context(), presented)
		if err != nil {
			t.Errorf("AuthenticateProxyToken(%q) errored: %v; a malformed or wrong token is a REFUSAL, "+
				"and only an outage is an error", presented, err)
		}
		if ok {
			t.Errorf("AuthenticateProxyToken(%q) accepted it as %+v", presented, caller)
		}
	}
}

func TestARevokedOrExpiredTokenStopsWorking(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	clock := now
	reg := fleet.New(st, fleet.WithClock(func() time.Time { return clock }))

	revoked, revokedID, err := reg.IssueProxyToken(ctx, uidTenant, "proxy-1", "revoke me", time.Time{})
	if err != nil {
		t.Fatalf("IssueProxyToken: %v", err)
	}
	expiring, _, err := reg.IssueProxyToken(ctx, uidTenant, "proxy-1", "expire me", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("IssueProxyToken: %v", err)
	}

	if err := st.ProxyTokens().Revoke(ctx, uidTenant, revokedID, now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	clock = now.Add(time.Second)
	if _, ok, err := reg.AuthenticateProxyToken(ctx, revoked.String()); err != nil || ok {
		t.Errorf("a revoked token was accepted (ok=%v, err=%v)", ok, err)
	}
	if _, ok, err := reg.AuthenticateProxyToken(ctx, expiring.String()); err != nil || !ok {
		t.Errorf("an unexpired token was refused (ok=%v, err=%v)", ok, err)
	}

	clock = now.Add(2 * time.Hour)
	if _, ok, err := reg.AuthenticateProxyToken(ctx, expiring.String()); err != nil || ok {
		t.Errorf("an expired token was accepted (ok=%v, err=%v)", ok, err)
	}
}

// A bound token may not speak for another proxy; an unbound one authenticates
// "a proxy of this tenant" and nothing narrower.
func TestTokenBindingDecidesWhoACallerMaySpeakFor(t *testing.T) {
	t.Parallel()

	bound := fleet.ProxyCaller{Tenant: uidTenant, ProxyID: "proxy-1"}
	if !bound.Authorises("proxy-1") {
		t.Error("a bound token may not speak for the proxy it was issued to")
	}
	if bound.Authorises("proxy-2") {
		t.Error("a bound token may speak for another proxy: the lease id an incident resolves a uid back " +
			"to would then name whoever asked")
	}

	unbound := fleet.ProxyCaller{Tenant: uidTenant}
	if unbound.Bound() {
		t.Error("a token with no proxy id reports itself as bound")
	}
	if !unbound.Authorises("proxy-2") {
		t.Error("an unbound token cannot speak for any proxy, which leaves it useless rather than narrow")
	}
}

// Enrollment mints the credential inside the transaction that admits the
// proxy. A fleet member with no way to call the API is a half-enrollment an
// operator has to repair by hand.
func TestEnrollmentHandsBackAWorkingChannelCredential(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)
	ctx := t.Context()

	grantToken, err := reg.IssueGrant(ctx, uidTenant, fleet.EnrollmentGrant{
		ProxyID: "proxy-1", GrantedZones: []fleet.Zone{"edge"}, CreatedBy: "operator",
	}, time.Time{})
	if err != nil {
		t.Fatalf("IssueGrant: %v", err)
	}

	key := []byte("proxy-1 public key material")
	enrollment, err := reg.Enroll(ctx, fleet.EnrollmentRequest{
		Token: grantToken.String(), ProxyID: "proxy-1", Zone: "edge", PublicKey: key,
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if enrollment.APIToken.Secret == "" {
		t.Fatal("enrollment handed back no channel credential, so the proxy cannot call anything — " +
			"including whatever endpoint would have issued it one")
	}

	caller, ok, err := reg.AuthenticateProxyToken(ctx, enrollment.APIToken.String())
	if err != nil || !ok {
		t.Fatalf("the credential enrollment handed back does not authenticate (ok=%v, err=%v)", ok, err)
	}
	if caller.ProxyID != "proxy-1" {
		t.Errorf("credential authenticates as %q, want proxy-1", caller.ProxyID)
	}

	// The key it enrolled with is what makes a chain leg recognisable, and
	// the fingerprint is derived from the key rather than kept beside it.
	proxyID, isFleet, err := reg.ProxyByKeyFingerprint(ctx, uidTenant, identity.KeyFingerprint(key))
	if err != nil {
		t.Fatalf("ProxyByKeyFingerprint: %v", err)
	}
	if !isFleet || proxyID != "proxy-1" {
		t.Errorf("the enrolled key resolved to (%q, %v), want proxy-1", proxyID, isFleet)
	}
}

// A proxy that is not ENROLLED is not a hop. A revoked proxy that kept its key
// must not be able to authenticate a chain leg by holding on to it.
func TestARevokedProxysKeyIsNotAChainLeg(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)
	ctx := t.Context()

	key := []byte("a key the fleet no longer trusts")
	if err := st.Proxies().Upsert(ctx, uidTenant, store.Proxy{
		ID: "gone", Zone: "edge", PublicKey: key, State: store.EnrollmentRevoked,
	}); err != nil {
		t.Fatalf("seed proxy: %v", err)
	}

	proxyID, isFleet, err := reg.ProxyByKeyFingerprint(ctx, uidTenant, identity.KeyFingerprint(key))
	if err != nil {
		t.Fatalf("ProxyByKeyFingerprint: %v", err)
	}
	if isFleet {
		t.Fatalf("a revoked proxy's key still authenticates a chain leg as %q", proxyID)
	}
}

// A key belonging to no proxy is "not one of ours" — false and no error, so a
// caller cannot mistake it for a failure or vice versa.
func TestAnUnknownKeyIsNotOneOfOurs(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)

	proxyID, isFleet, err := reg.ProxyByKeyFingerprint(t.Context(), uidTenant, "SHA256:nobody")
	if err != nil {
		t.Fatalf("ProxyByKeyFingerprint: %v", err)
	}
	if isFleet {
		t.Errorf("an unknown fingerprint resolved to %q", proxyID)
	}

	// An empty fingerprint is the same answer rather than an error, so the
	// caller's deny path is one shape for every malformed key.
	if _, isFleet, err := reg.ProxyByKeyFingerprint(t.Context(), uidTenant, ""); err != nil || isFleet {
		t.Errorf("an empty fingerprint resolved to a proxy (ok=%v, err=%v)", isFleet, err)
	}
}

// A tenant that cannot be carried in a token is refused at issuance rather
// than at verification: an ambiguous credential is one that can be made to
// resolve to the wrong tenant.
func TestATenantThatCannotBeCarriedIsRefusedAtIssuance(t *testing.T) {
	t.Parallel()

	if _, err := fleet.MintProxyToken("acme.corp"); err == nil {
		t.Fatal("a tenant containing the separator was accepted")
	}
	if _, err := fleet.MintProxyToken(""); err == nil {
		t.Fatal("an empty tenant was accepted")
	}

	token, err := fleet.MintProxyToken(uidTenant)
	if err != nil {
		t.Fatalf("MintProxyToken: %v", err)
	}
	parsed, ok := fleet.ParseProxyToken(token.String())
	if !ok || parsed.Tenant != uidTenant || parsed.Secret != token.Secret {
		t.Errorf("round trip = (%+v, %v), want the token back", parsed, ok)
	}
	if errors.Is(nil, nil) && len(token.Hash()) != 32 {
		t.Errorf("token hash is %d bytes, want a SHA-256", len(token.Hash()))
	}
}
