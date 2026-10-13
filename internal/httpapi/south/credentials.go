// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/credential"
)

// POST /v1/credentials/certificate: one brokered certificate per session
// (Hoplock/proxy#68, proxy D6a).
//
// A translation and a call, like every handler here. What binds an issuance to
// a decision this server made, and signs exactly the submitted key, is
// `credential.CA.IssueForSession`; what is chosen at this layer is which of the
// four answers the caller gets:
//
//   - 200 with the certificate;
//   - 400 for a request this server cannot act on (no session_id, a key that
//     does not parse or is itself a certificate);
//   - 401 through [refuseToMint] — a decision_id this server never made, one
//     that denied or names no certificate rung, a target or account it did
//     not name, or a decision made for another proxy;
//   - 503 through [noAuthority] — a tenant with no CA, or a deployment with
//     no `credential.key_encryption_key_env`;
//
// and everything else — a store failure reading the decision or writing the
// row — is a 5xx, never a 401 (M11).

var _ contract.CertificateIssuer = handlers{}

func (h handlers) issueCertificate(ctx context.Context, r *http.Request) (any, error) {
	var req contract.CertificateRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	return h.IssueCertificate(ctx, &req)
}

// IssueCertificate implements contract.CertificateIssuer.
func (h handlers) IssueCertificate(ctx context.Context, req *contract.CertificateRequest) (*contract.CertificateResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	if req.SessionID == "" || req.PublicKey == "" {
		return nil, invalid("session_id and public_key are required")
	}
	if h.s.ca == nil {
		return nil, noAuthority("no certificate authority is configured on this server; set credential.key_encryption_key_env")
	}

	cert, err := h.s.ca.IssueForSession(ctx, caller.Tenant, credential.SessionRequest{
		ProxyID:    caller.ProxyID,
		SessionID:  req.SessionID,
		DecisionID: req.DecisionID,
		Target:     req.Target,
		Username:   req.Username,
		PublicKey:  req.PublicKey,
	})
	var refusal *credential.MintRefusal
	var malformed *credential.MalformedRequestError
	switch {
	case errors.As(err, &refusal):
		h.s.logDeny(ctx, contract.PathCredentialCertificate, refusal.Reason,
			"code", refusal.Code, "decision_id", req.DecisionID, "session_id", req.SessionID)
		return nil, refuseToMint(refusal.Code, refusal.Message)
	case errors.As(err, &malformed):
		return nil, invalid(malformed.Message)
	case errors.Is(err, credential.ErrNoCA):
		return nil, noAuthority("no certificate authority is configured for this tenant")
	case err != nil:
		return nil, err
	}
	return &contract.CertificateResponse{
		Certificate:  cert.Certificate,
		Serial:       contract.FormatSerial(cert.Serial),
		ValidBefore:  cert.ValidBefore.UTC().Format(time.RFC3339),
		CAPublicKeys: cert.TrustBundle,
	}, nil
}
