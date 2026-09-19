// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"fmt"
	"slices"

	"github.com/hoplock/control/internal/store"
)

// Host-key reporting lives beside the capability store for the reason the two
// share: both are a proxy telling this server what it found about a TARGET,
// keyed by target, on the same south-bound listener. `internal/fleet` is where
// that kind of record lives (PLAN §3) — the alternative was a second package
// that owned half of the same answer.

// HostKeyReport is one sighting, as the proxy reported it.
type HostKeyReport struct {
	// Hostname and Port name the target, exactly as reported. The known
	// imprecision is deliberate and shared with the capability store and
	// the uid cursor: several names resolving to one host are several
	// records here, and tidying that is a target-identity question this
	// phase must not answer a third time.
	Hostname string
	Port     int32
	// Fingerprint is OpenSSH's SHA256 form of the key the target
	// presented.
	Fingerprint string
	// KeyType is the SSH algorithm name.
	KeyType string
	// ReportedBy is the proxy that saw it.
	ReportedBy string
}

// HostKeyDecision is the trust answer, plus what this server learned by
// answering it.
type HostKeyDecision struct {
	// Decision is what the proxy is told.
	Decision store.HostKeyDecisionKind
	// Known reports whether this EXACT key had been recorded before. A
	// first sighting is `false`, and recording it is what changes the
	// answer next time.
	Known bool
	// Reason is safe to disclose and may be empty.
	Reason string
	// Changed reports that this target has presented a DIFFERENT key
	// before. It is a security event — a man-in-the-middle, a rotated key,
	// or a rebuilt host — and it is true even when this key itself is
	// accepted, because the accept is about the key and the event is about
	// the target.
	Changed bool
	// PreviousFingerprints are the other keys this target has presented,
	// which is what an operator needs in order to tell a rotation from an
	// interception.
	PreviousFingerprints []string
}

// ReportHostKey records a sighting and answers the trust decision.
//
// The prototype policy is TRUST ON FIRST USE WITH A RECORD (proxy D7), and the
// answer is explicit every time so that a stricter per-target policy later
// needs no proxy change.
//
// THE CHANGED-KEY DETECTION IS A PROPERTY OF THE KEY, not of bookkeeping on
// top of it. A record is identified by (hostname, port, fingerprint) — exactly
// the shape the proxy keys reuse on — so a target presenting a different key
// is a different record and arrives here as a first sighting for a target that
// already has one. That is what keeps the detection honest under reuse: a
// proxy holding a cached answer for the old key misses on the new one and
// reports it on the first connection that sees it.
//
// This phase issues NO cache hint, so nothing above needs the M9 liveness read
// yet. See [Registry.HostKeyCacheHint] for why, and for what turning hints on
// costs.
func (r *Registry) ReportHostKey(ctx context.Context, tenant store.Tenant, rep HostKeyReport) (HostKeyDecision, error) {
	if rep.Hostname == "" {
		return HostKeyDecision{}, fmt.Errorf("fleet.ReportHostKey: hostname is required")
	}
	if rep.Fingerprint == "" {
		return HostKeyDecision{}, fmt.Errorf("fleet.ReportHostKey: fingerprint is required")
	}

	// The prior set is read BEFORE the write, because after it this key is
	// in it. A race with another proxy reporting the same new key costs at
	// worst a duplicate "changed" log line, and the alternative — deriving
	// the set from the write — cannot see the other fingerprints at all.
	prior, err := r.st.TargetHostKeys().ListForTarget(ctx, tenant, rep.Hostname, rep.Port)
	if err != nil {
		return HostKeyDecision{}, err
	}

	now := r.now()
	recorded, known, err := r.st.TargetHostKeys().Record(ctx, tenant, store.TargetHostKey{
		Hostname:        rep.Hostname,
		Port:            rep.Port,
		Fingerprint:     rep.Fingerprint,
		KeyType:         rep.KeyType,
		Decision:        store.HostKeyAccepted,
		LastSeenAt:      now,
		LastReportedBy:  rep.ReportedBy,
		FirstReportedBy: rep.ReportedBy,
	})
	if err != nil {
		return HostKeyDecision{}, err
	}

	out := HostKeyDecision{Decision: recorded.Decision, Known: known}
	for _, k := range prior {
		if k.Fingerprint != rep.Fingerprint {
			out.PreviousFingerprints = append(out.PreviousFingerprints, k.Fingerprint)
		}
	}
	slices.Sort(out.PreviousFingerprints)
	out.Changed = !known && len(out.PreviousFingerprints) > 0

	if out.Changed {
		// 0010 owns audit ingest; until then this is the record, and the
		// shape is fixed now so that the later ingest is a destination
		// change rather than a rewrite of every field name.
		r.log.WarnContext(ctx, "target presented a host key it has not presented before",
			"event", "host_key_changed",
			"tenant", tenant.String(),
			"target", rep.Hostname,
			"target_port", rep.Port,
			"fingerprint", rep.Fingerprint,
			"previous_fingerprints", out.PreviousFingerprints,
			"reported_by", rep.ReportedBy,
		)
	}
	return out, nil
}

// HostKeyCacheHint reports whether this server issues a `cache` hint on
// `/v1/hostkeys/report`, and it answers NO.
//
// The field exists, the contract names it as its own worked example of a field
// outside `policy_version`, and upstream measured this endpoint at 46% of the
// Control calls that survive an authorize cache hit — so the saving is real
// and it is not the reason to withhold it. M9 is: **never issue a hint the
// revocation stream cannot withdraw**, and this server does not serve
// `GET /v1/proxies/{proxy_id}/events` yet (0009). A hint issued now would be an
// access grant with no revocation path at all, which is strictly worse than
// the reporting traffic it saves.
//
// Absent means what every server did before the field existed — the proxy
// reports every connection — so answering no hint is a correct implementation
// rather than a gap, and the conformance suite grades it as a pass.
//
// The ISSUE PATH now exists above both responses: 0008 built
// [Registry.CacheHint], which takes the M9 liveness read
// ([Registry.EventStreamHealthy]), derives the opaque key and clamps the
// lifetime. This endpoint does not call it, and the reason it still answers no
// is unchanged — with no subscription source wired there is no healthy stream
// and therefore no withdrawable hint, so the shared path would answer no here
// too. What 0009 changes is the stream, not the rule.
//
// What 0009 owes before it may say yes, none of it optional:
//
//   - issue through [Registry.CacheHint] rather than beside it, so the M9
//     liveness read is taken on THIS path as well as on authorize.
//   - hint only a key already ruled on and accepted. A `reject` and a
//     `known: false` are never reused however they are hinted, so a hint on
//     either is dead weight that says the rule was not read.
//   - store the key on the host-key record. A subject-scoped
//     `cache_invalidate` cannot match a host-key decision — it was not made
//     for a person — so withdrawing one means publishing that decision's own
//     key, or `resync`. A key nobody stored is a decision nobody can withdraw
//     short of resyncing the entire fleet's cache.
//
// It is a function rather than a comment so that the answer is asserted by a
// test rather than remembered.
func (r *Registry) HostKeyCacheHint() bool { return false }
