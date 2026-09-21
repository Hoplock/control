// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"errors"
	"net/http"
	"time"

	"github.com/hoplock/control/internal/credential"
)

// The certificate authority's operator surface (proxy D6a).
//
// It is read-and-rotate, and it is deliberately small. What an operator needs
// from a CA is: what should my targets trust, and how do I change the key. The
// ISSUANCE path is not here — a certificate is minted on the decision path, for
// one session, and an endpoint that mints one on request would be a way to get a
// credential without a decision.

type caResponse struct {
	Tenant          string                  `json:"tenant"`
	ActiveKeyID     string                  `json:"active_key_id"`
	Algorithm       string                  `json:"algorithm"`
	ActivePublicKey string                  `json:"active_public_key"`
	Custodian       string                  `json:"custodian"`
	CreatedAt       time.Time               `json:"created_at"`
	TrustBundle     []credential.TrustedKey `json:"trust_bundle"`
	// TrustedUserCAKeys is the trust bundle as a file: every line a target's
	// `TrustedUserCAKeys` must contain. It is rendered here rather than left
	// to the caller because a rotation is only safe if the OLD key is still
	// in the file, and a client assembling the file itself is a client that
	// will one day publish only the active key.
	TrustedUserCAKeys string `json:"trusted_user_ca_keys"`
}

// requireCA answers the configuration error when this deployment has no
// certificate authority, so the three CA handlers do not each have to.
func (s *Server) requireCA(w http.ResponseWriter, r *http.Request) bool {
	if s.ca != nil {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, Error{
		Code: CodeCANotConfigured, CorrelationID: CorrelationIDFrom(r.Context()),
		Message: "this deployment has no certificate authority: set credential.key_encryption_key_env and restart",
		Parameters: map[string]any{
			"config_key": "credential.key_encryption_key_env",
		},
	})
	return false
}

func (s *Server) handleCADescribe(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	if !s.requireCA(w, r) {
		return
	}
	info, err := s.ca.Describe(r.Context(), tenant)
	if errors.Is(err, credential.ErrNoCA) {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message:    "this tenant has no certificate authority yet",
			Parameters: map[string]any{"tenant": tenant.String()},
		})
		return
	}
	if err != nil {
		s.fail(w, r, "describe ca", err)
		return
	}
	answer(w, http.StatusOK, caResponse{
		Tenant:            tenant.String(),
		ActiveKeyID:       info.ActiveKeyID,
		Algorithm:         info.Algorithm,
		ActivePublicKey:   info.ActivePublicKey,
		Custodian:         info.Custodian,
		CreatedAt:         info.CreatedAt,
		TrustBundle:       info.TrustBundle,
		TrustedUserCAKeys: trustedUserCAKeys(info),
	})
}

func trustedUserCAKeys(info credential.Info) string {
	out := ""
	for _, k := range info.TrustBundle {
		out += k.PublicKey + "\n"
	}
	return out
}

type caRotateRequest struct {
	// Comment is the operator's note about why.
	Comment string `json:"comment"`
	// Compromise decides the fate of outstanding certificates. It has no
	// default in the JSON sense — false is a routine rotation — and the
	// response states what was done either way, because "we rotated" without
	// that answer is not a rotation story.
	Compromise bool `json:"compromise"`
}

type caRotateResponse struct {
	PreviousKeyID string    `json:"previous_key_id"`
	NewKeyID      string    `json:"new_key_id"`
	Compromise    bool      `json:"compromise"`
	TrustedUntil  time.Time `json:"previous_key_trusted_until"`
	// RevokedCertificates is how many sessions this rotation ended: zero on a
	// routine rotation.
	RevokedCertificates int64      `json:"revoked_certificates"`
	CA                  caResponse `json:"ca"`
}

func (s *Server) handleCARotate(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	if !s.requireCA(w, r) {
		return
	}
	var body caRotateRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, Error{
				Code: CodeInvalidRequest, CorrelationID: CorrelationIDFrom(r.Context()),
				Message: "this request body could not be read",
			})
			return
		}
	}

	result, err := s.ca.Rotate(r.Context(), tenant, credential.RotateRequest{
		Comment:    body.Comment,
		Compromise: body.Compromise,
	})
	if errors.Is(err, credential.ErrNoCA) {
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: CorrelationIDFrom(r.Context()),
			Message:    "this tenant has no certificate authority to rotate",
			Parameters: map[string]any{"tenant": tenant.String()},
		})
		return
	}
	if err != nil {
		s.fail(w, r, "rotate ca", err)
		return
	}

	answer(w, http.StatusOK, caRotateResponse{
		PreviousKeyID:       result.PreviousKeyID,
		NewKeyID:            result.NewKeyID,
		Compromise:          body.Compromise,
		TrustedUntil:        result.TrustedUntil,
		RevokedCertificates: result.RevokedCertificates,
		CA: caResponse{
			Tenant:            tenant.String(),
			ActiveKeyID:       result.Info.ActiveKeyID,
			Algorithm:         result.Info.Algorithm,
			ActivePublicKey:   result.Info.ActivePublicKey,
			Custodian:         result.Info.Custodian,
			CreatedAt:         result.Info.CreatedAt,
			TrustBundle:       result.Info.TrustBundle,
			TrustedUserCAKeys: trustedUserCAKeys(result.Info),
		},
	})
}

type certificateView struct {
	Serial      int64     `json:"serial"`
	KeyID       string    `json:"key_id"`
	CAKeyID     string    `json:"ca_key_id"`
	Subject     string    `json:"subject"`
	SessionID   string    `json:"session_id,omitempty"`
	Principals  []string  `json:"principals"`
	Target      string    `json:"target"`
	TargetPort  int       `json:"target_port,omitempty"`
	ValidAfter  time.Time `json:"valid_after"`
	ValidBefore time.Time `json:"valid_before"`
	IssuedAt    time.Time `json:"issued_at"`
}

type certificatesResponse struct {
	Tenant       string            `json:"tenant"`
	Certificates []certificateView `json:"certificates"`
}

// handleCACertificates lists what is still usable.
//
// THE CERTIFICATE ITSELF IS NOT IN THE RESPONSE. It is not a secret — a
// certificate is a public document — but handing one back would make this
// endpoint a way to collect credentials that policy issued to a proxy, and there
// is no operator question that needs the bytes rather than the scope.
func (s *Server) handleCACertificates(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	if !s.requireCA(w, r) {
		return
	}
	rows, err := s.ca.Outstanding(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "list certificates", err)
		return
	}
	out := make([]certificateView, 0, len(rows))
	for _, c := range rows {
		out = append(out, certificateView{
			Serial:      c.Serial,
			KeyID:       c.KeyID,
			CAKeyID:     c.CAKeyID,
			Subject:     c.SubjectID,
			SessionID:   c.SessionID,
			Principals:  c.Principals,
			Target:      c.Target,
			TargetPort:  c.TargetPort,
			ValidAfter:  c.ValidAfter,
			ValidBefore: c.ValidBefore,
			IssuedAt:    c.IssuedAt,
		})
	}
	answer(w, http.StatusOK, certificatesResponse{Tenant: tenant.String(), Certificates: out})
}

type claimMappingResponse struct {
	Tenant string `json:"tenant"`
	// Version is what a decision record names (M4, M7).
	Version int    `json:"version"`
	Digest  string `json:"digest"`
	// Attributes and Groups are what this mapping CAN produce. A policy
	// author reads them to know which names a rule may match on, and it is
	// the answer to "why did Alice not match the sre rule" as often as the
	// rule is.
	Attributes  []string `json:"attributes"`
	Groups      []string `json:"groups"`
	Description string   `json:"description,omitempty"`
	Document    string   `json:"document"`
}

func (s *Server) handleClaimMapping(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	mapping, err := s.federation.ActiveMapping(r.Context(), tenant)
	if err != nil {
		s.fail(w, r, "read claim mapping", err)
		return
	}
	answer(w, http.StatusOK, claimMappingResponse{
		Tenant:      tenant.String(),
		Version:     mapping.Version,
		Digest:      mapping.Digest,
		Attributes:  mapping.MappedAttributeNames(),
		Groups:      mapping.MappedGroupNames(),
		Description: mapping.Description,
		Document:    mapping.Document(),
	})
}
