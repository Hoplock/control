// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The proxy's channel credential (M2).
//
// South-bound is a bearer token in the prototype, with mTLS as the intended
// production form — the seam is this file plus the middleware that calls it,
// and nothing above either knows which was presented.
//
// The token is `<tenant>.<secret>`, the same shape as an enrollment token and
// for the same reason (M18): THE CREDENTIAL CARRIES THE TENANT, so the tenant
// is resolved from something this server minted rather than from something the
// caller asserted. Nothing looks a token up across tenants — the tenant is
// parsed from the credential and the secret is then verified against the rows
// under that tenant, and a forged prefix simply fails the comparison in a
// tenant where no such token exists. The contract grows no tenant field, which
// is the strongest evidence the seam is in the right place.

// proxyTokenSecretBytes is how much entropy a channel token carries.
const proxyTokenSecretBytes = 32

// ProxyToken is the credential a proxy presents on every south-bound call.
type ProxyToken struct {
	// Tenant is the tenant the token lives in.
	Tenant store.Tenant
	// Secret is the high-entropy half. It is never stored — only its
	// SHA-256 is.
	Secret string
}

// String renders the token in the form an operator hands to a proxy.
//
// Like [EnrollmentToken.String], it is deliberately not a redaction: this
// value exists to be transported once. What must never be logged is the token,
// and that is a rule about callers.
func (t ProxyToken) String() string { return string(t.Tenant) + tokenSeparator + t.Secret }

// Hash is what the row stores.
func (t ProxyToken) Hash() []byte {
	sum := sha256.Sum256([]byte(t.Secret))
	return sum[:]
}

// MintProxyToken issues a channel token for a tenant.
//
// The caller shows the result ONCE and stores only [ProxyToken.Hash]. It must
// not be logged, echoed in an error, or written to an audit record: an audit
// record of a credential is a credential in the audit store, which is the one
// place in this system designed never to forget anything.
func MintProxyToken(tenant store.Tenant) (ProxyToken, error) {
	if tenant == "" {
		return ProxyToken{}, fmt.Errorf("fleet.MintProxyToken: tenant is required")
	}
	if strings.Contains(string(tenant), tokenSeparator) {
		return ProxyToken{}, fmt.Errorf(
			"fleet.MintProxyToken: tenant %q contains %q, which a token cannot carry",
			tenant, tokenSeparator)
	}
	buf := make([]byte, proxyTokenSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return ProxyToken{}, fmt.Errorf("fleet.MintProxyToken: %w", err)
	}
	return ProxyToken{Tenant: tenant, Secret: base64.RawURLEncoding.EncodeToString(buf)}, nil
}

// ParseProxyToken reads a presented token, resolving the tenant and nothing
// else.
//
// A malformed token is not an error here. "This is not shaped like a token"
// and "this token's secret is wrong" are the same fact to a caller, and
// telling them apart would make the listener an oracle — so both come back as
// "not recognised" from [Registry.AuthenticateProxyToken].
func ParseProxyToken(presented string) (ProxyToken, bool) {
	tenant, secret, ok := strings.Cut(presented, tokenSeparator)
	if !ok || tenant == "" || secret == "" {
		return ProxyToken{}, false
	}
	return ProxyToken{Tenant: store.Tenant(tenant), Secret: secret}, true
}

// ProxyCaller is who a verified token says is calling.
type ProxyCaller struct {
	// Tenant is resolved from the credential (M18).
	Tenant store.Tenant
	// TokenID identifies the credential, for revocation and for logs. It
	// is not a secret.
	TokenID string
	// ProxyID is the proxy the token was issued to, EMPTY when the token
	// is not bound to one.
	ProxyID string
}

// Bound reports whether the token names a single proxy.
func (c ProxyCaller) Bound() bool { return c.ProxyID != "" }

// Authorises reports whether this caller may speak for a proxy id.
//
// An unbound token authorises any proxy of its tenant; a bound one authorises
// only itself. That is what stops a token issued to an edge proxy from leasing
// uids in another proxy's name.
func (c ProxyCaller) Authorises(proxyID string) bool {
	return !c.Bound() || proxyID == "" || proxyID == c.ProxyID
}

// IssueProxyToken mints a channel credential and stores its hash.
//
// `proxyID` EMPTY mints an UNBOUND token: one that authenticates "a proxy of
// this tenant" and nothing narrower. That is a real and occasionally necessary
// credential — bootstrapping, and the conformance harness, which drives two
// proxy ids through one listener — and it is spelled out at the call site
// rather than arrived at by leaving a field blank.
func (r *Registry) IssueProxyToken(ctx context.Context, tenant store.Tenant, proxyID, label string, expiresAt time.Time) (ProxyToken, string, error) {
	token, err := MintProxyToken(tenant)
	if err != nil {
		return ProxyToken{}, "", err
	}
	tokenID, err := newTokenID()
	if err != nil {
		return ProxyToken{}, "", err
	}
	err = r.st.ProxyTokens().Insert(ctx, tenant, store.ProxyAPIToken{
		TokenID:   tokenID,
		ProxyID:   proxyID,
		TokenHash: token.Hash(),
		Label:     label,
		IssuedAt:  r.now(),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return ProxyToken{}, "", err
	}
	return token, tokenID, nil
}

// AuthenticateProxyToken verifies a presented credential.
//
// The three returns keep M11 structural rather than conventional: `ok == false`
// is a REFUSAL this server decided on, a non-nil error is an OUTAGE, and there
// is no value of either that the other could be mistaken for. A caller cannot
// accidentally turn a database timeout into a 401 because a database timeout
// never arrives as `ok == false`.
func (r *Registry) AuthenticateProxyToken(ctx context.Context, presented string) (ProxyCaller, bool, error) {
	token, ok := ParseProxyToken(presented)
	if !ok {
		return ProxyCaller{}, false, nil
	}

	row, err := r.st.ProxyTokens().GetByHash(ctx, token.Tenant, token.Hash())
	if err != nil {
		if store.IsNotFound(err) {
			return ProxyCaller{}, false, nil
		}
		return ProxyCaller{}, false, err
	}
	// The lookup was already by hash, so this comparison is belt and
	// braces against a future lookup that is not — and it is constant time
	// because there is no reason for it not to be.
	if subtle.ConstantTimeCompare(row.TokenHash, token.Hash()) != 1 {
		return ProxyCaller{}, false, nil
	}
	if !row.Usable(r.now()) {
		return ProxyCaller{}, false, nil
	}
	return ProxyCaller{Tenant: token.Tenant, TokenID: row.TokenID, ProxyID: row.ProxyID}, true, nil
}

// ProxyByKeyFingerprint answers "is this key one of the fleet's own proxies" —
// the chain-leg question on `/v1/auth/cert` (proxy D11).
//
// It reads the enrolled rows through a column generated from `public_key`, so
// the registry that already answers "which proxies are ours" is the one that
// answers "which keys are ours". A second list maintained beside it would
// drift the first time a proxy re-enrolled with a new key, and the failure
// would be silent in the direction that authenticates.
//
// A proxy that is not ENROLLED is not a hop: a revoked proxy that kept its key
// must not be able to authenticate a leg. That is the same rule liveness
// applies to routing, applied one step earlier — and note it is enrollment
// rather than liveness, because a proxy that is mid-restart is still one of
// ours.
func (r *Registry) ProxyByKeyFingerprint(ctx context.Context, tenant store.Tenant, fingerprint string) (string, bool, error) {
	if fingerprint == "" {
		return "", false, nil
	}
	p, err := r.st.Proxies().GetByKeyFingerprint(ctx, tenant, fingerprint)
	if err != nil {
		if store.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if p.State != store.EnrollmentEnrolled {
		return "", false, nil
	}
	return p.ID, true, nil
}

// newTokenID mints the non-secret handle an operator revokes a token by.
func newTokenID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("fleet: mint token id: %w", err)
	}
	return "tok-" + base64.RawURLEncoding.EncodeToString(buf), nil
}
