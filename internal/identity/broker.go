// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/hoplock/control/internal/store"
)

// ONE INTERFACE FOR OIDC AND SAML (M7).
//
// The two protocols differ in almost every detail and agree on the only thing
// this server needs: a browser goes somewhere, comes back carrying something,
// and what it carries names a person. Everything above this seam — the flow
// row, its single use, the claim mapping, the session — is written once and
// does not know which protocol produced the assertion. That is what makes the
// mapping's guarantee provable: there is one place claims become attributes,
// so "an unmapped claim cannot influence a decision" is a property of one
// function rather than of two code paths that happen to agree today.
//
// THE PROXY STILL NEVER TALKS TO AN IdP (M7, proxy D4). Nothing in this file
// is reachable from the south-bound listener, and `architecture_test.go` keeps
// it that way.

// Broker is one federation connector.
type Broker interface {
	// Name is the connector's name within the tenant. It becomes the
	// identity's Source, so it is part of the audit trail.
	Name() string
	// Kind is oidc or saml.
	Kind() store.ConnectorKind
	// Begin starts a login. It returns where to send the browser and the
	// per-flow secrets the caller must store — the caller stores them, not
	// the broker, because a flow is a row rather than process memory (PLAN
	// §6's rule for MFA challenges, and the same reason: nothing makes the
	// callback land on the node that started the flow).
	Begin(ctx context.Context, req BeginRequest) (Begun, error)
	// Complete turns what the browser came back with into an assertion.
	// The flow row is passed in already consumed, so a replayed callback
	// never reaches a broker at all.
	Complete(ctx context.Context, req CompleteRequest) (Assertion, error)
	// Metadata is what the IdP needs to know about this server. SAML
	// returns an EntityDescriptor; OIDC returns ("", nil, nil), because
	// there is nothing an OIDC IdP reads from us.
	Metadata(ctx context.Context) (contentType string, body []byte, err error)
}

// BeginRequest is what the federation service hands a broker to start a login.
type BeginRequest struct {
	// State is the opaque value the IdP echoes, allocated above the broker
	// so that every connector's flow row is keyed the same way.
	State string
	// RedirectURI is this server's callback for the connector.
	RedirectURI string
	// LoginHint is passed to the IdP where it accepts one. It is a
	// convenience for the user and is never trusted: the assertion names
	// the subject, not the hint.
	LoginHint string
}

// Begun is what a broker produced when a login started.
type Begun struct {
	// RedirectURL is where the browser goes.
	RedirectURL string
	// Nonce, PKCEVerifier and RequestID are the per-flow secrets. Which
	// are set depends on the protocol; all of them are stored on the flow
	// row and handed back on completion.
	Nonce        string
	PKCEVerifier string
	RequestID    string
}

// CompleteRequest is what came back from the IdP.
type CompleteRequest struct {
	// Flow is the consumed flow row: the state, the nonce, the verifier,
	// the request id.
	Flow store.FlowState
	// Params are the callback's query or form values.
	Params url.Values
	// RedirectURI is the callback the flow was started with, echoed to the
	// token endpoint because OAuth requires the two to match.
	RedirectURI string
}

// BrokerError is a federation failure that is SAFE TO SHOW A USER, at the
// granularity a login page needs and no finer.
//
// The distinction this type draws is M11's, one layer along: a login that this
// server REFUSED (a bad signature, an expired assertion, a state that was
// already spent) is a decision, and a login that this server could not
// COMPLETE (the IdP did not answer, the JWKS did not fetch) is an outage. The
// two are different return values for the same reason [Outcome] and error are:
// telling a user "login failed" during an IdP outage sends them to reset a
// password that was never wrong.
type BrokerError struct {
	// Code is stable and safe to render.
	Code string
	// Message is the English sentence a login page shows.
	Message string
	// Detail is for this server's log only and is never disclosed: it is
	// where "the assertion's audience was https://other" goes, which is an
	// oracle if rendered and a diagnosis if logged.
	Detail string
}

func (e *BrokerError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Detail)
}

// The broker rejection codes.
const (
	RejectFederationState      = "federation.state_invalid"
	RejectFederationProtocol   = "federation.protocol_error"
	RejectFederationSignature  = "federation.signature_invalid"
	RejectFederationAssertion  = "federation.assertion_invalid"
	RejectFederationExpired    = "federation.assertion_expired"
	RejectFederationAudience   = "federation.audience_mismatch"
	RejectFederationSubject    = "federation.subject_missing"
	RejectFederationDisabled   = "federation.connector_disabled"
	RejectFederationNoSuchOne  = "federation.connector_unknown"
	RejectFederationNotAllowed = "federation.login_refused"
)

func refuse(code, message, detail string) error {
	return &BrokerError{Code: code, Message: message, Detail: detail}
}

// IsBrokerRefusal reports whether an error is a deliberate refusal rather than
// an outage. It is the north-bound half of M11: only a refusal becomes a 401.
func IsBrokerRefusal(err error) bool {
	var be *BrokerError
	return err != nil && asBrokerError(err, &be)
}

func asBrokerError(err error, target **BrokerError) bool {
	for err != nil {
		if be, ok := err.(*BrokerError); ok {
			*target = be
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// BrokerRefusal returns the refusal an error carries, and false when it is an
// outage.
func BrokerRefusal(err error) (*BrokerError, bool) {
	var be *BrokerError
	if asBrokerError(err, &be) {
		return be, true
	}
	return nil, false
}

// secretFromEnv reads a connector secret out of the environment.
//
// THE SECRET IS NEVER A ROW AND NEVER A FIELD ON A CONFIG STRUCT THAT GETS
// LOGGED. A connector document names the variable; this reads it; the value
// lives in one string that is only ever passed to the token endpoint. The
// error deliberately names the VARIABLE and not its value, which is the whole
// point of the indirection.
func secretFromEnv(variable string) (string, error) {
	if strings.TrimSpace(variable) == "" {
		return "", fmt.Errorf("identity: the connector does not name an environment variable for its client secret")
	}
	v, ok := os.LookupEnv(variable)
	if !ok || v == "" {
		return "", fmt.Errorf("identity: the environment variable %s is unset, so this connector cannot authenticate to its IdP", variable)
	}
	return v, nil
}

// randomToken returns n bytes of entropy as base64url. It is the state, the
// nonce, the PKCE verifier and the SAML request id: every one of them is a
// value an attacker who can guess it can replay.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("identity: this server could not generate a login token")
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
