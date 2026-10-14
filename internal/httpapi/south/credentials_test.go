// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/south"
	"github.com/hoplock/control/internal/store"
)

// POST /v1/credentials/certificate (Hoplock/proxy#68): one certificate per
// session, bound to a decision this server made, over exactly the key the proxy
// sent — and a 401 through the third denier, a 503 without an authority, and a
// 5xx for anything this server suffered.

// certBundle adds two brokered-certificate routes to the authorize fixture: one
// whose bound is below the CA's default, and one whose bound is above it.
const certBundle = authorizeBundle + `
  - id: certificate
    effect: allow
    match:
      target:
        hostnames: [cert-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-certificate
          username: netadmin
          key_type: ed25519
          lifetime_seconds: 120
  - id: long-certificate
    effect: allow
    match:
      target:
        hostnames: [long-host.example.com]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-certificate
          username: netadmin
          lifetime_seconds: 7200
  - id: nobody
    effect: deny
    reason: nobody reaches this
    match:
      target:
        hostnames: [denied-host.example.com]
`

// testKEK is a TEST key-encryption key. It encrypts nothing outside a test
// schema that is dropped when the test ends.
var testKEK = []byte("0123456789abcdef0123456789abcdef")

// certHarness is the south harness with a certificate authority and the
// certificate policy active.
func certHarness(t *testing.T, ensure bool) (*harness, *credential.CA) {
	t.Helper()
	h := newHarness(t)
	keys, err := extdefault.NewSoftwareKeyStore(h.store, testKEK)
	if err != nil {
		t.Fatalf("key store: %v", err)
	}
	ca, err := credential.New(h.store, keys, credential.WithValidity(5*time.Minute),
		credential.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	h.rebuild(t, func(o *south.Options) { o.CA = ca })
	seedAuthorize(t, h)
	ctx := t.Context()
	for _, tgt := range []store.Target{
		{ID: "t-cert", Hostname: "cert-host.example.com", Zone: "edge"},
		{ID: "t-long", Hostname: "long-host.example.com", Zone: "edge"},
	} {
		if err := h.store.Targets().Upsert(ctx, testTenant, tgt); err != nil {
			t.Fatalf("seed target: %v", err)
		}
	}
	version, err := h.store.PolicyBundles().NextVersion(ctx, testTenant)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.PolicyBundles().Insert(ctx, testTenant, store.PolicyBundle{
		Version: version, Source: []byte(certBundle), Hash: "sha256:cert-fixture", UploadedBy: "south_test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.PolicyBundles().Activate(ctx, testTenant, version); err != nil {
		t.Fatal(err)
	}
	if ensure {
		if _, err := ca.Ensure(ctx, testTenant); err != nil {
			t.Fatalf("ensure: %v", err)
		}
	}
	return h, ca
}

// decisionFor authorizes a target from proxy-1 and returns the decision id.
func decisionFor(t *testing.T, h *harness, target string) string {
	t.Helper()
	req := authorizeRequest(target, "proxy-1")
	resp := decodeAuthorize(t, h.post(t, contract.PathAuthorize, req))
	if resp.DecisionID == "" {
		t.Fatal("the authorize answer carries no decision_id")
	}
	return resp.DecisionID
}

// sessionKey stands in for the key pair the proxy generates per session.
func sessionKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(ssh.MarshalAuthorizedKey(sshPub)), sshPub
}

func issue(t *testing.T, h *harness, req contract.CertificateRequest) *httptest.ResponseRecorder {
	t.Helper()
	return h.post(t, contract.PathCredentialCertificate, req)
}

func TestAnIssuanceSignsExactlyTheSubmittedKey(t *testing.T) {
	t.Parallel()
	h, _ := certHarness(t, true)
	decisionID := decisionFor(t, h, "cert-host.example.com")
	line, key := sessionKey(t)

	res := issue(t, h, contract.CertificateRequest{
		SessionID: "sess-a", DecisionID: decisionID, Target: "cert-host.example.com",
		Username: "netadmin", PublicKey: line,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("issue = %d: %s", res.Code, res.Body)
	}
	// serial is a decimal STRING, never a JSON number.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(res.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw["serial"]) == 0 || raw["serial"][0] != '"' {
		t.Fatalf("serial = %s, want a JSON string", raw["serial"])
	}
	var out contract.CertificateResponse
	decode(t, res, &out)

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(out.Certificate))
	if err != nil {
		t.Fatalf("certificate does not parse: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatal("the answer is not a certificate")
	}
	if cert.CertType != ssh.UserCert {
		t.Errorf("cert type = %d, want a user certificate", cert.CertType)
	}
	if string(cert.Key.Marshal()) != string(key.Marshal()) {
		t.Error("the certificate is over a key other than the one submitted")
	}
	if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != "netadmin" {
		t.Errorf("principals = %v, want exactly the route's username", cert.ValidPrincipals)
	}
	if out.Serial != strconv.FormatUint(cert.Serial, 10) {
		t.Errorf("serial = %q, the certificate's own is %d", out.Serial, cert.Serial)
	}
	validBefore, err := time.Parse(time.RFC3339, out.ValidBefore)
	if err != nil {
		t.Fatalf("valid_before %q: %v", out.ValidBefore, err)
	}
	if !validBefore.Equal(time.Unix(int64(cert.ValidBefore), 0)) {
		t.Errorf("valid_before = %s, the certificate's own is %d", validBefore, cert.ValidBefore)
	}
	if !validBefore.After(time.Now()) {
		t.Errorf("valid_before %s is not in the future", validBefore)
	}
	// The route bounds it at 120s, below the CA's 5m default: the shorter wins.
	if validBefore.After(time.Now().Add(120*time.Second + 30*time.Second)) {
		t.Errorf("valid_before %s exceeds the route's 120s bound", validBefore)
	}
	if len(out.CAPublicKeys) == 0 {
		t.Error("ca_public_keys is empty; the tenant's bundle should be read per call")
	}
}

// One issuance per session, and nothing answers one from memory: two calls
// citing one cached decision are two certificates, two serials and two rows.
func TestTwoIssuancesForOneDecisionAreTwoCertificates(t *testing.T) {
	t.Parallel()
	h, _ := certHarness(t, true)
	decisionID := decisionFor(t, h, "cert-host.example.com")

	serials := map[string]bool{}
	for _, session := range []string{"sess-1", "sess-2"} {
		line, _ := sessionKey(t)
		res := issue(t, h, contract.CertificateRequest{
			SessionID: session, DecisionID: decisionID, Target: "cert-host.example.com",
			Username: "netadmin", PublicKey: line,
		})
		if res.Code != http.StatusOK {
			t.Fatalf("issue = %d: %s", res.Code, res.Body)
		}
		var out contract.CertificateResponse
		decode(t, res, &out)
		serials[out.Serial] = true
	}
	if len(serials) != 2 {
		t.Fatalf("two issuances produced %d serials", len(serials))
	}
	rows, err := h.store.SSHCertificates().ListBySubject(t.Context(), testTenant, "alice@example.com", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows written, want 2", len(rows))
	}
	sessions := map[string]bool{rows[0].SessionID: true, rows[1].SessionID: true}
	if !sessions["sess-1"] || !sessions["sess-2"] {
		t.Errorf("rows record sessions %v, want the two the requests named", sessions)
	}
}

// A route's bound above the CA's default does not lengthen the certificate.
func TestARouteBoundAboveTheDefaultDoesNotLengthenIt(t *testing.T) {
	t.Parallel()
	h, _ := certHarness(t, true)
	decisionID := decisionFor(t, h, "long-host.example.com")
	line, _ := sessionKey(t)
	res := issue(t, h, contract.CertificateRequest{
		SessionID: "sess-long", DecisionID: decisionID, Target: "long-host.example.com",
		Username: "netadmin", PublicKey: line,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("issue = %d: %s", res.Code, res.Body)
	}
	var out contract.CertificateResponse
	decode(t, res, &out)
	validBefore, _ := time.Parse(time.RFC3339, out.ValidBefore)
	if validBefore.After(time.Now().Add(5*time.Minute + time.Minute)) {
		t.Errorf("valid_before %s: a 2h route bound lengthened a 5m certificate", validBefore)
	}
}

func TestARefusalToMintIs401ThroughItsOwnCode(t *testing.T) {
	t.Parallel()
	h, _ := certHarness(t, true)
	ctx := t.Context()
	certDecision := decisionFor(t, h, "cert-host.example.com")
	plainDecision := decisionFor(t, h, "edge-host.example.com")

	denyReq := authorizeRequest("denied-host.example.com", "proxy-1")
	denyReq.Conn.SessionID = "sess-denied"
	if res := h.post(t, contract.PathAuthorize, denyReq); res.Code != http.StatusUnauthorized {
		t.Fatalf("the denied target answered %d", res.Code)
	}
	denied, err := h.store.Decisions().ListBySession(ctx, testTenant, "sess-denied", 1)
	if err != nil || len(denied) != 1 {
		t.Fatalf("the denial's record: %v (%d)", err, len(denied))
	}

	line, _ := sessionKey(t)
	for _, tc := range []struct {
		name, code string
		req        contract.CertificateRequest
	}{
		{"a decision this server never made", contract.ErrCodeUnknownDecision, contract.CertificateRequest{
			DecisionID: "dec-never", Target: "cert-host.example.com", Username: "netadmin"}},
		{"no decision at all", contract.ErrCodeUnknownDecision, contract.CertificateRequest{
			Target: "cert-host.example.com", Username: "netadmin"}},
		{"a decision that denied", contract.ErrCodeUnknownDecision, contract.CertificateRequest{
			DecisionID: denied[0].ID, Target: "denied-host.example.com", Username: "netadmin"}},
		{"a decision naming no certificate rung", contract.ErrCodeUnknownDecision, contract.CertificateRequest{
			DecisionID: plainDecision, Target: "edge-host.example.com", Username: "alice"}},
		{"a target the decision did not name", contract.ErrCodeDecisionMismatch, contract.CertificateRequest{
			DecisionID: certDecision, Target: "edge-host.example.com", Username: "netadmin"}},
		{"an account the decision did not name", contract.ErrCodeDecisionMismatch, contract.CertificateRequest{
			DecisionID: certDecision, Target: "cert-host.example.com", Username: "root"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.SessionID = "sess-x"
			tc.req.PublicKey = line
			res := issue(t, h, tc.req)
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("issue = %d, want 401: %s", res.Code, res.Body)
			}
			h.requireEnvelope(t, res)
			var env contract.ErrorResponse
			decode(t, res, &env)
			if env.Error.Code != tc.code {
				t.Errorf("code = %q, want %q", env.Error.Code, tc.code)
			}
		})
	}
}

func TestAMalformedIssuanceIs400(t *testing.T) {
	t.Parallel()
	h, _ := certHarness(t, true)
	decisionID := decisionFor(t, h, "cert-host.example.com")
	line, _ := sessionKey(t)
	for name, req := range map[string]contract.CertificateRequest{
		"no session": {DecisionID: decisionID, Target: "cert-host.example.com", Username: "netadmin", PublicKey: line},
		"no key":     {SessionID: "s", DecisionID: decisionID, Target: "cert-host.example.com", Username: "netadmin"},
		"a key that is not one": {SessionID: "s", DecisionID: decisionID, Target: "cert-host.example.com",
			Username: "netadmin", PublicKey: "ssh-ed25519 not-base64"},
	} {
		if res := issue(t, h, req); res.Code != http.StatusBadRequest {
			t.Errorf("%s: issue = %d, want 400: %s", name, res.Code, res.Body)
		}
	}
}

// No authority is an operator's fix: 503, never a deny and never a 500.
func TestNoAuthorityIs503(t *testing.T) {
	t.Parallel()

	t.Run("a tenant with no CA", func(t *testing.T) {
		h, _ := certHarness(t, false)
		decisionID := decisionFor(t, h, "cert-host.example.com")
		line, _ := sessionKey(t)
		res := issue(t, h, contract.CertificateRequest{SessionID: "s", DecisionID: decisionID,
			Target: "cert-host.example.com", Username: "netadmin", PublicKey: line})
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("issue = %d, want 503: %s", res.Code, res.Body)
		}
		var env contract.ErrorResponse
		decode(t, res, &env)
		if env.Error.Code != contract.ErrCodeNoAuthority {
			t.Errorf("code = %q, want %q", env.Error.Code, contract.ErrCodeNoAuthority)
		}
	})

	t.Run("a deployment with no CA", func(t *testing.T) {
		h := newHarness(t)
		line, _ := sessionKey(t)
		res := issue(t, h, contract.CertificateRequest{SessionID: "s", DecisionID: "d",
			Target: "cert-host.example.com", Username: "netadmin", PublicKey: line})
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("issue = %d, want 503: %s", res.Code, res.Body)
		}
	})
}

// A store failure reading the decision is an outage, never a refusal to mint.
func TestAStoreFailureOnIssuanceIsA5xxAndNeverA401(t *testing.T) {
	t.Parallel()
	h, _ := certHarness(t, true)
	decisionID := decisionFor(t, h, "cert-host.example.com")
	if _, err := h.store.Pool().Exec(t.Context(), `ALTER TABLE decisions RENAME TO decisions_gone`); err != nil {
		t.Fatal(err)
	}
	line, _ := sessionKey(t)
	res := issue(t, h, contract.CertificateRequest{SessionID: "s", DecisionID: decisionID,
		Target: "cert-host.example.com", Username: "netadmin", PublicKey: line})
	if res.Code < 500 {
		t.Fatalf("issue = %d, want a 5xx: a store failure is not a decision (M11)", res.Code)
	}
}
