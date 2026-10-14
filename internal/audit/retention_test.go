// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Retention deletes a contiguous prefix and records where the chain resumes,
// so the verifier passes across a pass — and a deletion from the middle, which
// looks exactly like tampering, still breaks it.
func TestARetentionPassKeepsTheChainVerifying(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(8, "sess-retain")...)

	streams, err := st.Audit().Streams(ctx, tenantA)
	if err != nil || len(streams) != 1 {
		t.Fatalf("streams = %v, %v", streams, err)
	}
	stream := streams[0]

	marker, deleted, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 5, "admin@example.com")
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	if deleted != 5 || marker.ThroughSeq != 5 || marker.ThroughHash == "" {
		t.Fatalf("retained %d through %d (%q), want 5 through 5 with a hash", deleted, marker.ThroughSeq, marker.ThroughHash)
	}

	result, err := verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.OK() {
		t.Fatalf("a lawful retention pass broke the chain: %s", result.Streams[0].Break)
	}
	if s := result.Streams[0]; s.Records != 3 || s.RetainedThrough != 5 {
		t.Errorf("walked %d records from %d, want 3 resuming after 5", s.Records, s.RetainedThrough)
	}

	// Appending after a pass continues the same chain.
	mustIngest(ctx, t, in, tenantA, proxyA, records(2, "sess-after")...)
	if result, err := verifier.VerifyTenant(ctx, tenantA); err != nil || !result.OK() {
		t.Fatalf("appending after retention: %v %v", err, result.Streams)
	}

	// A record deleted from the middle is still a break, retention or not.
	if _, err := st.Pool().Exec(ctx,
		`DELETE FROM audit_records WHERE tenant = $1 AND stream = $2 AND chain_seq = 7`, tenantA, stream); err != nil {
		t.Fatalf("delete: %v", err)
	}
	result, err = verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.OK() || result.Streams[0].Break.Kind != audit.BreakGap {
		t.Errorf("a deletion from the middle after retention: %+v, want a gap", result.Streams[0])
	}
}

// What a pass refuses: going backwards, and the head.
func TestRetentionNeverMovesBackAndNeverTakesTheHead(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()
	mustIngest(ctx, t, in, tenantA, proxyA, records(4, "sess-bounds")...)
	streams, _ := st.Audit().Streams(ctx, tenantA)
	stream := streams[0]

	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 4, "x"); !store.IsInvalid(err) {
		t.Errorf("retaining through the head answered %v, want invalid", err)
	}
	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, "x"); err != nil {
		t.Fatalf("retain through 2: %v", err)
	}
	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, "x"); !store.IsInvalid(err) {
		t.Errorf("retaining through 2 twice answered %v, want invalid", err)
	}
	if _, _, err := st.Audit().RetainPrefix(ctx, "other-tenant", stream, 1, "x"); !store.IsNotFound(err) {
		t.Errorf("another tenant's retention of this stream answered %v, want not found", err)
	}
}

// A capture's bytes can go while its record stays: the chain still verifies,
// and the store says the bytes are gone rather than changed.
func TestRetainedCapturesAreGoneNotChanged(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	verifier := audit.NewVerifier(st, audit.WithCaptureVerification(true))
	ctx := t.Context()

	rec := record("rec-capture", "sess-capture")
	rec.Kind = "stream"
	rec.Timestamp = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	rec.Payload = base64.StdEncoding.EncodeToString([]byte("whoami\r\nroot\r\n"))
	mustIngest(ctx, t, in, tenantA, proxyA, []contract.LogRecord{rec}...)

	n, err := st.Audit().RetainCaptures(ctx, tenantA, time.Now().Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("retain captures = %d, %v; want 1", n, err)
	}
	if _, err := st.Audit().Capture(ctx, tenantA, rec.RecordID); !store.IsCaptureRetained(err) {
		t.Errorf("reading a retained capture answered %v, want ErrCaptureRetained", err)
	}
	result, err := verifier.VerifyTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.OK() || result.Streams[0].CapturesRetained != 1 {
		t.Errorf("verification after capture retention: %+v", result.Streams[0])
	}
}
