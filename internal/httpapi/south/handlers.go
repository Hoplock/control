// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The contract handlers.
//
// They are the ONLY place in this module that speaks both vocabularies: the
// wire shapes belong to `internal/contract` and the domain shapes to
// `internal/identity` and `internal/fleet` (PLAN §3), so a contract revision
// lands here and stops. Every one of them is a translation plus a call, which
// is deliberate — a handler with a decision in it is a decision the domain
// packages cannot be tested for.

// handlers implements the narrow contract seams 0002 defined. The compile-time
// assertions below are what make "this phase serves those four operations" a
// statement the build checks.
type handlers struct{ s *Server }

var (
	_ contract.Authenticator      = handlers{}
	_ contract.Authorizer         = handlers{}
	_ contract.HostKeyReporter    = handlers{}
	_ contract.CapabilityReporter = handlers{}
	_ contract.UIDLeaser          = handlers{}
	_ contract.LogIngester        = handlers{}
	_ contract.EventPublisher     = handlers{}
)

func (s *Server) handlers() handlers { return handlers{s: s} }

// endpoint adapts a decode-and-answer function to net/http.
//
// Every route goes through it, so the success path writes one shape and the
// failure path goes through [statusFor] — there is no handler that can choose
// a status code for itself.
func (s *Server) endpoint(fn func(context.Context, *http.Request) (any, error)) http.Handler {
	return s.endpointStatus(http.StatusOK, fn)
}

// endpointStatus is [Server.endpoint] with a success code other than 200. Only
// `/v1/logs/batch` uses it, and only because the contract distinguishes
// "accepted for storage" from "stored".
func (s *Server) endpointStatus(status int, fn func(context.Context, *http.Request) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := fn(r.Context(), r)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		writeJSON(w, status, body)
	})
}

// decode reads a JSON request body.
//
// Unknown fields are ACCEPTED rather than refused: the contract is additive
// within a vocabulary, so a newer proxy sending a field this build has not
// heard of is a proxy that will be upgraded past, not a malformed caller. The
// opposite rule holds on the RESPONSE, where the proxy decodes strictly — an
// unknown field there may be a dropped restriction (PLAN §4).
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return invalid("the request body is larger than this server accepts")
		}
		// The error text is the decoder's own and names a JSON offset, not
		// the body — so it is safe to disclose even on /v1/auth/password.
		return invalid("the request body is not the JSON object this endpoint expects")
	}
	// A second JSON value in one body is a caller sending something other
	// than the document the contract describes.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return invalid("the request body carries more than one JSON document")
	}
	return nil
}

// ---------------------------------------------------------------------------
// authentication
// ---------------------------------------------------------------------------

func (h handlers) authenticateCert(ctx context.Context, r *http.Request) (any, error) {
	var req contract.AuthenticateCertRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.AuthenticateCert(ctx, &req)
}

// AuthenticateCert implements contract.Authenticator.
func (h handlers) AuthenticateCert(ctx context.Context, req *contract.AuthenticateCertRequest) (*contract.AuthenticateResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	if req.Login == "" {
		return nil, invalid("login is required")
	}
	if req.PublicKey.Fingerprint == "" {
		return nil, invalid("public_key.fingerprint is required")
	}

	outcome, err := h.s.identity.AuthenticateKey(ctx, caller.Tenant, h.s.fleet, identity.KeyAttempt{
		Login:         req.Login,
		Fingerprint:   req.PublicKey.Fingerprint,
		IsCertificate: req.PublicKey.IsCertificate,
	})
	if err != nil {
		return nil, err
	}
	return h.answerAuth(ctx, contract.PathAuthCert, req.Login, outcome)
}

func (h handlers) authenticatePassword(ctx context.Context, r *http.Request) (any, error) {
	var req contract.AuthenticatePasswordRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	// The decoded request holds a password. It is passed down, used, and
	// dropped; it is never logged, echoed in an error, or stored (PLAN §7),
	// and `TestThePasswordAppearsInNoLogLine` asserts that against captured
	// output rather than against a reading of this file.
	return h.AuthenticatePassword(ctx, &req)
}

// AuthenticatePassword implements contract.Authenticator.
func (h handlers) AuthenticatePassword(ctx context.Context, req *contract.AuthenticatePasswordRequest) (*contract.AuthenticateResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	if req.Login == "" {
		return nil, invalid("login is required")
	}

	outcome, err := h.s.identity.AuthenticatePassword(ctx, caller.Tenant, identity.PasswordAttempt{
		Login:    req.Login,
		Password: req.Password,
	})
	if err != nil {
		return nil, err
	}
	return h.answerAuth(ctx, contract.PathAuthPassword, req.Login, outcome)
}

func (h handlers) pollMFA(ctx context.Context, r *http.Request) (any, error) {
	var req contract.MFAPollRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.PollMFA(ctx, &req)
}

// PollMFA implements contract.Authenticator.
func (h handlers) PollMFA(ctx context.Context, req *contract.MFAPollRequest) (*contract.AuthenticateResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	if req.Token == "" {
		return nil, invalid("token is required")
	}

	outcome, err := h.s.identity.PollMFA(ctx, caller.Tenant, req.Token)
	if err != nil {
		return nil, err
	}
	return h.answerAuth(ctx, contract.PathAuthMFAPoll, "", outcome)
}

// answerAuth is the ONE place an authentication outcome becomes a wire answer,
// and the one place in this package (beside the credential check in the
// middleware) that can produce a 401.
//
// It is a single function on purpose. The three auth endpoints answer with the
// same object and must classify the same way, and a second conversion would be
// a second chance to turn an outage into a deny.
func (h handlers) answerAuth(ctx context.Context, path, login string, o identity.Outcome) (*contract.AuthenticateResponse, error) {
	switch {
	case o.Deny != nil:
		// The reason is written down; the caller is told nothing beyond
		// "the credential was refused".
		h.s.logDeny(ctx, path, string(o.Deny.Reason), "login", login)
		return nil, deny("the credential was not accepted")
	case o.Identity != nil:
		return &contract.AuthenticateResponse{
			Status:   contract.AuthStatusAuthenticated,
			Identity: wireIdentity(*o.Identity),
		}, nil
	case o.Challenge != nil:
		return &contract.AuthenticateResponse{
			Status: contract.AuthStatusMFARequired,
			MFA: &contract.MFAChallenge{
				Token:       o.Challenge.Token,
				Prompt:      o.Challenge.Prompt,
				PollAfterMS: int32(o.Challenge.PollAfter.Milliseconds()),
				ExpiresAt:   o.Challenge.ExpiresAt.UTC().Format(time.RFC3339),
			},
		}, nil
	default:
		// An empty outcome is a bug in this server, so it is an outage.
		// Reading it as a deny would report our bug as the user's.
		return nil, errEmptyOutcome
	}
}

func wireIdentity(id identity.Identity) *contract.Identity {
	return &contract.Identity{
		Subject:     id.Subject,
		Login:       id.Login,
		DisplayName: id.DisplayName,
		Source:      id.Source,
		Principals:  id.Principals,
		Groups:      id.Groups,
		Claims:      id.Claims,
	}
}

// ---------------------------------------------------------------------------
// authorize
// ---------------------------------------------------------------------------

func (h handlers) authorize(ctx context.Context, r *http.Request) (any, error) {
	var req contract.AuthorizeRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.Authorize(ctx, &req)
}

// Authorize implements contract.Authorizer.
//
// It is a translation and a call, like every other handler here: the decision
// is `internal/decision`'s and the only thing chosen at this layer is which of
// the three answers the caller gets. That split is what M11 rests on — the
// service returns a typed [decision.Outcome] whose `Deny` field is the only
// refusal, so an outage cannot arrive as one.
func (h handlers) Authorize(ctx context.Context, req *contract.AuthorizeRequest) (*contract.AuthorizeResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	if !caller.Authorises(req.Conn.ProxyID) {
		// A token issued to one proxy may not ask in another's name. The
		// route is computed FROM the asking proxy (0008), so a credential
		// that could name anybody could ask for a route it does not sit
		// on — and the hop trail, which only ever narrows, would be
		// narrowing the wrong chain. Same refusal as the uid lease, for
		// the same reason.
		h.s.logDeny(ctx, contract.PathAuthorize, "proxy-id-not-authorised",
			"requested_proxy_id", req.Conn.ProxyID, "token_proxy_id", caller.ProxyID)
		return nil, deny("the credential was not accepted")
	}

	out, err := h.s.decision.Authorize(ctx, caller.Tenant, req)
	if err != nil {
		var mismatch *decision.VersionMismatchError
		if errors.As(err, &mismatch) {
			// A proxy this server cannot answer within the vocabulary it
			// declared is a ROLLOUT problem, and a 5xx says so: it is not
			// a statement about the user, and it is never a thinned
			// snapshot with the restriction quietly dropped.
			return nil, versionUnsupported(mismatch.Error())
		}
		return nil, err
	}
	switch {
	case out.Deny != nil:
		// The reason is written down — in this server's log and in the
		// decision record — and the caller is told nothing beyond "access
		// denied". A precise denial makes the proxy an oracle for probing
		// the estate (M4).
		h.s.logDeny(ctx, contract.PathAuthorize, out.Deny.Reason,
			"subject", req.Identity.Subject,
			"target", req.Target,
			"rule", out.Deny.Rule,
			"decision_id", out.Deny.DecisionID)
		return nil, deny("access denied")
	case out.Response != nil:
		return out.Response, nil
	default:
		// An empty outcome is a bug in this server, so it is an outage.
		// Reading it as a deny would report our bug as the user's.
		return nil, errEmptyOutcome
	}
}

// ---------------------------------------------------------------------------
// host keys
// ---------------------------------------------------------------------------

func (h handlers) reportHostKey(ctx context.Context, r *http.Request) (any, error) {
	var req contract.HostKeyReportRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.ReportHostKey(ctx, &req)
}

// ReportHostKey implements contract.HostKeyReporter.
//
// The `cache` hint is decided in `internal/fleet`, above both responses that
// carry one, so that the M9 liveness read happens once rather than in two
// handlers that would not fail together (PLAN §5.4). Nil is a real answer and
// the common one — no subscription, a first sighting, or a rejection — and it
// means what every server did before the field existed: the proxy reports
// every connection.
func (h handlers) ReportHostKey(ctx context.Context, req *contract.HostKeyReportRequest) (*contract.HostKeyReportResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	if req.Target == "" {
		return nil, invalid("target is required")
	}
	if req.HostKey.Fingerprint == "" {
		return nil, invalid("host_key.fingerprint is required")
	}

	decision, err := h.s.fleet.ReportHostKey(ctx, caller.Tenant, fleet.HostKeyReport{
		Hostname:    req.Target,
		Port:        req.TargetPort,
		Fingerprint: req.HostKey.Fingerprint,
		KeyType:     req.HostKey.Type,
		ReportedBy:  reporter(caller, req.Conn),
	})
	if err != nil {
		return nil, err
	}

	out := &contract.HostKeyReportResponse{
		Decision: contract.HostKeyDecision(decision.Decision),
		Known:    decision.Known,
		Reason:   decision.Reason,
		Cache:    decision.Cache,
	}
	if decision.Changed {
		// Safe to disclose, and worth disclosing: the proxy puts it in its
		// own audit record for the connection that saw the new key.
		out.Reason = "this target has presented a different host key before"
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// capability reports
// ---------------------------------------------------------------------------

func (h handlers) reportCapabilities(ctx context.Context, r *http.Request) (any, error) {
	var req contract.CapabilityReportRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.ReportCapabilities(ctx, &req)
}

// ReportCapabilities implements contract.CapabilityReporter.
//
// `accepted` IS ANSWERED TRUTHFULLY: a report this server did not record is
// one the proxy must not believe it made, so a write that did not land is a
// 5xx rather than a cheerful `accepted: true`. It is the same discipline the
// priority log path keeps — the ack means it is stored (PLAN §4).
//
// The record is an OBSERVATION and never a grant. Nothing here widens a
// decision: the authority for a rung is the authorize response, and the proxy
// re-checks it against the live target at provisioning time.
func (h handlers) ReportCapabilities(ctx context.Context, req *contract.CapabilityReportRequest) (*contract.CapabilityReportResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	if req.Target == "" {
		return nil, invalid("target is required")
	}

	rec := fleet.TargetCapabilities{
		Key: fleet.TargetCapabilityKey{
			Hostname: req.Target,
			Port:     req.TargetPort,
			Platform: req.Platform,
		},
		Detail:     req.Capabilities.Detail,
		ReportedBy: reporter(caller, req.Conn),
	}
	for _, r := range req.Capabilities.Execution {
		rec.Execution = append(rec.Execution, contract.ExecutionRung(r))
	}
	for _, r := range req.Capabilities.Reach {
		rec.Reach = append(rec.Reach, contract.ReachRung(r))
	}
	rec.ObservedAt = h.observedAt(ctx, req)

	reportAfter, err := h.s.fleet.ReportTargetCapabilities(ctx, caller.Tenant, rec)
	if err != nil {
		return nil, err
	}
	return &contract.CapabilityReportResponse{
		Accepted:           true,
		ReportAfterSeconds: int32(reportAfter.Seconds()),
	}, nil
}

// observedAt reads the report's observation time.
//
// AN UNREADABLE OR ABSENT TIME IS STORED UNDATED, not refused and not stamped
// with arrival. Undated reads back as STALE, and stale, undated and absent are
// deliberately one case (M17): each provides nothing that has to be applied
// and leaves untouched every rung that needs nothing of the target. Stamping
// arrival time instead would make a record the proxy could not date look
// fresh, which is precisely the fail-open the rule exists to prevent — and
// refusing the whole report would cost the proxy a retry loop over a field
// this server does not need in order to fail safe.
func (h handlers) observedAt(ctx context.Context, req *contract.CapabilityReportRequest) time.Time {
	raw := req.Capabilities.ObservedAt
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		h.s.log.WarnContext(ctx, "capability report carried an unreadable observed_at",
			"event", "capability_report_undated",
			"target", req.Target,
			"platform", req.Platform,
			"correlation_id", correlationIDFrom(ctx),
		)
		return time.Time{}
	}
	return t
}

// ---------------------------------------------------------------------------
// uid leases
// ---------------------------------------------------------------------------

func (h handlers) leaseUIDs(ctx context.Context, r *http.Request) (any, error) {
	var req contract.UIDLeaseRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.LeaseUIDs(ctx, &req)
}

// LeaseUIDs implements contract.UIDLeaser.
//
// A 409 here is the cursor at the top of its range, which the proxy treats
// exactly as an exhausted block — outage-class, nothing provisioned, and the
// remedy is the operator's. It is NEVER a 200 carrying an empty or inverted
// block.
func (h handlers) LeaseUIDs(ctx context.Context, req *contract.UIDLeaseRequest) (*contract.UIDLeaseResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	if req.ProxyID == "" {
		return nil, invalid("proxy_id is required")
	}
	if req.Target == "" {
		return nil, invalid("target is required")
	}
	if req.ObservedFloor < 0 {
		return nil, invalid("observed_floor must not be negative")
	}
	if !caller.Authorises(req.ProxyID) {
		// A token issued to one proxy may not lease in another's name: the
		// lease id is what an incident resolves a uid back to, and a
		// credential that could name anybody makes that answer worthless.
		h.s.logDeny(ctx, contract.PathUIDLease, "proxy-id-not-authorised",
			"requested_proxy_id", req.ProxyID, "token_proxy_id", caller.ProxyID)
		return nil, deny("the credential was not accepted")
	}

	lease, err := h.s.fleet.LeaseUIDs(ctx, caller.Tenant, fleet.UIDLeaseRequest{
		ProxyID:       req.ProxyID,
		Hostname:      req.Target,
		Port:          req.TargetPort,
		Count:         req.UIDCount,
		RangeMin:      req.RangeMin,
		RangeMax:      req.RangeMax,
		ObservedFloor: req.ObservedFloor,
	})
	switch {
	case err == nil:
	case errors.Is(err, fleet.ErrUIDRangeUnsatisfiable), store.IsExhausted(err):
		return nil, exhausted("the uid range for this target is exhausted")
	case errors.Is(err, fleet.ErrUIDRangeInvalid):
		return nil, invalid("range_max must be greater than range_min")
	default:
		return nil, err
	}

	return &contract.UIDLeaseResponse{
		LeaseID:     lease.LeaseID,
		UIDFrom:     lease.From,
		UIDTo:       lease.To,
		TermSeconds: int32(lease.Term.Seconds()),
	}, nil
}

// ---------------------------------------------------------------------------
// shared
// ---------------------------------------------------------------------------

// reporter names the proxy a report is attributed to.
//
// A bound token's own proxy id wins over the one in the body, because the
// credential is what this server verified and the body is what the caller
// said. An unbound token has nothing to verify against, so the body is all
// there is — which is exactly why an unbound token is a bootstrap credential
// rather than a deployment's steady state.
func reporter(c fleet.ProxyCaller, conn contract.ConnMeta) string {
	if c.Bound() {
		return c.ProxyID
	}
	return conn.ProxyID
}

// The two failures that mean this server is wrong rather than the caller.
// Both are unclassified, so both are 5xx — which is the answer M11 demands for
// anything that is not a decision.
var (
	errNoCaller     = errors.New("south: the request reached a handler with no verified caller")
	errEmptyOutcome = errors.New("south: the identity service returned an empty outcome")
)
