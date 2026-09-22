// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// The per-tenant SSH certificate authority (proxy D6a, M7, M18).
//
// WHAT IT IS FOR. Without it, every proxy in the fleet holds a long-lived
// management credential on disk — a key that opens the estate, sitting on the
// machine most exposed to it. With it, a proxy holds nothing: it generates a key
// pair per session, this server signs the public half into a certificate scoped
// to that session, and the certificate expires in minutes. A stolen proxy disk
// is then worth nothing, and a stolen certificate is worth one session on one
// target for the rest of a short window.
//
// THE CA IS PER TENANT, AND THAT IS STRUCTURAL. Key material, rotation and
// revocation are per tenant from the first issuance (M18's fourth consequence):
// one tenant's targets must not trust another tenant's CA. Every method here
// takes a tenant and there is no method that does not — the same discipline
// `internal/store` enforces by signature.
//
// WHAT SSH CAN AND CANNOT ENFORCE, SAID PLAINLY. A certificate is scoped by its
// principals, its validity window and its critical options. THERE IS NO
// HOSTNAME FIELD: OpenSSH has no notion of "this certificate is only for
// host X". So target scoping is enforced at ISSUANCE — this server mints a
// certificate only for a target the decision path authorised, only for that
// session — and recorded in `ssh_certificates.target`, which is what makes the
// scope auditable. The certificate itself carries the target in its `key_id`
// (so the target's own log records it) and, where the proxy's address is known,
// a `source-address` critical option. Pretending SSH enforces more than it does
// would be the more comfortable documentation and the less useful one.
//
// WHAT TRAVELS. The certificate and nothing else. The proxy generates the key
// pair; this server never sees, stores or transmits a private key. That is why
// the issuance API takes a public key rather than returning a key pair, and it
// is the property that makes the upstream contract change additive rather than
// dangerous.

// Defaults for the authority.
const (
	// DefaultValidity is how long an issued certificate lives. Minutes,
	// because the whole point is that a stolen one is worth almost nothing:
	// the shorter and narrower, the less it is worth.
	DefaultValidity = 5 * time.Minute
	// MaxValidity caps what a caller may ask for. A certificate good for a
	// day is a long-lived credential with extra steps.
	MaxValidity = 1 * time.Hour
	// DefaultRotationOverlap is how long a retired CA key stays in the trust
	// bundle after a routine rotation. It is longer than MaxValidity so that
	// no certificate the retired key signed can outlive the trust in it —
	// which is the half of a rotation story that is usually left unsaid.
	DefaultRotationOverlap = 2 * MaxValidity
	// CAKeyName is the role part of a CA key's name in the key store. The
	// generation is appended, because rotation creates a new name.
	CAKeyName = "ssh-ca"
	// certPermitPTY is the only extension an issued certificate carries.
	certPermitPTY = "permit-pty"
)

// Errors callers distinguish.
var (
	// ErrNoCA reports that a tenant has no certificate authority yet.
	ErrNoCA = errors.New("credential: this tenant has no certificate authority")
	// ErrCertificateExpired reports a certificate outside its window.
	ErrCertificateExpired = errors.New("credential: the certificate is outside its validity window")
	// ErrCertificateRevoked reports a certificate this server has withdrawn.
	ErrCertificateRevoked = errors.New("credential: the certificate has been revoked")
	// ErrUntrustedCA reports a certificate signed by a key that is not in
	// the tenant's trust bundle — including one signed by ANOTHER TENANT'S
	// CA, which is the case M18 exists to make impossible.
	ErrUntrustedCA = errors.New("credential: the certificate was not signed by a key this tenant trusts")
	// ErrPrincipalNotPermitted reports a certificate that does not carry the
	// login it is being used for.
	ErrPrincipalNotPermitted = errors.New("credential: the certificate does not permit that login")
)

// CA issues short-lived target certificates.
type CA struct {
	st   *store.Store
	keys ext.KeyStore
	log  *slog.Logger
	now  func() time.Time

	validity    time.Duration
	maxValidity time.Duration
	overlap     time.Duration
	algorithm   ext.KeyAlgorithm
}

// Option configures a CA.
type Option func(*CA)

// WithKeyStore sets where private key material lives.
//
// It is the `ext.KeyStore` seam (`ext.PointKeyStore`): an HSM-backed
// implementation works everywhere the software one does, because what crosses
// the seam is a `crypto.Signer` rather than a key.
func WithKeyStore(ks ext.KeyStore) Option {
	return func(c *CA) {
		if ks != nil {
			c.keys = ks
		}
	}
}

// WithValidity sets the default certificate lifetime, clamped to MaxValidity.
func WithValidity(d time.Duration) Option {
	return func(c *CA) {
		if d > 0 {
			c.validity = min(d, MaxValidity)
		}
	}
}

// WithMaxValidity lowers (never raises) the ceiling on a requested lifetime.
func WithMaxValidity(d time.Duration) Option {
	return func(c *CA) {
		if d > 0 {
			c.maxValidity = min(d, MaxValidity)
		}
	}
}

// WithRotationOverlap sets how long a retired key stays trusted.
func WithRotationOverlap(d time.Duration) Option {
	return func(c *CA) {
		if d > 0 {
			c.overlap = d
		}
	}
}

// WithAlgorithm sets the algorithm new CA keys are generated with.
func WithAlgorithm(a ext.KeyAlgorithm) Option {
	return func(c *CA) { c.algorithm = a }
}

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *CA) {
		if l != nil {
			c.log = l
		}
	}
}

// WithClock overrides the clock. Tests use it; nothing in production should.
func WithClock(now func() time.Time) Option {
	return func(c *CA) {
		if now != nil {
			c.now = now
		}
	}
}

// New builds an authority over a store and a key store.
func New(st *store.Store, ks ext.KeyStore, opts ...Option) (*CA, error) {
	if st == nil {
		return nil, fmt.Errorf("credential.New: a store is required")
	}
	if ks == nil {
		return nil, fmt.Errorf("credential.New: a key store is required")
	}
	c := &CA{
		st:          st,
		keys:        ks,
		log:         slog.Default(),
		now:         time.Now,
		validity:    DefaultValidity,
		maxValidity: MaxValidity,
		overlap:     DefaultRotationOverlap,
		algorithm:   ext.KeyAlgorithmEd25519,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Info describes a tenant's authority without exposing anything private.
type Info struct {
	// ActiveKeyID is the key new certificates are signed with.
	ActiveKeyID string
	// Algorithm is the active key's type.
	Algorithm string
	// ActivePublicKey is the active key in `authorized_keys` form, which is
	// what goes into a target's TrustedUserCAKeys file.
	ActivePublicKey string
	// TrustBundle is EVERY key a target must currently trust: the active one
	// plus every retired one still inside its overlap. Publishing only the
	// active key is how a rotation breaks every session signed a minute
	// before it.
	TrustBundle []TrustedKey
	// CreatedAt is when the active key came into being.
	CreatedAt time.Time
	// Custodian says where the private half lives.
	Custodian string
}

// TrustedKey is one entry of a trust bundle.
type TrustedKey struct {
	KeyID string `json:"key_id"`
	// PublicKey is the `authorized_keys` line.
	PublicKey string `json:"public_key"`
	// Active reports whether this key still signs.
	Active bool `json:"active"`
	// TrustedUntil is when a retired key leaves the bundle; zero on the
	// active one.
	TrustedUntil time.Time `json:"trusted_until,omitzero"`
}

// Ensure returns the tenant's authority, creating one if it has none.
//
// It is idempotent and safe to call on every boot. The partial unique index on
// `tenant_ca_keys` means two nodes racing produce one key and one conflict, and
// the loser re-reads rather than failing — a fleet that cannot start because two
// nodes started together is an outage nobody chose.
func (c *CA) Ensure(ctx context.Context, tenant store.Tenant) (Info, error) {
	info, err := c.Describe(ctx, tenant)
	if err == nil {
		return info, nil
	}
	if !errors.Is(err, ErrNoCA) {
		return Info{}, err
	}

	keyID, err := newKeyID(c.now())
	if err != nil {
		return Info{}, err
	}
	pub, err := c.generate(ctx, tenant, keyID)
	if err != nil {
		return Info{}, err
	}

	row := store.CAKey{
		KeyID:      keyID,
		Algorithm:  c.algorithm.String(),
		PublicKey:  pub.Marshal(),
		PrivateRef: keyRefName(keyID),
		Active:     true,
		Comment:    "created on first use",
	}
	if err := c.st.CAKeys().Insert(ctx, tenant, row); err != nil {
		if store.IsConflict(err) {
			// Another node won the race. Its key is as good as ours.
			return c.Describe(ctx, tenant)
		}
		return Info{}, err
	}
	c.log.InfoContext(ctx, "a tenant certificate authority was created",
		"event", "ssh_ca_created", "tenant", tenant.String(), "ca_key_id", keyID,
		"algorithm", row.Algorithm)
	return c.Describe(ctx, tenant)
}

// Describe returns the tenant's authority and trust bundle.
func (c *CA) Describe(ctx context.Context, tenant store.Tenant) (Info, error) {
	keys, err := c.st.CAKeys().List(ctx, tenant)
	if err != nil {
		return Info{}, err
	}
	now := c.now()

	var info Info
	for _, k := range keys {
		pub, perr := ssh.ParsePublicKey(k.PublicKey)
		if perr != nil {
			// A row whose public key does not parse is an outage, not
			// a key to skip: a trust bundle silently missing an entry
			// is a fleet-wide authentication failure that looks like a
			// policy problem.
			return Info{}, fmt.Errorf("credential: tenant %s's CA key %s does not parse", tenant, k.KeyID)
		}
		line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))

		switch {
		case k.Active:
			info.ActiveKeyID = k.KeyID
			info.Algorithm = k.Algorithm
			info.ActivePublicKey = line
			info.CreatedAt = k.CreatedAt
			info.Custodian = custodianOf(k.PrivateRef)
			info.TrustBundle = append(info.TrustBundle, TrustedKey{
				KeyID: k.KeyID, PublicKey: line, Active: true,
			})
		case k.TrustedUntil.IsZero() || now.Before(k.TrustedUntil):
			info.TrustBundle = append(info.TrustBundle, TrustedKey{
				KeyID: k.KeyID, PublicKey: line, TrustedUntil: k.TrustedUntil,
			})
		}
	}
	if info.ActiveKeyID == "" {
		return Info{}, ErrNoCA
	}
	return info, nil
}

// RotateRequest asks for a new CA key.
type RotateRequest struct {
	// Comment is the operator's note about why.
	Comment string
	// Compromise changes what happens to OUTSTANDING CERTIFICATES, which is
	// the half of a rotation story that is usually missing:
	//
	//   - false (routine): the retired key stays in the trust bundle for the
	//     rotation overlap, so certificates it already signed keep working
	//     for their remaining life. Nothing is revoked and no session drops.
	//   - true (compromise): the retired key leaves the bundle at the
	//     rotation instant and EVERY outstanding certificate it signed is
	//     revoked. Sessions using one stop working, which is the point.
	//
	// "We rotate" without an answer here is not a rotation story.
	Compromise bool
}

// RotateResult reports what a rotation did.
type RotateResult struct {
	// PreviousKeyID and NewKeyID name the two keys.
	PreviousKeyID string
	// NewKeyID is the key that signs from now on.
	NewKeyID string
	// TrustedUntil is when the retired key leaves the trust bundle. On a
	// compromise rotation it is the rotation instant.
	TrustedUntil time.Time
	// RevokedCertificates is how many outstanding certificates were
	// revoked: zero on a routine rotation, and on a compromise rotation the
	// number of sessions an operator has just ended.
	RevokedCertificates int64
	// Info is the authority as it now stands, including the trust bundle an
	// operator must publish.
	Info Info
}

// Rotate retires the active key and activates a new one.
//
// THE OUTSTANDING-CERTIFICATE QUESTION IS ANSWERED, NOT DEFERRED. See
// [RotateRequest.Compromise].
func (c *CA) Rotate(ctx context.Context, tenant store.Tenant, req RotateRequest) (RotateResult, error) {
	previous, err := c.st.CAKeys().Active(ctx, tenant)
	if store.IsNotFound(err) {
		return RotateResult{}, ErrNoCA
	}
	if err != nil {
		return RotateResult{}, err
	}

	keyID, err := newKeyID(c.now())
	if err != nil {
		return RotateResult{}, err
	}
	pub, err := c.generate(ctx, tenant, keyID)
	if err != nil {
		return RotateResult{}, err
	}

	now := c.now()
	trustedUntil := now.Add(c.overlap)
	if req.Compromise {
		trustedUntil = now
	}

	next := store.CAKey{
		KeyID:      keyID,
		Algorithm:  c.algorithm.String(),
		PublicKey:  pub.Marshal(),
		PrivateRef: keyRefName(keyID),
		Active:     true,
		Comment:    req.Comment,
	}
	if err := c.st.CAKeys().Rotate(ctx, tenant, next, now, trustedUntil); err != nil {
		return RotateResult{}, err
	}

	result := RotateResult{
		PreviousKeyID: previous.KeyID,
		NewKeyID:      keyID,
		TrustedUntil:  trustedUntil,
	}
	if req.Compromise {
		revoked, rerr := c.st.SSHCertificates().RevokeByCAKey(ctx, tenant, previous.KeyID, now,
			"the signing key was rotated after a suspected compromise")
		if rerr != nil {
			return RotateResult{}, rerr
		}
		result.RevokedCertificates = revoked
	}

	c.log.WarnContext(ctx, "a tenant certificate authority was rotated",
		"event", "ssh_ca_rotated",
		"tenant", tenant.String(),
		"previous_ca_key_id", previous.KeyID,
		"ca_key_id", keyID,
		"compromise", req.Compromise,
		"trusted_until", trustedUntil,
		"revoked_certificates", result.RevokedCertificates,
	)

	info, err := c.Describe(ctx, tenant)
	if err != nil {
		return RotateResult{}, err
	}
	result.Info = info
	return result, nil
}

// IssueRequest is one certificate's whole scope.
//
// Every field narrows what the certificate is worth if it is stolen, which is
// why there are no optional ones except the two the caller may genuinely not
// know (SourceAddress and SessionID).
type IssueRequest struct {
	// SubjectID is who the certificate is for. It goes into the key id, so
	// the TARGET'S OWN audit trail names the person rather than a shared
	// service account.
	SubjectID string
	// Principals are the logins the certificate is valid for — normally
	// exactly one, the account on the target. An empty list is refused: a
	// certificate with no principals is valid for every account on every
	// host that trusts the CA.
	Principals []string
	// Target and TargetPort are what it was minted to reach. See the note at
	// the top of this file about what SSH can enforce.
	Target     string
	TargetPort int
	// SessionID ties the certificate to one session.
	SessionID string
	// SourceAddress, when set, becomes a `source-address` critical option:
	// the certificate then works only from the proxy that asked for it.
	SourceAddress string
	// PublicKey is the key the PROXY generated, in SSH wire form. This
	// server never sees a private key.
	PublicKey []byte
	// ValidFor is how long it lives. Zero takes the CA's default; anything
	// above the ceiling is clamped down, never up.
	ValidFor time.Duration
}

// Issued is a signed certificate.
type Issued struct {
	// Serial is the per-tenant serial, which is how a revocation names it.
	Serial int64
	// KeyID is the certificate's `key_id`.
	KeyID string
	// CAKeyID names the key that signed it.
	CAKeyID string
	// Certificate is the `authorized_keys`-form certificate: what the proxy
	// presents to the target.
	Certificate string
	// Principals, Target, ValidAfter and ValidBefore restate the scope, so a
	// caller logging the issuance does not have to parse the certificate to
	// know what it granted.
	Principals  []string
	Target      string
	ValidAfter  time.Time
	ValidBefore time.Time
}

// Issue signs one short-lived certificate.
func (c *CA) Issue(ctx context.Context, tenant store.Tenant, req IssueRequest) (Issued, error) {
	if req.SubjectID == "" {
		return Issued{}, fmt.Errorf("credential: a certificate must name the subject it is for")
	}
	if len(req.Principals) == 0 {
		return Issued{}, fmt.Errorf("credential: a certificate must name at least one principal; one with none is valid for every account")
	}
	if req.Target == "" {
		return Issued{}, fmt.Errorf("credential: a certificate must name the target it was minted for")
	}
	if len(req.PublicKey) == 0 {
		return Issued{}, fmt.Errorf("credential: a certificate needs the public key the proxy generated")
	}
	pub, err := ssh.ParsePublicKey(req.PublicKey)
	if err != nil {
		return Issued{}, fmt.Errorf("credential: the public key offered for signing does not parse")
	}
	if _, isCert := pub.(*ssh.Certificate); isCert {
		// Signing a certificate as if it were a key would produce
		// something no target reads, and accepting one here would mean
		// the caller had confused the two.
		return Issued{}, fmt.Errorf("credential: a certificate cannot be presented for signing; offer the public key")
	}

	active, err := c.st.CAKeys().Active(ctx, tenant)
	if store.IsNotFound(err) {
		return Issued{}, ErrNoCA
	}
	if err != nil {
		return Issued{}, err
	}

	signer, err := c.signer(ctx, tenant, active)
	if err != nil {
		return Issued{}, err
	}

	validity := req.ValidFor
	if validity <= 0 {
		validity = c.validity
	}
	validity = min(validity, c.maxValidity)

	now := c.now()
	// A minute of backdating absorbs clock skew between this server and the
	// target. It is deliberately small: it is also a minute of extra life.
	validAfter := now.Add(-time.Minute)
	validBefore := now.Add(validity)

	serial, err := c.st.SSHCertificates().NextSerial(ctx, tenant)
	if err != nil {
		return Issued{}, err
	}

	keyID := certificateKeyID(tenant, req, serial)
	cert := &ssh.Certificate{
		Key:             pub,
		Serial:          uint64(serial),
		CertType:        ssh.UserCert,
		KeyId:           keyID,
		ValidPrincipals: req.Principals,
		ValidAfter:      uint64(validAfter.Unix()),
		ValidBefore:     uint64(validBefore.Unix()),
		Permissions: ssh.Permissions{
			// ONE EXTENSION. No agent forwarding, no port
			// forwarding, no X11, no user rc: every one of those is a
			// way for a session to become something other than a
			// session, and a credential minted for one purpose should
			// not carry four others.
			Extensions:      map[string]string{certPermitPTY: ""},
			CriticalOptions: criticalOptions(req),
		},
	}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		return Issued{}, fmt.Errorf("credential: the certificate could not be signed")
	}

	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(cert)))
	row := store.SSHCertificate{
		Serial:        serial,
		KeyID:         keyID,
		CAKeyID:       active.KeyID,
		SubjectID:     req.SubjectID,
		SessionID:     req.SessionID,
		Principals:    req.Principals,
		Target:        req.Target,
		TargetPort:    req.TargetPort,
		SourceAddress: req.SourceAddress,
		ValidAfter:    validAfter,
		ValidBefore:   validBefore,
		IssuedAt:      now,
		Certificate:   line,
	}
	if err := c.st.SSHCertificates().Insert(ctx, tenant, row); err != nil {
		// The record is written BEFORE the certificate is handed over,
		// so a certificate this server cannot account for is never
		// issued. An unrecorded short-lived credential is one nobody can
		// revoke and nobody can explain.
		return Issued{}, err
	}

	return Issued{
		Serial:      serial,
		KeyID:       keyID,
		CAKeyID:     active.KeyID,
		Certificate: line,
		Principals:  req.Principals,
		Target:      req.Target,
		ValidAfter:  validAfter,
		ValidBefore: validBefore,
	}, nil
}

// criticalOptions builds the certificate's critical options.
//
// `source-address` is the only one set, and only when the caller knows the
// address. `force-command` is deliberately absent: what a session may run is
// the filter policy's job (0005, §5.2), enforced by the proxy, and encoding it
// in a certificate would put one of the two answers somewhere policy cannot
// change it.
func criticalOptions(req IssueRequest) map[string]string {
	if req.SourceAddress == "" {
		return nil
	}
	return map[string]string{"source-address": req.SourceAddress}
}

// certificateKeyID is what the TARGET's own log records.
//
// It names the tenant, the subject, the target and the session, because the
// question somebody asks of a target's auth log is "who was this, and which
// session" — and the answer has to be in the log the target wrote, not only in
// this server's.
func certificateKeyID(tenant store.Tenant, req IssueRequest, serial int64) string {
	parts := []string{
		"hoplock",
		string(tenant),
		req.SubjectID,
		req.Target,
	}
	if req.SessionID != "" {
		parts = append(parts, req.SessionID)
	}
	parts = append(parts, fmt.Sprintf("serial=%d", serial))
	return strings.Join(parts, "/")
}

// VerifyRequest is what a certificate is being checked against.
type VerifyRequest struct {
	// Principal is the login it is being used for.
	Principal string
	// At is the instant to check the window against. Zero takes the CA's
	// clock.
	At time.Time
}

// Verify checks a certificate against the tenant's trust bundle.
//
// It exists because the CA's promises are only worth what something checks: the
// tests exercise an expired certificate, a revoked one, and one signed by
// another tenant's CA through this one function, so all three are the same kind
// of answer rather than three separate beliefs.
func (c *CA) Verify(ctx context.Context, tenant store.Tenant, certificate string, req VerifyRequest) error {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(certificate))
	if err != nil {
		return fmt.Errorf("credential: that is not an authorized_keys certificate")
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return fmt.Errorf("credential: that is a public key, not a certificate")
	}

	at := req.At
	if at.IsZero() {
		at = c.now()
	}

	info, err := c.Describe(ctx, tenant)
	if err != nil {
		return err
	}
	trusted := false
	signerLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(cert.SignatureKey)))
	for _, k := range info.TrustBundle {
		if k.PublicKey == signerLine {
			trusted = true
			break
		}
	}
	if !trusted {
		// This is the branch that makes "one tenant's targets do not
		// trust another tenant's CA" true rather than intended: the
		// bundle is read per tenant, so a certificate from tenant A
		// reaches here in tenant B and finds nothing.
		return ErrUntrustedCA
	}

	// The checker is given THIS CA'S CLOCK and THIS TENANT'S trust bundle.
	// Its own defaults are the real clock and no authority at all, and a test
	// that could only assert expiry by sleeping would be a test nobody runs.
	checker := &ssh.CertChecker{
		Clock: func() time.Time { return at },
		IsUserAuthority: func(key ssh.PublicKey) bool {
			return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) == signerLine && trusted
		},
	}
	if err := checker.CheckCert(req.Principal, cert); err != nil {
		// CertChecker folds several refusals into one error. The two
		// that callers act on differently are separated out, because
		// "expired" is a retry and "wrong principal" is not.
		unix := uint64(at.Unix())
		if unix < cert.ValidAfter || unix >= cert.ValidBefore {
			return ErrCertificateExpired
		}
		if !principalPermitted(cert, req.Principal) {
			return ErrPrincipalNotPermitted
		}
		return fmt.Errorf("credential: the certificate was refused: %w", err)
	}
	// CertChecker uses its own clock, so the window is checked again against
	// the instant the caller asked about. A test that could only assert
	// expiry by sleeping would be a test nobody runs.
	unix := uint64(at.Unix())
	if unix < cert.ValidAfter || unix >= cert.ValidBefore {
		return ErrCertificateExpired
	}

	row, err := c.st.SSHCertificates().Get(ctx, tenant, int64(cert.Serial))
	if store.IsNotFound(err) {
		// A certificate this server cannot account for is refused. It
		// was either issued by a CA key this tenant trusts but a
		// different deployment holds, or it is forged; neither is a
		// certificate to accept.
		return ErrUntrustedCA
	}
	if err != nil {
		return err
	}
	if !row.RevokedAt.IsZero() && !at.Before(row.RevokedAt) {
		return ErrCertificateRevoked
	}
	return nil
}

func principalPermitted(cert *ssh.Certificate, principal string) bool {
	if len(cert.ValidPrincipals) == 0 {
		return true
	}
	for _, p := range cert.ValidPrincipals {
		if p == principal {
			return true
		}
	}
	return false
}

// Revoke withdraws one certificate.
func (c *CA) Revoke(ctx context.Context, tenant store.Tenant, serial int64, reason string) error {
	return c.st.SSHCertificates().Revoke(ctx, tenant, serial, c.now(), reason)
}

// Outstanding returns the certificates that are still usable.
func (c *CA) Outstanding(ctx context.Context, tenant store.Tenant) ([]store.SSHCertificate, error) {
	return c.st.SSHCertificates().Outstanding(ctx, tenant, c.now())
}

// generate makes a new CA key in the key store and returns its public half.
func (c *CA) generate(ctx context.Context, tenant store.Tenant, keyID string) (ssh.PublicKey, error) {
	info, err := c.keys.Generate(ctx, ext.KeyRef{
		Tenant: ext.Tenant(tenant),
		Name:   keyRefName(keyID),
	}, c.algorithm)
	if err != nil {
		return nil, err
	}
	pub, err := ssh.NewPublicKey(info.Public)
	if err != nil {
		return nil, fmt.Errorf("credential: the generated CA key cannot be used for SSH")
	}
	return pub, nil
}

// signer resolves the active key's signer through the key store seam.
func (c *CA) signer(ctx context.Context, tenant store.Tenant, key store.CAKey) (ssh.Signer, error) {
	cryptoSigner, err := c.keys.Signer(ctx, ext.KeyRef{
		Tenant: ext.Tenant(tenant),
		Name:   key.PrivateRef,
	})
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromSigner(cryptoSigner)
	if err != nil {
		return nil, fmt.Errorf("credential: tenant %s's CA key cannot sign SSH certificates", tenant)
	}
	return signer, nil
}

// keyRefName is the name a CA key generation has in the key store. The role and
// the generation are both in it, because rotation creates a new name.
func keyRefName(keyID string) string { return CAKeyName + ":" + keyID }

// custodianOf renders the custodian an operator sees from a stored ref.
func custodianOf(privateRef string) string {
	if idx := strings.Index(privateRef, ":"); idx > 0 && !strings.HasPrefix(privateRef, CAKeyName+":") {
		return privateRef[:idx]
	}
	return "software"
}

// newKeyID names a generation: the day it was made, plus enough entropy that
// two rotations on one day do not collide.
func newKeyID(now time.Time) (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("credential: a CA key id could not be generated")
	}
	return now.UTC().Format("20060102") + "-" + base64.RawURLEncoding.EncodeToString(buf), nil
}

// ParseAuthorizedKey reads an `authorized_keys` line and returns the SSH wire
// encoding [CA.Issue] takes.
//
// It exists so a caller — the CLI, a test, and one day the decision path — hands
// this package the same shape whichever form it started from, rather than each
// of them importing `x/crypto/ssh` to do one conversion.
func ParseAuthorizedKey(blob []byte) ([]byte, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey(blob)
	if err != nil {
		return nil, fmt.Errorf("credential: that is not an authorized_keys public key")
	}
	return pub.Marshal(), nil
}
