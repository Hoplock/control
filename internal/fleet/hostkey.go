// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/contract"
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
	// Cache is the hint this server issues on the response, or nil for
	// none. It is produced by [Registry.CacheHint] — the one issue path,
	// with the M9 liveness read already in front of it — and never beside
	// it. See [Registry.HostKeyCacheHint].
	Cache *contract.CacheHint
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
// The `cache` hint is issued here, through the one issue path, and only for a
// key this server has ruled on and accepted — see [Registry.HostKeyCacheHint].
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
		// Written on every sighting, from the one derivation, so the key
		// on the record and the key on the wire cannot drift apart. It
		// identifies the DECISION rather than any particular cached copy
		// of it: whether a proxy holds one is what the hint below
		// decides, and an operator withdrawing the decision publishes
		// this either way.
		CacheKey: HostKeyCacheKey(tenant, rep.Hostname, rep.Port, rep.Fingerprint),
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

	out.Cache, err = r.hostKeyCacheHint(ctx, tenant, rep, out)
	if err != nil {
		// Not a failure of the report: the sighting is recorded and the
		// trust answer is decided. Refusing a correct decision over an
		// optimisation is the worse trade (M5), so the answer goes out
		// without a hint and the reason is written down.
		r.log.WarnContext(ctx, "could not decide whether to hint a host-key decision",
			"event", "hostkey_cache_hint_undecided",
			"tenant", tenant.String(),
			"target", rep.Hostname,
			"proxy_id", rep.ReportedBy,
			"error", err.Error(),
		)
	}

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
// `/v1/hostkeys/report`, and since 0009 wired the revocation stream it answers
// YES — subject to the three conditions below, every one of which can withhold
// one on a given report.
//
// The saving is real: upstream measured this endpoint at 46% of the Control
// calls that survive an authorize cache hit. What kept it withheld until now
// was M9 — NEVER ISSUE A HINT THE REVOCATION STREAM CANNOT WITHDRAW — and the
// answer to that is the stream itself, not a change of mind about the rule.
//
// The three conditions, in the order [Registry.hostKeyCacheHint] applies them:
//
//   - THE STREAM MUST BE HEALTHY FOR THE ASKING PROXY. That read is
//     [Registry.EventStreamHealthy] and it is taken inside
//     [Registry.CacheHint], which is the one issue path both responses go
//     through. Two copies of "may I hint this proxy right now" would be two
//     places to get M9 wrong and they would not fail together (PLAN §5.4).
//   - THE KEY MUST BE ONE THIS SERVER HAS RULED ON AND ACCEPTED. A `reject`
//     and a `known: false` are never reused by the proxy however they are
//     hinted, so a hint on either is dead weight — and a hint on a first
//     sighting would replay trust-on-first-use into the audit log for every
//     later connection.
//   - THE KEY THE HINT CARRIES MUST BE ON THE RECORD. It is written by
//     [Registry.ReportHostKey] on every sighting, from the same derivation the
//     hint uses, because a subject-scoped `cache_invalidate` cannot match a
//     host-key decision — it was not made for a person — so withdrawing one
//     means publishing that decision's own key, or `resync`. A key nobody
//     stored is a decision nobody can withdraw short of resyncing the whole
//     fleet's cache.
//
// It is a function rather than a comment so that the answer is asserted by a
// test rather than remembered.
func (r *Registry) HostKeyCacheHint() bool { return true }

// DefaultHostKeyCacheTTL is how long a host-key decision may be reused when
// nothing configures it.
//
// It is the server's risk appetite rather than an optimisation dial: within it,
// a target that has rotated its key, been rebuilt, or been intercepted is
// reported late by the proxies that already hold the old answer. The ceiling in
// [Registry.ClampCacheTTL] bounds it further and only downward.
const DefaultHostKeyCacheTTL = 5 * time.Minute

// hostKeyCacheHint applies the three conditions above.
func (r *Registry) hostKeyCacheHint(ctx context.Context, tenant store.Tenant, rep HostKeyReport, out HostKeyDecision) (*contract.CacheHint, error) {
	if out.Decision != store.HostKeyAccepted || !out.Known {
		// A first sighting and a rejection are both answers the proxy
		// must keep bringing back.
		return nil, nil
	}
	ttl := r.hostKeyCacheTTL
	if ttl <= 0 {
		ttl = DefaultHostKeyCacheTTL
	}
	return r.CacheHint(ctx, tenant, CacheHintRequest{
		ProxyID: rep.ReportedBy,
		TTL:     ttl,
		Scope:   HostKeyCacheScope(rep.Hostname, rep.Port, rep.Fingerprint),
	})
}

// HostKeyCacheScope is the sharing scope of a host-key decision.
//
// It names exactly what the proxy keys its own reuse on — target, port and key
// fingerprint — and nothing wider. What is reused is therefore the answer to
// "may this target, presenting THIS key, be reached", so a target presenting a
// different key is a different lookup, misses, and is reported (proxy D7).
// There is no subject in it, which is the same fact that makes a subject-scoped
// invalidation unable to reach one.
func HostKeyCacheScope(hostname string, port int32, fingerprint string) []string {
	return CacheScope(
		[2]string{"kind", "hostkey"},
		[2]string{"target", hostname},
		[2]string{"port", strconv.FormatInt(int64(port), 10)},
		[2]string{"fingerprint", fingerprint},
	)
}

// HostKeyCacheKey is the key a host-key decision is hinted and withdrawn under.
func HostKeyCacheKey(tenant store.Tenant, hostname string, port int32, fingerprint string) string {
	return CacheKey(tenant, HostKeyCacheScope(hostname, port, fingerprint))
}

// Why withdrawing a host-key decision is a lookup rather than a derivation.
//
// The key is derived, so an operator surface could compute one and publish it
// without asking this server anything — and it would then publish a key for a
// target and fingerprint nobody has ever reported, report success, and drop
// nothing. A revocation that silently misses is worse than one that refuses,
// so the decision is resolved from the record and the two failures below are
// returned rather than papered over.
var (
	// ErrNoHostKeyRecord is a target and fingerprint this server has never
	// been told about. There is no decision to withdraw.
	ErrNoHostKeyRecord = errors.New("fleet: no host-key decision is recorded for that target and fingerprint")
	// ErrNoHostKeyCacheKey is a record written before this server issued
	// keys (migration 0005). The decision exists; the key it would be
	// withdrawn under was never recorded, so `resync` is the only honest
	// answer until the next sighting backfills it.
	ErrNoHostKeyCacheKey = errors.New("fleet: that host-key decision carries no cache key, so it cannot be withdrawn by key")
)

// HostKeyCacheKeyOf resolves a recorded host-key decision to the key it is
// withdrawn under.
func (r *Registry) HostKeyCacheKeyOf(ctx context.Context, tenant store.Tenant, hostname string, port int32, fingerprint string) (string, error) {
	if hostname == "" || fingerprint == "" {
		return "", fmt.Errorf("fleet.HostKeyCacheKeyOf: a hostname and a fingerprint are required")
	}
	keys, err := r.st.TargetHostKeys().ListForTarget(ctx, tenant, hostname, port)
	if err != nil {
		return "", err
	}
	for _, k := range keys {
		if k.Fingerprint != fingerprint {
			continue
		}
		if k.CacheKey == "" {
			return "", ErrNoHostKeyCacheKey
		}
		return k.CacheKey, nil
	}
	return "", ErrNoHostKeyRecord
}
