// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// One certificate per session: what POST /v1/credentials/certificate does
// (Hoplock/proxy#68, proxy PLAN §5.4).
//
// A route whose ladder names `brokered-certificate` reaches a proxy as POLICY
// (ladder.go). Once the proxy has generated a key pair for the session it asks
// here for a user certificate over the public half, citing the decision it is
// serving the session under. This file binds that request to a decision this
// server made and then signs exactly the submitted key.
//
// NOTHING HERE IS A CACHE. Every call signs the key it was sent, under a new
// serial, and writes its own row. Not by decision_id — a decision reused across
// connections cites the same one from each — not by session_id, and not by key.
// The contract makes one call per session the PROXY's rule, and upstream holds
// it in its types (its caching client implements no issuer); this side's half
// is never to be the cache. The authorize decision stays cacheable, because its
// entry carries only policy; a certificate is the one thing no hint may cover.

// DecisionSetupAllowance is how long after its own reuse window a decision may
// still be cited: the time between the answer and the issuance — the proxy
// generating a key, a clock between two of this server's nodes — which is
// seconds, held here at minutes so that no honest session meets it.
//
// A decision with no cache hint serves the one connection it answered, so it
// may be cited for this long after it was made; one with a hint may be reused
// for the hint's TTL, and cited for that plus this. A conforming proxy never
// cites an older one (proxy PLAN §5.4: one call per session, after the route
// is decided), so an older one is a replayed id, and a certificate minted on it
// would be a fresh credential under a decision whose policy, grant or session
// may since have been withdrawn — the cache_invalidate and session_kill that
// end a decision's reuse would not end its issuance.
const DecisionSetupAllowance = 2 * time.Minute

// SessionRequest is one issuance, as the proxy asked for it.
type SessionRequest struct {
	// ProxyID is the proxy the caller's credential is bound to, empty for an
	// unbound credential (M22).
	ProxyID string
	// SessionID is the proxy's session id; it is what the row records.
	SessionID string
	// DecisionID is the authorize decision the session is served under.
	// The contract lets a server that requires one refuse a call without
	// it, and this one does: every answer it gives carries one (M4).
	DecisionID string
	// Target and Username are cross-checked against that decision.
	Target   string
	Username string
	// PublicKey is the proxy's session key in `authorized_keys` form.
	PublicKey string
}

// SessionCertificate is one issued certificate, rendered from the certificate
// itself so that nothing beside it can disagree with it.
type SessionCertificate struct {
	// Certificate is the `authorized_keys`-form user certificate.
	Certificate string
	// Serial is the certificate's own serial.
	Serial uint64
	// ValidBefore is the certificate's own `valid before` instant, which is
	// whole seconds: the proxy refuses a response whose `valid_before`
	// differs from it, so it is never rendered from the issuance's
	// sub-second clock.
	ValidBefore time.Time
	// TrustBundle is the tenant's current CA keys, read per call so that a
	// rotation reaches the proxy fresh. The proxy carries it and acts on
	// none of it.
	TrustBundle []string
}

// MintRefusal is a refusal to mint: the decision cited is not one this server
// made for the method, or the request does not match it. It becomes a 401
// through the south-bound listener's own denier for it, which is neither the
// user's credential being refused nor the proxy's token (M11): the session was
// already authorized, and the proxy treats every non-200 here as an outage.
type MintRefusal struct {
	// Code is contract.ErrCodeUnknownDecision or ErrCodeDecisionMismatch.
	Code string
	// Message is safe to disclose.
	Message string
	// Reason is for this server's log only.
	Reason string
}

func (r *MintRefusal) Error() string { return r.Code + ": " + r.Message }

// MalformedRequestError is a public key that cannot be signed: one that does not
// parse, or one that is itself a certificate. A 400.
type MalformedRequestError struct{ Message string }

func (e *MalformedRequestError) Error() string { return "credential: " + e.Message }

func unknownDecision(reason string) error {
	return &MintRefusal{
		Code:    contract.ErrCodeUnknownDecision,
		Message: "the decision this issuance cites is not one this server made for brokered-certificate",
		Reason:  reason,
	}
}

func staleDecision(reason string) error {
	return &MintRefusal{
		Code:    contract.ErrCodeUnknownDecision,
		Message: "the decision this issuance cites is past the window a session may be served under it",
		Reason:  reason,
	}
}

func decisionMismatch(reason string) error {
	return &MintRefusal{
		Code:    contract.ErrCodeDecisionMismatch,
		Message: "the target or account does not match the decision this issuance cites",
		Reason:  reason,
	}
}

// IssueForSession binds an issuance to a decision this server made, and signs.
//
// Its answers: a certificate; a [*MintRefusal] (401); a [*MalformedRequestError]
// (400); [ErrNoCA] (503: an operator's fix); anything else is an outage — a
// store failure reading the decision or writing the row is never a refusal.
//
// The request's session_id is deliberately NOT required to equal the stored
// decision's: a cached decision serves many sessions, and each asks for its own
// certificate citing the same decision_id.
func (c *CA) IssueForSession(ctx context.Context, tenant store.Tenant, req SessionRequest) (SessionCertificate, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return SessionCertificate{}, &MalformedRequestError{Message: "session_id is required"}
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil {
		return SessionCertificate{}, &MalformedRequestError{Message: "public_key is not an authorized_keys public key"}
	}
	if _, isCert := pub.(*ssh.Certificate); isCert {
		return SessionCertificate{}, &MalformedRequestError{Message: "public_key is a certificate, not a key"}
	}

	// The authority first, as the contract's reference does: with none,
	// nothing the request says could be served.
	if _, err := c.st.CAKeys().Active(ctx, tenant); store.IsNotFound(err) {
		return SessionCertificate{}, ErrNoCA
	} else if err != nil {
		return SessionCertificate{}, err
	}

	rung, decided, err := c.boundDecision(ctx, tenant, req, c.now())
	if err != nil {
		return SessionCertificate{}, err
	}

	var bound time.Duration
	if raw := rung.Params[contract.ParamLifetimeSeconds]; raw != "" {
		secs, perr := strconv.Atoi(raw)
		if perr != nil || secs <= 0 {
			// The compiler refuses a non-positive lifetime, so a stored
			// decision carrying one is this server's own fault.
			return SessionCertificate{}, fmt.Errorf("credential: decision %s carries lifetime_seconds %q", req.DecisionID, raw)
		}
		bound = time.Duration(secs) * time.Second
	}

	issued, err := c.Issue(ctx, tenant, IssueRequest{
		SubjectID:  decided.SubjectID,
		Principals: []string{req.Username},
		Target:     decided.target,
		TargetPort: int(decided.port),
		SessionID:  req.SessionID,
		PublicKey:  pub.Marshal(),
		UpperBound: bound,
	})
	if err != nil {
		return SessionCertificate{}, err
	}

	cert, err := certificateOf(issued.Certificate)
	if err != nil {
		return SessionCertificate{}, err
	}
	info, err := c.Describe(ctx, tenant)
	if err != nil {
		return SessionCertificate{}, err
	}
	bundle := make([]string, 0, len(info.TrustBundle))
	for _, k := range info.TrustBundle {
		bundle = append(bundle, k.PublicKey)
	}
	return SessionCertificate{
		Certificate: issued.Certificate,
		Serial:      cert.Serial,
		ValidBefore: time.Unix(int64(cert.ValidBefore), 0).UTC(),
		TrustBundle: bundle,
	}, nil
}

// decided is what an issuance takes from the decision it cites.
type decided struct {
	store.Decision
	target string
	port   int32
}

// boundDecision reads the cited decision in the caller's tenant and
// cross-checks the request against it, at now. Every failure that is about the
// request is a refusal; every failure to read is an outage.
func (c *CA) boundDecision(ctx context.Context, tenant store.Tenant, req SessionRequest, now time.Time) (contract.TargetAuth, decided, error) {
	if req.DecisionID == "" {
		return contract.TargetAuth{}, decided{}, unknownDecision("no decision_id; this server requires one")
	}
	d, err := c.st.Decisions().Get(ctx, tenant, req.DecisionID)
	if store.IsNotFound(err) {
		return contract.TargetAuth{}, decided{}, unknownDecision("no such decision in this tenant")
	}
	if err != nil {
		return contract.TargetAuth{}, decided{}, err
	}
	if d.Effect != "allow" || len(d.Snapshot) == 0 {
		return contract.TargetAuth{}, decided{}, unknownDecision("the decision did not allow")
	}
	if req.ProxyID != "" && d.ProxyID != req.ProxyID {
		// Each leg of a chain asks for its own decision, so the proxy that
		// dials the target is the one the decision was made for; a
		// credential bound to another proxy is asking on somebody else's
		// session.
		return contract.TargetAuth{}, decided{}, decisionMismatch("the decision was made for proxy " + d.ProxyID)
	}
	var snap contract.AuthorizeResponse
	if err := json.Unmarshal(d.Snapshot, &snap); err != nil {
		return contract.TargetAuth{}, decided{}, fmt.Errorf("credential: decision %s's stored snapshot does not decode: %w", d.ID, err)
	}
	if window := citeWindow(snap.Cache); now.Sub(d.DecidedAt) > window {
		return contract.TargetAuth{}, decided{}, staleDecision(fmt.Sprintf(
			"the decision was made at %s and may be cited for %s", d.DecidedAt.UTC().Format(time.RFC3339), window))
	}
	var rungs []contract.TargetAuth
	for _, r := range snap.TargetAuthLadder {
		if r.Method == contract.TargetAuthBrokeredCertificate {
			rungs = append(rungs, r)
		}
	}
	if len(rungs) == 0 {
		return contract.TargetAuth{}, decided{}, unknownDecision("the decision names no brokered-certificate rung")
	}
	if req.Target != snap.Target {
		return contract.TargetAuth{}, decided{}, decisionMismatch("target " + req.Target + " is not the decision's " + snap.Target)
	}
	for _, r := range rungs {
		if r.Params[contract.ParamUsername] == req.Username {
			return r, decided{Decision: d, target: snap.Target, port: snap.TargetPort}, nil
		}
	}
	return contract.TargetAuth{}, decided{}, decisionMismatch("account " + req.Username + " is named by no certificate rung of the decision")
}

// citeWindow is how long after it was made a decision may be cited: the reuse
// its cache hint allowed, if it carried one, and the setup allowance.
func citeWindow(hint *contract.CacheHint) time.Duration {
	window := DecisionSetupAllowance
	if hint != nil && hint.TTLSeconds > 0 {
		window += time.Duration(hint.TTLSeconds) * time.Second
	}
	return window
}

// certificateOf parses an issued certificate line back.
func certificateOf(line string) (*ssh.Certificate, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, fmt.Errorf("credential: an issued certificate does not parse: %w", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("credential: an issued certificate is not a certificate")
	}
	return cert, nil
}
