// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The OIDC broker: authorization code + PKCE, an ID token verified against the
// issuer's published keys, and nothing else.
//
// The implicit and hybrid flows are not implemented and will not be: they put
// a token in a URL, which puts it in a browser history, a referrer header and
// a proxy log. PKCE is not optional either — this server always sends a
// challenge, whether or not the IdP requires one, because the cost is one hash
// and the failure it prevents is an intercepted authorization code being
// redeemed by whoever intercepted it.

// OIDCConfig is a connector's configuration document.
//
// The client secret is NOT here. `client_secret_env` names the environment
// variable it is read from, which is what keeps the acceptance criterion — no
// IdP client secret in any log, error or stored row — true by construction
// rather than by review (see secretFromEnv).
type OIDCConfig struct {
	// Issuer is the IdP's issuer identifier. Discovery is done against it,
	// and the ID token's `iss` is compared with it exactly.
	Issuer string `json:"issuer"`
	// ClientID is this server's registration with the IdP.
	ClientID string `json:"client_id"`
	// ClientSecretEnv names the environment variable holding the secret.
	// A public client (no secret) leaves it empty and relies on PKCE.
	ClientSecretEnv string `json:"client_secret_env"`
	// RedirectURI is this server's callback, as registered with the IdP.
	RedirectURI string `json:"redirect_uri"`
	// Scopes are requested in addition to `openid`, which is always sent.
	Scopes []string `json:"scopes"`
	// LoginClaim names the claim carrying the username to seed a new
	// subject with. It is NOT the subject id — `sub` is — because a
	// username is a thing an IdP administrator can change.
	LoginClaim string `json:"login_claim"`
	// UserInfo asks the broker to merge the userinfo endpoint's claims
	// into the assertion. Some IdPs only put group membership there.
	UserInfo bool `json:"userinfo"`
	// ClockSkew is how much clock difference the ID token's time claims are
	// allowed. Zero takes DefaultOIDCSkew.
	ClockSkew time.Duration `json:"clock_skew"`
}

// Defaults for the OIDC broker.
const (
	// DefaultOIDCSkew is how far apart this server's clock and the IdP's
	// may be. Small on purpose: an ID token is minted seconds before it is
	// verified, and a wide window is a wide replay window.
	DefaultOIDCSkew = 60 * time.Second
	// DefaultDiscoveryTTL is how long a discovery document and its key set
	// are cached. A signing key rotating mid-TTL is handled by the retry in
	// Complete, not by making this short.
	DefaultDiscoveryTTL = 15 * time.Minute
	// oidcHTTPTimeout bounds one call to the IdP. A login that hangs is a
	// login that holds a request handler.
	oidcHTTPTimeout = 10 * time.Second
	// stateBytes, nonceBytes and verifierBytes size the per-flow secrets.
	stateBytes    = 24
	nonceBytes    = 24
	verifierBytes = 32
)

// oidcDiscovery is the subset of the discovery document that is read.
type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

// OIDCBroker brokers one OIDC connector.
type OIDCBroker struct {
	name   string
	cfg    OIDCConfig
	client *http.Client
	now    func() time.Time

	mu        sync.Mutex
	discovery oidcDiscovery
	keys      jwkSet
	fetchedAt time.Time
	ttl       time.Duration
}

// NewOIDCBroker builds a broker for a connector.
//
// It does NOT contact the IdP: a control plane that cannot start because an
// IdP is down is a control plane whose availability is the IdP's. Discovery
// happens on the first login and is cached.
func NewOIDCBroker(name string, cfg OIDCConfig, opts ...BrokerOption) (*OIDCBroker, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("identity: an OIDC connector needs a name")
	}
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURI == "" {
		return nil, fmt.Errorf("identity: connector %q needs an issuer, a client id and a redirect uri", name)
	}
	if _, err := url.Parse(cfg.Issuer); err != nil {
		return nil, fmt.Errorf("identity: connector %q has an unparseable issuer", name)
	}
	if cfg.ClockSkew <= 0 {
		cfg.ClockSkew = DefaultOIDCSkew
	}

	b := &OIDCBroker{
		name:   name,
		cfg:    cfg,
		client: &http.Client{Timeout: oidcHTTPTimeout},
		now:    time.Now,
		ttl:    DefaultDiscoveryTTL,
	}
	for _, opt := range opts {
		opt.applyOIDC(b)
	}
	return b, nil
}

// BrokerOption configures a broker. It is one type for both protocols so a
// test harness can pass the same clock and HTTP client to either.
type BrokerOption interface {
	applyOIDC(*OIDCBroker)
	applySAML(*SAMLBroker)
}

type brokerOptionFunc struct {
	oidc func(*OIDCBroker)
	saml func(*SAMLBroker)
}

func (f brokerOptionFunc) applyOIDC(b *OIDCBroker) {
	if f.oidc != nil {
		f.oidc(b)
	}
}

func (f brokerOptionFunc) applySAML(b *SAMLBroker) {
	if f.saml != nil {
		f.saml(b)
	}
}

// WithBrokerHTTPClient overrides the client used to reach an IdP. Tests point
// it at an httptest server; nothing in production should change it.
func WithBrokerHTTPClient(c *http.Client) BrokerOption {
	return brokerOptionFunc{
		oidc: func(b *OIDCBroker) {
			if c != nil {
				b.client = c
			}
		},
		saml: func(b *SAMLBroker) {},
	}
}

// WithBrokerClock overrides the clock. Tests use it; nothing in production
// should.
func WithBrokerClock(now func() time.Time) BrokerOption {
	return brokerOptionFunc{
		oidc: func(b *OIDCBroker) {
			if now != nil {
				b.now = now
			}
		},
		saml: func(b *SAMLBroker) {
			if now != nil {
				b.now = now
			}
		},
	}
}

// WithDiscoveryTTL overrides how long discovery is cached.
func WithDiscoveryTTL(d time.Duration) BrokerOption {
	return brokerOptionFunc{
		oidc: func(b *OIDCBroker) {
			if d > 0 {
				b.ttl = d
			}
		},
		saml: func(b *SAMLBroker) {},
	}
}

// Name returns the connector's name.
func (b *OIDCBroker) Name() string { return b.name }

// Kind returns store.ConnectorOIDC.
func (b *OIDCBroker) Kind() store.ConnectorKind { return store.ConnectorOIDC }

// Metadata returns nothing: an OIDC IdP reads nothing from this server.
func (b *OIDCBroker) Metadata(context.Context) (string, []byte, error) { return "", nil, nil }

// Begin builds the authorization URL and the per-flow secrets.
func (b *OIDCBroker) Begin(ctx context.Context, req BeginRequest) (Begun, error) {
	disco, _, err := b.metadata(ctx, false)
	if err != nil {
		return Begun{}, err
	}

	nonce, err := randomToken(nonceBytes)
	if err != nil {
		return Begun{}, err
	}
	verifier, err := randomToken(verifierBytes)
	if err != nil {
		return Begun{}, err
	}

	redirect := req.RedirectURI
	if redirect == "" {
		redirect = b.cfg.RedirectURI
	}

	scopes := append([]string{"openid"}, b.cfg.Scopes...)
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", b.cfg.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", req.State)
	q.Set("nonce", nonce)
	q.Set("code_challenge", s256(verifier))
	q.Set("code_challenge_method", "S256")
	if req.LoginHint != "" {
		q.Set("login_hint", req.LoginHint)
	}

	u, err := url.Parse(disco.AuthorizationEndpoint)
	if err != nil {
		return Begun{}, fmt.Errorf("identity: connector %q published an unparseable authorization endpoint", b.name)
	}
	u.RawQuery = q.Encode()

	return Begun{RedirectURL: u.String(), Nonce: nonce, PKCEVerifier: verifier}, nil
}

// tokenResponse is the token endpoint's answer, with only what is read.
type tokenResponse struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// Complete redeems the code and verifies the ID token.
func (b *OIDCBroker) Complete(ctx context.Context, req CompleteRequest) (Assertion, error) {
	if errCode := req.Params.Get("error"); errCode != "" {
		// The IdP refused. That is a decision, not an outage, and the
		// description goes to the log rather than to the user: an IdP's
		// error text is not written for this audience.
		return Assertion{}, refuse(RejectFederationNotAllowed,
			"the identity provider refused this login",
			"idp error "+errCode+": "+req.Params.Get("error_description"))
	}
	code := req.Params.Get("code")
	if code == "" {
		return Assertion{}, refuse(RejectFederationProtocol,
			"this login could not be completed", "the callback carried no authorization code")
	}

	disco, keys, err := b.metadata(ctx, false)
	if err != nil {
		return Assertion{}, err
	}

	redirect := req.RedirectURI
	if redirect == "" {
		redirect = b.cfg.RedirectURI
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirect)
	form.Set("client_id", b.cfg.ClientID)
	form.Set("code_verifier", req.Flow.PKCEVerifier)

	secret := ""
	if b.cfg.ClientSecretEnv != "" {
		secret, err = secretFromEnv(b.cfg.ClientSecretEnv)
		if err != nil {
			// A missing secret is a deployment fault, so it is an
			// outage rather than a refusal: the user did nothing
			// wrong and must not be told their credential failed.
			return Assertion{}, err
		}
	}

	var token tokenResponse
	if err := b.postForm(ctx, disco.TokenEndpoint, form, b.cfg.ClientID, secret, &token); err != nil {
		return Assertion{}, err
	}
	if token.Error != "" {
		return Assertion{}, refuse(RejectFederationNotAllowed,
			"the identity provider refused this login",
			"token endpoint error "+token.Error+": "+token.ErrorDesc)
	}
	if token.IDToken == "" {
		return Assertion{}, refuse(RejectFederationProtocol,
			"this login could not be completed", "the token response carried no id_token")
	}

	payload, err := verifyJWS(token.IDToken, keys)
	if err == ErrNoVerifyingKey {
		// A signing key that has rotated since the cache was filled is
		// normal. Refresh ONCE and retry; a second failure is a
		// signature this server will not accept.
		_, refreshed, ferr := b.metadata(ctx, true)
		if ferr != nil {
			return Assertion{}, ferr
		}
		payload, err = verifyJWS(token.IDToken, refreshed)
	}
	if err != nil {
		return Assertion{}, refuse(RejectFederationSignature,
			"this login could not be verified", err.Error())
	}

	assertion, err := b.assertionFrom(payload, req.Flow.Nonce)
	if err != nil {
		return Assertion{}, err
	}

	if b.cfg.UserInfo && disco.UserInfoEndpoint != "" && token.AccessToken != "" {
		b.mergeUserInfo(ctx, disco.UserInfoEndpoint, token.AccessToken, assertion.Subject, &assertion)
	}
	return assertion, nil
}

// assertionFrom validates an ID token's claims and flattens the rest.
//
// The validation order is the one a reader should be able to check against the
// spec: issuer, audience, nonce, then the time claims.
func (b *OIDCBroker) assertionFrom(payload []byte, expectNonce string) (Assertion, error) {
	var claims claimSet
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Assertion{}, refuse(RejectFederationAssertion,
			"this login could not be verified", "the id_token payload is not a claim set")
	}
	if claims.Issuer != b.cfg.Issuer {
		return Assertion{}, refuse(RejectFederationAssertion,
			"this login could not be verified",
			fmt.Sprintf("the id_token names issuer %q", claims.Issuer))
	}
	if !slices.Contains(claims.Audience, b.cfg.ClientID) {
		return Assertion{}, refuse(RejectFederationAudience,
			"this login was not issued for this server",
			"the id_token's audience does not include this client")
	}
	if len(claims.Audience) > 1 && claims.AZP != "" && claims.AZP != b.cfg.ClientID {
		// A multi-audience token must name this client as the
		// authorized party, or it is a token minted for somebody else
		// that happens to list us.
		return Assertion{}, refuse(RejectFederationAudience,
			"this login was not issued for this server",
			"the id_token's azp names a different client")
	}
	if expectNonce == "" || claims.Nonce != expectNonce {
		return Assertion{}, refuse(RejectFederationAssertion,
			"this login could not be verified", "the id_token's nonce does not match the one this flow issued")
	}

	now := b.now()
	skew := b.cfg.ClockSkew
	if claims.Expiry == 0 || now.After(time.Unix(claims.Expiry, 0).Add(skew)) {
		return Assertion{}, refuse(RejectFederationExpired,
			"this login has expired; please try again", "the id_token is past its exp")
	}
	if claims.NotBefore != 0 && now.Add(skew).Before(time.Unix(claims.NotBefore, 0)) {
		return Assertion{}, refuse(RejectFederationExpired,
			"this login is not yet valid; please try again", "the id_token is before its nbf")
	}
	if claims.IssuedAt != 0 && now.Add(skew).Before(time.Unix(claims.IssuedAt, 0)) {
		return Assertion{}, refuse(RejectFederationExpired,
			"this login is not yet valid; please try again", "the id_token is issued in the future")
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return Assertion{}, refuse(RejectFederationSubject,
			"the identity provider did not identify you", "the id_token carries no sub")
	}

	var raw map[string]json.RawMessage
	_ = json.Unmarshal(payload, &raw)
	single, multi := flattenClaims(raw)

	return Assertion{
		Connector:   b.name,
		Subject:     claims.Subject,
		Login:       single[b.loginClaim()],
		DisplayName: single["name"],
		Email:       single["email"],
		Claims:      single,
		MultiClaims: multi,
	}, nil
}

func (b *OIDCBroker) loginClaim() string {
	if b.cfg.LoginClaim != "" {
		return b.cfg.LoginClaim
	}
	return "preferred_username"
}

// mergeUserInfo folds the userinfo endpoint's claims into the assertion.
//
// A userinfo call that FAILS is not a failed login: the ID token already
// authenticated the person, and userinfo is additional attributes. What it
// must never do is authenticate somebody else, so a response whose `sub` does
// not match the ID token's is discarded entirely.
func (b *OIDCBroker) mergeUserInfo(ctx context.Context, endpoint, accessToken, subject string, into *Assertion) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIdPResponseBytes))
	if err != nil {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return
	}
	single, multi := flattenClaims(raw)
	if single["sub"] != subject {
		return
	}
	for k, v := range single {
		if _, exists := into.Claims[k]; !exists {
			into.Claims[k] = v
		}
	}
	for k, v := range multi {
		if _, exists := into.MultiClaims[k]; !exists {
			into.MultiClaims[k] = v
		}
	}
}

// maxIdPResponseBytes bounds anything read from an IdP. A discovery document,
// a key set and a token response are all small; an unbounded read from a peer
// this server does not control is a memory exhaustion away from an outage.
const maxIdPResponseBytes = 1 << 20

// metadata returns the discovery document and key set, fetching them when the
// cache is cold, stale, or force is set.
func (b *OIDCBroker) metadata(ctx context.Context, force bool) (oidcDiscovery, jwkSet, error) {
	b.mu.Lock()
	fresh := !force && b.fetchedAt.Add(b.ttl).After(b.now()) && b.discovery.Issuer != ""
	disco, keys := b.discovery, b.keys
	b.mu.Unlock()
	if fresh {
		return disco, keys, nil
	}

	wellKnown := strings.TrimSuffix(b.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	if err := b.getJSON(ctx, wellKnown, &disco); err != nil {
		return oidcDiscovery{}, jwkSet{}, err
	}
	if disco.Issuer != b.cfg.Issuer {
		// A discovery document naming a different issuer is either a
		// misconfiguration or a redirect somewhere it should not be.
		return oidcDiscovery{}, jwkSet{}, fmt.Errorf(
			"identity: connector %q published a discovery document for a different issuer", b.name)
	}
	if disco.AuthorizationEndpoint == "" || disco.TokenEndpoint == "" || disco.JWKSURI == "" {
		return oidcDiscovery{}, jwkSet{}, fmt.Errorf(
			"identity: connector %q published an incomplete discovery document", b.name)
	}
	if err := b.getJSON(ctx, disco.JWKSURI, &keys); err != nil {
		return oidcDiscovery{}, jwkSet{}, err
	}
	if len(keys.Keys) == 0 {
		return oidcDiscovery{}, jwkSet{}, fmt.Errorf(
			"identity: connector %q published an empty key set", b.name)
	}

	b.mu.Lock()
	b.discovery, b.keys, b.fetchedAt = disco, keys, b.now()
	b.mu.Unlock()
	return disco, keys, nil
}

func (b *OIDCBroker) getJSON(ctx context.Context, endpoint string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("identity: connector %q has an unusable endpoint", b.name)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("identity: connector %q could not be reached", b.name)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("identity: connector %q answered %d", b.name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIdPResponseBytes))
	if err != nil {
		return fmt.Errorf("identity: connector %q's response could not be read", b.name)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("identity: connector %q's response was not the JSON this server expects", b.name)
	}
	return nil
}

func (b *OIDCBroker) postForm(ctx context.Context, endpoint string, form url.Values, clientID, secret string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("identity: connector %q has an unusable token endpoint", b.name)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if secret != "" {
		// client_secret_basic, which every IdP supports. The secret is
		// set on the request and never logged: the access log on this
		// server writes no header values (0007), and the error paths
		// below name the connector rather than the credential.
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("identity: connector %q could not be reached", b.name)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIdPResponseBytes))
	if err != nil {
		return fmt.Errorf("identity: connector %q's response could not be read", b.name)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("identity: connector %q answered %d", b.name, resp.StatusCode)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("identity: connector %q's token response was not JSON", b.name)
	}
	return nil
}

// ParseOIDCConfig decodes a connector's stored configuration.
func ParseOIDCConfig(raw json.RawMessage) (OIDCConfig, error) {
	var cfg OIDCConfig
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return OIDCConfig{}, fmt.Errorf("identity: this OIDC connector's configuration could not be read")
	}
	return cfg, nil
}
