// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/hoplock/control/internal/contract"
)

const (
	groupHostKeys     = "host keys (POST /v1/hostkeys/report)"
	groupCapabilities = "capabilities (POST /v1/capabilities/report)"
)

// CheckHostKeys grades the trust decision and its optional cache hint.
//
// Whether a given key is worth hinting is the implementation's business (phase
// 0007). What the contract owns is the envelope, and that a response carrying
// no hint is EQUALLY a pass: absent means "report every connection", which is
// what every server did before the field existed.
func (s *Suite) CheckHostKeys() {
	e := &s.expect.HostKeys

	s.run(groupHostKeys, "a first sighting is accepted and reported as unknown", func(c *Case) {
		// A fingerprint unique to this run, because "first sighting" is only
		// first once and a suite that reused one would pass on its first run
		// against a server and fail on every later one.
		fp := fmt.Sprintf("SHA256:pdpconform%sfirstsighting%04d", s.runID, s.seq)
		r, err := s.postObject(contract.PathHostKeyReport, contract.HostKeyReportRequest{
			Target:  e.FirstSighting.Target,
			HostKey: contract.PublicKeyMaterial{Type: e.FirstSighting.Type, Fingerprint: fp},
			Conn:    s.conn(""),
		})
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 200, "want 200, got %d: %s", r.status, snippet(r.body))

		var got contract.HostKeyReportResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(r.has("decision"), "response omits decision, which is required")
		c.require(r.has("known"), "response omits known, which is required")
		c.require(got.Decision == contract.HostKeyAccept || got.Decision == contract.HostKeyReject,
			"decision is %q, which is not a decision the contract defines", got.Decision)
		c.require(!got.Known,
			"a key the server has never seen answered known: true; recording it is what changes the answer")
		s.requireHostKeyCache(c, &got)
	})

	s.run(groupHostKeys, "a known key round-trips, and an absent cache hint is a pass", func(c *Case) {
		r, err := s.postObject(contract.PathHostKeyReport, contract.HostKeyReportRequest{
			Target:  e.Known.Target,
			HostKey: contract.PublicKeyMaterial{Type: e.Known.Type, Fingerprint: e.Known.Fingerprint},
			Conn:    s.conn(""),
		})
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 200, "want 200, got %d: %s", r.status, snippet(r.body))

		var got contract.HostKeyReportResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(got.Known, "%s / %s was expected to be a key the server already holds and answered known: false",
			e.Known.Target, e.Known.Fingerprint)
		s.requireHostKeyCache(c, &got)

		if r.has("cache") {
			c.note("cache hint offered, ttl %ds", got.Cache.TTLSeconds)
		} else {
			// Absent is not a gap. HostKeyReportResponse.cache is the
			// contract's own worked example of a field OUTSIDE policy_version,
			// and a proxy that has never heard of it keeps reporting every
			// connection — correct behaviour, not a thinned answer. So this
			// case is not gated on a version either.
			c.note("no cache hint: the proxy reports every connection, which is the pre-field behaviour and a pass")
		}
	})
}

// requireHostKeyCache asserts that a hint, where one is offered, is the same
// CacheHint shape /v1/authorize answers with — the field rides on two responses
// and means the same thing on both.
func (s *Suite) requireHostKeyCache(c *Case, got *contract.HostKeyReportResponse) {
	if got.Cache == nil {
		return
	}
	c.require(got.Cache.TTLSeconds >= 0, "cache.ttl_seconds is negative (%d)", got.Cache.TTLSeconds)
	if got.Cache.Cacheable() {
		c.require(got.Cache.Key != "",
			"cache.ttl_seconds is %d and cache.key is empty; the key is what a cache_invalidate event names",
			got.Cache.TTLSeconds)
	}
}

// CheckCapabilities grades the v4 capability report: a recorded report answers
// accepted, and report_after_seconds bounds how long a proxy may wait before
// re-observing — sooner is always allowed, later is not.
func (s *Suite) CheckCapabilities() {
	e := &s.expect.Capabilities

	s.run(groupCapabilities, "a recorded capability report answers accepted", func(c *Case) {
		r, err := s.postObject(contract.PathCapabilitiesReport, contract.CapabilityReportRequest{
			Target:   e.Target,
			Platform: e.Platform,
			Capabilities: contract.TargetCapabilities{
				Execution:  []string{string(contract.ExecutionProxyInspected), string(contract.ExecutionNoInteractiveShell)},
				Reach:      []string{string(contract.ReachProxyChannelPolicy)},
				ObservedAt: nowRFC3339(),
				Detail:     map[string]string{"init": "systemd-257"},
			},
			Conn: s.conn(""),
		})
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 200, "want 200, got %d: %s", r.status, snippet(r.body))

		var got contract.CapabilityReportResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(r.has("accepted"), "response omits accepted, which is required")
		c.require(got.Accepted,
			"accepted is false; a report the server did not record is one the proxy must not believe it made")
	})

	s.run(groupCapabilities, "report_after_seconds bounds the next observation without demanding one", func(c *Case) {
		r, err := s.postObject(contract.PathCapabilitiesReport, contract.CapabilityReportRequest{
			Target: e.Target,
			Capabilities: contract.TargetCapabilities{
				Execution:  []string{string(contract.ExecutionProxyInspected)},
				ObservedAt: nowRFC3339(),
			},
			Conn: s.conn(""),
		})
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 200, "want 200, got %d: %s", r.status, snippet(r.body))

		var got contract.CapabilityReportResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(got.ReportAfterSeconds >= 0,
			"report_after_seconds is %d; a negative interval names no instant", got.ReportAfterSeconds)

		if e.ExpectReportAfterSeconds >= 0 {
			c.require(got.ReportAfterSeconds == e.ExpectReportAfterSeconds,
				"want report_after_seconds %d, got %d", e.ExpectReportAfterSeconds, got.ReportAfterSeconds)
		} else if !r.has("report_after_seconds") || got.ReportAfterSeconds == 0 {
			// Absent and 0 are the same answer: the interval is the proxy's.
			// That is a pass, and it is stated here rather than asserted so a
			// reader of the report can see the case was not skipped.
			c.note("absent or zero: the server leaves the interval to the proxy")
		} else {
			c.note("server asks to be re-observed after %ds; a proxy may re-observe sooner, never later", got.ReportAfterSeconds)
		}
	})
}
