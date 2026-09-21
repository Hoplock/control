// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

const testClientID = "hoplock-control"

func oidcBroker(t *testing.T, idp *testIdP, mutate func(*identity.OIDCConfig)) (*identity.OIDCBroker, string) {
	t.Helper()
	redirect := "https://control.example.com/api/v1/session/federated/okta/callback"
	cfg := identity.OIDCConfig{
		Issuer:      idp.issuer(),
		ClientID:    testClientID,
		RedirectURI: redirect,
		Scopes:      []string{"profile", "email", "groups"},
		LoginClaim:  "preferred_username",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	broker, err := identity.NewOIDCBroker("okta", cfg)
	if err != nil {
		t.Fatalf("broker: %v", err)
	}
	return broker, redirect
}

// completeOIDCLogin drives a whole login and returns the assertion.
func completeOIDCLogin(t *testing.T, idp *testIdP, broker *identity.OIDCBroker) (identity.Assertion, error) {
	t.Helper()
	ctx := context.Background()

	begun, err := broker.Begin(ctx, identity.BeginRequest{State: "state-1"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	params := idp.login(t, begun.RedirectURL)

	return broker.Complete(ctx, identity.CompleteRequest{
		Flow: store.FlowState{
			State:        "state-1",
			Connector:    "okta",
			Kind:         store.ConnectorOIDC,
			Nonce:        begun.Nonce,
			PKCEVerifier: begun.PKCEVerifier,
		},
		Params: params,
	})
}

func TestAnOIDCLoginProducesAnIdentityWithItsClaimsAndGroups(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)

	assertion, err := completeOIDCLogin(t, idp, broker)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if assertion.Subject != "00u1" {
		t.Errorf("subject: %q", assertion.Subject)
	}
	if assertion.Login != "alice" {
		t.Errorf("login: %q", assertion.Login)
	}
	if assertion.Email != "alice@example.com" || assertion.DisplayName != "Alice Example" {
		t.Errorf("display fields: %q / %q", assertion.Email, assertion.DisplayName)
	}
	if assertion.Connector != "okta" {
		t.Errorf("connector: %q", assertion.Connector)
	}
	if got := assertion.MultiClaims["groups"]; !slices.Equal(got, []string{"okta-sre", "okta-everyone"}) {
		t.Errorf("groups: %v", got)
	}
	if assertion.Claims["department"] != "engineering" {
		t.Errorf("department: %q", assertion.Claims["department"])
	}
	// The raw assertion carries everything the IdP said, INCLUDING what no
	// mapping names. That is correct at this layer: the allow-list is the
	// mapping's, and its test is in mapping_test.go.
	if assertion.Claims["role"] != "admin" {
		t.Errorf("the broker dropped a claim before the mapping could refuse it")
	}
}

func TestTheAuthorizationRequestAlwaysCarriesPKCE(t *testing.T) {
	// PKCE is not optional: the cost is one hash and the failure it prevents is
	// an intercepted authorization code being redeemed by whoever intercepted
	// it.
	idp := newTestIdP(t, testClientID)
	broker, redirect := oidcBroker(t, idp, nil)

	begun, err := broker.Begin(context.Background(), identity.BeginRequest{State: "state-1"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	u, err := url.Parse(begun.RedirectURL)
	if err != nil {
		t.Fatalf("redirect url: %v", err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("challenge method: %q — `plain` is a challenge that is its own answer", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" || begun.PKCEVerifier == "" {
		t.Error("no PKCE challenge was sent")
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type %q — the implicit and hybrid flows put a token in a URL", q.Get("response_type"))
	}
	if q.Get("nonce") == "" || begun.Nonce == "" {
		t.Error("no nonce was sent")
	}
	if q.Get("redirect_uri") != redirect {
		t.Errorf("redirect_uri: %q", q.Get("redirect_uri"))
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("scope %q does not request openid", q.Get("scope"))
	}

	// And the verifier really reaches the token endpoint.
	idp.login(t, begun.RedirectURL)
	if _, err := broker.Complete(context.Background(), identity.CompleteRequest{
		Flow:   store.FlowState{State: "state-1", Connector: "okta", Nonce: begun.Nonce, PKCEVerifier: begun.PKCEVerifier},
		Params: url.Values{"code": {"code-state-1"}, "state": {"state-1"}},
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if idp.lastVerifier != begun.PKCEVerifier {
		t.Errorf("the token request carried verifier %q, not the one the flow issued", idp.lastVerifier)
	}
}

func TestATokenWhoseNonceDoesNotMatchTheFlowIsRefused(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)
	ctx := context.Background()

	begun, err := broker.Begin(ctx, identity.BeginRequest{State: "state-1"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	params := idp.login(t, begun.RedirectURL)

	// The flow's nonce is replaced with somebody else's: a replayed id_token.
	_, err = broker.Complete(ctx, identity.CompleteRequest{
		Flow:   store.FlowState{State: "state-1", Connector: "okta", Nonce: "a-different-nonce", PKCEVerifier: begun.PKCEVerifier},
		Params: params,
	})
	assertRefusal(t, err, identity.RejectFederationAssertion)
}

func TestAFlowWithNoNonceIsRefusedRatherThanSkippingTheCheck(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)
	ctx := context.Background()

	begun, err := broker.Begin(ctx, identity.BeginRequest{State: "state-1"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	idp.login(t, begun.RedirectURL)
	idp.overrideClaims = func(c map[string]any) { delete(c, "nonce") }

	_, err = broker.Complete(ctx, identity.CompleteRequest{
		Flow:   store.FlowState{State: "state-1", Connector: "okta", PKCEVerifier: begun.PKCEVerifier},
		Params: url.Values{"code": {"code-state-1"}},
	})
	assertRefusal(t, err, identity.RejectFederationAssertion)
}

func TestATokenForAnotherAudienceIsRefused(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)
	idp.overrideClaims = func(c map[string]any) { c["aud"] = "somebody-else" }

	_, err := completeOIDCLogin(t, idp, broker)
	assertRefusal(t, err, identity.RejectFederationAudience)
}

func TestAMultiAudienceTokenNamingAnotherAuthorizedPartyIsRefused(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)
	idp.overrideClaims = func(c map[string]any) {
		c["aud"] = []string{testClientID, "somebody-else"}
		c["azp"] = "somebody-else"
	}

	_, err := completeOIDCLogin(t, idp, broker)
	assertRefusal(t, err, identity.RejectFederationAudience)
}

func TestATokenFromAnotherIssuerIsRefused(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)
	idp.overrideClaims = func(c map[string]any) { c["iss"] = "https://evil.example.com" }

	_, err := completeOIDCLogin(t, idp, broker)
	assertRefusal(t, err, identity.RejectFederationAssertion)
}

func TestAnExpiredTokenIsRefused(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, func(c *identity.OIDCConfig) { c.ClockSkew = time.Second })
	idp.overrideClaims = func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }

	_, err := completeOIDCLogin(t, idp, broker)
	assertRefusal(t, err, identity.RejectFederationExpired)
}

func TestATokenIssuedInTheFutureIsRefused(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, func(c *identity.OIDCConfig) { c.ClockSkew = time.Second })
	idp.overrideClaims = func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() }

	_, err := completeOIDCLogin(t, idp, broker)
	assertRefusal(t, err, identity.RejectFederationExpired)
}

func TestATokenWithNoSubjectIsRefused(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)
	idp.overrideClaims = func(c map[string]any) { delete(c, "sub") }

	_, err := completeOIDCLogin(t, idp, broker)
	assertRefusal(t, err, identity.RejectFederationSubject)
}

func TestATokenSignedWithAKeyTheIdPDoesNotPublishIsRefused(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	other := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)
	idp.signWith = other.key

	_, err := completeOIDCLogin(t, idp, broker)
	assertRefusal(t, err, identity.RejectFederationSignature)
}

func TestTheAlgorithmComesFromTheKeyAndNeverFromTheToken(t *testing.T) {
	// The classic JOSE forgery: claim `alg: HS256` and sign with the RSA public
	// key as the HMAC secret. Nothing here can turn an RSA public key into an
	// HMAC secret, so the header's claim reaches no branch at all — and neither
	// does `none`.
	for _, alg := range []string{"HS256", "none", "NONE", "RS1", "ES256", "EdDSA"} {
		t.Run(alg, func(t *testing.T) {
			idp := newTestIdP(t, testClientID)
			broker, _ := oidcBroker(t, idp, nil)
			idp.forgeAlg = alg

			_, err := completeOIDCLogin(t, idp, broker)
			assertRefusal(t, err, identity.RejectFederationSignature)
		})
	}
}

func TestAnIdPThatRefusesTheLoginIsADenialAndNotAnOutage(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)

	_, err := broker.Complete(context.Background(), identity.CompleteRequest{
		Flow: store.FlowState{State: "state-1", Connector: "okta"},
		Params: url.Values{
			"error":             {"access_denied"},
			"error_description": {"the user said no"},
		},
	})
	be := assertRefusal(t, err, identity.RejectFederationNotAllowed)
	// The IdP's own error text is a diagnosis for a log, not a sentence for
	// this audience.
	if strings.Contains(be.Message, "the user said no") {
		t.Errorf("the IdP's error text reached the disclosed message: %q", be.Message)
	}
	if !strings.Contains(be.Detail, "access_denied") {
		t.Errorf("the IdP's error code did not reach the log detail: %q", be.Detail)
	}
}

func TestUserInfoIsMergedOnlyWhenItNamesTheSameSubject(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, func(c *identity.OIDCConfig) { c.UserInfo = true })
	idp.userInfo = map[string]any{
		"sub":    "00u1",
		"groups": []string{"okta-sre", "okta-oncall"},
		"team":   "platform",
	}

	assertion, err := completeOIDCLogin(t, idp, broker)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if assertion.Claims["team"] != "platform" {
		t.Errorf("userinfo claims were not merged: %v", assertion.Claims)
	}
	// The ID TOKEN's groups win: it is the signed document.
	if got := assertion.MultiClaims["groups"]; !slices.Equal(got, []string{"okta-sre", "okta-everyone"}) {
		t.Errorf("userinfo overwrote a signed claim: %v", got)
	}

	// A userinfo response naming somebody else is discarded entirely: it must
	// never authenticate a different person.
	idp.userInfo = map[string]any{"sub": "00u2", "team": "finance"}
	assertion, err = completeOIDCLogin(t, idp, broker)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, ok := assertion.Claims["team"]; ok {
		t.Errorf("a userinfo response for another subject was merged: %v", assertion.Claims)
	}
}

func TestADiscoveryDocumentForAnotherIssuerIsAnOutageAndNotADenial(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, func(c *identity.OIDCConfig) {
		// The configured issuer and the published one disagree, which is
		// either a misconfiguration or a redirect somewhere it should not
		// be. Either way the user did nothing wrong.
		c.Issuer = idp.issuer() + "/other"
	})

	_, err := broker.Begin(context.Background(), identity.BeginRequest{State: "state-1"})
	if err == nil {
		t.Fatal("a mismatched discovery document was accepted")
	}
	if identity.IsBrokerRefusal(err) {
		t.Fatalf("a deployment fault was reported as a refusal: %v", err)
	}
}

func TestABrokerRefusesToBuildWithoutTheFieldsItNeeds(t *testing.T) {
	for name, cfg := range map[string]identity.OIDCConfig{
		"no issuer":    {ClientID: "c", RedirectURI: "https://x"},
		"no client id": {Issuer: "https://i", RedirectURI: "https://x"},
		"no redirect":  {Issuer: "https://i", ClientID: "c"},
	} {
		if _, err := identity.NewOIDCBroker("okta", cfg); err == nil {
			t.Errorf("%s: the broker built anyway", name)
		}
	}
	if _, err := identity.NewOIDCBroker("", identity.OIDCConfig{
		Issuer: "https://i", ClientID: "c", RedirectURI: "https://x",
	}); err == nil {
		t.Error("a nameless connector built")
	}
}

// assertRefusal asserts that err is a deliberate refusal carrying a code, and
// returns it.
func assertRefusal(t *testing.T, err error, code string) *identity.BrokerError {
	t.Helper()
	be, ok := identity.BrokerRefusal(err)
	if !ok {
		t.Fatalf("want refusal %s, got %v", code, err)
	}
	if be.Code != code {
		t.Fatalf("want refusal %s, got %s (%s)", code, be.Code, be.Detail)
	}
	return be
}
