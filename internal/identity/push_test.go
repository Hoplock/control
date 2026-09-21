// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoplock/control/internal/identity"
)

// The real out-of-band second factor, against a test MFA service.
//
// What is asserted here is the provider's whole job — "start one" and "how is it
// going" — plus the two rules that are NOT a provider's business and must not be
// delegated to one: every request is signed, and an answer this build cannot read
// is an OUTAGE rather than a guess.

const pushSecretEnv = "HOPLOCK_TEST_PUSH_SECRET"

// testMFAService is an MFA vendor's far end.
type testMFAService struct {
	t      *testing.T
	secret []byte
	server *httptest.Server

	// result is what the next poll answers.
	result string
	// beginError and pollError make the service refuse.
	beginError string
	pollError  string
	// seenSignature records whether the last request verified.
	seenSignature bool
	lastPolls     int
}

func newTestMFAService(t *testing.T, secret string) *testMFAService {
	t.Helper()
	svc := &testMFAService{t: t, secret: []byte(secret), result: "pending"}
	mux := http.NewServeMux()
	mux.HandleFunc("/begin", svc.begin)
	mux.HandleFunc("/poll", svc.poll)
	svc.server = httptest.NewServer(mux)
	t.Cleanup(svc.server.Close)
	return svc
}

func (s *testMFAService) verify(r *http.Request) []byte {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.t.Fatalf("mfa service body: %v", err)
	}
	want := identity.SignPushRequest(s.secret, body)
	s.seenSignature = subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Hoplock-Signature")), []byte(want)) == 1
	return body
}

func (s *testMFAService) begin(w http.ResponseWriter, r *http.Request) {
	s.verify(r)
	if s.beginError != "" {
		writeTestJSON(w, map[string]string{"error": s.beginError})
		return
	}
	writeTestJSON(w, map[string]any{
		"ref":           "challenge-1",
		"prompt":        "Approve on your phone",
		"poll_after_ms": 1500,
		"ttl_seconds":   90,
	})
}

func (s *testMFAService) poll(w http.ResponseWriter, r *http.Request) {
	body := s.verify(r)
	var req struct {
		Polls int `json:"polls"`
	}
	_ = json.Unmarshal(body, &req)
	s.lastPolls = req.Polls

	if s.pollError != "" {
		writeTestJSON(w, map[string]string{"error": s.pollError})
		return
	}
	writeTestJSON(w, map[string]string{"result": s.result})
}

func newPushProvider(t *testing.T, svc *testMFAService) *identity.PushMFA {
	t.Helper()
	t.Setenv(pushSecretEnv, string(svc.secret))
	provider, err := identity.NewPushMFA(identity.PushConfig{
		Name:      "push",
		BeginURL:  svc.server.URL + "/begin",
		PollURL:   svc.server.URL + "/poll",
		SecretEnv: pushSecretEnv,
	})
	if err != nil {
		t.Fatalf("push provider: %v", err)
	}
	return provider
}

func pushEnrollment() json.RawMessage { return json.RawMessage(`{"device":"phone-1"}`) }

func TestAPushChallengeIsStartedAndPolledToApproval(t *testing.T) {
	svc := newTestMFAService(t, "shared-secret")
	provider := newPushProvider(t, svc)
	ctx := context.Background()

	terms, err := provider.Begin(ctx, identity.Identity{Subject: "alice", Login: "alice"}, pushEnrollment())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if terms.Ref != "challenge-1" {
		t.Errorf("ref: %q", terms.Ref)
	}
	if terms.Prompt != "Approve on your phone" {
		t.Errorf("prompt: %q", terms.Prompt)
	}
	if terms.PollAfter.Milliseconds() != 1500 || terms.TTL.Seconds() != 90 {
		t.Errorf("terms: %v / %v", terms.PollAfter, terms.TTL)
	}

	got, err := provider.Poll(ctx, pushEnrollment(), terms.Ref, 1)
	if err != nil || got != identity.MFAPending {
		t.Fatalf("first poll: %v %v", got, err)
	}
	svc.result = "approved"
	got, err = provider.Poll(ctx, pushEnrollment(), terms.Ref, 2)
	if err != nil || got != identity.MFAApproved {
		t.Fatalf("second poll: %v %v", got, err)
	}
	if svc.lastPolls != 2 {
		t.Errorf("the poll count did not reach the service: %d", svc.lastPolls)
	}
}

func TestEveryPushRequestIsSigned(t *testing.T) {
	// "Approve challenge X" is a request worth forging, so an unsigned one is
	// not something this provider can be configured into sending.
	svc := newTestMFAService(t, "shared-secret")
	provider := newPushProvider(t, svc)

	if _, err := provider.Begin(context.Background(), identity.Identity{Subject: "alice"}, pushEnrollment()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if !svc.seenSignature {
		t.Fatal("the begin request did not carry a signature the service could verify")
	}
	if _, err := provider.Poll(context.Background(), pushEnrollment(), "challenge-1", 1); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !svc.seenSignature {
		t.Fatal("the poll request did not carry a signature the service could verify")
	}
}

func TestARefusalIsADenialAndAServiceFailureIsAnOutage(t *testing.T) {
	// M11 one layer down: an MFA service that is down must not deny
	// everybody's login.
	svc := newTestMFAService(t, "shared-secret")
	provider := newPushProvider(t, svc)
	ctx := context.Background()

	svc.result = "refused"
	got, err := provider.Poll(ctx, pushEnrollment(), "challenge-1", 1)
	if err != nil || got != identity.MFARefused {
		t.Fatalf("a refusal must be MFARefused with no error: %v %v", got, err)
	}

	svc.pollError = "backend unavailable"
	got, err = provider.Poll(ctx, pushEnrollment(), "challenge-1", 2)
	if err == nil {
		t.Fatal("a service failure was reported as a result rather than an outage")
	}
	if got == identity.MFARefused {
		t.Fatal("a service failure became a refusal")
	}
}

func TestAnAnswerThisBuildCannotReadIsAnOutageRatherThanAGuess(t *testing.T) {
	// Coercing an unknown answer to "pending" holds the session open forever;
	// coercing it to "refused" denies a person who may well have approved.
	svc := newTestMFAService(t, "shared-secret")
	provider := newPushProvider(t, svc)
	svc.result = "maybe"

	got, err := provider.Poll(context.Background(), pushEnrollment(), "challenge-1", 1)
	if err == nil {
		t.Fatalf("an unrecognised answer was accepted as %v", got)
	}
}

func TestAProviderWithNoSharedSecretDoesNotBuild(t *testing.T) {
	svc := newTestMFAService(t, "shared-secret")
	if _, err := identity.NewPushMFA(identity.PushConfig{
		BeginURL: svc.server.URL + "/begin",
		PollURL:  svc.server.URL + "/poll",
	}); err == nil {
		t.Fatal("a provider that would send unsigned requests built")
	}
	t.Setenv(pushSecretEnv, "")
	if _, err := identity.NewPushMFA(identity.PushConfig{
		BeginURL: svc.server.URL + "/begin", PollURL: svc.server.URL + "/poll",
		SecretEnv: pushSecretEnv,
	}); err == nil {
		t.Fatal("a provider whose secret variable is empty built")
	}
}

func TestAnEnrollmentWithNoDeviceIsRefused(t *testing.T) {
	svc := newTestMFAService(t, "shared-secret")
	provider := newPushProvider(t, svc)

	for _, config := range []json.RawMessage{
		nil,
		json.RawMessage(`{}`),
		json.RawMessage(`{"phone":"x"}`),
	} {
		if _, err := provider.Begin(context.Background(), identity.Identity{Subject: "alice"}, config); err == nil {
			t.Errorf("an enrollment %s was accepted", config)
		}
	}
}

func TestTheSecretVariableIsNamedInErrorsAndTheSecretIsNot(t *testing.T) {
	// The whole point of reading a secret through a name.
	_, err := identity.NewPushMFA(identity.PushConfig{
		BeginURL: "https://x/begin", PollURL: "https://x/poll", SecretEnv: "HOPLOCK_TEST_ABSENT_SECRET",
	})
	if err == nil {
		t.Fatal("an unset secret built")
	}
	if got := err.Error(); !contains(got, "HOPLOCK_TEST_ABSENT_SECRET") {
		t.Errorf("the error does not name the variable: %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
