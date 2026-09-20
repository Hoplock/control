// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
)

const groupEvents = "revocation stream (GET /v1/proxies/{proxy_id}/events)"

// stream is one open NDJSON subscription.
type stream struct {
	cancel context.CancelFunc
	body   io.ReadCloser
	lines  *bufio.Scanner
}

func (st *stream) close() {
	if st == nil {
		return
	}
	st.cancel()
	_ = st.body.Close()
}

// next reads one event, or reports that none arrived inside the deadline.
func (st *stream) next(deadline time.Duration) (*contract.RevocationEvent, error) {
	type result struct {
		ev  *contract.RevocationEvent
		err error
	}
	ch := make(chan result, 1)
	go func() {
		for st.lines.Scan() {
			line := bytes.TrimSpace(st.lines.Bytes())
			if len(line) == 0 {
				continue
			}
			var ev contract.RevocationEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				ch <- result{err: fmt.Errorf("stream line is not a RevocationEvent: %s", snippet(line))}
				return
			}
			ch <- result{ev: &ev}
			return
		}
		err := st.lines.Err()
		if err == nil {
			err = io.EOF
		}
		ch <- result{err: err}
	}()

	select {
	case r := <-ch:
		return r.ev, r.err
	case <-time.After(deadline):
		return nil, fmt.Errorf("no event inside %s", deadline)
	}
}

// subscribe opens the stream. lastEventID is sent only when non-empty: an absent
// one means a fresh subscription, and the server starts from now and replays
// nothing.
func (s *Suite) subscribe(proxyID, lastEventID string) (*stream, int, error) {
	path := strings.ReplaceAll(contract.PathProxyEvents, "{proxy_id}", url.PathEscape(proxyID))
	u := s.baseURL + path
	if lastEventID != "" {
		u += "?last_event_id=" + url.QueryEscape(lastEventID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		cancel()
		return nil, 0, err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	// The stream never ends on its own, so it cannot run under the client's
	// request timeout.
	client := &http.Client{}
	resp, err := client.Do(req) //nolint:bodyclose // closed by stream.close
	if err != nil {
		cancel()
		return nil, 0, err
	}
	if resp.StatusCode != 200 {
		defer func() { _ = resp.Body.Close() }()
		cancel()
		b, _ := io.ReadAll(resp.Body)
		return nil, resp.StatusCode, fmt.Errorf("subscribe answered %d: %s", resp.StatusCode, snippet(b))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &stream{cancel: cancel, body: resp.Body, lines: sc}, 200, nil
}

// publish asks the server to emit an event.
//
// There is no contract endpoint for this — the contract says so outright, and
// says why: an event originates from an operator action, on a surface it does
// not describe, and putting one on `/v1` would make every Hoplock Control
// implement an API no proxy calls. So the URL, the body and the credential are
// all suite INPUTS, and nothing here asserts anything about their shape.
//
// The credential falls back to the proxy token, which is what a server serving
// both from one listener needs. A server that keeps the operator surface on
// its own listener says so with `events.publish_token`.
func (s *Suite) publish() error {
	var body []byte
	if s.expect.Events.PublishBody != "" {
		body = []byte(s.expect.Events.PublishBody)
	}
	token := s.expect.Events.PublishToken
	if token == "" {
		token = s.token
	}
	r, err := s.do(http.MethodPost, s.expect.Events.PublishURL, body, token)
	if err != nil {
		return err
	}
	if r.status < 200 || r.status >= 300 {
		return fmt.Errorf("publish answered %d: %s", r.status, snippet(r.body))
	}
	return nil
}

// The heartbeat obligation is TWO obligations (upstream Hoplock/proxy#56), and
// meeting either alone is a failure:
//
//  1. the server keeps the interval it ADVERTISES on the stream, and
//  2. that interval is within the contract's ceiling.
//
// The second is the one that catches a server advertising 600s and honestly
// keeping to it — which passes the first while breaking every proxy in the
// fleet. They are graded as two cases so that a failure names which half.
//
// THE BOUND IS READ OFF THE WIRE. Before #56 the contract carried no field for
// it, so this suite graded a number a human typed into an expectation file
// rather than a claim the server made. `events.heartbeat_interval_seconds` is
// now the FALLBACK, for a server that advertises nothing — which stays a
// conformant server, because absent means what every server did before the
// field existed.

// heartbeatSlack is the allowance on a measured interval.
//
// It is for scheduling and the network, not a relaxation of the claim: a
// server that misses its own interval by a scheduler tick has not broken
// anything a proxy would notice, and one whose writer has stalled misses it by
// the whole run. The gap between those two is wide enough that a couple of
// seconds cannot hide the second.
const heartbeatSlack = 2 * time.Second

// heartbeatBound decides what a server is held to, and says where it came from.
//
// An error means the run is UNGRADEABLE rather than passing: a server that
// advertises nothing, against an expectation file that configures no fallback,
// could keep any interval at all and this suite would have nothing to compare
// it with. A vacuous pass is worse than a failure.
func heartbeatBound(advertised time.Duration, advertisedOK bool, fallbackSeconds int) (time.Duration, string, error) {
	if advertisedOK {
		return advertised, fmt.Sprintf("the %s this server advertises on the stream", advertised), nil
	}
	if fallbackSeconds <= 0 {
		return 0, "", fmt.Errorf(
			"this server advertises no heartbeat_interval_seconds and the expectation file configures no " +
				"events.heartbeat_interval_seconds to fall back to, so nothing here grades anything")
	}
	d := time.Duration(fallbackSeconds) * time.Second
	return d, fmt.Sprintf("the %s configured in events.heartbeat_interval_seconds (this server advertises none)", d), nil
}

// heartbeatLate reports whether a measured gap broke the bound.
func heartbeatLate(gap, bound time.Duration) bool { return gap > bound+heartbeatSlack }

// withinCeiling reports whether an interval is one a proxy can live with.
func withinCeiling(d time.Duration) bool { return d <= contract.MaxHeartbeatInterval }

// advertisement is what the stream said about its own heartbeat interval.
type advertisement struct {
	interval time.Duration
	present  bool
}

// readAdvertisement reads lines until the server states an interval or it has
// seen enough to conclude that it never will.
//
// It takes the value off ANY event rather than only a heartbeat, because the
// contract lets a server set it on any of them and a reader takes it wherever
// it appears.
func (s *Suite) readAdvertisement(c *Case, st *stream, deadline time.Duration) advertisement {
	var beats int
	for lines := 0; lines < 8 && beats < 2; lines++ {
		ev, err := st.next(deadline)
		c.must(err == nil, "reading the stream: %v", err)
		c.require(ev.EventID != "", "event carries no event_id, so a reconnect has nothing to resume from")
		c.require(ev.Timestamp != "", "event carries no timestamp")
		if d, ok := ev.AdvertisedHeartbeatInterval(); ok {
			return advertisement{interval: d, present: true}
		}
		if ev.Type == contract.EventTypeHeartbeat {
			beats++
		}
	}
	return advertisement{}
}

// CheckEvents grades the stream: heartbeats, delivery, and gap recovery.
func (s *Suite) CheckEvents() {
	s.checkHeartbeats()
	s.checkDelivery()
}

// checkHeartbeats grades the two halves of the heartbeat obligation.
//
// It is a function of its own so that the suite's own tests can point it at a
// server that fails one half and pass the other, which is how those two cases
// are known to grade anything at all.
func (s *Suite) checkHeartbeats() {
	e := &s.expect.Events
	// DISCOVERY IS BOUNDED BY THE FALLBACK, GRADING IS BOUNDED BY THE CLAIM.
	// A read has to have a deadline before the server has said anything, and
	// the file is the only number available at that point; when the file says
	// nothing either, the contract's ceiling is the outer bound, because a
	// server past it has already failed one of the two cases below.
	fallback := time.Duration(e.HeartbeatIntervalSeconds) * time.Second
	if fallback <= 0 {
		fallback = contract.MaxHeartbeatInterval
	}
	readDeadline := fallback + heartbeatSlack

	s.run(groupEvents, "the heartbeat interval this server advertises is within the contract's ceiling", func(c *Case) {
		// The half a server can fail while honestly keeping its own claim.
		// A stream heartbeating every 600s, advertising 600s, never misses
		// its advertisement — and every proxy in the fleet drops past its
		// staleness threshold and stops serving cached decisions.
		st, _, err := s.subscribe(e.ProxyID, "")
		c.must(err == nil, "subscribe failed: %v", err)
		defer st.close()

		ad := s.readAdvertisement(c, st, readDeadline)
		bound, from, err := heartbeatBound(ad.interval, ad.present, e.HeartbeatIntervalSeconds)
		c.must(err == nil, "%v", err)
		c.require(withinCeiling(bound),
			"the interval graded here is %s, past the contract's ceiling of %s (%s): two consecutive "+
				"intervals must fit inside the proxy's 20s reconnect timeout, so one lost heartbeat is "+
				"not mistaken for a dead stream",
			bound, contract.MaxHeartbeatInterval, from)
		c.note("graded %s against the %s ceiling (%s)", bound, contract.MaxHeartbeatInterval, from)
	})

	s.run(groupEvents, "heartbeats arrive within the interval the server advertises", func(c *Case) {
		// A stream that goes silent is indistinguishable from a healthy idle
		// one, and a proxy past its staleness threshold stops serving cached
		// decisions entirely. A server that stalls its heartbeat writer
		// degrades the whole fleet to uncached — correctly, but for the wrong
		// reason.
		st, _, err := s.subscribe(e.ProxyID, "")
		c.must(err == nil, "subscribe failed: %v", err)
		defer st.close()

		ad := s.readAdvertisement(c, st, readDeadline)
		bound, from, err := heartbeatBound(ad.interval, ad.present, e.HeartbeatIntervalSeconds)
		c.must(err == nil, "%v", err)

		// Two consecutive heartbeats, timed. One heartbeat says the stream
		// is alive; the gap between two is the claim being kept.
		var (
			beats int
			since = time.Now()
		)
		for beats < 2 {
			ev, err := st.next(bound + heartbeatSlack)
			c.must(err == nil, "after %d heartbeat(s): %v (the bound is %s, %s)", beats, err, bound, from)
			if ev.Type != contract.EventTypeHeartbeat {
				continue
			}
			gap := time.Since(since)
			since = time.Now()
			beats++
			c.require(!heartbeatLate(gap, bound),
				"heartbeat %d arrived %s after the last one, past %s", beats, gap.Round(time.Millisecond), from)
		}
		c.note("two heartbeats, each inside %s", from)
	})

}

// checkDelivery grades fan-out and gap recovery.
func (s *Suite) checkDelivery() {
	e := &s.expect.Events
	// These cases wait for a line without grading the interval, so the bound
	// is whatever the file says, or the contract's ceiling when it says
	// nothing — a stream that is slower than that has already failed above.
	fallback := time.Duration(e.HeartbeatIntervalSeconds) * time.Second
	if fallback <= 0 {
		fallback = contract.MaxHeartbeatInterval
	}
	readDeadline := fallback + heartbeatSlack

	s.run(groupEvents, "a published event is delivered on an open subscription", func(c *Case) {
		st, _, err := s.subscribe(e.ProxyID, "")
		c.must(err == nil, "subscribe failed: %v", err)
		defer st.close()

		c.must(s.publish() == nil, "publishing via %s failed", e.PublishURL)

		deadline := readDeadline + 3*time.Second
		for i := 0; i < 16; i++ {
			ev, err := st.next(deadline)
			c.must(err == nil, "waiting for the published event: %v", err)
			if ev.Type == contract.EventTypeHeartbeat {
				continue
			}
			c.require(ev.EventID != "", "delivered event carries no event_id")
			c.require(ev.Type != "", "delivered event carries no type")
			c.note("delivered %s as %s", ev.Type, ev.EventID)
			return
		}
		c.require(false, "16 events arrived and all of them were heartbeats")
	})

	s.run(groupEvents, "resubscribing with last_event_id replays or resyncs, and skips nothing", func(c *Case) {
		st, _, err := s.subscribe(e.ProxyID, "")
		c.must(err == nil, "subscribe failed: %v", err)

		// Two events on the open stream, so the suite holds a real
		// last_event_id and knows exactly which ids it has already processed.
		var processed []string
		for len(processed) < 2 {
			c.must(s.publish() == nil, "publishing via %s failed", e.PublishURL)
			for {
				ev, err := st.next(readDeadline + 3*time.Second)
				c.must(err == nil, "waiting for event %d: %v", len(processed)+1, err)
				if ev.Type == contract.EventTypeHeartbeat {
					continue
				}
				processed = append(processed, ev.EventID)
				break
			}
		}
		last := processed[len(processed)-1]

		// Drop the connection and publish into the gap.
		st.close()
		c.must(s.publish() == nil, "publishing into the gap via %s failed", e.PublishURL)

		resumed, _, err := s.subscribe(e.ProxyID, last)
		c.must(err == nil, "resubscribe failed: %v", err)
		defer resumed.close()

		first, err := resumed.next(readDeadline + 3*time.Second)
		c.must(err == nil, "nothing arrived on the resumed stream: %v", err)

		if first.Type == contract.EventTypeResync {
			// The server chose to say the id is too old, unknown, or unkept.
			// That is a pass: it tells the proxy it has missed events it cannot
			// be given, so the proxy drops its cache and re-authorizes from
			// scratch — which is the safe answer, not a skip.
			c.note("answered resync as the first line, so nothing was replayed and nothing was claimed to be")
			return
		}

		// Otherwise the server chose to replay. What it must not do is resume
		// live delivery having silently dropped the event published into the
		// gap, which is what "skipped" looks like from the proxy's side.
		for i := 0; i < 16; i++ {
			ev := first
			if i > 0 {
				var err error
				ev, err = resumed.next(readDeadline + 3*time.Second)
				c.must(err == nil, "waiting for the replayed event: %v", err)
			}
			if ev.Type == contract.EventTypeHeartbeat {
				continue
			}
			c.require(ev.Type != contract.EventTypeResync,
				"a resync arrived after live delivery had already begun; it must be the FIRST line and nothing older")
			for _, done := range processed {
				c.require(ev.EventID != done,
					"the resumed stream re-delivered %s, which was already processed and named in last_event_id", done)
			}
			c.note("replayed %s (%s) after %s", ev.EventID, ev.Type, last)
			return
		}

		c.require(false,
			"the resumed stream delivered no event after %s and did not answer resync either; "+
				"one event was published into the gap, so it was silently skipped", last)
	})
}
