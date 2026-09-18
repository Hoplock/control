// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
)

func TestErrorfWrapsTheSentinelForItsKind(t *testing.T) {
	cases := []struct {
		kind     ext.Kind
		sentinel error
		is       func(error) bool
	}{
		{ext.KindUnavailable, ext.ErrUnavailable, ext.IsUnavailable},
		{ext.KindInvalid, ext.ErrInvalid, ext.IsInvalid},
		{ext.KindNotFound, ext.ErrNotFound, ext.IsNotFound},
		{ext.KindConflict, ext.ErrConflict, ext.IsConflict},
		{ext.KindDisabled, ext.ErrDisabled, ext.IsDisabled},
		{ext.KindMalformed, ext.ErrMalformed, ext.IsMalformed},
		{ext.KindDenied, ext.ErrDenied, ext.IsDenied},
	}
	for _, c := range cases {
		t.Run(c.kind.String(), func(t *testing.T) {
			err := ext.Errorf(ext.PointAuditSink, "example.com/sink", "AuditSink.Export", c.kind, "because %d", 7)
			if !errors.Is(err, c.sentinel) {
				t.Errorf("errors.Is did not match %v", c.sentinel)
			}
			if !c.is(err) {
				t.Errorf("the Is helper for %v returned false", c.kind)
			}
			if got := ext.KindOf(err); got != c.kind {
				t.Errorf("KindOf = %v, want %v", got, c.kind)
			}
			msg := err.Error()
			for _, want := range []string{"AuditSink.Export", "example.com/sink", "because 7", c.kind.String()} {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

// TestAnUnclassifiedFailureIsAnOutageRatherThanADeny is M11 as a test. A
// vendor integration returning a bare error must never be able to reach the
// one kind that can become "access denied".
func TestAnUnclassifiedFailureIsAnOutageRatherThanADeny(t *testing.T) {
	for _, err := range []error{
		errors.New("the SIEM fell over"),
		fmt.Errorf("wrapped: %w", errors.New("timeout")),
		ext.Errorf(ext.PointAccessContextProvider, "p", "op", ext.KindInternal, "boom"),
	} {
		if got := ext.KindOf(err); got != ext.KindInternal {
			t.Errorf("KindOf(%v) = %v, want internal", err, got)
		}
		if ext.IsDenied(err) {
			t.Errorf("an unclassified error (%v) classified as a deny", err)
		}
	}
	if got := ext.KindOf(nil); got != ext.KindInternal {
		t.Errorf("KindOf(nil) = %v, want the zero value", got)
	}
}

func TestNoEvidenceIsNeitherADenialNorAFailure(t *testing.T) {
	err := fmt.Errorf("probe: %w", ext.ErrNoEvidence)
	if !ext.IsNoEvidence(err) {
		t.Error("IsNoEvidence did not recognise a wrapped ErrNoEvidence")
	}
	if ext.IsDenied(err) {
		t.Error("silence classified as a denial; whether absence is evidence is policy's decision, not the provider's")
	}
}

func TestErrorUnwrapsAndNamesControlWhenNoProviderIsGiven(t *testing.T) {
	cause := errors.New("root")
	err := &ext.Error{Point: ext.PointKeyStore, Op: "KeyStore.Signer", Kind: ext.KindNotFound, Err: cause}
	if !errors.Is(err, cause) {
		t.Error("Unwrap did not expose the cause")
	}
	if !strings.Contains(err.Error(), ext.ProviderControl) {
		t.Errorf("message %q does not attribute a provider-less failure to Control", err)
	}
	bare := &ext.Error{Point: ext.PointKeyStore, Op: "KeyStore.Signer", Kind: ext.KindNotFound}
	if !strings.Contains(bare.Error(), "KeyStore") {
		t.Errorf("message %q does not name the point", bare)
	}
}

func TestKindStringsAreStableCodes(t *testing.T) {
	want := map[ext.Kind]string{
		ext.KindInternal:    "internal",
		ext.KindUnavailable: "unavailable",
		ext.KindInvalid:     "invalid",
		ext.KindNotFound:    "not_found",
		ext.KindConflict:    "conflict",
		ext.KindDisabled:    "disabled",
		ext.KindMalformed:   "malformed",
		ext.KindDenied:      "denied",
	}
	for kind, s := range want {
		if got := kind.String(); got != s {
			t.Errorf("Kind(%d).String() = %q, want %q", int(kind), got, s)
		}
	}
	if got := ext.Kind(99).String(); got != "internal" {
		t.Errorf("an unknown kind rendered as %q; it must fall to the safe direction", got)
	}
}
