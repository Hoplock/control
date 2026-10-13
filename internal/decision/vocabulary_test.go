// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision_test

import (
	"errors"
	"testing"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/decision"
)

// declaring returns a request that declares one vocabulary.
func atVersion(version int32, target string) *contract.AuthorizeRequest {
	req := request(target, "proxy-1")
	req.PolicyVersion = &version
	return req
}

// requireMismatch asserts an outage naming both numbers, and never a deny.
func requireMismatch(t *testing.T, h *harness, req *contract.AuthorizeRequest, needed int32) {
	t.Helper()
	out, err := h.authorize(t, req)
	if out.Deny != nil {
		t.Fatal("a vocabulary mismatch was DENIED; it is a rollout problem, not a statement about the user (M11)")
	}
	if out.Response != nil {
		t.Fatalf("a proxy declaring %d was answered; the route needs %d and must be refused whole, never thinned",
			*req.PolicyVersion, needed)
	}
	var mismatch *decision.VersionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want a *decision.VersionMismatchError", err)
	}
	if mismatch.Needed != needed || mismatch.Declared != *req.PolicyVersion {
		t.Errorf("mismatch = %+v, want needed=%d declared=%d", mismatch, needed, *req.PolicyVersion)
	}
}

// brokered-certificate is vocabulary 5 (Hoplock/proxy#68). A proxy declaring 4
// is refused that route WHOLE — never answered with the brokered-key rung alone,
// which would drop a rung the policy wrote — and still gets every route it can
// read.
func TestTheBrokeredCertificateTier(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	requireMismatch(t, h, atVersion(contract.VocabularyBaseline, "cert-host.example.com"),
		contract.VocabularyBrokeredCertificate)

	resp := h.mustAllow(t, atVersion(contract.VocabularyBrokeredCertificate, "cert-host.example.com"))
	if len(resp.TargetAuthLadder) != 2 || resp.TargetAuthLadder[0].Method != contract.TargetAuthBrokeredCertificate {
		t.Fatalf("ladder = %+v, want the certificate rung first and both rungs", resp.TargetAuthLadder)
	}
	for name := range resp.TargetAuthLadder[0].Params {
		switch name {
		case contract.ParamUsername, contract.ParamKeyType, contract.ParamLifetimeSeconds:
		default:
			t.Errorf("the certificate rung carries %q, outside the three policy names", name)
		}
	}

	// A route without the method is answered to the older proxy unchanged.
	h.mustAllow(t, atVersion(contract.VocabularyBaseline, "edge-host.example.com"))
}
