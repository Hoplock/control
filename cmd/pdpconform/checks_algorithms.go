// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"time"

	"github.com/hoplock/control/internal/contract"
)

const groupAlgorithms = "algorithm floor and bans (authorize, capabilities report)"

// authorizeWithCapabilities answers a route at a declared version with exactly
// the capabilities given — nil omits the field entirely, which is a different
// request from an empty declaration.
func (s *Suite) authorizeWithCapabilities(route AuthorizeRoute, version int32, caps map[string]any) (*response, error) {
	var body map[string]any
	if err := json.Unmarshal(s.authorizeBody(route, &version), &body); err != nil {
		return nil, err
	}
	if caps == nil {
		delete(body, "capabilities")
	} else {
		body["capabilities"] = caps
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return s.postJSON(contract.PathAuthorize, b)
}

// declaring is the suite's usual capability declaration plus algorithm floor
// levels. The key exchanges named are the curve25519 pair every level the
// contract defines accepts; what is graded is whether the level is declared.
func declaring(levels ...string) map[string]any {
	floors := make([]map[string]any, 0, len(levels))
	for _, l := range levels {
		floors = append(floors, map[string]any{
			"level": l, "key_exchanges": []string{contract.KexCurve25519, contract.KexCurve25519LibSSH},
		})
	}
	return map[string]any{"execution": executionRungs, "reach": reachRungs, "algorithm_floors": floors}
}

// CheckAlgorithms grades the algorithm floor and bans (Hoplock/proxy#69, #72):
// their vocabulary tier, the per-level declaration, and the key-exchange report.
func (s *Suite) CheckAlgorithms() {
	e := &s.expect.Algorithms
	floorTier := contract.VocabularyAlgorithmFloor
	below := floorTier - 1

	s.run(groupAlgorithms, "a floor or a ban is refused to a proxy declaring 5, and everything else is answered there", func(c *Case) {
		for _, route := range []AuthorizeRoute{e.Floor, e.Ban} {
			r, err := s.authorizeWithCapabilities(route, below, declaring(e.FloorLevel))
			c.must(err == nil, "request failed: %v", err)
			s.requireVersionRefusal(c, r, below, floorTier)
		}
		// A brokered-certificate route needs only 5, and a route with
		// neither is answered unchanged: the tier is per route.
		cert, _, err := s.authorizeAt(s.expect.Certificate.Route, below)
		c.must(err == nil, "request failed: %v", err)
		c.require(cert.status == 200, "a brokered-certificate route at %d answered %d: %s", below, cert.status, snippet(cert.body))
		plain, err := s.authorize(s.expect.Authorize.Direct, below)
		c.must(err == nil, "request failed: %v", err)
		c.require(plain.status == 200, "a route with neither at %d answered %d: %s", below, plain.status, snippet(plain.body))
	})

	s.run(groupAlgorithms, "a declared floor level is answered; an undeclared one, or no capabilities, is a 5xx and never stripped", func(c *Case) {
		r, err := s.authorizeWithCapabilities(e.Floor, floorTier, declaring(e.FloorLevel))
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 200, "a declared level answered %d: %s", r.status, snippet(r.body))
		var got contract.AuthorizeResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(string(got.AlgorithmFloor) == e.FloorLevel,
			"algorithm_floor is %q, want %q", got.AlgorithmFloor, e.FloorLevel)

		other := string(contract.AlgorithmFloorPQHybridKex)
		if e.FloorLevel == other {
			other = string(contract.AlgorithmFloorModernKex)
		}
		for name, caps := range map[string]map[string]any{
			"another level declared": declaring(other),
			"no capabilities at all": nil,
		} {
			r, err := s.authorizeWithCapabilities(e.Floor, floorTier, caps)
			c.must(err == nil, "%s: request failed: %v", name, err)
			c.require(r.status >= 500, "%s: answered %d, want a 5xx — never the route with its floor stripped: %s",
				name, r.status, snippet(r.body))
			c.require(r.status != 401, "%s: answered 401; a build that cannot enforce a level is an outage, not a deny", name)
			if r.status >= 500 {
				s.requireEnvelope(c, r)
			}
		}
	})

	s.run(groupAlgorithms, "a ban is carried on the answer as authored", func(c *Case) {
		r, err := s.authorizeWithCapabilities(e.Ban, floorTier, declaring(e.FloorLevel))
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 200, "the ban route answered %d: %s", r.status, snippet(r.body))
		c.require(r.has("algorithm_bans"), "the route's algorithm_bans are not on the answer: %s", snippet(r.body))
		var got contract.AuthorizeResponse
		c.must(r.into(&got) == nil, "undecodable body")
		c.require(got.AlgorithmBans != nil && !got.AlgorithmBans.Empty(), "algorithm_bans is present and empty")
	})

	s.run(groupAlgorithms, "a key-exchange report is accepted, and the three malformed reports answer 400", func(c *Case) {
		now := time.Now().UTC().Format(time.RFC3339)
		ok, err := s.postObject(contract.PathCapabilitiesReport, contract.CapabilityReportRequest{
			Target: e.KexTarget, Conn: s.conn(""),
			Capabilities: contract.TargetCapabilities{Kex: &contract.KexObservation{
				FloorMet: contract.FloorMetModernKex, Negotiated: contract.KexCurve25519, ObservedAt: now,
			}},
		})
		c.must(err == nil, "request failed: %v", err)
		c.require(ok.status == 200, "a key-exchange report answered %d: %s", ok.status, snippet(ok.body))

		for name, caps := range map[string]contract.TargetCapabilities{
			"rungs with no observed_at": {Execution: []string{string(contract.ExecutionProxyInspected)}},
			"neither observation":       {},
			"a kex with no floor_met":   {Kex: &contract.KexObservation{Negotiated: contract.KexCurve25519, ObservedAt: now}},
		} {
			r, err := s.postObject(contract.PathCapabilitiesReport, contract.CapabilityReportRequest{
				Target: e.KexTarget, Capabilities: caps, Conn: s.conn(""),
			})
			c.must(err == nil, "%s: request failed: %v", name, err)
			c.require(r.status == 400, "%s answered %d, want 400: %s", name, r.status, snippet(r.body))
			if r.status == 400 {
				s.requireEnvelope(c, r)
			}
		}
	})
}
