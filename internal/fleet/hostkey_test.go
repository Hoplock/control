// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"errors"
	"testing"
	"time"

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

// The host-key cache hint, and the three conditions that withhold one.
//
// M9 is the rule that kept this off until now — never issue a hint the
// revocation stream cannot withdraw — and 0009 serves the stream, so the
// answer flips. What does not flip is the rule: the gate is still
// [fleet.Registry.EventStreamHealthy], still taken inside the one issue path
// both responses go through, and still the reason a proxy with no
// subscription is answered without a hint.
func TestTheHostKeyCacheHintIsIssuedOnlyWhenItCanBeWithdrawn(t *testing.T) {
	t.Parallel()

	const (
		target      = "hinted.example.com"
		port        = int32(22)
		fingerprint = "SHA256:hinted"
	)
	report := fleet.HostKeyReport{
		Hostname: target, Port: port, Fingerprint: fingerprint, ReportedBy: "proxy-1",
	}

	if !fleet.New(storetest.New(t)).HostKeyCacheHint() {
		t.Fatal("this server no longer issues a host-key cache hint; if that is deliberate, " +
			"say so in Registry.HostKeyCacheHint and change this test with it")
	}

	t.Run("no subscription, no hint", func(t *testing.T) {
		t.Parallel()
		// Nothing wired: there is no stream a withdrawal could travel
		// over, so a hint would be a grant with no revocation path.
		st := storetest.New(t)
		reg := fleet.New(st)
		ctx := t.Context()

		if _, err := reg.ReportHostKey(ctx, uidTenant, report); err != nil {
			t.Fatalf("first report: %v", err)
		}
		got, err := reg.ReportHostKey(ctx, uidTenant, report)
		if err != nil {
			t.Fatalf("second report: %v", err)
		}
		if !got.Known {
			t.Fatal("a key reported a moment ago answered known: false")
		}
		if got.Cache != nil {
			t.Fatalf("cache = %+v, want none: proxy-1 holds no event subscription", got.Cache)
		}
	})

	t.Run("a first sighting is never hinted", func(t *testing.T) {
		t.Parallel()
		st, reg, c := newRegistry(t, fleet.WithSubscriptionState(subs{"proxy-1": time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}))
		_, _ = st, c

		got, err := reg.ReportHostKey(t.Context(), uidTenant, report)
		if err != nil {
			t.Fatalf("report: %v", err)
		}
		if got.Cache != nil {
			t.Fatalf("cache = %+v, want none: reusing a first sighting replays trust-on-first-use "+
				"into the audit log for every later connection", got.Cache)
		}
	})

	t.Run("a known accepted key on a live stream is hinted, and the key is on the record", func(t *testing.T) {
		t.Parallel()
		st, reg, _ := newRegistry(t, fleet.WithSubscriptionState(subs{"proxy-1": time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}))
		ctx := t.Context()

		if _, err := reg.ReportHostKey(ctx, uidTenant, report); err != nil {
			t.Fatalf("first report: %v", err)
		}
		got, err := reg.ReportHostKey(ctx, uidTenant, report)
		if err != nil {
			t.Fatalf("second report: %v", err)
		}
		if !got.Cache.Cacheable() {
			t.Fatalf("cache = %+v, want a usable hint", got.Cache)
		}

		// THE KEY ON THE WIRE AND THE KEY ON THE RECORD ARE ONE VALUE.
		// A withdrawal publishes what the record holds, so a record
		// holding anything else is a decision that cannot be withdrawn.
		stored, err := reg.HostKeyCacheKeyOf(ctx, uidTenant, target, port, fingerprint)
		if err != nil {
			t.Fatalf("HostKeyCacheKeyOf: %v", err)
		}
		if stored != got.Cache.Key {
			t.Fatalf("the record holds %q and the response issued %q", stored, got.Cache.Key)
		}

		keys, err := st.TargetHostKeys().ListForTarget(ctx, uidTenant, target, port)
		if err != nil || len(keys) != 1 {
			t.Fatalf("ListForTarget = %v, %v", keys, err)
		}
		if keys[0].CacheKey != stored {
			t.Fatalf("the stored key is %q, want %q", keys[0].CacheKey, stored)
		}
	})

	t.Run("the scope is the proxy's own lookup and nothing wider", func(t *testing.T) {
		t.Parallel()
		// The proxy keys host-key reuse on target, port and fingerprint.
		// A key that varied by anything else would be an entry the proxy
		// never looks up; one that varied by less would be two decisions
		// sharing an entry.
		base := fleet.HostKeyCacheKey(uidTenant, target, port, fingerprint)
		for _, tc := range []struct {
			name string
			key  string
		}{
			{"a different target", fleet.HostKeyCacheKey(uidTenant, "other.example.com", port, fingerprint)},
			{"a different port", fleet.HostKeyCacheKey(uidTenant, target, 2222, fingerprint)},
			{"a different fingerprint", fleet.HostKeyCacheKey(uidTenant, target, port, "SHA256:other")},
			{"a different tenant", fleet.HostKeyCacheKey(store.Tenant("other"), target, port, fingerprint)},
		} {
			if tc.key == base {
				t.Errorf("%s produces the same cache key, so two decisions share one entry", tc.name)
			}
		}
		// And the same question always produces the same answer: two
		// Control nodes must derive one key, or the fleet holds two
		// entries for one decision and a withdrawal drops one of them.
		if again := fleet.HostKeyCacheKey(uidTenant, target, port, fingerprint); again != base {
			t.Fatalf("the derivation is not stable: %q then %q", base, again)
		}
	})

	t.Run("a decision nobody recorded cannot be withdrawn by key", func(t *testing.T) {
		t.Parallel()
		// Deriving one here would always succeed and would match nothing
		// any proxy holds, so the withdrawal would report success having
		// dropped nothing.
		_, reg, _ := newRegistry(t)
		_, err := reg.HostKeyCacheKeyOf(t.Context(), uidTenant, "unreported.example.com", 22, "SHA256:nothing")
		if !errors.Is(err, fleet.ErrNoHostKeyRecord) {
			t.Fatalf("HostKeyCacheKeyOf on an unknown decision = %v, want ErrNoHostKeyRecord", err)
		}
	})
}
