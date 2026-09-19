// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// The issue path for a cache hint, in ONE place (PLAN §5.4).
//
// The same `CacheHint` object rides on two responses — `/v1/authorize` (0008)
// and `POST /v1/hostkeys/report` (0007) — under the same rules, and the rule
// that matters most is M9's: never issue a hint the revocation stream cannot
// withdraw. A cached allow with no way to revoke it is not a faster decision,
// it is a grant with no revocation path.
//
// Two copies of "may I hint this proxy right now" would be two places to get
// that wrong, and they would not fail together — so the liveness read, the key
// derivation and the lifetime clamp live here, above both callers, rather than
// inside either handler.

// DefaultMaxCacheTTL is the longest lifetime this server will authorise,
// whatever a bundle asks for.
//
// The lifetime is policy (PLAN §5.4) and this is not a second opinion about it:
// it is the ceiling under which "we can withdraw this" stays true, since a
// withdrawal travels over a stream whose staleness threshold is measured in
// tens of seconds and a decision cached for an hour outlives any reasonable
// bound on noticing that the stream broke. It clamps DOWNWARD only — the same
// direction the proxy's own optional ceiling clamps, and the same direction as
// every other bound on this contract.
const DefaultMaxCacheTTL = 5 * time.Minute

// CacheKeyVersion prefixes every key this server derives.
//
// It is on the wire, so it is a compatibility surface: a change to what a key
// is derived FROM has to change what a key looks like, or a proxy holding
// entries from the old scheme would keep serving them against keys that now
// mean something else. Bumping the prefix retires every outstanding entry at
// once, which is the behaviour to want.
const CacheKeyVersion = "hc1"

// CacheHintRequest is one "may I hint this, and if so under what key" question.
type CacheHintRequest struct {
	// ProxyID is the proxy that would hold the entry. The M9 read is about
	// THIS proxy's event stream: a hint is only as withdrawable as the
	// stream that reaches the holder.
	ProxyID string
	// TTL is the lifetime policy authored. It is clamped downward, never up.
	TTL time.Duration
	// Scope is the sharing scope, as ordered `component=value` pairs. The
	// caller names the components; this function never invents one, because
	// a key is only as narrow as what went into it and a key shared across
	// identities serves one user another user's policy.
	Scope []string
}

// CacheHint answers whether a hint may be issued for this request, and renders
// it when it may.
//
// A nil hint and a nil error is the ordinary "no" — policy asked for one and
// this server will not authorise it — and it is not a failure: absence means
// what every proxy did before the field existed, which is to re-ask. An ERROR
// is this server being unable to tell whether the stream is healthy; the caller
// answers the request WITHOUT a hint rather than failing it, because refusing a
// correct decision over an optimisation is the worse trade (M5, M11).
func (r *Registry) CacheHint(ctx context.Context, tenant store.Tenant, req CacheHintRequest) (*contract.CacheHint, error) {
	if req.TTL <= 0 || req.ProxyID == "" || len(req.Scope) == 0 {
		return nil, nil
	}
	healthy, err := r.EventStreamHealthy(ctx, tenant, req.ProxyID)
	if err != nil {
		return nil, err
	}
	if !healthy {
		return nil, nil
	}
	ttl := r.ClampCacheTTL(req.TTL)
	if ttl <= 0 {
		return nil, nil
	}
	return &contract.CacheHint{
		Key:        CacheKey(tenant, req.Scope),
		TTLSeconds: int32(ttl.Seconds()),
	}, nil
}

// EventStreamHealthy reports whether this proxy's revocation stream is in a
// state that could carry a withdrawal (M9).
//
// WITH NO SUBSCRIPTION SOURCE WIRED THE ANSWER IS NO, and that is the whole
// behaviour today rather than a placeholder: the stream is 0009's, and until it
// exists there is no path by which a cached decision could be withdrawn. So
// this server issues no hints, which is exactly what every server did before
// the field existed — the proxy re-asks. 0009 wires [WithSubscriptionState] and
// the hints start flowing through this same function, with the M9 read already
// in front of them.
func (r *Registry) EventStreamHealthy(ctx context.Context, tenant store.Tenant, proxyID string) (bool, error) {
	if r.subs == nil || proxyID == "" {
		return false, nil
	}
	subs, err := r.subs.LiveSubscriptions(ctx, tenant)
	if err != nil {
		// Not "unhealthy": this server does not know. The caller withholds
		// the hint either way, and the distinction is kept because one of
		// them is worth a log line and the other is the normal state of a
		// fleet member that is not subscribed.
		return false, err
	}
	seen, ok := subs[proxyID]
	if !ok || seen.IsZero() {
		return false, nil
	}
	return !r.now().After(seen.Add(r.liveness.HeartbeatTTL)), nil
}

// ClampCacheTTL bounds an authored lifetime. It only ever shortens.
func (r *Registry) ClampCacheTTL(ttl time.Duration) time.Duration {
	maxTTL := r.maxCacheTTL
	if maxTTL <= 0 {
		maxTTL = DefaultMaxCacheTTL
	}
	if ttl > maxTTL {
		return maxTTL
	}
	return ttl
}

// CacheKey derives the opaque key a hint carries.
//
// It is a hash rather than the components themselves for two reasons, and the
// second is the load-bearing one: the key travels to a proxy and into its logs,
// so it must not spell out a subject; and it is echoed back in a
// `cache_invalidate` event, so it must be DERIVED — two Control nodes answering
// the same question have to produce the same key, or the fleet holds two
// entries for one decision and a withdrawal drops one of them.
//
// The tenant is always part of the input even though no caller passes it: two
// tenants may name a subject and a target identically, and a key they shared
// would be one tenant's revocation dropping another tenant's session.
func CacheKey(tenant store.Tenant, scope []string) string {
	h := sha256.New()
	// Length-prefixed, so that ["a=b", "c=d"] and ["a=bc=d"] cannot hash
	// alike — an ambiguity here is two different scopes sharing one entry.
	writeField(h, string(tenant))
	for _, part := range scope {
		writeField(h, part)
	}
	sum := h.Sum(nil)
	return CacheKeyVersion + ":" + base64.RawURLEncoding.EncodeToString(sum)
}

func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	var n [8]byte
	l := uint64(len(s))
	for i := range n {
		n[i] = byte(l >> (8 * (7 - i)))
	}
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(s))
}

// CacheScope renders an ordered set of `component=value` pairs.
//
// It exists so that a caller building a scope cannot accidentally produce one
// that collides with another caller's: every pair names its component, and the
// order is the caller's rather than the map's.
func CacheScope(pairs ...[2]string) []string {
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p[0]+"="+p[1])
	}
	return out
}

// CacheScopeString renders a scope for a log line. It is not the key: the key
// is the hash, and a log that printed both would put the subject back beside
// the value that exists to stand in for it.
func CacheScopeString(scope []string) string { return strings.Join(scope, ",") }
