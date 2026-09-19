// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

const tenant = store.Tenant("acme")

// testIterations is the work factor fixtures hash at.
//
// It is real PBKDF2 and it is nowhere near a production cost, which is the
// point: at the default a single hash takes about a second under the race
// detector, and a suite that seeds a fixture per case would spend minutes
// proving the standard library is slow. A digest is verified with the
// parameters stored beside it, so nothing under test behaves differently.
// TestHashPasswordUsesTheProductionWorkFactor is what keeps this from leaking
// into a production path.
const testIterations = 4096

// ---------------------------------------------------------------------------
// certificate / key authentication
// ---------------------------------------------------------------------------

func TestAKeyResolvesToItsOwnerAndOnlyToItsOwner(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	dir.addKey("SHA256:alice", "alice@example.com")
	svc := identity.NewService(dir)

	out, err := svc.AuthenticateKey(t.Context(), tenant, nil, identity.KeyAttempt{
		Login: "alice", Fingerprint: "SHA256:alice",
	})
	if err != nil {
		t.Fatalf("AuthenticateKey: %v", err)
	}
	mustAuthenticate(t, out, "alice@example.com")
	if out.Identity.Source == "" {
		t.Error("identity carries no source, so the audit trail cannot say who decided")
	}

	// The same key offered for somebody else's login. Recognising a key is
	// not a licence to be anyone: this is what stops one key being a
	// skeleton key for every account on the estate.
	out, err = svc.AuthenticateKey(t.Context(), tenant, nil, identity.KeyAttempt{
		Login: "bob", Fingerprint: "SHA256:alice",
	})
	if err != nil {
		t.Fatalf("AuthenticateKey (wrong login): %v", err)
	}
	mustDeny(t, out, identity.DenyLoginMismatch)
}

func TestAnUnknownKeyIsDeniedAndNotAnError(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	svc := identity.NewService(dir)

	out, err := svc.AuthenticateKey(t.Context(), tenant, nil, identity.KeyAttempt{
		Login: "alice", Fingerprint: "SHA256:nobody-has-this",
	})
	if err != nil {
		t.Fatalf("AuthenticateKey: %v", err)
	}
	mustDeny(t, out, identity.DenyUnknownKey)
}

// Certificate validation is where revocation bites, and authentication is
// never cached — so each of these is read on every call rather than
// distributed.
func TestKeyWindowsAndRevocationAreEnforcedOnEveryCall(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		key  store.SubjectKey
		want identity.DenyReason
	}{
		{
			name: "revoked",
			key:  store.SubjectKey{RevokedAt: now.Add(-time.Hour)},
			want: identity.DenyKeyRevoked,
		},
		{
			name: "not yet valid",
			key:  store.SubjectKey{ValidFrom: now.Add(time.Hour)},
			want: identity.DenyKeyNotYetValid,
		},
		{
			name: "expired",
			key:  store.SubjectKey{ValidTo: now.Add(-time.Second)},
			want: identity.DenyKeyExpired,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := newFakeDirectory()
			dir.addSubject("alice@example.com", "alice")
			key := tc.key
			key.Fingerprint, key.SubjectID = "SHA256:alice", "alice@example.com"
			dir.keys[key.Fingerprint] = key

			svc := identity.NewService(dir, identity.WithClock(func() time.Time { return now }))
			out, err := svc.AuthenticateKey(t.Context(), tenant, nil, identity.KeyAttempt{
				Login: "alice", Fingerprint: "SHA256:alice",
			})
			if err != nil {
				t.Fatalf("AuthenticateKey: %v", err)
			}
			mustDeny(t, out, tc.want)
		})
	}

	// A key inside its window still works, so the cases above are refusing
	// the window rather than the key.
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	dir.keys["SHA256:alice"] = store.SubjectKey{
		Fingerprint: "SHA256:alice", SubjectID: "alice@example.com",
		IsCertificate: true, ValidFrom: now.Add(-time.Hour), ValidTo: now.Add(time.Hour),
	}
	svc := identity.NewService(dir, identity.WithClock(func() time.Time { return now }))
	out, err := svc.AuthenticateKey(t.Context(), tenant, nil, identity.KeyAttempt{
		Login: "alice", Fingerprint: "SHA256:alice", IsCertificate: true,
	})
	if err != nil {
		t.Fatalf("AuthenticateKey (in window): %v", err)
	}
	mustAuthenticate(t, out, "alice@example.com")
}

// ---------------------------------------------------------------------------
// the chain leg (proxy D11)
// ---------------------------------------------------------------------------

// The assertion that matters is the SUBJECT: a test that only checks for a
// successful answer passes on precisely the bug this exists to prevent — a
// hop authenticating as itself and carrying the proxy's identity into the
// session instead of the user's.
func TestAChainLegAnswersTheUsersIdentityCarryingTheHop(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	svc := identity.NewService(dir)
	fleet := fakeFleet{"SHA256:proxy-2-key": "proxy-2"}

	out, err := svc.AuthenticateKey(t.Context(), tenant, fleet, identity.KeyAttempt{
		Login: "alice", Fingerprint: "SHA256:proxy-2-key",
	})
	if err != nil {
		t.Fatalf("AuthenticateKey: %v", err)
	}
	mustAuthenticate(t, out, "alice@example.com")
	if got := out.Identity.Claims[identity.ClaimChainHop]; got != "proxy-2" {
		t.Errorf("%s = %q, want proxy-2: it is the one thing the leg adds, and 0008 pairs it with conn.hop_trail",
			identity.ClaimChainHop, got)
	}
	if out.Identity.Login != "alice" {
		t.Errorf("login = %q, want the user's", out.Identity.Login)
	}
}

// A recognised proxy key is not a wildcard: it says "this caller is a
// legitimate hop" and nothing more.
func TestAChainLegWithAnUnknownLoginIsDenied(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	svc := identity.NewService(dir)
	fleet := fakeFleet{"SHA256:proxy-2-key": "proxy-2"}

	out, err := svc.AuthenticateKey(t.Context(), tenant, fleet, identity.KeyAttempt{
		Login: "nobody", Fingerprint: "SHA256:proxy-2-key",
	})
	if err != nil {
		t.Fatalf("AuthenticateKey: %v", err)
	}
	mustDeny(t, out, identity.DenyUnknownLogin)
}

// A key belonging to neither a user nor a fleet proxy is refused even when the
// login is real.
func TestAKeyThatIsNeitherAUsersNorTheFleetsIsDenied(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	svc := identity.NewService(dir)
	fleet := fakeFleet{"SHA256:proxy-2-key": "proxy-2"}

	out, err := svc.AuthenticateKey(t.Context(), tenant, fleet, identity.KeyAttempt{
		Login: "alice", Fingerprint: "SHA256:stolen-laptop",
	})
	if err != nil {
		t.Fatalf("AuthenticateKey: %v", err)
	}
	mustDeny(t, out, identity.DenyUnknownKey)
}

// Without a fleet to ask, no key can be a chain leg. Answering "yes" would
// authenticate a hop this server cannot place.
func TestWithoutAFleetNoKeyIsAChainLeg(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	svc := identity.NewService(dir)

	out, err := svc.AuthenticateKey(t.Context(), tenant, nil, identity.KeyAttempt{
		Login: "alice", Fingerprint: "SHA256:proxy-2-key",
	})
	if err != nil {
		t.Fatalf("AuthenticateKey: %v", err)
	}
	mustDeny(t, out, identity.DenyUnknownKey)
}

// ---------------------------------------------------------------------------
// M11: a failure is never a deny
// ---------------------------------------------------------------------------

// The regression test M11 exists for, and the one that is easiest to lose. A
// database failure on the auth path must arrive as an ERROR — which the
// transport answers 5xx — and never as an outcome carrying a denial.
func TestADirectoryFailureIsAnOutageAndNeverADeny(t *testing.T) {
	t.Parallel()
	boom := errors.New("connection refused")

	for _, tc := range []struct {
		name string
		fail func(*fakeDirectory)
		call func(*identity.Service) (identity.Outcome, error)
	}{
		{
			name: "key lookup",
			fail: func(d *fakeDirectory) { d.failKeys = boom },
			call: func(s *identity.Service) (identity.Outcome, error) {
				return s.AuthenticateKey(context.Background(), tenant, nil,
					identity.KeyAttempt{Login: "alice", Fingerprint: "SHA256:alice"})
			},
		},
		{
			name: "subject lookup behind a known key",
			fail: func(d *fakeDirectory) { d.failSubjects = boom },
			call: func(s *identity.Service) (identity.Outcome, error) {
				return s.AuthenticateKey(context.Background(), tenant, nil,
					identity.KeyAttempt{Login: "alice", Fingerprint: "SHA256:alice"})
			},
		},
		{
			name: "login lookup on the password path",
			fail: func(d *fakeDirectory) { d.failSubjects = boom },
			call: func(s *identity.Service) (identity.Outcome, error) {
				return s.AuthenticatePassword(context.Background(), tenant,
					identity.PasswordAttempt{Login: "alice", Password: "hunter2"})
			},
		},
		{
			name: "password lookup",
			fail: func(d *fakeDirectory) { d.failPasswords = boom },
			call: func(s *identity.Service) (identity.Outcome, error) {
				return s.AuthenticatePassword(context.Background(), tenant,
					identity.PasswordAttempt{Login: "alice", Password: "hunter2"})
			},
		},
		{
			name: "challenge lookup on a poll",
			fail: func(d *fakeDirectory) { d.challenges.fail = boom },
			call: func(s *identity.Service) (identity.Outcome, error) {
				return s.PollMFA(context.Background(), tenant, "some-token")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := newFakeDirectory()
			dir.addSubject("alice@example.com", "alice")
			dir.addKey("SHA256:alice", "alice@example.com")
			dir.addPassword("alice@example.com", "hunter2")
			tc.fail(dir)

			out, err := tc.call(identity.NewService(dir))
			if err == nil {
				t.Fatalf("a failing directory produced no error; outcome = %+v", out)
			}
			if !errors.Is(err, boom) {
				t.Errorf("error = %v, want it to wrap the underlying failure", err)
			}
			if out.Deny != nil {
				t.Fatalf("a failing directory produced a DENY (%s): the proxy would tell a "+
					"real user access was refused during an outage (M11)", out.Deny.Reason)
			}
		})
	}
}

// A fleet lookup that fails is the same rule one step further out.
func TestAFleetLookupFailureIsAnOutage(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	svc := identity.NewService(dir)

	out, err := svc.AuthenticateKey(t.Context(), tenant, failingFleet{}, identity.KeyAttempt{
		Login: "alice", Fingerprint: "SHA256:unknown",
	})
	if err == nil {
		t.Fatalf("a failing fleet lookup produced no error; outcome = %+v", out)
	}
	if out.Deny != nil {
		t.Fatalf("a failing fleet lookup produced a deny (%s)", out.Deny.Reason)
	}
}

// ---------------------------------------------------------------------------
// password + MFA
// ---------------------------------------------------------------------------

func TestAPasswordWithNoSecondFactorCompletesTheFlow(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("bob@example.com", "bob")
	dir.addPassword("bob@example.com", "bob-dev-password")
	svc := identity.NewService(dir)

	out, err := svc.AuthenticatePassword(t.Context(), tenant, identity.PasswordAttempt{
		Login: "bob", Password: "bob-dev-password",
	})
	if err != nil {
		t.Fatalf("AuthenticatePassword: %v", err)
	}
	mustAuthenticate(t, out, "bob@example.com")
}

func TestAWrongPasswordIsRefusedOutright(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	dir.addPassword("alice@example.com", "alice-dev-password")
	dir.addMFA("alice@example.com", identity.ScriptedMFAConfig{PendingPolls: 1, Decision: identity.ScriptApprove})
	svc := identity.NewService(dir, identity.WithMFAProvider(identity.ScriptedMFA{}))

	out, err := svc.AuthenticatePassword(t.Context(), tenant, identity.PasswordAttempt{
		Login: "alice", Password: "not-the-password",
	})
	if err != nil {
		t.Fatalf("AuthenticatePassword: %v", err)
	}
	mustDeny(t, out, identity.DenyBadPassword)

	// The oracle this leaves is KNOWN, EVALUATED AND ACCEPTED (PLAN §4,
	// contract `200` on /v1/auth/password = "the password was accepted"). A
	// decoy challenge for a wrong password would be a contract violation
	// and an amplifier handed to the attacker, so this assertion is here to
	// stop a later session "hardening" it.
	if out.Challenge != nil {
		t.Fatal("a wrong password produced a challenge: the contract says a 200 here means the " +
			"password was accepted, so a decoy would be a contract violation rather than a hardening")
	}
}

func TestTheMFAConversationRunsPendingThenAuthenticated(t *testing.T) {
	t.Parallel()
	// A clock the test advances, so the polls below are spaced the way a
	// proxy honouring `poll_after_ms` spaces them. Polling faster than that
	// is its own case, further down.
	clock := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	svc := mfaServiceWithClock(t,
		identity.ScriptedMFAConfig{PendingPolls: 2, Decision: identity.ScriptApprove},
		func() time.Time { return clock })

	challenge := startChallenge(t, svc)
	if challenge.Token == "" || challenge.ExpiresAt.IsZero() {
		t.Fatalf("challenge = %+v, want a token and an expiry the proxy can act on", challenge)
	}

	var pending int
	for i := range 6 {
		clock = clock.Add(5 * time.Second)
		out, err := svc.PollMFA(t.Context(), tenant, challenge.Token)
		if err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
		if out.Challenge != nil {
			if out.Challenge.Token != challenge.Token {
				t.Fatalf("poll rotated the token (%q -> %q); it stays valid until expires_at",
					challenge.Token, out.Challenge.Token)
			}
			pending++
			continue
		}
		mustAuthenticate(t, out, "alice@example.com")
		if pending != 2 {
			t.Errorf("resolved after %d pending polls, want the script's 2", pending)
		}
		return
	}
	t.Fatal("the challenge never resolved")
}

func TestADeniedChallengeIsADeny(t *testing.T) {
	t.Parallel()
	svc, _ := mfaService(t, identity.ScriptedMFAConfig{PendingPolls: 0, Decision: identity.ScriptDeny})

	challenge := startChallenge(t, svc)
	out, err := svc.PollMFA(t.Context(), tenant, challenge.Token)
	if err != nil {
		t.Fatalf("PollMFA: %v", err)
	}
	mustDeny(t, out, identity.DenyMFARefused)
}

// Expiry is a DENY, never a 200 that leaves the proxy polling forever.
func TestAnExpiredChallengeIsADeny(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	dir.addPassword("alice@example.com", "alice-dev-password")
	dir.addMFA("alice@example.com", identity.ScriptedMFAConfig{PendingPolls: 5, Decision: identity.ScriptApprove})

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	clock := now
	svc := identity.NewService(dir,
		identity.WithMFAProvider(identity.ScriptedMFA{}),
		identity.WithChallengeTTL(30*time.Second),
		identity.WithClock(func() time.Time { return clock }),
	)

	challenge := startChallenge(t, svc)
	clock = clock.Add(31 * time.Second)

	out, err := svc.PollMFA(t.Context(), tenant, challenge.Token)
	if err != nil {
		t.Fatalf("PollMFA: %v", err)
	}
	mustDeny(t, out, identity.DenyChallengeExpired)

	// A later poll says SPENT rather than UNKNOWN. Two different facts,
	// two different audit records, one status code.
	out, err = svc.PollMFA(t.Context(), tenant, challenge.Token)
	if err != nil {
		t.Fatalf("second PollMFA: %v", err)
	}
	mustDeny(t, out, identity.DenyChallengeSpent)
}

func TestAnUnknownTokenIsADeny(t *testing.T) {
	t.Parallel()
	svc, _ := mfaService(t, identity.ScriptedMFAConfig{Decision: identity.ScriptApprove})

	out, err := svc.PollMFA(t.Context(), tenant, "never-issued-this-token")
	if err != nil {
		t.Fatalf("PollMFA: %v", err)
	}
	mustDeny(t, out, identity.DenyUnknownChallenge)
}

// Replaying a RESOLVED challenge is the one that matters most: without it, one
// approval becomes an unlimited supply of authentications.
func TestAResolvedChallengeCannotBeReplayed(t *testing.T) {
	t.Parallel()
	svc, _ := mfaService(t, identity.ScriptedMFAConfig{PendingPolls: 0, Decision: identity.ScriptApprove})

	challenge := startChallenge(t, svc)
	out, err := svc.PollMFA(t.Context(), tenant, challenge.Token)
	if err != nil {
		t.Fatalf("first poll: %v", err)
	}
	mustAuthenticate(t, out, "alice@example.com")

	out, err = svc.PollMFA(t.Context(), tenant, challenge.Token)
	if err != nil {
		t.Fatalf("replayed poll: %v", err)
	}
	mustDeny(t, out, identity.DenyChallengeSpent)
}

// Poll-rate enforcement has two halves: a poll inside the advertised interval
// does not reach the provider, and a challenge polled past its budget is
// abandoned rather than left open.
func TestPollRateIsEnforcedAndTheBudgetIsFinite(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	dir.addPassword("alice@example.com", "alice-dev-password")
	dir.addMFA("alice@example.com", identity.ScriptedMFAConfig{PendingPolls: 1, Decision: identity.ScriptApprove})

	clock := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	counting := &countingProvider{}
	svc := identity.NewService(dir,
		identity.WithMFAProvider(counting),
		identity.WithPollAfter(2*time.Second),
		identity.WithMaxPolls(4),
		identity.WithClock(func() time.Time { return clock }),
	)

	challenge := startChallenge(t, svc)

	// First poll: nothing to be too soon after, so the provider is asked.
	if _, err := svc.PollMFA(t.Context(), tenant, challenge.Token); err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	if counting.calls != 1 {
		t.Fatalf("the first poll consulted the provider %d times, want 1", counting.calls)
	}

	// Second poll, immediately: inside the rate limit, so it is answered
	// from the stored row without asking the provider.
	clock = clock.Add(100 * time.Millisecond)
	out, err := svc.PollMFA(t.Context(), tenant, challenge.Token)
	if err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	if out.Challenge == nil {
		t.Fatalf("a rate-limited poll answered %+v, want the challenge back", out)
	}
	if counting.calls != 1 {
		t.Errorf("a poll inside the advertised interval consulted the provider (%d calls)", counting.calls)
	}

	// Past the budget the challenge is abandoned rather than held open.
	for range 4 {
		clock = clock.Add(5 * time.Second)
		out, err = svc.PollMFA(t.Context(), tenant, challenge.Token)
		if err != nil {
			t.Fatalf("budget poll: %v", err)
		}
		if out.Deny != nil {
			break
		}
	}
	mustDeny(t, out, identity.DenyChallengeAbandoned)
}

// A provider that cannot be asked is an OUTAGE. It is never a refusal, and the
// difference is M11 one layer down.
func TestAProviderFailureIsAnOutage(t *testing.T) {
	t.Parallel()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	dir.addPassword("alice@example.com", "alice-dev-password")
	dir.addMFA("alice@example.com", identity.ScriptedMFAConfig{Decision: identity.ScriptApprove})

	failing := &countingProvider{err: errors.New("the push service did not answer")}
	svc := identity.NewService(dir, identity.WithMFAProvider(failing))
	challenge := startChallenge(t, svc)

	failing.err = errors.New("the push service did not answer")
	out, err := svc.PollMFA(t.Context(), tenant, challenge.Token)
	if err == nil {
		t.Fatalf("a failing provider produced no error; outcome = %+v", out)
	}
	if out.Deny != nil {
		t.Fatalf("a failing provider produced a deny (%s)", out.Deny.Reason)
	}
}

// ---------------------------------------------------------------------------
// password hashing
// ---------------------------------------------------------------------------

func TestPasswordsAreSaltedAndVerifiable(t *testing.T) {
	t.Parallel()

	first, err := identity.HashPasswordWith("alice@example.com", "hunter2", testIterations)
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}
	second, err := identity.HashPasswordWith("alice@example.com", "hunter2", testIterations)
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}
	if string(first.Digest) == string(second.Digest) {
		t.Error("two hashes of one password are identical; the salt is not doing anything")
	}
	if string(first.Digest) == "hunter2" {
		t.Fatal("the stored digest is the password")
	}
	if first.Iterations != testIterations || len(first.Salt) == 0 {
		t.Errorf("digest = %+v, want a salt and the work factor it was written with stored beside it", first)
	}

	// A non-positive work factor is a caller's bug and is refused rather than
	// quietly becoming a default: a digest written at zero iterations is not a
	// digest.
	if _, err := identity.HashPasswordWith("alice@example.com", "hunter2", 0); err == nil {
		t.Error("a work factor of zero was accepted")
	}
}

// The cheap fixtures above must not be able to leak into a deployment. This is
// the assertion that keeps HashPasswordWith a test facility: the production
// entry point writes the production work factor, whatever tests do.
func TestHashPasswordUsesTheProductionWorkFactor(t *testing.T) {
	t.Parallel()

	got, err := identity.HashPassword("alice@example.com", "hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if got.Iterations != identity.DefaultPBKDF2Iterations {
		t.Errorf("HashPassword wrote %d iterations, want the production default of %d",
			got.Iterations, identity.DefaultPBKDF2Iterations)
	}
	if got.Algorithm != identity.AlgorithmPBKDF2SHA256 {
		t.Errorf("HashPassword wrote algorithm %q, want %q", got.Algorithm, identity.AlgorithmPBKDF2SHA256)
	}
}

// ---------------------------------------------------------------------------
// the OpenSSH fingerprint
// ---------------------------------------------------------------------------

// The Go helper and the generated SQL column must agree, or every chain leg is
// a 401. This is the Go half; `TestProxyKeyFingerprintIsTheOpenSSHForm` in
// internal/store is the other.
func TestKeyFingerprintIsTheOpenSSHForm(t *testing.T) {
	t.Parallel()

	// `printf '' | sha256sum` is e3b0c442…, and OpenSSH renders the digest
	// as unpadded standard base64 after a `SHA256:` prefix.
	got := identity.KeyFingerprint(nil)
	const want = "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"
	if got != want {
		t.Errorf("KeyFingerprint(nil) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustAuthenticate(t *testing.T, out identity.Outcome, subject string) {
	t.Helper()
	if out.Deny != nil {
		t.Fatalf("denied (%s), want an identity for %s", out.Deny.Reason, subject)
	}
	if out.Identity == nil {
		t.Fatalf("outcome = %+v, want an identity for %s", out, subject)
	}
	if out.Identity.Subject != subject {
		t.Fatalf("subject = %q, want %q", out.Identity.Subject, subject)
	}
}

func mustDeny(t *testing.T, out identity.Outcome, want identity.DenyReason) {
	t.Helper()
	if out.Deny == nil {
		t.Fatalf("outcome = %+v, want a denial (%s)", out, want)
	}
	if out.Deny.Reason != want {
		t.Fatalf("denied for %q, want %q", out.Deny.Reason, want)
	}
}

func mfaService(t *testing.T, script identity.ScriptedMFAConfig) (*identity.Service, *fakeDirectory) {
	t.Helper()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	dir.addPassword("alice@example.com", "alice-dev-password")
	dir.addMFA("alice@example.com", script)
	return identity.NewService(dir, identity.WithMFAProvider(identity.ScriptedMFA{})), dir
}

func mfaServiceWithClock(t *testing.T, script identity.ScriptedMFAConfig, now func() time.Time) *identity.Service {
	t.Helper()
	dir := newFakeDirectory()
	dir.addSubject("alice@example.com", "alice")
	dir.addPassword("alice@example.com", "alice-dev-password")
	dir.addMFA("alice@example.com", script)
	return identity.NewService(dir,
		identity.WithMFAProvider(identity.ScriptedMFA{}),
		identity.WithClock(now))
}

func startChallenge(t *testing.T, svc *identity.Service) identity.Challenge {
	t.Helper()
	out, err := svc.AuthenticatePassword(t.Context(), tenant, identity.PasswordAttempt{
		Login: "alice", Password: "alice-dev-password",
	})
	if err != nil {
		t.Fatalf("AuthenticatePassword: %v", err)
	}
	if out.Challenge == nil {
		t.Fatalf("outcome = %+v, want a challenge", out)
	}
	return *out.Challenge
}

// countingProvider records how often it was consulted, which is how poll-rate
// enforcement is observable at all.
type countingProvider struct {
	calls int
	err   error
}

func (*countingProvider) Name() string { return identity.ScriptedMFAName }

func (p *countingProvider) Begin(context.Context, identity.Identity, json.RawMessage) (identity.MFATerms, error) {
	return identity.MFATerms{Prompt: "approve"}, nil
}

func (p *countingProvider) Poll(_ context.Context, _ json.RawMessage, _ string, _ int) (identity.MFAResult, error) {
	p.calls++
	if p.err != nil {
		return "", p.err
	}
	return identity.MFAPending, nil
}

// fakeFleet answers the chain-leg question from a map.
type fakeFleet map[string]string

func (f fakeFleet) ProxyByKeyFingerprint(_ context.Context, _ store.Tenant, fp string) (string, bool, error) {
	id, ok := f[fp]
	return id, ok, nil
}

type failingFleet struct{}

func (failingFleet) ProxyByKeyFingerprint(context.Context, store.Tenant, string) (string, bool, error) {
	return "", false, errors.New("the fleet registry did not answer")
}

// fakeDirectory is an in-memory Directory whose every lookup can be made to
// fail, which is what the M11 regression test needs.
type fakeDirectory struct {
	subjects   map[string]store.Subject
	principals map[string]string
	keys       map[string]store.SubjectKey
	passwords  map[string]store.PasswordDigest
	mfa        map[string]store.MFAEnrollment
	challenges *fakeChallenges

	failSubjects  error
	failKeys      error
	failPasswords error
	failMFA       error
}

func newFakeDirectory() *fakeDirectory {
	return &fakeDirectory{
		subjects:   map[string]store.Subject{},
		principals: map[string]string{},
		keys:       map[string]store.SubjectKey{},
		passwords:  map[string]store.PasswordDigest{},
		mfa:        map[string]store.MFAEnrollment{},
		challenges: &fakeChallenges{rows: map[string]store.MFAChallenge{}},
	}
}

func (d *fakeDirectory) addSubject(id string, principals ...string) {
	d.subjects[id] = store.Subject{ID: id, Source: "local", Principals: principals}
	for _, p := range principals {
		d.principals[p] = id
	}
}

func (d *fakeDirectory) addKey(fingerprint, subjectID string) {
	d.keys[fingerprint] = store.SubjectKey{Fingerprint: fingerprint, SubjectID: subjectID}
}

func (d *fakeDirectory) addPassword(subjectID, password string) {
	digest, err := identity.HashPasswordWith(subjectID, password, testIterations)
	if err != nil {
		panic(err)
	}
	d.passwords[subjectID] = digest
}

func (d *fakeDirectory) addMFA(subjectID string, cfg identity.ScriptedMFAConfig) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	d.mfa[subjectID] = store.MFAEnrollment{
		SubjectID: subjectID, Provider: identity.ScriptedMFAName, Config: raw,
	}
}

func (d *fakeDirectory) SubjectByPrincipal(_ context.Context, _ store.Tenant, login string) (store.Subject, error) {
	if d.failSubjects != nil {
		return store.Subject{}, d.failSubjects
	}
	id, ok := d.principals[login]
	if !ok {
		return store.Subject{}, absent()
	}
	return d.subjects[id], nil
}

func (d *fakeDirectory) SubjectByID(_ context.Context, _ store.Tenant, id string) (store.Subject, error) {
	if d.failSubjects != nil {
		return store.Subject{}, d.failSubjects
	}
	s, ok := d.subjects[id]
	if !ok {
		return store.Subject{}, absent()
	}
	return s, nil
}

func (d *fakeDirectory) KeyByFingerprint(_ context.Context, _ store.Tenant, fp string) (store.SubjectKey, error) {
	if d.failKeys != nil {
		return store.SubjectKey{}, d.failKeys
	}
	k, ok := d.keys[fp]
	if !ok {
		return store.SubjectKey{}, absent()
	}
	return k, nil
}

func (d *fakeDirectory) PasswordFor(_ context.Context, _ store.Tenant, id string) (store.PasswordDigest, error) {
	if d.failPasswords != nil {
		return store.PasswordDigest{}, d.failPasswords
	}
	p, ok := d.passwords[id]
	if !ok {
		return store.PasswordDigest{}, absent()
	}
	return p, nil
}

func (d *fakeDirectory) MFAFor(_ context.Context, _ store.Tenant, id string) (store.MFAEnrollment, error) {
	if d.failMFA != nil {
		return store.MFAEnrollment{}, d.failMFA
	}
	e, ok := d.mfa[id]
	if !ok {
		return store.MFAEnrollment{}, absent()
	}
	return e, nil
}

func (d *fakeDirectory) Challenges() store.MFARepository { return d.challenges }

// fakeChallenges is the challenge store, single-goroutine and enough for the
// semantics under test. What it must reproduce faithfully is the single-use
// predicate: a second resolution is a conflict.
type fakeChallenges struct {
	rows map[string]store.MFAChallenge
	fail error
}

func (f *fakeChallenges) CreateChallenge(_ context.Context, _ store.Tenant, c store.MFAChallenge) error {
	if f.fail != nil {
		return f.fail
	}
	if _, exists := f.rows[c.Token]; exists {
		return fmt.Errorf("duplicate token: %w", store.ErrConflict)
	}
	f.rows[c.Token] = c
	return nil
}

func (f *fakeChallenges) GetChallenge(_ context.Context, _ store.Tenant, token string) (store.MFAChallenge, error) {
	if f.fail != nil {
		return store.MFAChallenge{}, f.fail
	}
	c, ok := f.rows[token]
	if !ok {
		return store.MFAChallenge{}, absent()
	}
	return c, nil
}

func (f *fakeChallenges) PollChallenge(_ context.Context, _ store.Tenant, token string, at time.Time) (store.MFAChallenge, error) {
	if f.fail != nil {
		return store.MFAChallenge{}, f.fail
	}
	c, ok := f.rows[token]
	if !ok {
		return store.MFAChallenge{}, absent()
	}
	// The row as it stood BEFORE this poll, with the poll counted: what the
	// real repository returns, and what poll-rate enforcement reads.
	before := c
	before.Polls++
	c.Polls++
	c.LastPolledAt = at
	f.rows[token] = c
	return before, nil
}

func (f *fakeChallenges) ResolveChallenge(_ context.Context, _ store.Tenant, token string, state store.MFAChallengeState, at time.Time) error {
	if f.fail != nil {
		return f.fail
	}
	c, ok := f.rows[token]
	if !ok {
		return absent()
	}
	if c.State != store.MFAChallengePending {
		return fmt.Errorf("already resolved: %w", store.ErrConflict)
	}
	c.State, c.ResolvedAt = state, at
	f.rows[token] = c
	return nil
}

func (f *fakeChallenges) GetEnrollment(context.Context, store.Tenant, string) (store.MFAEnrollment, error) {
	return store.MFAEnrollment{}, absent()
}

func (f *fakeChallenges) PutEnrollment(context.Context, store.Tenant, store.MFAEnrollment) error {
	return nil
}

func absent() error { return fmt.Errorf("no such row: %w", store.ErrNotFound) }
