// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/httpapi/south"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

const (
	testTenant   = store.Tenant("acme")
	testSecret   = "conformance-dev-secret"
	alicePass    = "alice-dev-password"
	aliceKeyBlob = "alice public key material"
	proxyKeyBlob = "proxy-2 public key material"
)

// ---------------------------------------------------------------------------
// M2: this listener serves the contract and nothing else
// ---------------------------------------------------------------------------

// The day somebody mounts an admin route on the wrong mux is not the day
// anyone notices, so the route table is asserted rather than sampled.
func TestTheListenerServesExactlyTheContractPathsThisPhaseImplements(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	want := []string{
		contract.PathAuthCert,
		contract.PathAuthMFAPoll,
		contract.PathAuthPassword,
		contract.PathCapabilitiesReport,
		contract.PathHostKeyReport,
		contract.PathUIDLease,
	}
	got := h.server.Routes()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("routes = %v, want exactly %v — a route added here must be one the contract defines "+
			"and this phase implements (M2)", got, want)
	}
}

// A north-bound path must not be reachable here whatever credential is
// presented. A proxy token that reaches a policy-authoring endpoint is
// privilege escalation from "can ask about decisions" to "can author them".
func TestNoNorthBoundRouteIsReachable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for _, path := range []string{
		"/v1/policy/bundles",
		"/v1/policy/bundles/1/activate",
		"/api/v1/proxies",
		"/api/v1/grants",
		"/admin",
		"/metrics",
		"/debug/pprof/",
		"/debug/logs",
		"/ui/",
		"/",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			res := h.do(method, path, nil, h.token())
			if res.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404: the south-bound listener serves the contract only",
					method, path, res.Code)
			}
			h.requireEnvelope(t, res)
		}
	}
}

// Every path refuses a bad credential the same way, INCLUDING the ones this
// phase does not serve. One route answering something else to a token another
// rejects is how a listener ends up with an unauthenticated corner.
func TestEveryPathRefusesABadCredentialIdentically(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	paths := append(slices.Clone(h.server.Routes()),
		contract.PathAuthorize, contract.PathLogsBatch, "/admin", "/")
	for _, path := range paths {
		for _, token := range []string{"", "not-a-token", testTenant.String() + ".wrong-secret"} {
			res := h.do(http.MethodPost, path, []byte(`{}`), token)
			if res.Code != http.StatusUnauthorized {
				t.Errorf("POST %s with token %q = %d, want 401", path, token, res.Code)
			}
			h.requireEnvelope(t, res)
		}
	}
}

// ---------------------------------------------------------------------------
// M11: exactly one path produces a 401, and it is a deliberate deny
// ---------------------------------------------------------------------------

// The regression test M11 exists for, and the one the prompt says is easiest
// to lose. A database failure on the auth path must answer 5xx.
//
// The failure is injected by closing the pool the server reads through, which
// is as close to a real outage as a test gets: every query fails the way a
// Postgres failover makes them fail.
func TestADatabaseFailureOnTheAuthPathIsA5xxAndNeverA401(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// The same calls succeed first, so the assertions below are about the
	// failure rather than about a request that was always going to be
	// refused.
	if got := h.authCert(t, "alice", identity.KeyFingerprint([]byte(aliceKeyBlob))).Code; got != 200 {
		t.Fatalf("cert auth before the failure = %d, want 200", got)
	}

	h.store.Close()

	for _, tc := range []struct {
		name string
		res  *httptest.ResponseRecorder
	}{
		{"cert", h.authCert(t, "alice", identity.KeyFingerprint([]byte(aliceKeyBlob)))},
		{"password", h.authPassword(t, "alice", alicePass)},
		{"mfa poll", h.post(t, contract.PathAuthMFAPoll, contract.MFAPollRequest{Token: "anything"})},
		{"host key", h.post(t, contract.PathHostKeyReport, contract.HostKeyReportRequest{
			Target: "t", HostKey: contract.PublicKeyMaterial{Fingerprint: "SHA256:x"},
		})},
		{"capabilities", h.post(t, contract.PathCapabilitiesReport, contract.CapabilityReportRequest{
			Target: "t", Capabilities: contract.TargetCapabilities{ObservedAt: nowRFC3339()},
		})},
		{"uid lease", h.post(t, contract.PathUIDLease, contract.UIDLeaseRequest{
			ProxyID: "proxy-1", Target: "t", UIDCount: 16, RangeMin: 2_000_000, RangeMax: 2_009_999,
		})},
	} {
		if tc.res.Code == http.StatusUnauthorized {
			t.Errorf("%s answered 401 during a database outage: the proxy would tell a real user "+
				"access was denied and send the operator to debug permissions (M11)", tc.name)
			continue
		}
		if tc.res.Code < 500 {
			t.Errorf("%s answered %d during a database outage, want a 5xx", tc.name, tc.res.Code)
		}
		h.requireEnvelope(t, tc.res)
	}
}

// The mapper is the only place a status code is chosen, so the mapping is
// asserted directly as well as through the wire.
func TestOnlyADeliberateDenyMapsTo401(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"a deliberate deny", contract.Denied("nope"), http.StatusUnauthorized},
		{"a malformed request", contract.Invalid("nope"), http.StatusBadRequest},
		{"an exhausted range", contract.Exhausted("nope"), http.StatusConflict},
		{"an unserveable vocabulary", contract.VersionUnsupported("nope"), http.StatusInternalServerError},
		{"a bare error", errors.New("connection refused"), http.StatusInternalServerError},
		{"a wrapped error", fmt.Errorf("outer: %w", errors.New("inner")), http.StatusInternalServerError},
		{"a cancelled context", context.Canceled, http.StatusInternalServerError},
		{"a deadline", context.DeadlineExceeded, http.StatusInternalServerError},
		{"an unclassified StatusError", &contract.StatusError{Message: "?"}, http.StatusInternalServerError},
		{"a store failure", fmt.Errorf("query: %w", store.ErrUnavailable), http.StatusInternalServerError},
		{"a store absence", fmt.Errorf("row: %w", store.ErrNotFound), http.StatusInternalServerError},
	} {
		if got := south.StatusCodeFor(tc.err); got != tc.want {
			t.Errorf("%s mapped to %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A panic is an outage, and it answers the contract's envelope rather than a
// closed connection: a caller that got neither cannot tell an outage from a
// network fault, and the proxy's fail-closed rule then has nothing to
// classify.
func TestAPanicAnswersA5xxWithTheEnvelope(t *testing.T) {
	t.Parallel()
	h := newHarnessWithFleet(t, panickingFleet{})

	res := h.do(http.MethodPost, contract.PathAuthCert, mustJSON(contract.AuthenticateCertRequest{
		Login: "alice", PublicKey: contract.PublicKeyMaterial{Fingerprint: "SHA256:x"},
	}), h.token())
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("a panicking handler answered %d, want 500", res.Code)
	}
	h.requireEnvelope(t, res)
	if res.Header().Get(south.CorrelationHeader) == "" {
		t.Error("a 500 carries no correlation id, so the operator cannot find the panic in the log")
	}
}

// ---------------------------------------------------------------------------
// PLAN §7: the password exists only in transit
// ---------------------------------------------------------------------------

// Asserted against captured output rather than by inspection, because the
// failure this prevents is a log line somebody adds later.
func TestThePasswordAppearsInNoLogLineOrAnswer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Both halves: the password that works and the one that does not. A
	// server that logs only failures still logs passwords.
	cases := []struct {
		login, password string
	}{
		{"alice", alicePass},
		{"alice", "a-wrong-password-nobody-should-see"},
		{"nobody", "another-wrong-password"},
	}
	for _, c := range cases {
		res := h.authPassword(t, c.login, c.password)
		if strings.Contains(res.Body.String(), c.password) {
			t.Fatalf("the response body echoed the password for %q", c.login)
		}
	}

	logged := h.logs.String()
	for _, c := range cases {
		if strings.Contains(logged, c.password) {
			t.Fatalf("the password for %q appears in the server's own log output:\n%s", c.login, logged)
		}
	}
	if !strings.Contains(logged, "southbound_request") {
		t.Fatal("no request was logged at all, so this test would pass against a server that logs nothing")
	}

	// Nor is it stored. The credential table holds a digest; nothing else
	// holds the plaintext.
	digest, err := h.store.SubjectPasswords().Get(t.Context(), testTenant, "alice@example.com")
	if err != nil {
		t.Fatalf("read the digest back: %v", err)
	}
	if strings.Contains(string(digest.Digest), alicePass) {
		t.Fatal("the stored digest contains the password")
	}
}

// ---------------------------------------------------------------------------
// authentication over the wire
// ---------------------------------------------------------------------------

func TestCertificateAuthenticationNamesAnIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.authCert(t, "alice", identity.KeyFingerprint([]byte(aliceKeyBlob)))
	if res.Code != http.StatusOK {
		t.Fatalf("cert auth = %d: %s", res.Code, res.Body)
	}
	var got contract.AuthenticateResponse
	decode(t, res, &got)
	if got.Status != contract.AuthStatusAuthenticated {
		t.Fatalf("status = %q, want %q: certificate authentication never returns mfa_required",
			got.Status, contract.AuthStatusAuthenticated)
	}
	if got.Identity == nil || got.Identity.Subject != "alice@example.com" {
		t.Fatalf("identity = %+v, want alice@example.com", got.Identity)
	}
	if got.Identity.Source == "" {
		t.Error("identity carries no source")
	}
	if got.MFA != nil {
		t.Error("an authenticated answer also carried an MFA challenge")
	}
}

// THE CHAIN LEG, end to end. The assertion is the SUBJECT: a test that only
// checked for a 200 would pass on precisely the bug this exists to prevent.
func TestAChainLegAuthenticatesAsTheUserAndNamesTheHop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.authCert(t, "alice", identity.KeyFingerprint([]byte(proxyKeyBlob)))
	if res.Code != http.StatusOK {
		t.Fatalf("chain leg = %d: %s", res.Code, res.Body)
	}
	var got contract.AuthenticateResponse
	decode(t, res, &got)
	if got.Identity == nil {
		t.Fatal("chain leg carried no identity")
	}
	if got.Identity.Subject != "alice@example.com" {
		t.Fatalf("chain leg authenticated as %q, want the USER alice@example.com — answering with the "+
			"proxy's identity is the bug this case exists to catch", got.Identity.Subject)
	}
	if got.Identity.Claims[identity.ClaimChainHop] != "proxy-2" {
		t.Errorf("%s = %q, want proxy-2", identity.ClaimChainHop, got.Identity.Claims[identity.ClaimChainHop])
	}

	// A recognised proxy key is not a wildcard: the login still has to
	// resolve to a real identity here.
	res = h.authCert(t, "nobody-here", identity.KeyFingerprint([]byte(proxyKeyBlob)))
	if res.Code != http.StatusUnauthorized {
		t.Errorf("a chain leg for an unknown login = %d, want 401", res.Code)
	}

	// And a key belonging to neither a user nor a fleet proxy is a 401.
	res = h.authCert(t, "alice", "SHA256:nobody-has-this-key")
	if res.Code != http.StatusUnauthorized {
		t.Errorf("an unknown key = %d, want 401", res.Code)
	}
	h.requireEnvelope(t, res)
}

func TestTheMFAConversationOverTheWire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.authPassword(t, "alice", alicePass)
	if res.Code != http.StatusOK {
		t.Fatalf("password = %d: %s", res.Code, res.Body)
	}
	var started contract.AuthenticateResponse
	decode(t, res, &started)
	if started.Status != contract.AuthStatusMFARequired || started.MFA == nil {
		t.Fatalf("password answered %+v, want a challenge", started)
	}
	if started.MFA.Token == "" || started.MFA.ExpiresAt == "" {
		t.Fatalf("challenge = %+v, want a token and an expiry", started.MFA)
	}
	if _, err := time.Parse(time.RFC3339, started.MFA.ExpiresAt); err != nil {
		t.Errorf("mfa.expires_at %q is not RFC 3339", started.MFA.ExpiresAt)
	}

	for i := range 8 {
		h.advance(time.Duration(started.MFA.PollAfterMS) * time.Millisecond)
		res := h.post(t, contract.PathAuthMFAPoll, contract.MFAPollRequest{Token: started.MFA.Token})
		if res.Code != http.StatusOK {
			t.Fatalf("poll %d = %d: %s", i+1, res.Code, res.Body)
		}
		var got contract.AuthenticateResponse
		decode(t, res, &got)
		if got.Status == contract.AuthStatusAuthenticated {
			if got.Identity == nil || got.Identity.Subject != "alice@example.com" {
				t.Fatalf("authenticated poll carried %+v", got.Identity)
			}
			// Replaying the resolved challenge is a 401, not a second
			// authentication.
			replay := h.post(t, contract.PathAuthMFAPoll, contract.MFAPollRequest{Token: started.MFA.Token})
			if replay.Code != http.StatusUnauthorized {
				t.Fatalf("replaying a resolved challenge = %d, want 401", replay.Code)
			}
			return
		}
		if got.MFA == nil || got.MFA.Token != started.MFA.Token {
			t.Fatalf("a pending poll rotated or dropped the token: %+v", got.MFA)
		}
	}
	t.Fatal("the challenge never resolved")
}

func TestAnUnknownChallengeTokenIs401(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.post(t, contract.PathAuthMFAPoll, contract.MFAPollRequest{Token: "never-issued-this"})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token = %d, want 401", res.Code)
	}
	h.requireEnvelope(t, res)
}

// ---------------------------------------------------------------------------
// host keys
// ---------------------------------------------------------------------------

func TestHostKeyReportingAndTheChangedKeyEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	first := h.reportHostKey(t, "rotate.example.com", "SHA256:original")
	var got contract.HostKeyReportResponse
	decode(t, first, &got)
	if got.Decision != contract.HostKeyAccept || got.Known {
		t.Fatalf("first sighting = %+v, want accept and known: false", got)
	}

	// THIS PHASE ISSUES NO HINT (M9 — the stream that would withdraw one is
	// 0009's), and the difference between "we did not get to it" and "we
	// decided not to" is this assertion.
	if got.Cache != nil {
		t.Fatalf("a cache hint was issued (%+v) without a revocation stream to withdraw it", got.Cache)
	}
	if raw := fieldsOf(t, first); hasKey(raw, "cache") {
		t.Error("the response carries a `cache` key; absent means the proxy reports every connection")
	}

	second := h.reportHostKey(t, "rotate.example.com", "SHA256:original")
	decode(t, second, &got)
	if !got.Known {
		t.Error("a key reported a moment ago answered known: false")
	}

	changed := h.reportHostKey(t, "rotate.example.com", "SHA256:rotated")
	decode(t, changed, &got)
	if got.Known {
		t.Error("a key this target has never presented answered known: true")
	}
	if got.Reason == "" {
		t.Error("a changed host key was answered with no reason, so the proxy's audit record says nothing")
	}
	if !strings.Contains(h.logs.String(), "host_key_changed") {
		t.Error("a changed host key produced no log event; 0010 moves this to the audit store, " +
			"but until then this line IS the record")
	}
}

// ---------------------------------------------------------------------------
// capability reports
// ---------------------------------------------------------------------------

func TestACapabilityReportIsStoredAndAcknowledgedTruthfully(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	report := contract.CapabilityReportRequest{
		Target: "cap.example.com", Platform: "linux",
		Capabilities: contract.TargetCapabilities{
			Execution:  []string{string(contract.ExecutionProxyInspected)},
			Reach:      []string{string(contract.ReachProxyChannelPolicy)},
			ObservedAt: nowRFC3339(),
			Detail:     map[string]string{"init": "systemd-257"},
		},
	}
	res := h.post(t, contract.PathCapabilitiesReport, report)
	if res.Code != http.StatusOK {
		t.Fatalf("capability report = %d: %s", res.Code, res.Body)
	}
	var got contract.CapabilityReportResponse
	decode(t, res, &got)
	if !got.Accepted {
		t.Fatal("accepted is false on a report that was stored")
	}
	if got.ReportAfterSeconds < 0 {
		t.Errorf("report_after_seconds = %d; a negative interval names no instant", got.ReportAfterSeconds)
	}

	// Readable through 0006's store, which is the only home for this data.
	rec, err := h.store.TargetCapabilities().Get(t.Context(), testTenant, "cap.example.com", 0, "linux")
	if err != nil {
		t.Fatalf("read the report back: %v", err)
	}
	if len(rec.Execution) != 1 || rec.ObservedAt.IsZero() {
		t.Errorf("stored record = %+v, want the reported rungs and its observation time", rec)
	}

	// A second report REPLACES the first rather than accumulating.
	report.Capabilities.Execution = []string{string(contract.ExecutionNoInteractiveShell)}
	if code := h.post(t, contract.PathCapabilitiesReport, report).Code; code != http.StatusOK {
		t.Fatalf("second report = %d", code)
	}
	all, err := h.store.TargetCapabilities().List(t.Context(), testTenant)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var forTarget int
	for _, r := range all {
		if r.Hostname == "cap.example.com" {
			forTarget++
		}
	}
	if forTarget != 1 {
		t.Errorf("two reports for one target produced %d records, want 1", forTarget)
	}
}

// Stale, undated and absent are ONE case (M17), so a report with no
// observation time is stored undated rather than refused or stamped with
// arrival — stamping arrival would make a record the proxy could not date look
// fresh, which is the fail-open the rule exists to prevent.
func TestAnUndatedCapabilityReportReadsBackAsStale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.post(t, contract.PathCapabilitiesReport, contract.CapabilityReportRequest{
		Target: "undated.example.com",
		Capabilities: contract.TargetCapabilities{
			Execution: []string{string(contract.ExecutionAccountConfined)},
		},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("undated report = %d: %s", res.Code, res.Body)
	}

	rungs, err := h.fleet.TargetRungs(t.Context(), testTenant, fleet.TargetCapabilityKey{
		Hostname: "undated.example.com",
	})
	if err != nil {
		t.Fatalf("TargetRungs: %v", err)
	}
	if rungs.Observed {
		t.Error("an undated record was treated as a fresh observation")
	}
	if rungs.AllowsExecution(contract.ExecutionAccountConfined) {
		t.Error("an undated record contributed a rung that needs the target to have been provisioned")
	}
}

// A report the server did not record is one the proxy must not believe it
// made, so a failed write is a 5xx rather than a cheerful accepted: true.
func TestAFailedCapabilityWriteIsNotAcknowledged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.store.Close()

	res := h.post(t, contract.PathCapabilitiesReport, contract.CapabilityReportRequest{
		Target: "cap.example.com",
		Capabilities: contract.TargetCapabilities{
			Execution: []string{string(contract.ExecutionProxyInspected)}, ObservedAt: nowRFC3339(),
		},
	})
	if res.Code < 500 {
		t.Fatalf("a report whose write failed answered %d, want a 5xx", res.Code)
	}
	var got contract.CapabilityReportResponse
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if got.Accepted {
		t.Fatal("a write that did not land answered accepted: true")
	}
}

// ---------------------------------------------------------------------------
// uid leases
// ---------------------------------------------------------------------------

func TestAUIDLeaseGrantsANonEmptyBlockInsideTheRequestedRange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.post(t, contract.PathUIDLease, contract.UIDLeaseRequest{
		ProxyID: "proxy-1", Target: "uid.example.com", UIDCount: 4096,
		RangeMin: 2_000_000, RangeMax: 2_099_999,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("lease = %d: %s", res.Code, res.Body)
	}
	var got contract.UIDLeaseResponse
	decode(t, res, &got)
	if got.LeaseID == "" {
		t.Error("grant carries no lease_id; an incident cannot say which proxy's block a uid came from")
	}
	if got.UIDTo <= got.UIDFrom {
		t.Fatalf("block [%d,%d) is empty or inverted", got.UIDFrom, got.UIDTo)
	}
	if got.UIDFrom < 2_000_000 || got.UIDTo-1 > 2_099_999 {
		t.Errorf("block [%d,%d) escaped the requested range", got.UIDFrom, got.UIDTo)
	}
}

// A spent range is a 409 with the contract's envelope, which the proxy treats
// exactly as an exhausted block.
func TestAnExhaustedRangeIs409WithTheEnvelope(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	req := contract.UIDLeaseRequest{
		ProxyID: "proxy-1", Target: "narrow.example.com", UIDCount: 4096,
		RangeMin: 3_000_000, RangeMax: 3_012_287,
	}
	var granted int
	for range 8 {
		res := h.post(t, contract.PathUIDLease, req)
		if res.Code == http.StatusConflict {
			if granted == 0 {
				t.Fatal("the first lease answered 409, so this case watched no grant at all")
			}
			h.requireEnvelope(t, res)
			return
		}
		if res.Code != http.StatusOK {
			t.Fatalf("lease = %d: %s", res.Code, res.Body)
		}
		granted++
	}
	t.Fatalf("leased %d blocks from a range 12288 wide without a 409", granted)
}

// A bound token may not lease in another proxy's name.
func TestABoundTokenCannotLeaseForAnotherProxy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	token, _, err := h.fleet.IssueProxyToken(t.Context(), testTenant, "proxy-1", "bound", time.Time{})
	if err != nil {
		t.Fatalf("IssueProxyToken: %v", err)
	}

	body := mustJSON(contract.UIDLeaseRequest{
		ProxyID: "proxy-9", Target: "uid.example.com", UIDCount: 16,
		RangeMin: 2_000_000, RangeMax: 2_009_999,
	})
	res := h.do(http.MethodPost, contract.PathUIDLease, body, token.String())
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("a bound token leasing for another proxy = %d, want 401", res.Code)
	}

	body = mustJSON(contract.UIDLeaseRequest{
		ProxyID: "proxy-1", Target: "uid.example.com", UIDCount: 16,
		RangeMin: 2_000_000, RangeMax: 2_009_999,
	})
	if res := h.do(http.MethodPost, contract.PathUIDLease, body, token.String()); res.Code != http.StatusOK {
		t.Fatalf("a bound token leasing for itself = %d, want 200: %s", res.Code, res.Body)
	}
}

// ---------------------------------------------------------------------------
// request discipline
// ---------------------------------------------------------------------------

func TestAMalformedOrIncompleteBodyIs400AndNever401(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"truncated JSON", contract.PathAuthCert, `{"login": "alice"`},
		{"wrong shape", contract.PathAuthCert, `{"login": 7}`},
		{"no login", contract.PathAuthCert, `{"public_key":{"fingerprint":"SHA256:x"}}`},
		{"no fingerprint", contract.PathAuthCert, `{"login":"alice","public_key":{}}`},
		{"empty object on password", contract.PathAuthPassword, `{}`},
		{"empty token on poll", contract.PathAuthMFAPoll, `{"token":""}`},
		{"no target on host key", contract.PathHostKeyReport, `{"host_key":{"fingerprint":"SHA256:x"}}`},
		{"no target on capabilities", contract.PathCapabilitiesReport, `{"capabilities":{}}`},
		{"no proxy on lease", contract.PathUIDLease, `{"target":"t"}`},
		{"two documents", contract.PathAuthMFAPoll, `{"token":"a"}{"token":"b"}`},
	} {
		res := h.do(http.MethodPost, tc.path, []byte(tc.body), h.token())
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: POST %s = %d, want 400", tc.name, tc.path, res.Code)
		}
		if res.Code == http.StatusUnauthorized {
			t.Errorf("%s answered 401; nobody was named to refuse", tc.name)
		}
		h.requireEnvelope(t, res)
	}
}

// A body larger than the listener accepts is refused rather than read.
func TestAnOversizedBodyIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarnessWith(t, func(o *south.Options) { o.MaxBodyBytes = 512 })

	body := append([]byte(`{"login":"alice","public_key":{"fingerprint":"`), bytes.Repeat([]byte("x"), 4096)...)
	body = append(body, []byte(`"}}`)...)
	res := h.do(http.MethodPost, contract.PathAuthCert, body, h.token())
	if res.Code != http.StatusBadRequest {
		t.Fatalf("an oversized body = %d, want 400", res.Code)
	}
	h.requireEnvelope(t, res)
}

// A caller may tie its logs to this server's, and cannot put anything into
// them by doing so.
func TestTheCorrelationIDIsEchoedAndFiltered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.doWithHeaders(http.MethodPost, contract.PathAuthMFAPoll, []byte(`{"token":"x"}`), h.token(),
		map[string]string{south.CorrelationHeader: "abc-123"})
	if got := res.Header().Get(south.CorrelationHeader); got != "abc-123" {
		t.Errorf("correlation id = %q, want the caller's", got)
	}

	res = h.doWithHeaders(http.MethodPost, contract.PathAuthMFAPoll, []byte(`{"token":"x"}`), h.token(),
		map[string]string{south.CorrelationHeader: "not a\nvalid id"})
	got := res.Header().Get(south.CorrelationHeader)
	if got == "" {
		t.Fatal("a rejected correlation id left the response with none")
	}
	if strings.Contains(got, "\n") || strings.Contains(got, " ") {
		t.Errorf("correlation id %q survived filtering; it goes into log lines and 5xx messages", got)
	}
}

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

type harness struct {
	server *south.Server
	store  *store.Store
	fleet  *fleet.Registry
	logs   *bytes.Buffer
	clock  time.Time
	secret string
}

func newHarness(t *testing.T) *harness { return newHarnessWith(t, nil) }

func newHarnessWith(t *testing.T, tweak func(*south.Options)) *harness {
	t.Helper()
	st := storetest.New(t)
	h := &harness{
		store:  st,
		logs:   &bytes.Buffer{},
		clock:  time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
		secret: testSecret,
	}
	logger := slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h.fleet = fleet.New(st,
		fleet.WithUIDAllocation(fleet.UIDAllocation{BlockSize: 4096}),
		fleet.WithLogger(logger),
	)
	h.seed(t)

	opts := south.Options{
		Identity: identity.NewService(identity.NewStoreDirectory(st),
			identity.WithMFAProvider(identity.ScriptedMFA{}),
			identity.WithClock(h.now),
		),
		Fleet:  h.fleet,
		Logger: logger,
		Now:    h.now,
	}
	if tweak != nil {
		tweak(&opts)
	}
	srv, err := south.New(opts)
	if err != nil {
		t.Fatalf("south.New: %v", err)
	}
	h.server = srv
	return h
}

// newHarnessWithFleet is the one variation that needs a different fleet, for
// the panic case.
func newHarnessWithFleet(t *testing.T, keys identity.FleetKeys) *harness {
	t.Helper()
	h := newHarness(t)
	srv, err := south.New(south.Options{
		Identity: identity.NewService(panickingDirectory{}, identity.WithClock(h.now)),
		Fleet:    h.fleet,
		Logger:   slog.New(slog.NewJSONHandler(h.logs, nil)),
		Now:      h.now,
	})
	if err != nil {
		t.Fatalf("south.New: %v", err)
	}
	_ = keys
	h.server = srv
	return h
}

func (h *harness) now() time.Time { return h.clock }

func (h *harness) advance(d time.Duration) { h.clock = h.clock.Add(d) }

func (h *harness) token() string { return testTenant.String() + "." + h.secret }

func (h *harness) seed(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	st := h.store

	if err := st.Subjects().Upsert(ctx, testTenant, store.Subject{
		ID: "alice@example.com", Source: "local", DisplayName: "Alice",
		Principals: []string{"alice"}, Groups: []string{"sre"},
	}); err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	if err := st.SubjectKeys().Put(ctx, testTenant, store.SubjectKey{
		Fingerprint: identity.KeyFingerprint([]byte(aliceKeyBlob)),
		SubjectID:   "alice@example.com", KeyType: "ssh-ed25519",
	}); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	digest, err := identity.HashPassword("alice@example.com", alicePass)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := st.SubjectPasswords().Put(ctx, testTenant, digest); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	cfg, err := json.Marshal(identity.ScriptedMFAConfig{
		PendingPolls: 1, Decision: identity.ScriptApprove, PollAfterMS: 50,
	})
	if err != nil {
		t.Fatalf("marshal mfa config: %v", err)
	}
	if err := st.MFA().PutEnrollment(ctx, testTenant, store.MFAEnrollment{
		SubjectID: "alice@example.com", Provider: identity.ScriptedMFAName, Config: cfg,
	}); err != nil {
		t.Fatalf("seed mfa: %v", err)
	}

	if err := st.Proxies().Upsert(ctx, testTenant, store.Proxy{
		ID: "proxy-2", Zone: "deep", PublicKey: []byte(proxyKeyBlob), State: store.EnrollmentEnrolled,
	}); err != nil {
		t.Fatalf("seed proxy: %v", err)
	}

	token := fleet.ProxyToken{Tenant: testTenant, Secret: h.secret}
	if err := st.ProxyTokens().Insert(ctx, testTenant, store.ProxyAPIToken{
		TokenID: "tok-test", TokenHash: token.Hash(), Label: "test harness",
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}
}

func (h *harness) do(method, path string, body []byte, token string) *httptest.ResponseRecorder {
	return h.doWithHeaders(method, path, body, token, nil)
}

func (h *harness) doWithHeaders(method, path string, body []byte, token string, headers map[string]string) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res := httptest.NewRecorder()
	h.server.ServeHTTP(res, req)
	return res
}

func (h *harness) post(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return h.do(http.MethodPost, path, mustJSON(body), h.token())
}

func (h *harness) authCert(t *testing.T, login, fingerprint string) *httptest.ResponseRecorder {
	t.Helper()
	return h.post(t, contract.PathAuthCert, contract.AuthenticateCertRequest{
		Login:     login,
		PublicKey: contract.PublicKeyMaterial{Type: "ssh-ed25519", Fingerprint: fingerprint},
		Conn:      contract.ConnMeta{SessionID: "s", ProxyID: "proxy-1", Timestamp: nowRFC3339()},
	})
}

func (h *harness) authPassword(t *testing.T, login, password string) *httptest.ResponseRecorder {
	t.Helper()
	return h.post(t, contract.PathAuthPassword, contract.AuthenticatePasswordRequest{
		Login: login, Password: password,
		Conn: contract.ConnMeta{SessionID: "s", ProxyID: "proxy-1", Timestamp: nowRFC3339()},
	})
}

func (h *harness) reportHostKey(t *testing.T, target, fingerprint string) *httptest.ResponseRecorder {
	t.Helper()
	res := h.post(t, contract.PathHostKeyReport, contract.HostKeyReportRequest{
		Target:  target,
		HostKey: contract.PublicKeyMaterial{Type: "ssh-ed25519", Fingerprint: fingerprint},
		Conn:    contract.ConnMeta{SessionID: "s", ProxyID: "proxy-1", Timestamp: nowRFC3339()},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("host key report = %d: %s", res.Code, res.Body)
	}
	return res
}

// requireEnvelope asserts the contract's error envelope on a non-2xx answer.
// It is checked everywhere rather than on the 401 alone: the envelope is how a
// caller tells a decision from an outage.
func (h *harness) requireEnvelope(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	if res.Code < 400 {
		return
	}
	var env contract.ErrorResponse
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Errorf("a %d answer is not the contract's envelope: %s", res.Code, res.Body)
		return
	}
	if env.Error.Code == "" || env.Error.Message == "" {
		t.Errorf("a %d answer carries an envelope with no code or message: %s", res.Code, res.Body)
	}
}

func decode(t *testing.T, res *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(res.Body.Bytes(), v); err != nil {
		t.Fatalf("undecodable body: %s", res.Body)
	}
}

// fieldsOf decodes a body as a generic object, which is what makes "this key is
// not present" expressible — a zero value and an omitted key look identical on
// a decoded struct.
func fieldsOf(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &m); err != nil {
		t.Fatalf("undecodable body: %s", res.Body)
	}
	return m
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// panickingDirectory makes a handler panic, so the recovery middleware can be
// graded on what a caller actually receives.
type panickingDirectory struct{}

func (panickingDirectory) SubjectByPrincipal(context.Context, store.Tenant, string) (store.Subject, error) {
	panic("the directory exploded")
}

func (panickingDirectory) SubjectByID(context.Context, store.Tenant, string) (store.Subject, error) {
	panic("the directory exploded")
}

func (panickingDirectory) KeyByFingerprint(context.Context, store.Tenant, string) (store.SubjectKey, error) {
	panic("the directory exploded")
}

func (panickingDirectory) PasswordFor(context.Context, store.Tenant, string) (store.PasswordDigest, error) {
	panic("the directory exploded")
}

func (panickingDirectory) MFAFor(context.Context, store.Tenant, string) (store.MFAEnrollment, error) {
	panic("the directory exploded")
}

func (panickingDirectory) Challenges() store.MFARepository { panic("the directory exploded") }

type panickingFleet struct{}

func (panickingFleet) ProxyByKeyFingerprint(context.Context, store.Tenant, string) (string, bool, error) {
	panic("the fleet exploded")
}
