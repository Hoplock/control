// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"testing"

	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Trust on first use WITH A RECORD (proxy D7): the answer is explicit every
// time, so a stricter per-target policy later needs no proxy change.
func TestAFirstSightingIsAcceptedAndUnknown(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)

	got, err := reg.ReportHostKey(t.Context(), uidTenant, fleet.HostKeyReport{
		Hostname: "fresh.example.com", Port: 22,
		Fingerprint: "SHA256:first", KeyType: "ssh-ed25519", ReportedBy: "proxy-1",
	})
	if err != nil {
		t.Fatalf("ReportHostKey: %v", err)
	}
	if got.Decision != store.HostKeyAccepted {
		t.Errorf("decision = %q, want accept", got.Decision)
	}
	if got.Known {
		t.Error("a key the server has never seen answered known: true; recording it is what changes the answer")
	}
	if got.Changed {
		t.Error("the first key a target ever presented was reported as changed")
	}
}

func TestAKnownKeyRoundTrips(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)
	ctx := t.Context()

	rep := fleet.HostKeyReport{
		Hostname: "known.example.com", Port: 22,
		Fingerprint: "SHA256:known", KeyType: "ssh-ed25519", ReportedBy: "proxy-1",
	}
	if _, err := reg.ReportHostKey(ctx, uidTenant, rep); err != nil {
		t.Fatalf("first report: %v", err)
	}
	got, err := reg.ReportHostKey(ctx, uidTenant, rep)
	if err != nil {
		t.Fatalf("second report: %v", err)
	}
	if !got.Known {
		t.Error("a key reported a moment ago answered known: false")
	}
	if got.Changed {
		t.Error("the same key reported twice was reported as changed")
	}
}

// THE SECURITY EVENT. A target presenting a key it has not presented before is
// a man-in-the-middle, a rotated key or a rebuilt host, and this server has to
// notice all three.
//
// The detection is a property of the key rather than of bookkeeping on top: a
// record is identified by (hostname, port, fingerprint), which is exactly the
// shape the proxy keys reuse on — so a proxy holding a cached answer for the
// old key MISSES on the new one and reports it on the first connection that
// sees it. That is what keeps this honest under reuse, and it is why the case
// below is the same whether or not a hint was ever issued.
func TestAChangedHostKeyIsDetectedAndNamesWhatCameBefore(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)
	ctx := t.Context()

	base := fleet.HostKeyReport{
		Hostname: "rotated.example.com", Port: 22, KeyType: "ssh-ed25519", ReportedBy: "proxy-1",
	}
	old := base
	old.Fingerprint = "SHA256:the-old-key"
	if _, err := reg.ReportHostKey(ctx, uidTenant, old); err != nil {
		t.Fatalf("report the old key: %v", err)
	}

	fresh := base
	fresh.Fingerprint = "SHA256:the-new-key"
	got, err := reg.ReportHostKey(ctx, uidTenant, fresh)
	if err != nil {
		t.Fatalf("report the new key: %v", err)
	}
	if !got.Changed {
		t.Fatal("a target presented a key it had not presented before and it was not reported as changed")
	}
	if got.Known {
		t.Error("the new key answered known: true")
	}
	if len(got.PreviousFingerprints) != 1 || got.PreviousFingerprints[0] != "SHA256:the-old-key" {
		t.Errorf("previous fingerprints = %v, want the old key: an operator tells a rotation from an "+
			"interception by what came before", got.PreviousFingerprints)
	}

	// The old key's record is KEPT. A rotation that erased the previous key
	// would erase the evidence the event is made of.
	all, err := st.TargetHostKeys().ListForTarget(ctx, uidTenant, "rotated.example.com", 22)
	if err != nil {
		t.Fatalf("ListForTarget: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("the target holds %d key records, want both the old and the new", len(all))
	}
}

// A different PORT is a different target, keyed as the proxy keys it. The same
// key on a different port is therefore a first sighting rather than a change.
func TestTheHostKeyRecordIsKeyedByPortAsWellAsHost(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	reg := fleet.New(st)
	ctx := t.Context()

	rep := fleet.HostKeyReport{
		Hostname: "multi.example.com", Port: 22, Fingerprint: "SHA256:same", ReportedBy: "proxy-1",
	}
	if _, err := reg.ReportHostKey(ctx, uidTenant, rep); err != nil {
		t.Fatalf("report on 22: %v", err)
	}
	rep.Port = 2222
	got, err := reg.ReportHostKey(ctx, uidTenant, rep)
	if err != nil {
		t.Fatalf("report on 2222: %v", err)
	}
	if got.Known {
		t.Error("the same fingerprint on a different port answered known: true")
	}
	if got.Changed {
		t.Error("the first key on a port was reported as a change")
	}
}

// THIS PHASE ISSUES NO CACHE HINT, and the difference between "we did not get
// to it" and "we decided not to" is this assertion.
//
// M9: never issue a hint the revocation stream cannot withdraw. The stream is
// 0009's, so a hint issued now would be an access grant with no revocation
// path at all — strictly worse than the reporting traffic it saves. Absent
// means what every server did before the field existed, which the conformance
// suite grades as a pass.
func TestNoHostKeyCacheHintIsIssuedUntilTheStreamCanWithdrawIt(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	if fleet.New(st).HostKeyCacheHint() {
		t.Fatal("this phase issues a host-key cache hint, but does not serve the revocation stream that " +
			"would withdraw one (M9). Before turning it on: enforce the liveness read on this path, " +
			"hint only an already-known accepted key, and store the issued key on the host-key record " +
			"so 0009 can publish it — a subject-scoped invalidation cannot reach a host-key decision.")
	}
}
