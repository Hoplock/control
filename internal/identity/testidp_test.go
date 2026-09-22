// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

// A LOCAL TEST IdP, for both protocols.
//
// The acceptance criterion is that the OIDC and SAML flows are tested against a
// local test IdP producing an identity whose groups and claims match
// expectations — so the IdP is a real one: it signs, it publishes a key set, it
// redeems a code, and it can be told to sign the wrong thing so that the
// refusals are tested against the same machinery as the successes.

// testIdP is an OpenID Provider over httptest.
type testIdP struct {
	t        *testing.T
	server   *httptest.Server
	key      *rsa.PrivateKey
	clientID string

	// codes maps an authorization code to the nonce the flow carried, so the
	// token endpoint can echo it.
	codes map[string]string
	// lastChallenge records the PKCE challenge the authorization request
	// carried, so a test can assert it was sent.
	lastChallenge string
	lastVerifier  string
	// userInfo, when set, is served from the userinfo endpoint.
	userInfo map[string]any
	// overrideClaims lets a test bend one claim of the next id_token.
	overrideClaims func(map[string]any)
	// signWith, when set, signs with a different key than the one published.
	signWith *rsa.PrivateKey
	// forgeAlg, when set, replaces the JWS header's alg.
	forgeAlg string
}

func newTestIdP(t *testing.T, clientID string) *testIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("test idp key: %v", err)
	}
	idp := &testIdP{t: t, key: key, clientID: clientID, codes: map[string]string{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", idp.discovery)
	mux.HandleFunc("/jwks", idp.jwks)
	mux.HandleFunc("/authorize", idp.authorize)
	mux.HandleFunc("/token", idp.token)
	mux.HandleFunc("/userinfo", idp.userinfoHandler)
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (i *testIdP) issuer() string { return i.server.URL }

func (i *testIdP) discovery(w http.ResponseWriter, _ *http.Request) {
	writeTestJSON(w, map[string]string{
		"issuer":                 i.server.URL,
		"authorization_endpoint": i.server.URL + "/authorize",
		"token_endpoint":         i.server.URL + "/token",
		"jwks_uri":               i.server.URL + "/jwks",
		"userinfo_endpoint":      i.server.URL + "/userinfo",
	})
}

func (i *testIdP) jwks(w http.ResponseWriter, _ *http.Request) {
	writeTestJSON(w, map[string]any{"keys": []any{map[string]string{
		"kty": "RSA",
		"kid": "test-1",
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(i.key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(i.key.E)).Bytes()),
	}}})
}

// authorize records what the broker asked for and hands back a code. It is a
// stand-in for the browser leg: the test drives it directly.
func (i *testIdP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	i.lastChallenge = q.Get("code_challenge")
	code := "code-" + q.Get("state")
	i.codes[code] = q.Get("nonce")
	http.Redirect(w, r, q.Get("redirect_uri")+"?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(q.Get("state")),
		http.StatusFound)
}

func (i *testIdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	i.lastVerifier = r.PostForm.Get("code_verifier")
	nonce, ok := i.codes[r.PostForm.Get("code")]
	if !ok {
		writeTestJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	writeTestJSON(w, map[string]string{
		"id_token":     i.idToken(nonce),
		"access_token": "access-token",
		"token_type":   "Bearer",
	})
}

func (i *testIdP) userinfoHandler(w http.ResponseWriter, _ *http.Request) {
	if i.userInfo == nil {
		http.Error(w, "no userinfo", http.StatusNotFound)
		return
	}
	writeTestJSON(w, i.userInfo)
}

// idToken mints an ID token with the claims a test expects to see mapped.
func (i *testIdP) idToken(nonce string) string {
	now := time.Now()
	claims := map[string]any{
		"iss":                i.server.URL,
		"sub":                "00u1",
		"aud":                i.clientID,
		"exp":                now.Add(5 * time.Minute).Unix(),
		"iat":                now.Unix(),
		"nonce":              nonce,
		"email":              "alice@example.com",
		"name":               "Alice Example",
		"preferred_username": "alice",
		"department":         "engineering",
		// The claim that must NOT reach policy: nothing maps it.
		"role":   "admin",
		"groups": []string{"okta-sre", "okta-everyone"},
	}
	if i.overrideClaims != nil {
		i.overrideClaims(claims)
	}
	return i.sign(claims)
}

func (i *testIdP) sign(claims map[string]any) string {
	header := map[string]string{"alg": "RS256", "kid": "test-1", "typ": "JWT"}
	if i.forgeAlg != "" {
		header["alg"] = i.forgeAlg
	}
	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(claims)

	signed := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON)

	key := i.key
	if i.signWith != nil {
		key = i.signWith
	}
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		i.t.Fatalf("test idp sign: %v", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// login drives the whole browser leg and returns the callback's parameters.
func (i *testIdP) login(t *testing.T, redirectURL string) url.Values {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(redirectURL)
	if err != nil {
		t.Fatalf("authorization request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorization answered %d, want a redirect", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("callback location: %v", err)
	}
	return location.Query()
}

func writeTestJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// ---------------------------------------------------------------------------
// the SAML half
// ---------------------------------------------------------------------------

// testSAMLIdP mints signed SAML responses.
type testSAMLIdP struct {
	t    *testing.T
	key  *rsa.PrivateKey
	cert *x509.Certificate
	der  []byte
}

func newTestSAMLIdP(t *testing.T) *testSAMLIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("saml idp key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-saml-idp"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("saml idp certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("saml idp certificate parse: %v", err)
	}
	return &testSAMLIdP{t: t, key: key, cert: cert, der: der}
}

// certificateBase64 is what a connector document carries.
func (s *testSAMLIdP) certificateBase64() string {
	return base64.StdEncoding.EncodeToString(s.der)
}

// samlResponseOptions bends one part of a response so a refusal can be tested
// against the same machinery as a success.
type samlResponseOptions struct {
	InResponseTo string
	Destination  string
	Audience     string
	NotOnOrAfter time.Time
	NameID       string
	Groups       []string
	Unsigned     bool
	// SignWith, when set, signs with a key the SP does not trust.
	SignWith *testSAMLIdP
}

func (o samlResponseOptions) orDefaults(requestID, acs, entityID string) samlResponseOptions {
	if o.InResponseTo == "" {
		o.InResponseTo = requestID
	}
	if o.Destination == "" {
		o.Destination = acs
	}
	if o.Audience == "" {
		o.Audience = entityID
	}
	if o.NotOnOrAfter.IsZero() {
		o.NotOnOrAfter = time.Now().Add(5 * time.Minute)
	}
	if o.NameID == "" {
		o.NameID = "alice@example.com"
	}
	if o.Groups == nil {
		o.Groups = []string{"okta-sre", "okta-everyone"}
	}
	return o
}

// response builds a SAML Response with a signed Assertion.
func (s *testSAMLIdP) response(t *testing.T, issuer, requestID, acs, entityID string, opts samlResponseOptions) string {
	t.Helper()
	opts = opts.orDefaults(requestID, acs, entityID)

	var groups strings.Builder
	for _, g := range opts.Groups {
		fmt.Fprintf(&groups, `<AttributeValue>%s</AttributeValue>`, g)
	}

	assertion := fmt.Sprintf(`<Assertion xmlns="urn:oasis:names:tc:SAML:2.0:assertion" ID="_assert1" Version="2.0" IssueInstant="%s">`+
		`<Issuer>%s</Issuer>`+
		`<Subject><NameID>%s</NameID>`+
		`<SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">`+
		`<SubjectConfirmationData InResponseTo="%s" Recipient="%s" NotOnOrAfter="%s"/>`+
		`</SubjectConfirmation></Subject>`+
		`<Conditions NotBefore="%s" NotOnOrAfter="%s">`+
		`<AudienceRestriction><Audience>%s</Audience></AudienceRestriction></Conditions>`+
		`<AttributeStatement>`+
		`<Attribute Name="email"><AttributeValue>alice@example.com</AttributeValue></Attribute>`+
		`<Attribute Name="displayName"><AttributeValue>Alice Example</AttributeValue></Attribute>`+
		`<Attribute Name="department"><AttributeValue>engineering</AttributeValue></Attribute>`+
		`<Attribute Name="role"><AttributeValue>admin</AttributeValue></Attribute>`+
		`<Attribute Name="username"><AttributeValue>alice</AttributeValue></Attribute>`+
		`<Attribute Name="groups">%s</Attribute>`+
		`</AttributeStatement></Assertion>`,
		rfc3339(time.Now()), issuer, opts.NameID, opts.InResponseTo, opts.Destination,
		rfc3339(opts.NotOnOrAfter), rfc3339(time.Now().Add(-time.Minute)), rfc3339(opts.NotOnOrAfter),
		opts.Audience, groups.String())

	if !opts.Unsigned {
		signer := s
		if opts.SignWith != nil {
			signer = opts.SignWith
		}
		assertion = signer.signXML(t, assertion)
	}

	response := fmt.Sprintf(`<?xml version="1.0"?><Response xmlns="urn:oasis:names:tc:SAML:2.0:protocol" `+
		`ID="_resp1" Version="2.0" IssueInstant="%s" Destination="%s" InResponseTo="%s">`+
		`<Status><StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/></Status>`+
		`%s</Response>`,
		rfc3339(time.Now()), opts.Destination, opts.InResponseTo, assertion)
	return base64.StdEncoding.EncodeToString([]byte(response))
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// signXML wraps an element in an enveloped XML signature.
//
// It uses the same library the broker verifies with, which is the point: a test
// that hand-rolled canonicalisation would be testing its own bug rather than
// the broker's behaviour.
func (s *testSAMLIdP) signXML(t *testing.T, element string) string {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromString(element); err != nil {
		t.Fatalf("test saml assertion does not parse: %v", err)
	}
	ctx := dsig.NewDefaultSigningContext(testKeyStore{key: s.key, cert: s.der})
	signed, err := ctx.SignEnveloped(doc.Root())
	if err != nil {
		t.Fatalf("test saml signature: %v", err)
	}
	out := etree.NewDocument()
	out.SetRoot(signed)
	text, err := out.WriteToString()
	if err != nil {
		t.Fatalf("test saml serialise: %v", err)
	}
	return text
}

// testKeyStore hands goxmldsig the signing key.
type testKeyStore struct {
	key  *rsa.PrivateKey
	cert []byte
}

func (k testKeyStore) GetKeyPair() (*rsa.PrivateKey, []byte, error) { return k.key, k.cert, nil }
