// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"time"

	"github.com/hoplock/control/internal/store"
)

// Defaults for the MFA conversation this server owns (PLAN §6).
//
// Each is a number rather than a feeling, and each is an upper bound the
// provider cannot widen:
//
//   - the TTL is how long a user has to reach for their phone while their SSH
//     handshake is held open. Too short refuses people who are being careful;
//     too long is a connection pinned open per guess.
//   - the poll interval is how often the proxy asks. It is advertised on the
//     challenge, and the proxy honours it — so it, and not the provider's
//     patience, is what sets the load of an outstanding challenge.
//   - the poll budget bounds a challenge's total cost independently of the
//     rate, which is what makes a caller that ignores the interval finite.
const (
	DefaultChallengeTTL = 2 * time.Minute
	DefaultPollAfter    = 2 * time.Second
	DefaultMaxPolls     = 120
	// MaxChallengeTTL caps what a provider may ask for.
	MaxChallengeTTL = 10 * time.Minute
	// challengeTokenBytes is how much entropy a challenge token carries. A
	// guessable token is a second factor anybody can answer.
	challengeTokenBytes = 32
)

// Service owns the authentication conversation end to end.
//
// It is the interface 0011 implements behind: everything here resolves an
// identity out of `internal/store`, and an IdP broker slots in by replacing
// [Directory] and [MFAProvider] without the HTTP layer noticing. What must NOT
// move behind that seam is anything in this file that is about the
// conversation rather than about the factor — lifetime, poll rate, single use,
// expiry-as-deny — because those are the same whoever supplies the factor.
type Service struct {
	dir       Directory
	providers map[string]MFAProvider
	now       func() time.Time

	challengeTTL time.Duration
	pollAfter    time.Duration
	maxPolls     int
}

// Directory is where identities and their credentials come from.
//
// It is an interface rather than a *store.Store so that 0011's IdP broker is a
// substitution rather than a rewrite, and so that a test can inject a failure
// on the auth path — which is how M11's regression test proves a database
// failure answers 5xx and not 401.
type Directory interface {
	// SubjectByPrincipal resolves a login. Absent is store.ErrNotFound.
	SubjectByPrincipal(ctx context.Context, tenant store.Tenant, login string) (store.Subject, error)
	// SubjectByID resolves a subject id. Absent is store.ErrNotFound.
	SubjectByID(ctx context.Context, tenant store.Tenant, subjectID string) (store.Subject, error)
	// KeyByFingerprint resolves an offered key. Absent is
	// store.ErrNotFound.
	KeyByFingerprint(ctx context.Context, tenant store.Tenant, fingerprint string) (store.SubjectKey, error)
	// PasswordFor returns a subject's verifier. Absent is
	// store.ErrNotFound.
	PasswordFor(ctx context.Context, tenant store.Tenant, subjectID string) (store.PasswordDigest, error)
	// MFAFor returns a subject's second-factor enrollment. Absent is
	// store.ErrNotFound and means the subject has none.
	MFAFor(ctx context.Context, tenant store.Tenant, subjectID string) (store.MFAEnrollment, error)
	// Challenges is the outstanding-challenge store.
	Challenges() store.MFARepository
}

// FleetKeys answers "is this key one of the fleet's own proxies".
//
// It is an interface here and the fleet registry over there, so that this
// package does not depend on the graph in order to authenticate — and so that
// the answer has exactly one source. A second list of proxy key fingerprints
// maintained beside the enrolled rows would drift the first time somebody
// re-enrolled a proxy with a new key.
type FleetKeys interface {
	// ProxyByKeyFingerprint returns the enrolled, non-revoked proxy that
	// owns a key fingerprint, and false when the key belongs to no proxy.
	// An error is an outage; "not one of ours" is `false, nil`.
	ProxyByKeyFingerprint(ctx context.Context, tenant store.Tenant, fingerprint string) (string, bool, error)
}

// Option configures a Service.
type Option func(*Service)

// WithMFAProvider registers a provider under its own name. The last
// registration for a name wins, so a deployment can replace the scripted
// provider without removing it from the code.
func WithMFAProvider(p MFAProvider) Option {
	return func(s *Service) {
		if p != nil {
			s.providers[p.Name()] = p
		}
	}
}

// WithChallengeTTL sets how long a challenge lives. Values above
// MaxChallengeTTL are clamped.
func WithChallengeTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.challengeTTL = min(d, MaxChallengeTTL)
		}
	}
}

// WithPollAfter sets the interval advertised on a challenge.
func WithPollAfter(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.pollAfter = d
		}
	}
}

// WithMaxPolls sets the per-challenge poll budget.
func WithMaxPolls(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.maxPolls = n
		}
	}
}

// WithClock overrides the clock. Tests use it; nothing in production should.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// NewService builds a Service over a directory.
func NewService(dir Directory, opts ...Option) *Service {
	s := &Service{
		dir:          dir,
		providers:    map[string]MFAProvider{},
		now:          time.Now,
		challengeTTL: DefaultChallengeTTL,
		pollAfter:    DefaultPollAfter,
		maxPolls:     DefaultMaxPolls,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ---------------------------------------------------------------------------
// certificate / public key
// ---------------------------------------------------------------------------

// KeyAttempt is one offered key and the login it was offered for.
type KeyAttempt struct {
	// Login is the SSH login, target segment already stripped (proxy D1).
	Login string
	// Fingerprint is OpenSSH's SHA256 form of the offered key.
	Fingerprint string
	// IsCertificate says whether the material was a certificate. It is
	// recorded rather than trusted: the validity window this server checks
	// is the one IT stored against the fingerprint, never one parsed out of
	// material the caller supplied.
	IsCertificate bool
}

// AuthenticateKey resolves an offered key to an identity, or denies.
//
// The key may be a USER's or one of the FLEET's own proxies' — the second is a
// chain leg (proxy D11). Telling them apart is this server's job and nobody
// else's: the proxy relays a key and a login and asserts nothing, which is
// what lets a compromised proxy offer only its own key and leaves this server
// deciding what that key may reach.
//
// `fleet` may be nil, in which case no key is a chain leg. That is the
// fail-safe reading and not a convenience: a server that cannot ask "is this
// one of ours" must answer "no", because answering "yes" would authenticate a
// hop it cannot place.
func (s *Service) AuthenticateKey(ctx context.Context, tenant store.Tenant, fleet FleetKeys, a KeyAttempt) (Outcome, error) {
	key, err := s.dir.KeyByFingerprint(ctx, tenant, a.Fingerprint)
	switch {
	case err == nil:
		return s.authenticateSubjectKey(ctx, tenant, key, a)
	case store.IsNotFound(err):
		// Not a user's key. It may still be a chain leg.
	default:
		// EVERY other failure is an outage. This is the M11 branch that
		// matters most: "the database did not answer" must never become
		// "no identity matches the offered key".
		return Outcome{}, fmt.Errorf("identity: resolve key: %w", err)
	}

	if fleet == nil {
		return Denied(DenyUnknownKey), nil
	}
	proxyID, isFleet, err := fleet.ProxyByKeyFingerprint(ctx, tenant, a.Fingerprint)
	if err != nil {
		return Outcome{}, fmt.Errorf("identity: resolve fleet key: %w", err)
	}
	if !isFleet {
		return Denied(DenyUnknownKey), nil
	}
	return s.authenticateChainLeg(ctx, tenant, proxyID, a.Login)
}

// authenticateSubjectKey is the ordinary case: the key belongs to a user.
func (s *Service) authenticateSubjectKey(ctx context.Context, tenant store.Tenant, key store.SubjectKey, a KeyAttempt) (Outcome, error) {
	now := s.now()
	if !key.RevokedAt.IsZero() && !now.Before(key.RevokedAt) {
		return Denied(DenyKeyRevoked), nil
	}
	// The validity window is checked on EVERY call, because certificate
	// validation is where revocation bites and authentication is never
	// cached (proxy §6.4). A window this server stored is also the only one
	// it will honour: parsing one out of the offered material would be
	// trusting the credential to describe its own limits.
	if !key.ValidFrom.IsZero() && now.Before(key.ValidFrom) {
		return Denied(DenyKeyNotYetValid), nil
	}
	if !key.ValidTo.IsZero() && !now.Before(key.ValidTo) {
		return Denied(DenyKeyExpired), nil
	}

	subject, err := s.dir.SubjectByID(ctx, tenant, key.SubjectID)
	if err != nil {
		if store.IsNotFound(err) {
			// A key whose subject has been deleted authenticates nobody.
			return Denied(DenyUnknownLogin), nil
		}
		return Outcome{}, fmt.Errorf("identity: resolve subject for key: %w", err)
	}
	if !slices.Contains(subject.Principals, a.Login) {
		// The key is real and the login is not one its owner may present.
		// Refusing here is what stops a key from being a skeleton key for
		// every account on the estate.
		return Denied(DenyLoginMismatch), nil
	}
	return Authenticated(fromSubject(subject, a.Login)), nil
}

// authenticateChainLeg answers a hop that offered its own key.
//
// The answer is the USER's identity, established here, exactly as it would be
// for that user's own client — plus the claim naming which proxy's key
// authenticated the leg. Two rules make that safe and neither is optional:
// nothing is taken from the calling proxy (it asserted nothing, and the
// contract gives it no field to assert one in), and recognising the key says
// only "this caller is a legitimate hop" — the login still has to resolve, and
// /v1/authorize still decides for that hop separately.
func (s *Service) authenticateChainLeg(ctx context.Context, tenant store.Tenant, proxyID, login string) (Outcome, error) {
	subject, err := s.dir.SubjectByPrincipal(ctx, tenant, login)
	if err != nil {
		if store.IsNotFound(err) {
			// A recognised proxy key is NOT a wildcard.
			return Denied(DenyUnknownLogin), nil
		}
		return Outcome{}, fmt.Errorf("identity: resolve chain leg login: %w", err)
	}
	return Authenticated(fromSubject(subject, login).WithClaim(ClaimChainHop, proxyID)), nil
}

// ---------------------------------------------------------------------------
// password + MFA
// ---------------------------------------------------------------------------

// PasswordAttempt is one password offered for a login.
//
// The password is a field on a value that is never stored, never logged, and
// never returned in an error. It exists for the length of one call.
type PasswordAttempt struct {
	Login    string
	Password string
}

// AuthenticatePassword verifies a password and, where a second factor is
// enrolled, starts the MFA conversation.
//
// A WRONG PASSWORD IS REFUSED OUTRIGHT AND A CORRECT ONE IS ANSWERED WITH A
// CHALLENGE, so the presence of the challenge confirms the first factor. That
// oracle is known, evaluated and ACCEPTED for this product, and it is not this
// package's to close:
//
//   - the contract REQUIRES it. `200` on /v1/auth/password is documented as
//     "the password was accepted", so answering a wrong password with a decoy
//     challenge would return a 200 that the contract says means something else
//     (M1: the contract wins for wire shapes).
//   - a decoy is an amplifier handed to the attacker: upstream measured one
//     failed guess going from 1 Control call to ~121, and from a stateless
//     rejection to a connection held open for the challenge's lifetime.
//
// The control that blunts enumeration here is rate limiting, which is not this
// phase's. A change of mind starts UPSTREAM, at that `200` description in
// `contract/control.yaml` — until that sentence is relaxed, a decoy is a
// contract violation.
func (s *Service) AuthenticatePassword(ctx context.Context, tenant store.Tenant, a PasswordAttempt) (Outcome, error) {
	subject, err := s.dir.SubjectByPrincipal(ctx, tenant, a.Login)
	if err != nil {
		if store.IsNotFound(err) {
			return Denied(DenyUnknownLogin), nil
		}
		return Outcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}

	digest, err := s.dir.PasswordFor(ctx, tenant, subject.ID)
	if err != nil {
		if store.IsNotFound(err) {
			return Denied(DenyNoPassword), nil
		}
		return Outcome{}, fmt.Errorf("identity: resolve password: %w", err)
	}

	ok, err := verifyPassword(digest, a.Password)
	if err != nil {
		// "I cannot check this" is an outage. The error above is built
		// without the plaintext, which is why it is safe to return.
		return Outcome{}, err
	}
	if !ok {
		return Denied(DenyBadPassword), nil
	}

	id := fromSubject(subject, a.Login)

	enrollment, err := s.dir.MFAFor(ctx, tenant, subject.ID)
	if err != nil {
		if store.IsNotFound(err) {
			// No second factor enrolled. The password alone completes
			// the flow, which is a deployment's choice rather than a
			// gap in this code.
			return Authenticated(id), nil
		}
		return Outcome{}, fmt.Errorf("identity: resolve mfa enrollment: %w", err)
	}
	return s.beginChallenge(ctx, tenant, id, enrollment)
}

// beginChallenge starts and stores an outstanding second factor.
func (s *Service) beginChallenge(ctx context.Context, tenant store.Tenant, id Identity, e store.MFAEnrollment) (Outcome, error) {
	provider, ok := s.providers[e.Provider]
	if !ok {
		// A subject enrolled with a provider this build does not have is
		// an outage: this server cannot complete the conversation, and
		// answering "denied" would report a deployment error as the
		// user's fault.
		return Outcome{}, fmt.Errorf("identity: subject %q is enrolled with MFA provider %q, "+
			"which this build does not implement", e.SubjectID, e.Provider)
	}

	terms, err := provider.Begin(ctx, id, e.Config)
	if err != nil {
		return Outcome{}, fmt.Errorf("identity: begin %s challenge: %w", e.Provider, err)
	}

	token, err := newChallengeToken()
	if err != nil {
		return Outcome{}, err
	}

	// THE TERMS ARE THIS SERVER'S, not the provider's. A provider proposes a
	// lifetime and an interval; the orchestrator clamps both, because the
	// cost of a long-lived challenge is a held-open SSH handshake and the
	// provider is not the party paying it.
	now := s.now()
	ttl := s.challengeTTL
	if terms.TTL > 0 {
		ttl = min(terms.TTL, MaxChallengeTTL)
	}
	pollAfter := s.pollAfter
	if terms.PollAfter > 0 {
		pollAfter = terms.PollAfter
	}

	challenge := store.MFAChallenge{
		Token:       token,
		SubjectID:   id.Subject,
		Login:       id.Login,
		Provider:    provider.Name(),
		ProviderRef: terms.Ref,
		Prompt:      terms.Prompt,
		PollAfterMS: int32(pollAfter.Milliseconds()),
		State:       store.MFAChallengePending,
		IssuedAt:    now,
		ExpiresAt:   now.Add(ttl),
	}
	if err := s.dir.Challenges().CreateChallenge(ctx, tenant, challenge); err != nil {
		return Outcome{}, fmt.Errorf("identity: store challenge: %w", err)
	}
	return Pending(wireChallenge(challenge)), nil
}

// PollMFA resolves an outstanding challenge, or says it is still pending.
//
// Everything that is not the provider's answer is decided here: an unknown
// token, a spent one, an expired one and an over-polled one are all DENIES,
// and each is a deny because a caller that cannot complete this conversation
// must not be left polling a 200 forever.
func (s *Service) PollMFA(ctx context.Context, tenant store.Tenant, token string) (Outcome, error) {
	if token == "" {
		return Denied(DenyUnknownChallenge), nil
	}

	now := s.now()
	challenges := s.dir.Challenges()

	// The poll is stamped under a row lock, so two polls arriving together
	// cannot both observe a pending challenge and both resolve it.
	challenge, err := challenges.PollChallenge(ctx, tenant, token, now)
	if err != nil {
		if store.IsNotFound(err) {
			return Denied(DenyUnknownChallenge), nil
		}
		return Outcome{}, fmt.Errorf("identity: poll challenge: %w", err)
	}

	// SINGLE USE. A challenge that has already answered is never replayable,
	// whichever way it answered — an approved one most of all, because
	// replaying it would turn one approval into an unlimited supply.
	if challenge.State != store.MFAChallengePending {
		return Denied(DenyChallengeSpent), nil
	}

	// EXPIRY IS A DENY. The row is resolved rather than deleted, so a later
	// poll is told "spent" rather than "never existed" — two different facts
	// that deserve two different audit records even though they share a
	// status code.
	if !now.Before(challenge.ExpiresAt) {
		if err := s.resolve(ctx, tenant, token, store.MFAChallengeExpired, now); err != nil {
			return Outcome{}, err
		}
		return Denied(DenyChallengeExpired), nil
	}

	// THE POLL BUDGET bounds a challenge's total cost independently of how
	// fast it is polled. Past it the challenge is abandoned rather than left
	// open: a caller ignoring `poll_after_ms` is not one this server keeps
	// a connection's worth of state for.
	if challenge.Polls > s.maxPolls {
		if err := s.resolve(ctx, tenant, token, store.MFAChallengeDenied, now); err != nil {
			return Outcome{}, err
		}
		return Denied(DenyChallengeAbandoned), nil
	}

	// POLL-RATE ENFORCEMENT. A poll arriving sooner than half the interval
	// this server advertised is answered from the stored row without asking
	// the provider: it cannot have made progress, and the provider is the
	// expensive party. Half rather than the whole interval because the proxy
	// sleeps the advertised time and then adds a network hop, and a server
	// that refused the resulting jitter would be refusing a correct client.
	if s.polledTooSoon(challenge, now) {
		return Pending(wireChallenge(challenge)), nil
	}

	provider, ok := s.providers[challenge.Provider]
	if !ok {
		return Outcome{}, fmt.Errorf("identity: challenge was issued by MFA provider %q, "+
			"which this build does not implement", challenge.Provider)
	}

	enrollment, err := s.dir.MFAFor(ctx, tenant, challenge.SubjectID)
	if err != nil && !store.IsNotFound(err) {
		return Outcome{}, fmt.Errorf("identity: resolve mfa enrollment: %w", err)
	}

	result, err := provider.Poll(ctx, enrollment.Config, challenge.ProviderRef, challenge.Polls)
	if err != nil {
		// A provider that could not be asked is an OUTAGE. It is never a
		// refusal, and the difference is the whole of M11 one layer down.
		return Outcome{}, fmt.Errorf("identity: poll %s: %w", challenge.Provider, err)
	}

	switch result {
	case MFAPending:
		return Pending(wireChallenge(challenge)), nil
	case MFAApproved:
		if err := s.resolve(ctx, tenant, token, store.MFAChallengeApproved, now); err != nil {
			return Outcome{}, err
		}
		subject, err := s.dir.SubjectByID(ctx, tenant, challenge.SubjectID)
		if err != nil {
			if store.IsNotFound(err) {
				// The identity stopped existing while the user was
				// answering. That is a deny and not an outage: the
				// answer to "may this person in" is now no.
				return Denied(DenyUnknownLogin), nil
			}
			return Outcome{}, fmt.Errorf("identity: resolve subject after approval: %w", err)
		}
		return Authenticated(fromSubject(subject, challenge.Login)), nil
	case MFARefused:
		if err := s.resolve(ctx, tenant, token, store.MFAChallengeDenied, now); err != nil {
			return Outcome{}, err
		}
		return Denied(DenyMFARefused), nil
	default:
		return Outcome{}, fmt.Errorf("identity: MFA provider %q answered %q, which is not a result",
			challenge.Provider, result)
	}
}

// polledTooSoon reports whether this poll arrived inside the rate limit.
func (s *Service) polledTooSoon(c store.MFAChallenge, now time.Time) bool {
	// Polls counts this poll, so the first poll of a challenge is Polls == 1
	// and has nothing to be too soon after.
	if c.Polls <= 1 || c.LastPolledAt.IsZero() {
		return false
	}
	interval := time.Duration(c.PollAfterMS) * time.Millisecond / 2
	if interval <= 0 {
		return false
	}
	return now.Sub(c.LastPolledAt) < interval
}

// resolve spends a challenge and turns the "already spent" race into a deny
// rather than an outage.
//
// Two polls can reach this together — the row lock orders the reads, not the
// decisions that follow them — and the loser finding the challenge resolved is
// the mechanism working rather than failing.
func (s *Service) resolve(ctx context.Context, tenant store.Tenant, token string, state store.MFAChallengeState, at time.Time) error {
	err := s.dir.Challenges().ResolveChallenge(ctx, tenant, token, state, at)
	if err == nil || store.IsConflict(err) {
		return nil
	}
	return fmt.Errorf("identity: resolve challenge: %w", err)
}

// wireChallenge renders a stored challenge as the caller needs it. The token
// is carried through unchanged: it stays valid until `expires_at` and is never
// rotated mid-flight.
func wireChallenge(c store.MFAChallenge) Challenge {
	return Challenge{
		Token:     c.Token,
		Prompt:    c.Prompt,
		PollAfter: time.Duration(c.PollAfterMS) * time.Millisecond,
		ExpiresAt: c.ExpiresAt,
	}
}

// newChallengeToken mints an unguessable token.
func newChallengeToken() (string, error) {
	buf := make([]byte, challengeTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("identity: mint challenge token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ---------------------------------------------------------------------------
// the store-backed directory
// ---------------------------------------------------------------------------

// StoreDirectory is [Directory] over `internal/store`.
//
// It is what a deployment runs until 0011 lands federation, and it is the
// reason [Directory] exists as an interface at all: an IdP broker replaces
// this type and nothing above it changes.
type StoreDirectory struct{ st *store.Store }

// NewStoreDirectory builds a directory over a store.
func NewStoreDirectory(st *store.Store) *StoreDirectory { return &StoreDirectory{st: st} }

// SubjectByPrincipal implements Directory.
func (d *StoreDirectory) SubjectByPrincipal(ctx context.Context, tenant store.Tenant, login string) (store.Subject, error) {
	return d.st.Subjects().GetByPrincipal(ctx, tenant, login)
}

// SubjectByID implements Directory.
func (d *StoreDirectory) SubjectByID(ctx context.Context, tenant store.Tenant, subjectID string) (store.Subject, error) {
	return d.st.Subjects().Get(ctx, tenant, subjectID)
}

// KeyByFingerprint implements Directory.
func (d *StoreDirectory) KeyByFingerprint(ctx context.Context, tenant store.Tenant, fingerprint string) (store.SubjectKey, error) {
	if fingerprint == "" {
		// An empty fingerprint matches nothing, and saying so as absence
		// rather than as an argument error keeps the caller's deny path
		// the same shape for every malformed key.
		return store.SubjectKey{}, fmt.Errorf("identity: no fingerprint offered: %w", store.ErrNotFound)
	}
	return d.st.SubjectKeys().GetByFingerprint(ctx, tenant, fingerprint)
}

// PasswordFor implements Directory.
func (d *StoreDirectory) PasswordFor(ctx context.Context, tenant store.Tenant, subjectID string) (store.PasswordDigest, error) {
	return d.st.SubjectPasswords().Get(ctx, tenant, subjectID)
}

// MFAFor implements Directory.
func (d *StoreDirectory) MFAFor(ctx context.Context, tenant store.Tenant, subjectID string) (store.MFAEnrollment, error) {
	return d.st.MFA().GetEnrollment(ctx, tenant, subjectID)
}

// Challenges implements Directory.
func (d *StoreDirectory) Challenges() store.MFARepository { return d.st.MFA() }
