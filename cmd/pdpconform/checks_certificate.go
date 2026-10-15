// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/internal/contract"
)

const groupCertificate = "brokered certificates (POST /v1/credentials/certificate)"

// certificateClockSkew is the skew the proxy allows between itself and this
// server when it holds valid_before to a route's lifetime_seconds.
const certificateClockSkew = 30 * time.Second

// authorizeAt answers a route at a declared version and returns the session id
// the request carried, which an issuance cites.
func (s *Suite) authorizeAt(route AuthorizeRoute, version int32) (*response, string, error) {
	body := s.authorizeBody(route, &version)
	var req struct {
		Conn contract.ConnMeta `json:"conn"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, "", err
	}
	r, err := s.postJSON(contract.PathAuthorize, body)
	return r, req.Conn.SessionID, err
}

// requireVersionRefusal asserts the answer to a route asked below its tier: a
// 5xx with the envelope naming both numbers, and never a 401 or a thinned 200.
func (s *Suite) requireVersionRefusal(c *Case, r *response, declared, needed int32) {
	c.require(r.status != 401, "a version mismatch answered 401; it is a rollout problem, not a decision about a user (M11)")
	c.require(r.status != 200, "the route was answered at vocabulary %d; it needs %d, and a stripped answer is a widened session", declared, needed)
	if !c.require(r.status >= 500, "want a 5xx, got %d: %s", r.status, snippet(r.body)) {
		return
	}
	s.requireEnvelope(c, r)
	var env contract.ErrorResponse
	if r.into(&env) == nil {
		msg := env.Error.Message
		c.require(strings.Contains(msg, strconv.Itoa(int(declared))) && strings.Contains(msg, strconv.Itoa(int(needed))),
			"the refusal %q does not name both numbers (%d declared, %d needed)", msg, declared, needed)
	}
}

// CheckCertificate grades brokered certificates (Hoplock/proxy#68): the
// method's vocabulary tier, the policy-only ladder entry, and issuance against
// a decision this server made — bound to it, one certificate per call.
func (s *Suite) CheckCertificate() {
	e := &s.expect.Certificate

	s.run(groupCertificate, "a route naming brokered-certificate is refused to a proxy declaring 4, with a 5xx naming both numbers", func(c *Case) {
		r, _, err := s.authorizeAt(e.Route, contract.VocabularyBaseline)
		c.must(err == nil, "request failed: %v", err)
		s.requireVersionRefusal(c, r, contract.VocabularyBaseline, contract.VocabularyBrokeredCertificate)
	})

	s.run(groupCertificate, "the ladder entry carries policy only: username, and at most key_type and lifetime_seconds", func(c *Case) {
		r := s.mustAuthorize(c, e.Route, contract.PolicyVersion)
		var got contract.AuthorizeResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		entry, ok := brokeredCertificateEntry(&got)
		c.must(ok, "the route's ladder names no brokered-certificate entry: %s", snippet(r.body))
		c.require(entry.Params[contract.ParamUsername] == e.Username,
			"username is %q, want %q", entry.Params[contract.ParamUsername], e.Username)
		for k := range entry.Params {
			c.require(slices.Contains([]string{contract.ParamUsername, contract.ParamKeyType, contract.ParamLifetimeSeconds}, k),
				"the entry carries %q; a certificate, its serial or a CA bundle on a cacheable decision would be replayed to every connection it serves", k)
		}
	})

	s.run(groupCertificate, "an issuance certifies exactly the submitted key, for the route's username, with its own serial and validity", func(c *Case) {
		r, session, err := s.authorizeAt(e.Route, contract.PolicyVersion)
		c.must(err == nil && r.status == 200, "authorize: %v %d %s", err, r.status, snippet(r.body))
		var decision contract.AuthorizeResponse
		c.must(r.into(&decision) == nil, "undecodable body: %s", snippet(r.body))

		pub, wire := sessionKey(c)
		got := s.issue(c, contract.CertificateRequest{
			SessionID: session, DecisionID: decision.DecisionID, Target: decision.Target, Username: e.Username, PublicKey: wire,
		}, 200)
		cert := parseCertificate(c, got.Certificate)
		c.require(string(cert.Key.Marshal()) == string(pub.Marshal()), "the certificate certifies a key other than the one submitted")
		c.require(cert.CertType == ssh.UserCert, "the certificate is not a user certificate")
		c.require(slices.Equal(cert.ValidPrincipals, []string{e.Username}),
			"principals are %v, want exactly [%s]", cert.ValidPrincipals, e.Username)
		c.require(got.Serial == strconv.FormatUint(cert.Serial, 10),
			"serial %q is not the certificate's own %d as a decimal string", got.Serial, cert.Serial)
		before, err := time.Parse(time.RFC3339, got.ValidBefore)
		c.require(err == nil && before.Unix() == int64(cert.ValidBefore),
			"valid_before %q is not the certificate's own instant %d", got.ValidBefore, cert.ValidBefore)
		c.require(time.Unix(int64(cert.ValidBefore), 0).After(time.Now()), "the certificate is already expired")

		// The route's lifetime_seconds is an upper bound: a server may
		// shorten it and may not widen it, judged as the proxy judges it,
		// from now and with its 30 seconds of clock skew.
		entry, ok := brokeredCertificateEntry(&decision)
		c.must(ok, "the decision names no brokered-certificate entry: %s", snippet(r.body))
		if raw := entry.Params[contract.ParamLifetimeSeconds]; raw != "" {
			secs, err := strconv.Atoi(raw)
			c.must(err == nil && secs > 0, "the entry's lifetime_seconds %q is not a positive integer", raw)
			limit := time.Now().Add(time.Duration(secs)*time.Second + certificateClockSkew)
			c.require(!time.Unix(int64(cert.ValidBefore), 0).After(limit),
				"the certificate is valid until %s, past the route's %ss bound from now (with %s of skew)",
				time.Unix(int64(cert.ValidBefore), 0).UTC().Format(time.RFC3339), raw, certificateClockSkew)
			c.note("valid_before %s, inside the route's %ss bound", got.ValidBefore, raw)
		} else {
			c.note("the route sets no lifetime_seconds, so only expiry is graded")
		}
	})

	s.run(groupCertificate, "two issuances citing one decision are two certificates with two serials", func(c *Case) {
		r, session, err := s.authorizeAt(e.Route, contract.PolicyVersion)
		c.must(err == nil && r.status == 200, "authorize: %v %d", err, r.status)
		var decision contract.AuthorizeResponse
		c.must(r.into(&decision) == nil, "undecodable body")
		var serials []string
		for range 2 {
			_, wire := sessionKey(c)
			got := s.issue(c, contract.CertificateRequest{
				SessionID: session, DecisionID: decision.DecisionID, Target: decision.Target, Username: e.Username, PublicKey: wire,
			}, 200)
			serials = append(serials, got.Serial)
		}
		c.require(serials[0] != serials[1], "two issuances answered one serial %s", serials[0])
	})

	s.run(groupCertificate, "an issuance the server cannot tie to its own decision is 401 with the envelope", func(c *Case) {
		r, session, err := s.authorizeAt(e.Route, contract.PolicyVersion)
		c.must(err == nil && r.status == 200, "authorize: %v %d", err, r.status)
		var decision contract.AuthorizeResponse
		c.must(r.into(&decision) == nil, "undecodable body")
		_, wire := sessionKey(c)
		for name, req := range map[string]contract.CertificateRequest{
			"a decision this server never made": {SessionID: session, DecisionID: "pdpconform-never-made-this-decision",
				Target: decision.Target, Username: e.Username, PublicKey: wire},
			"a username the decision did not name": {SessionID: session, DecisionID: decision.DecisionID,
				Target: decision.Target, Username: e.Username + "-not-this-one", PublicKey: wire},
		} {
			r, err := s.postObject(contract.PathCredentialCertificate, req)
			c.must(err == nil, "%s: request failed: %v", name, err)
			c.require(r.status == 401, "%s answered %d, want 401: %s", name, r.status, snippet(r.body))
			if r.status == 401 {
				s.requireEnvelope(c, r)
			}
		}
	})
}

func brokeredCertificateEntry(resp *contract.AuthorizeResponse) (contract.TargetAuth, bool) {
	for _, entry := range resp.TargetAuthLadder {
		if entry.Method == contract.TargetAuthBrokeredCertificate {
			return entry, true
		}
	}
	return contract.TargetAuth{}, false
}

// sessionKey generates the key pair a proxy would for one session, and renders
// its public half the way the request carries it.
func sessionKey(c *Case) (ssh.PublicKey, string) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	c.must(err == nil, "generate key: %v", err)
	key, err := ssh.NewPublicKey(pub)
	c.must(err == nil, "public key: %v", err)
	return key, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func (s *Suite) issue(c *Case, req contract.CertificateRequest, want int) contract.CertificateResponse {
	r, err := s.postObject(contract.PathCredentialCertificate, req)
	c.must(err == nil, "issuance failed: %v", err)
	c.must(r.status == want, "issuance answered %d, want %d: %s", r.status, want, snippet(r.body))
	var got contract.CertificateResponse
	c.must(r.into(&got) == nil, "undecodable issuance: %s", snippet(r.body))
	return got
}

func parseCertificate(c *Case, text string) *ssh.Certificate {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(text))
	c.must(err == nil, "the certificate does not parse: %v", err)
	cert, ok := key.(*ssh.Certificate)
	c.must(ok, "the answer is a %s, not a certificate", key.Type())
	return cert
}
