// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
)

const groupUIDs = "uid leases (POST /v1/uids/lease)"

// block is one granted block, kept so later grants can be compared against it.
// The endpoint's invariant cannot be graded one request at a time: a server that
// hands out the same uid twice passes every single-lease assertion.
type block struct {
	from, to int32
	term     int32
	leaseID  string
	proxyID  string
	fate     string // "used", "abandoned", or "expired"
}

func (b block) String() string {
	return fmt.Sprintf("[%d,%d) %s via %s (%s)", b.from, b.to, b.leaseID, b.proxyID, b.fate)
}

func overlaps(a, b block) bool { return a.from < b.to && b.from < a.to }

// uidTarget makes a target name unique to this run.
//
// The per-target cursor only ever advances and is never reset — that is the
// invariant — so a suite that leased against a fixed name would grade the
// invariant on its first run against a server and nothing at all on its
// second: every lease would answer 409 from a cursor already at the top, and
// the exhaustion case would "pass" having watched zero grants. The suffix goes
// on the first label so the name stays a plausible host in the server's own
// domain.
func (s *Suite) uidTarget(name string) string {
	if host, rest, found := strings.Cut(name, "."); found {
		return host + "-" + s.runID + "." + rest
	}
	return name + "-" + s.runID
}

// CheckUIDs grades the one invariant the whole endpoint exists for.
func (s *Suite) CheckUIDs() {
	e := &s.expect.UIDs
	var (
		nonOverlapTarget = s.uidTarget(e.Target)
		floorTarget      = s.uidTarget(e.Floor.Target)
		exhaustTarget    = s.uidTarget(e.Exhaustion.Target)
	)

	s.run(groupUIDs, "a granted block is non-empty and inside the range asked for", func(c *Case) {
		got := s.mustLease(c, contract.UIDLeaseRequest{
			ProxyID:  s.expect.ProxyID,
			Target:   nonOverlapTarget,
			UIDCount: e.UIDCount,
			RangeMin: e.RangeMin,
			RangeMax: e.RangeMax,
		})
		c.require(got.LeaseID != "",
			"grant carries no lease_id; an incident cannot then say which proxy's block a uid came from")
		c.require(got.UIDTo > got.UIDFrom,
			"uid_to (%d) is not strictly greater than uid_from (%d); a server that cannot grant a non-empty block answers 409",
			got.UIDTo, got.UIDFrom)
		if e.RangeMin != 0 || e.RangeMax != 0 {
			c.require(got.UIDFrom >= e.RangeMin && got.UIDTo-1 <= e.RangeMax,
				"block [%d,%d) falls outside the requested [%d,%d]; the proxy refuses such a block as an outage rather than allocating from it",
				got.UIDFrom, got.UIDTo, e.RangeMin, e.RangeMax)
		}
		c.require(got.TermSeconds >= 0, "term_seconds is negative (%d)", got.TermSeconds)
	})

	s.run(groupUIDs, "repeated leases for one target never overlap, across abandonment, expiry, and two proxies", func(c *Case) {
		// This is the assertion that grades the invariant. Everything else on
		// this endpoint looks fine on a server that recycles blocks.
		var held []block

		lease := func(proxyID, fate string) block {
			got := s.mustLease(c, contract.UIDLeaseRequest{
				ProxyID:  proxyID,
				Target:   nonOverlapTarget,
				UIDCount: e.UIDCount,
				RangeMin: e.RangeMin,
				RangeMax: e.RangeMax,
			})
			b := block{
				from: got.UIDFrom, to: got.UIDTo, term: got.TermSeconds,
				leaseID: got.LeaseID, proxyID: proxyID, fate: fate,
			}
			for _, prior := range held {
				c.require(!overlaps(prior, b),
					"a uid was granted twice: %s intersects %s — the per-target cursor must only ever advance, "+
						"and a block is never reclaimed whether it was used, abandoned, or allowed to expire",
					b, prior)
			}
			held = append(held, b)
			return b
		}

		// A block that is used normally.
		lease(s.expect.ProxyID, "used")

		// Blocks taken and never allocated from. A server that reclaims one to
		// save uids is the exact failure this endpoint exists to prevent.
		for i := 0; i < e.AbandonedLeases; i++ {
			lease(s.expect.ProxyID, "abandoned")
		}

		// A block allowed to expire past its term. term_seconds bounds how long
		// the proxy keeps ALLOCATING from a block; it is not what makes the
		// uids non-reusable, so an expired block is one this proxy stops using,
		// never one the server hands to somebody else.
		expiring := lease(s.expect.ProxyID, "expired")

		// Wait the term the SERVER stated, plus the configured slack. Deriving
		// it from the grant rather than from a constant is what keeps this case
		// honest against a server with a different term; when the server states
		// none (absent or 0 leaves it to the proxy), the slack is the whole
		// wait.
		wait := time.Duration(e.ExpiryWaitSeconds) * time.Second
		if expiring.term > 0 {
			wait += time.Duration(expiring.term) * time.Second
		}
		c.note("waiting %s for block %s to pass its term", wait, expiring)
		time.Sleep(wait)

		// Exclusivity is per TARGET, not per proxy. A server that keyed its
		// cursor by proxy would look perfect to a single-proxy suite and hand
		// the same uid to two proxies on the same host.
		lease(s.expect.SecondProxyID, "used")
		lease(s.expect.SecondProxyID, "abandoned")
		lease(s.expect.ProxyID, "used")

		sort.Slice(held, func(i, j int) bool { return held[i].from < held[j].from })
		lines := make([]string, len(held))
		for i, b := range held {
			lines[i] = b.String()
		}
		c.note("%d disjoint blocks: %v", len(held), lines)
	})

	s.run(groupUIDs, "observed_floor may raise the cursor and may never lower it", func(c *Case) {
		req := contract.UIDLeaseRequest{
			ProxyID:  s.expect.ProxyID,
			Target:   floorTarget,
			UIDCount: e.UIDCount,
			RangeMin: e.Floor.RangeMin,
			RangeMax: e.Floor.RangeMax,
		}
		first := s.mustLease(c, req)

		// A floor ABOVE the last grant: the cursor catches up rather than
		// granting blocks the proxy must immediately discard.
		floor := first.UIDTo + 10000
		if e.Floor.RangeMax != 0 && floor >= e.Floor.RangeMax {
			floor = first.UIDTo
		}
		raised := req
		raised.ObservedFloor = floor
		second := s.mustLease(c, raised)
		c.require(second.UIDFrom >= floor,
			"observed_floor %d was relayed and the next block starts at %d; a floor may only ever raise the cursor",
			floor, second.UIDFrom)

		// A floor BELOW the last grant must not pull the cursor back down. It
		// is a target's word relayed by an untrusted party, so it is the
		// server's to clamp or ignore — never to obey downward.
		lowered := req
		lowered.ObservedFloor = first.UIDFrom
		third := s.mustLease(c, lowered)
		c.require(third.UIDFrom >= second.UIDTo,
			"an observed_floor of %d (below the last grant) pulled the cursor back: next block starts at %d, "+
				"and the previous grant ended at %d — a lowered floor is uid reuse",
			first.UIDFrom, third.UIDFrom, second.UIDTo)
		c.note("blocks [%d,%d) then [%d,%d) then [%d,%d)",
			first.UIDFrom, first.UIDTo, second.UIDFrom, second.UIDTo, third.UIDFrom, third.UIDTo)
	})

	s.run(groupUIDs, "a cursor at the top of its range answers 409 with the envelope", func(c *Case) {
		req := contract.UIDLeaseRequest{
			ProxyID:  s.expect.ProxyID,
			Target:   exhaustTarget,
			UIDCount: e.UIDCount,
			RangeMin: e.Exhaustion.RangeMin,
			RangeMax: e.Exhaustion.RangeMax,
		}
		width := int64(e.Exhaustion.RangeMax) - int64(e.Exhaustion.RangeMin) + 1

		var blockSize int64
		var last *contract.UIDLeaseResponse
		// A bound derived from the observed block size rather than guessed, so
		// a server with a different block size is graded rather than timed out.
		attempts := 0
		for {
			attempts++
			r, err := s.postObject(contract.PathUIDLease, req)
			c.must(err == nil, "request failed: %v", err)

			if r.status == 409 {
				s.requireEnvelope(c, r)
				c.require(attempts > 1,
					"the very first lease against %s answered 409 over a range %d wide; the case watched no grant at all, "+
						"so it graded nothing — a cursor at the top before anything was granted is a server that did not start at range_min",
					exhaustTarget, width)
				c.note("exhausted after %d grant(s) over a range %d wide", attempts-1, width)
				return
			}
			c.must(r.status == 200, "want 200 or 409, got %d: %s", r.status, snippet(r.body))

			var got contract.UIDLeaseResponse
			c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
			c.must(got.UIDTo > got.UIDFrom,
				"a 200 carrying an empty or inverted block [%d,%d); the answer at the top of a range is 409",
				got.UIDFrom, got.UIDTo)
			c.require(int64(got.UIDTo)-1 <= int64(e.Exhaustion.RangeMax),
				"block [%d,%d) runs past range_max %d", got.UIDFrom, got.UIDTo, e.Exhaustion.RangeMax)
			if last != nil {
				c.must(got.UIDFrom >= last.UIDTo,
					"block [%d,%d) intersects the previous [%d,%d) while walking to exhaustion",
					got.UIDFrom, got.UIDTo, last.UIDFrom, last.UIDTo)
			}
			if blockSize == 0 {
				blockSize = int64(got.UIDTo - got.UIDFrom)
			}
			grant := got
			last = &grant

			if int64(attempts) > width/max64(blockSize, 1)+4 {
				c.require(false,
					"leased %d blocks of ~%d uids from a range only %d wide without ever seeing a 409; "+
						"a server whose cursor never reaches the top is one that is reusing uids",
					attempts, blockSize, width)
				return
			}
		}
	})
}

func (s *Suite) mustLease(c *Case, req contract.UIDLeaseRequest) contract.UIDLeaseResponse {
	r, err := s.postObject(contract.PathUIDLease, req)
	c.must(err == nil, "request failed: %v", err)
	c.must(r.status == 200, "want 200 for %s, got %d: %s", req.Target, r.status, snippet(r.body))

	var got contract.UIDLeaseResponse
	c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
	c.must(r.has("lease_id") && r.has("uid_from") && r.has("uid_to"),
		"grant omits one of the required lease_id / uid_from / uid_to: %s", snippet(r.body))
	return got
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
