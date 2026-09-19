// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/hoplock/control/internal/contract"
)

const groupErrors = "error discipline (M11)"

// CheckErrors grades the rule the contract's ground rules open with and M11
// binds this side hardest by: a 401 is a DECISION, and everything else is an
// outage. A server that answers 401 for a database timeout sends the operator
// to debug permissions during an outage.
func (s *Suite) CheckErrors() {
	s.run(groupErrors, "a malformed body answers 400 with the envelope, never 401", func(c *Case) {
		r, err := s.postJSON(contract.PathAuthorize, []byte(`{"identity": "not an object"`))
		c.must(err == nil, "request failed: %v", err)
		c.require(r.status == 400, "want 400 for a malformed body, got %d: %s", r.status, snippet(r.body))
		c.require(r.status != 401,
			"a malformed body answered 401; the proxy will tell a user access was denied for a request nobody could read")
		if r.status == 400 {
			s.requireEnvelope(c, r)
		}
	})

	s.run(groupErrors, "a valid body missing a required field answers 400", func(c *Case) {
		// Well-formed JSON, wrong shape: no identity, no target, no conn. This
		// is the case a server passes by accident when it decodes leniently and
		// then denies whatever it ended up with.
		r, err := s.postJSON(contract.PathAuthorize, []byte(`{"policy_version": 4}`))
		c.must(err == nil, "request failed: %v", err)
		c.require(r.status == 400,
			"want 400 for a request with no identity, target, or conn, got %d: %s", r.status, snippet(r.body))
		c.require(r.status != 401,
			"an empty authorize request answered 401, which says a user was refused; nobody was named to refuse")
		if r.status == 400 {
			s.requireEnvelope(c, r)
		}
	})

	s.run(groupErrors, "a bad proxy token answers 401 with the envelope", func(c *Case) {
		body := s.authorizeBody(s.expect.Authorize.Direct, ptr(contract.PolicyVersion))
		r, err := s.do("POST", s.baseURL+contract.PathAuthorize, body, "pdpconform-not-a-real-token")
		c.must(err == nil, "request failed: %v", err)
		c.require(r.status == 401,
			"want 401 for a rejected proxy token, got %d: %s", r.status, snippet(r.body))
		if r.status == 401 {
			s.requireEnvelope(c, r)
		}
	})

	s.run(groupErrors, "every endpoint rejects a bad token the same way", func(c *Case) {
		// One path answering 200 to a token another path rejects is how a
		// listener ends up with an unauthenticated corner.
		paths := map[string][]byte{
			contract.PathAuthCert:           []byte(`{"login":"x","public_key":{"type":"ssh-ed25519","blob":"","fingerprint":"SHA256:x"},"conn":{"session_id":"s","proxy_id":"p","client_addr":"1.2.3.4:1","timestamp":"2026-01-01T00:00:00Z"}}`),
			contract.PathHostKeyReport:      []byte(`{"target":"t","host_key":{"type":"ssh-ed25519","blob":"","fingerprint":"SHA256:x"},"conn":{"session_id":"s","proxy_id":"p","client_addr":"1.2.3.4:1","timestamp":"2026-01-01T00:00:00Z"}}`),
			contract.PathCapabilitiesReport: []byte(`{"target":"t","capabilities":{"observed_at":"2026-01-01T00:00:00Z"},"conn":{"session_id":"s","proxy_id":"p","client_addr":"1.2.3.4:1","timestamp":"2026-01-01T00:00:00Z"}}`),
			contract.PathUIDLease:           []byte(`{"proxy_id":"p","target":"t"}`),
			contract.PathLogsBatch:          []byte(`{"records":[{"record_id":"r","session_id":"s","timestamp":"2026-01-01T00:00:00Z","kind":"error","severity":"info"}]}`),
			contract.PathLogsPriority:       []byte(`{"record":{"record_id":"r","session_id":"s","timestamp":"2026-01-01T00:00:00Z","kind":"error","severity":"critical"}}`),
		}
		for path, body := range paths {
			r, err := s.do("POST", s.baseURL+path, body, "pdpconform-not-a-real-token")
			if !c.require(err == nil, "%s: request failed: %v", path, err) {
				continue
			}
			c.require(r.status == 401, "%s: want 401 for a rejected token, got %d: %s",
				path, r.status, snippet(r.body))
			if r.status == 401 {
				s.requireEnvelope(c, r)
			}
		}
	})
}

func ptr[T any](v T) *T { return &v }
