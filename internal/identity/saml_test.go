// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

const (
	samlEntityID    = "https://control.example.com/saml"
	samlACS         = "https://control.example.com/api/v1/session/federated/adfs/acs"
	samlIdPEntityID = "https://idp.example.com/saml"
	samlSSO         = "https://idp.example.com/sso"
)

func samlBroker(t *testing.T, idp *testSAMLIdP, mutate func(*identity.SAMLConfig)) *identity.SAMLBroker {
	t.Helper()
	cfg := identity.SAMLConfig{
		EntityID:        samlEntityID,
		ACSURL:          samlACS,
		IdPEntityID:     samlIdPEntityID,
		IdPSSOURL:       samlSSO,
		IdPCertificates: []string{idp.certificateBase64()},
		LoginAttribute:  "username",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	broker, err := identity.NewSAMLBroker("adfs", cfg)
	if err != nil {
		t.Fatalf("broker: %v", err)
	}
	return broker
}

// completeSAMLLogin drives a whole login and returns the assertion.
func completeSAMLLogin(t *testing.T, idp *testSAMLIdP, broker *identity.SAMLBroker, opts samlResponseOptions) (identity.Assertion, error) {
	t.Helper()
	ctx := context.Background()

	begun, err := broker.Begin(ctx, identity.BeginRequest{State: "state-1"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	response := idp.response(t, samlIdPEntityID, begun.RequestID, samlACS, samlEntityID, opts)

	return broker.Complete(ctx, identity.CompleteRequest{
		Flow: store.FlowState{
			State: "state-1", Connector: "adfs", Kind: store.ConnectorSAML,
			RequestID: begun.RequestID,
		},
		Params: url.Values{"SAMLResponse": {response}, "RelayState": {"state-1"}},
	})
}

func TestASAMLLoginProducesAnIdentityWithItsAttributesAndGroups(t *testing.T) {
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	assertion, err := completeSAMLLogin(t, idp, broker, samlResponseOptions{})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if assertion.Subject != "alice@example.com" {
		t.Errorf("subject (the NameID): %q", assertion.Subject)
	}
	if assertion.Login != "alice" {
		t.Errorf("login: %q", assertion.Login)
	}
	if assertion.Connector != "adfs" {
		t.Errorf("connector: %q", assertion.Connector)
	}
	if assertion.Email != "alice@example.com" || assertion.DisplayName != "Alice Example" {
		t.Errorf("display fields: %q / %q", assertion.Email, assertion.DisplayName)
	}
	if got := assertion.MultiClaims["groups"]; !slices.Equal(got, []string{"okta-sre", "okta-everyone"}) {
		t.Errorf("groups: %v", got)
	}
	if assertion.Claims["department"] != "engineering" {
		t.Errorf("department: %q", assertion.Claims["department"])
	}
}

func TestBothProtocolsProduceTheSameShapeOfAssertion(t *testing.T) {
	// The thing that makes the mapping's guarantee provable: OIDC and SAML
	// hand the layer above the SAME type, so "an unmapped claim cannot
	// influence a decision" is a property of one function rather than of two
	// code paths that happen to agree today.
	oidcIdP := newTestIdP(t, testClientID)
	oidcAssertion, err := completeOIDCLogin(t, oidcIdP, mustOIDC(t, oidcIdP))
	if err != nil {
		t.Fatalf("oidc: %v", err)
	}
	samlIdP := newTestSAMLIdP(t)
	samlAssertion, err := completeSAMLLogin(t, samlIdP, samlBroker(t, samlIdP, nil), samlResponseOptions{})
	if err != nil {
		t.Fatalf("saml: %v", err)
	}

	mapping := mustParse(t, mappingSRE)
	fromOIDC := mapping.Apply(oidcAssertion)
	fromSAML := mapping.Apply(samlAssertion)

	if !slices.Equal(fromOIDC.Groups, fromSAML.Groups) {
		t.Errorf("the two protocols produced different groups: %v / %v", fromOIDC.Groups, fromSAML.Groups)
	}
	for _, name := range []string{"email", "department"} {
		if fromOIDC.Claims[name] != fromSAML.Claims[name] {
			t.Errorf("%s: %q / %q", name, fromOIDC.Claims[name], fromSAML.Claims[name])
		}
	}
	// And the claim nothing maps is dropped on both.
	if _, ok := fromOIDC.Claims["role"]; ok {
		t.Error("an unmapped claim survived the OIDC path")
	}
	if _, ok := fromSAML.Claims["role"]; ok {
		t.Error("an unmapped claim survived the SAML path")
	}
}

func mustOIDC(t *testing.T, idp *testIdP) *identity.OIDCBroker {
	t.Helper()
	broker, _ := oidcBroker(t, idp, nil)
	return broker
}

func TestAnUnsignedAssertionIsRefusedUnderEveryConfiguration(t *testing.T) {
	// There is no setting under which this server accepts an unsigned
	// assertion, which is why the broker refuses to build without a signing
	// certificate at all.
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	_, err := completeSAMLLogin(t, idp, broker, samlResponseOptions{Unsigned: true})
	assertRefusal(t, err, identity.RejectFederationSignature)

	if _, err := identity.NewSAMLBroker("adfs", identity.SAMLConfig{
		EntityID: samlEntityID, ACSURL: samlACS, IdPSSOURL: samlSSO,
	}); err == nil {
		t.Fatal("a connector with no signing certificate built")
	}
}

func TestAnAssertionSignedByAnotherIdPIsRefused(t *testing.T) {
	idp := newTestSAMLIdP(t)
	other := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	_, err := completeSAMLLogin(t, idp, broker, samlResponseOptions{SignWith: other})
	assertRefusal(t, err, identity.RejectFederationSignature)
}

func TestAnAssertionAnsweringADifferentRequestIsRefused(t *testing.T) {
	// InResponseTo is what makes an assertion nobody asked for — an
	// IdP-initiated login, or a replayed one — impossible to complete here.
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	_, err := completeSAMLLogin(t, idp, broker, samlResponseOptions{InResponseTo: "_somebody-elses-request"})
	assertRefusal(t, err, identity.RejectFederationState)
}

func TestAnExpiredAssertionIsRefused(t *testing.T) {
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, func(c *identity.SAMLConfig) { c.ClockSkew = time.Second })

	_, err := completeSAMLLogin(t, idp, broker, samlResponseOptions{
		NotOnOrAfter: time.Now().Add(-time.Hour),
	})
	if _, ok := identity.BrokerRefusal(err); !ok {
		t.Fatalf("an expired assertion was not refused: %v", err)
	}
}

func TestAnAssertionForAnotherAudienceIsRefused(t *testing.T) {
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	_, err := completeSAMLLogin(t, idp, broker, samlResponseOptions{Audience: "https://somebody-else"})
	assertRefusal(t, err, identity.RejectFederationAudience)
}

func TestAResponseForAnotherDestinationIsRefused(t *testing.T) {
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	_, err := completeSAMLLogin(t, idp, broker, samlResponseOptions{
		Destination: "https://somebody-else/acs",
	})
	assertRefusal(t, err, identity.RejectFederationAudience)
}

func TestAnAssertionFromAnotherIssuerIsRefused(t *testing.T) {
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)
	ctx := context.Background()

	begun, err := broker.Begin(ctx, identity.BeginRequest{State: "state-1"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	response := idp.response(t, "https://evil.example.com", begun.RequestID, samlACS, samlEntityID,
		samlResponseOptions{})

	_, err = broker.Complete(ctx, identity.CompleteRequest{
		Flow:   store.FlowState{State: "state-1", Connector: "adfs", RequestID: begun.RequestID},
		Params: url.Values{"SAMLResponse": {response}},
	})
	assertRefusal(t, err, identity.RejectFederationAssertion)
}

func TestACallbackWithNoResponseIsRefused(t *testing.T) {
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	_, err := broker.Complete(context.Background(), identity.CompleteRequest{
		Flow:   store.FlowState{State: "state-1", Connector: "adfs", RequestID: "_r"},
		Params: url.Values{},
	})
	assertRefusal(t, err, identity.RejectFederationProtocol)
}

func TestTheAuthnRequestNamesThisServerAndCarriesARequestID(t *testing.T) {
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	begun, err := broker.Begin(context.Background(), identity.BeginRequest{State: "state-1"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if !strings.HasPrefix(begun.RequestID, "_") {
		t.Errorf("a SAML ID is an XML ID and must not start with a digit: %q", begun.RequestID)
	}

	u, err := url.Parse(begun.RedirectURL)
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}
	if u.Query().Get("RelayState") != "state-1" {
		t.Errorf("RelayState: %q", u.Query().Get("RelayState"))
	}
	request := inflateSAMLRequest(t, u.Query().Get("SAMLRequest"))
	for _, want := range []string{samlEntityID, samlACS, begun.RequestID, "HTTP-POST"} {
		if !strings.Contains(request, want) {
			t.Errorf("the AuthnRequest does not carry %q:\n%s", want, request)
		}
	}
}

func TestTheServiceProviderMetadataSaysWhatThisServerRequires(t *testing.T) {
	idp := newTestSAMLIdP(t)
	broker := samlBroker(t, idp, nil)

	contentType, body, err := broker.Metadata(context.Background())
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if contentType != "application/samlmetadata+xml" {
		t.Errorf("content type: %q", contentType)
	}
	// Both are facts about this implementation rather than settings.
	for _, want := range []string{`WantAssertionsSigned="true"`, `AuthnRequestsSigned="false"`, samlACS, samlEntityID} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metadata does not carry %q:\n%s", want, body)
		}
	}
}

func TestAnOIDCConnectorPublishesNoMetadata(t *testing.T) {
	idp := newTestIdP(t, testClientID)
	broker, _ := oidcBroker(t, idp, nil)
	contentType, body, err := broker.Metadata(context.Background())
	if err != nil || contentType != "" || body != nil {
		t.Fatalf("an OIDC connector published metadata: %q %q %v", contentType, body, err)
	}
}

// inflateSAMLRequest reverses the HTTP-Redirect binding's encoding.
func inflateSAMLRequest(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("SAMLRequest is not base64: %v", err)
	}
	r := flate.NewReader(bytes.NewReader(raw))
	defer func() { _ = r.Close() }()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("SAMLRequest is not raw DEFLATE: %v", err)
	}
	return string(out)
}
