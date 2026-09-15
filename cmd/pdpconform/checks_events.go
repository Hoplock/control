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

// publish asks the server to emit an event. There is no contract endpoint for
// this — publishing is an operator action and which surface offers it is the
// implementation's business — so the URL is a suite input.
func (s *Suite) publish() error {
	var body []byte
	if s.expect.Events.PublishBody != "" {
		body = []byte(s.expect.Events.PublishBody)
	}
	r, err := s.do(http.MethodPost, s.expect.Events.PublishURL, body, s.token)
	if err != nil {
		return err
	}
	if r.status < 200 || r.status >= 300 {
		return fmt.Errorf("publish answered %d: %s", r.status, snippet(r.body))
	}
	return nil
}

// CheckEvents grades the stream: heartbeats, delivery, and gap recovery.
func (s *Suite) CheckEvents() {
	e := &s.expect.Events
	heartbeatBound := time.Duration(e.HeartbeatIntervalSeconds) * time.Second

	s.run(groupEvents, "heartbeats arrive within the interval the server advertises", func(c *Case) {
		// A stream that goes silent is indistinguishable from a healthy idle
		// one, and a proxy past its staleness threshold stops serving cached
		// decisions entirely. A server that stalls its heartbeat writer
		// degrades the whole fleet to uncached — correctly, but for the wrong
		// reason.
		st, _, err := s.subscribe(e.ProxyID, "")
		c.must(err == nil, "subscribe failed: %v", err)
		defer st.close()

		var beats int
		deadline := heartbeatBound + 2*time.Second
		for beats < 2 {
			ev, err := st.next(deadline)
			c.must(err == nil, "after %d heartbeat(s): %v (the bound is %s)", beats, err, heartbeatBound)
			c.require(ev.EventID != "", "event carries no event_id, so a reconnect has nothing to resume from")
			c.require(ev.Timestamp != "", "event carries no timestamp")
			if ev.Type == contract.EventTypeHeartbeat {
				beats++
			}
		}
		c.note("two heartbeats inside %s each", deadline)
	})

	s.run(groupEvents, "a published event is delivered on an open subscription", func(c *Case) {
		st, _, err := s.subscribe(e.ProxyID, "")
		c.must(err == nil, "subscribe failed: %v", err)
		defer st.close()

		c.must(s.publish() == nil, "publishing via %s failed", e.PublishURL)

		deadline := heartbeatBound + 5*time.Second
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
				ev, err := st.next(heartbeatBound + 5*time.Second)
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

		first, err := resumed.next(heartbeatBound + 5*time.Second)
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
				ev, err = resumed.next(heartbeatBound + 5*time.Second)
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
