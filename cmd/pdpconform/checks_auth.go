// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"time"

	"github.com/hoplock/control/internal/contract"
)

const groupAuth = "authentication (POST /v1/auth/{cert,password,mfa/poll})"

// CheckAuth grades the authentication conversation, including the MFA flow the
// server owns end to end: the proxy never contacts an MFA provider itself, so
// everything here is this server's behaviour.
func (s *Suite) CheckAuth() {
	e := &s.expect.Auth

	s.run(groupAuth, "certificate authentication succeeds and names an identity", func(c *Case) {
		r, err := s.postObject(contract.PathAuthCert, contract.AuthenticateCertRequest{
			Login:     e.CertAccept.Login,
			Target:    e.CertAccept.Target,
			PublicKey: keyOf(e.CertAccept.Key),
			Conn:      s.conn(""),
		})
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 200, "want 200, got %d: %s", r.status, snippet(r.body))

		var got contract.AuthenticateResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(got.Status == contract.AuthStatusAuthenticated,
			"want status %q, got %q — certificate authentication never returns mfa_required",
			contract.AuthStatusAuthenticated, got.Status)
		if c.require(got.Identity != nil, "status is authenticated and identity is absent") {
			c.require(got.Identity.Subject != "", "identity carries no subject")
			c.require(got.Identity.Source != "", "identity carries no source, so the audit trail cannot say who decided")
			if e.CertAccept.ExpectSubject != "" {
				c.require(got.Identity.Subject == e.CertAccept.ExpectSubject,
					"want subject %q, got %q", e.CertAccept.ExpectSubject, got.Identity.Subject)
			}
		}
		c.require(got.MFA == nil, "authenticated answer also carries an MFA challenge")
	})

	s.run(groupAuth, "an unknown key is denied with 401 and the envelope", func(c *Case) {
		r, err := s.postObject(contract.PathAuthCert, contract.AuthenticateCertRequest{
			Login:     e.CertDeny.Login,
			PublicKey: keyOf(e.CertDeny.Key),
			Conn:      s.conn(""),
		})
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 401, "want 401 (a deny is a decision), got %d: %s", r.status, snippet(r.body))
		s.requireEnvelope(c, r)
	})

	if e.PasswordNoMFA.Login != "" {
		s.run(groupAuth, "a password without MFA completes the flow", func(c *Case) {
			r, err := s.postObject(contract.PathAuthPassword, contract.AuthenticatePasswordRequest{
				Login:    e.PasswordNoMFA.Login,
				Password: e.PasswordNoMFA.Password,
				Conn:     s.conn(""),
			})
			c.must(err == nil, "request failed: %v", err)
			c.must(r.status == 200, "want 200, got %d: %s", r.status, snippet(r.body))

			var got contract.AuthenticateResponse
			c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
			c.require(got.Status == contract.AuthStatusAuthenticated,
				"want status %q, got %q", contract.AuthStatusAuthenticated, got.Status)
			c.require(got.Identity != nil, "status is authenticated and identity is absent")
		})
	}

	s.run(groupAuth, "a password with MFA challenges, stays pending, then authenticates", func(c *Case) {
		ch := s.startMFA(c, e.PasswordMFAApprove.Login, e.PasswordMFAApprove.Password)

		// The challenge's own terms are part of the contract, and a proxy that
		// honours poll_after_ms against a challenge that never expires polls
		// forever.
		c.require(ch.Token != "", "mfa challenge carries no token to poll with")
		c.require(ch.PollAfterMS >= 0, "mfa.poll_after_ms is negative (%d)", ch.PollAfterMS)
		c.require(ch.ExpiresAt != "", "mfa challenge carries no expires_at")
		if ch.ExpiresAt != "" {
			_, err := time.Parse(time.RFC3339, ch.ExpiresAt)
			c.require(err == nil, "mfa.expires_at %q is not RFC 3339", ch.ExpiresAt)
		}

		var pending int
		for i := 0; i < e.PasswordMFAApprove.MaxPolls; i++ {
			sleepMS(ch.PollAfterMS)
			r, err := s.postObject(contract.PathAuthMFAPoll, contract.MFAPollRequest{
				Token: ch.Token, Conn: s.conn(""),
			})
			c.must(err == nil, "poll failed: %v", err)
			c.must(r.status == 200, "poll %d: want 200, got %d: %s", i+1, r.status, snippet(r.body))

			var got contract.AuthenticateResponse
			c.must(r.into(&got) == nil, "undecodable poll body: %s", snippet(r.body))

			switch got.Status {
			case contract.AuthStatusMFARequired:
				pending++
				c.must(got.MFA != nil, "poll answered mfa_required with no refreshed challenge")
				c.require(got.MFA.Token == ch.Token,
					"poll rotated the challenge token (%q -> %q); it stays valid until expires_at",
					ch.Token, got.MFA.Token)
				ch = *got.MFA
			case contract.AuthStatusAuthenticated:
				c.require(got.Identity != nil, "authenticated poll carries no identity")
				c.note("resolved after %d pending poll(s)", pending)
				return
			default:
				c.must(false, "poll answered unknown status %q", got.Status)
			}
		}
		c.require(false, "challenge never resolved in %d polls", e.PasswordMFAApprove.MaxPolls)
	})

	s.run(groupAuth, "a denied MFA challenge answers 401 with the envelope", func(c *Case) {
		ch := s.startMFA(c, e.PasswordMFADeny.Login, e.PasswordMFADeny.Password)
		for i := 0; i < e.PasswordMFADeny.MaxPolls; i++ {
			sleepMS(ch.PollAfterMS)
			r, err := s.postObject(contract.PathAuthMFAPoll, contract.MFAPollRequest{
				Token: ch.Token, Conn: s.conn(""),
			})
			c.must(err == nil, "poll failed: %v", err)
			if r.status == 401 {
				s.requireEnvelope(c, r)
				c.note("denied after %d poll(s)", i+1)
				return
			}
			c.must(r.status == 200, "poll %d: want 200 or 401, got %d: %s", i+1, r.status, snippet(r.body))

			var got contract.AuthenticateResponse
			c.must(r.into(&got) == nil, "undecodable poll body: %s", snippet(r.body))
			c.must(got.Status != contract.AuthStatusAuthenticated,
				"a challenge the server denies resolved to authenticated")
			if got.MFA != nil {
				ch = *got.MFA
			}
		}
		c.require(false, "denial never arrived in %d polls", e.PasswordMFADeny.MaxPolls)
	})

	if e.PasswordMFAExpiry.Login != "" {
		s.run(groupAuth, "an expired MFA challenge answers 401, not 200", func(c *Case) {
			ch := s.startMFA(c, e.PasswordMFAExpiry.Login, e.PasswordMFAExpiry.Password)

			// Wait past the challenge's own expiry, as the challenge states it.
			// Deriving the wait from expires_at rather than from a constant is
			// what keeps this case honest against a server with a different TTL.
			wait := time.Duration(e.PasswordMFAExpiry.WaitSeconds) * time.Second
			if t, err := time.Parse(time.RFC3339, ch.ExpiresAt); err == nil {
				if d := time.Until(t); d > 0 {
					wait = d + time.Duration(e.PasswordMFAExpiry.WaitSeconds)*time.Second
				}
			}
			time.Sleep(wait)

			r, err := s.postObject(contract.PathAuthMFAPoll, contract.MFAPollRequest{
				Token: ch.Token, Conn: s.conn(""),
			})
			c.must(err == nil, "poll failed: %v", err)
			c.require(r.status == 401,
				"want 401 after expiry (the token is dead), got %d: %s", r.status, snippet(r.body))
			if r.status == 401 {
				s.requireEnvelope(c, r)
			}
		})
	}

	s.run(groupAuth, "an unknown challenge token answers 401, never 200", func(c *Case) {
		r, err := s.postObject(contract.PathAuthMFAPoll, contract.MFAPollRequest{
			Token: e.UnknownChallengeToken, Conn: s.conn(""),
		})
		c.must(err == nil, "request failed: %v", err)
		c.require(r.status == 401, "want 401, got %d: %s", r.status, snippet(r.body))
		if r.status == 401 {
			s.requireEnvelope(c, r)
		}
	})
}

// startMFA runs the password leg and returns the challenge it produced, failing
// the case if the server answered anything else.
func (s *Suite) startMFA(c *Case, login, password string) contract.MFAChallenge {
	r, err := s.postObject(contract.PathAuthPassword, contract.AuthenticatePasswordRequest{
		Login: login, Password: password, Conn: s.conn(""),
	})
	c.must(err == nil, "password request failed: %v", err)
	c.must(r.status == 200, "password: want 200, got %d: %s", r.status, snippet(r.body))

	var got contract.AuthenticateResponse
	c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
	c.must(got.Status == contract.AuthStatusMFARequired,
		"want status %q, got %q", contract.AuthStatusMFARequired, got.Status)
	c.must(got.MFA != nil, "status is mfa_required and mfa is absent")
	return *got.MFA
}

func keyOf(k KeyMaterial) contract.PublicKeyMaterial {
	return contract.PublicKeyMaterial{Type: k.Type, Fingerprint: k.Fingerprint, Blob: k.Blob}
}

func sleepMS(ms int32) {
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}
