// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// supervisor is a fake supervisor, and the reference far end of the
// registration that the 0046 and 0047 learnings point a supervisor's author
// at. It remembers the last event id it was sent, across connections, as a
// real one would, and resumes from it. It enables full duplex (HTTP/1.1) so it
// can read the request body after writing its headers, and it ends its
// response when it reads the end of the request body: that is what lets a
// revoked registration's last line arrive before the connection ends. It
// skips a line kind it does not recognise, and refuses none.
type supervisor struct {
	mu        sync.Mutex
	lastID    string
	open      int // connections open now
	maxOpen   int
	conns     []*supConn
	inbound   []Line // extra lines to send after the resume
	instances map[string]bool
	cred      string
	// endAfter, when set, ends each response that long after it began,
	// without waiting for the request body: a supervisor that drops the
	// registration.
	endAfter time.Duration
	// silent sends nothing after the resume: a supervisor that has gone
	// quiet, which the registration's liveness watchdog drops.
	silent bool
}

// supConn is what one connection's request body carried, and how it ended.
type supConn struct {
	proto int      // the HTTP major version it arrived over
	lines []Line   // every line, decoded
	raw   []string // the same lines, as sent
	ended bool     // the request body ended
	clean bool     // ...at its end, and not on an error
}

func newSupervisor(cred string) *supervisor {
	return &supervisor{cred: cred, instances: map[string]bool{}}
}

func (s *supervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+s.cred {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.EnableFullDuplex() // HTTP/1.1; HTTP/2 is full duplex already
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	s.mu.Lock()
	s.open++
	s.maxOpen = max(s.maxOpen, s.open)
	c := &supConn{proto: r.ProtoMajor}
	s.conns = append(s.conns, c)
	s.instances[r.Header.Get("Hoplock-Instance-Id")] = true
	resume := Line{Kind: InboundResume, LastEventID: s.lastID}
	extra := slices.Clone(s.inbound)
	endAfter, silent := s.endAfter, s.silent
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.open--
		s.mu.Unlock()
	}()

	enc := json.NewEncoder(w)
	_ = enc.Encode(resume)
	for _, l := range extra {
		_ = enc.Encode(l)
	}
	_ = rc.Flush()

	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(r.Body)
		sc.Buffer(make([]byte, 0, 1<<16), 1<<22)
		for sc.Scan() {
			var l Line
			if json.Unmarshal(sc.Bytes(), &l) != nil {
				continue
			}
			s.mu.Lock()
			c.lines = append(c.lines, l)
			c.raw = append(c.raw, sc.Text())
			// An event takes a position; a line that names one without
			// taking it (hello, report, heartbeat, revoked) does not.
			if _, _, ok := ParseEventID(l.ID); ok && !slices.Contains([]string{"heartbeat", "hello", "report", OutboundRevoked}, l.Kind) {
				s.lastID = l.ID
			}
			s.mu.Unlock()
		}
		s.mu.Lock()
		c.ended, c.clean = true, sc.Err() == nil
		s.mu.Unlock()
	}()
	var ending <-chan time.Time
	if endAfter > 0 {
		ending = time.After(endAfter)
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			// The end of the request body: end the response too.
			return
		case <-r.Context().Done():
			return
		case <-ending:
			return
		case <-tick.C:
			if silent {
				continue
			}
			if enc.Encode(Line{Kind: InboundHeartbeat}) != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

func (s *supervisor) lines(conn int) []Line {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conn >= len(s.conns) {
		return nil
	}
	return slices.Clone(s.conns[conn].lines)
}

// conn is a copy of one connection's record.
func (s *supervisor) conn(i int) supConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.conns) {
		return supConn{}
	}
	c := *s.conns[i]
	c.lines, c.raw = slices.Clone(c.lines), slices.Clone(c.raw)
	return c
}

func (s *supervisor) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *supervisor) openNow() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

func (s *supervisor) mostOpen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxOpen
}

// revoked is every `revoked` line the supervisor read, on any connection, as
// "connection index: raw line".
func (s *supervisor) revoked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for i, c := range s.conns {
		for j, l := range c.lines {
			if l.Kind == OutboundRevoked {
				out = append(out, fmt.Sprintf("%d: %s", i, c.raw[j]))
			}
		}
	}
	return out
}

func kinds(ls []Line) []string {
	out := []string{}
	for _, l := range ls {
		out = append(out, l.Kind)
	}
	return out
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func realService(t *testing.T, st *store.Store, coord ext.ClusterCoordinator) *Service {
	t.Helper()
	svc, err := New(Options{
		Store: st, Coordinator: coord, Fleet: fleet.New(st),
		Versions: Versions{Software: "v0.33.0", NorthAPI: 1, NorthAPIMin: 1}, DisplayName: "lab",
		ExpectedSchema: expectedSchema(t), ReportInterval: 50 * time.Millisecond,
		MinInterval: time.Millisecond, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func registration(t *testing.T, svc *Service, url, cred string, opts ...func(*RegistrationOptions)) *Registration {
	t.Helper()
	o := RegistrationOptions{
		Endpoint: url, Credential: cred, Heartbeat: 30 * time.Millisecond, Liveness: 500 * time.Millisecond,
		DialTimeout: 500 * time.Millisecond, MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		Logger: slog.New(slog.DiscardHandler),
	}
	for _, f := range opts {
		f(&o)
	}
	reg, err := NewRegistration(svc, NewFeed(svc, "node-a", 16), o)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// logSink keeps what a registration logged, so a test can read it back.
type logSink struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// events is every logged record whose event is name.
func (s *logSink) events(name string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, line := range bytes.Split(s.b.Bytes(), []byte("\n")) {
		var m map[string]any
		if json.Unmarshal(line, &m) == nil && m["event"] == name {
			out = append(out, m)
		}
	}
	return out
}

// revokedLog is a registration whose log is kept, and whose heartbeat is long
// enough that a supervisor on loopback always ends its response inside the
// bound, so `told_supervisor` is decided by the far end and not the clock.
func revokedLog(sink *logSink) func(*RegistrationOptions) {
	return func(o *RegistrationOptions) {
		o.Heartbeat, o.Liveness = 100*time.Millisecond, 2*time.Second
		o.Logger = slog.New(slog.NewJSONHandler(sink, nil))
	}
}

// An instant the store keeps exactly (microseconds, UTC), so a line's `at`
// can be compared with it, and one after it.
var (
	firstRevokedAt = time.Date(2026, 11, 5, 9, 30, 15, 123456000, time.UTC)
	laterRevokedAt = firstRevokedAt.Add(time.Minute)
)

func setRevoked(t *testing.T, st *store.Store, revoked bool, at time.Time) {
	t.Helper()
	by := ""
	if revoked {
		by = "ops"
	}
	if _, err := st.Deployment().SetSupervisionRevoked(context.Background(), revoked, by, at); err != nil {
		t.Error(err)
	}
}

func storedRevocation(t *testing.T, st *store.Store) time.Time {
	t.Helper()
	d, err := st.Deployment().Identity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return d.SupervisionRevokedAt
}

// assertSaysRevoked holds one connection to what a revoked registration
// sends: exactly one `revoked` line, as its last line, with the heartbeat's
// id form under the feed's epoch, `at` the instant the store holds, no other
// member, and a clean end of the body after it.
func assertSaysRevoked(t *testing.T, st *store.Store, c supConn) {
	t.Helper()
	if !c.ended || !c.clean {
		t.Fatalf("the request body did not end cleanly after the line (ended %v, clean %v): %v", c.ended, c.clean, kinds(c.lines))
	}
	if n := len(c.lines); n == 0 || c.lines[n-1].Kind != OutboundRevoked {
		t.Fatalf("the last line is not %q: %v", OutboundRevoked, kinds(c.lines))
	}
	if n := slices.IndexFunc(c.lines, func(l Line) bool { return l.Kind == OutboundRevoked }); n != len(c.lines)-1 {
		t.Fatalf("more than one %q line: %v", OutboundRevoked, kinds(c.lines))
	}
	raw := c.raw[len(c.raw)-1]
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &members); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(members)); !slices.Equal(got, []string{"at", "id", "kind"}) {
		t.Fatalf("the line carries %v, want kind, id and at and nothing else: %s", got, raw)
	}
	stream, err := st.Deployment().SupervisionStream(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	l := c.lines[len(c.lines)-1]
	if !regexp.MustCompile(`^evt-` + regexp.QuoteMeta(stream.Epoch) + `-\d{12}\.\d{12}$`).MatchString(l.ID) {
		t.Fatalf("the line's id %q is not a heartbeat's under epoch %s", l.ID, stream.Epoch)
	}
	if want := storedRevocation(t, st); l.At == nil || !l.At.Equal(want) {
		t.Fatalf("the line says revoked at %v, the store holds %v", l.At, want)
	}
}

// toldSupervisor is what the one `supervision_revoked` record says.
func toldSupervisor(t *testing.T, sink *logSink) bool {
	t.Helper()
	var evs []map[string]any
	eventually(t, "the supervision_revoked record", func() bool {
		evs = sink.events("supervision_revoked")
		return len(evs) > 0
	})
	if len(evs) != 1 {
		t.Fatalf("%d supervision_revoked records, want one", len(evs))
	}
	told, ok := evs[0]["told_supervisor"].(bool)
	if !ok {
		t.Fatalf("supervision_revoked has no told_supervisor: %v", evs[0])
	}
	return told
}

func appendEvent(t *testing.T, st *store.Store, kind string) string {
	t.Helper()
	e, s, err := st.Deployment().AppendSupervision(t.Context(), store.SupervisionEntry{
		Kind: kind, Body: []byte(`{}`), LoggedAt: time.Now(), Origin: "test",
	}, 16)
	if err != nil {
		t.Fatal(err)
	}
	return EventID(s.Epoch, e.Seq)
}

// A new supervisor gets the hello, then the whole picture (resync), then the
// feed as it grows, and heartbeats.
func TestARegistrationResyncsANewSupervisorThenStreams(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("s3cret")
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})
	reg := registration(t, svc, srv.URL, "s3cret")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go reg.Run(ctx)

	eventually(t, "hello and resync", func() bool {
		ls := sup.lines(0)
		return len(ls) >= 2 && ls[0].Kind == "hello" && ls[1].Kind == "resync"
	})
	first := sup.lines(0)
	if first[0].Protocol != 2 || ProtocolVersion != 2 {
		t.Fatalf("hello names protocol %d (ProtocolVersion %d), want 2", first[0].Protocol, ProtocolVersion)
	}
	var rep Report
	if err := json.Unmarshal(first[1].Data, &rep); err != nil || rep.Identity.InstanceID == "" {
		t.Fatalf("resync carries no identity: %v %s", err, first[1].Data)
	}

	id := appendEvent(t, st, EventNodeJoined)
	eventually(t, "the logged event and a heartbeat", func() bool {
		ls := sup.lines(0)
		return slices.ContainsFunc(ls, func(l Line) bool { return l.ID == id && l.Kind == EventNodeJoined }) &&
			slices.ContainsFunc(ls, func(l Line) bool { return l.Kind == "heartbeat" })
	})
}

// A supervisor that already holds an id is replayed what came after it, not
// resynced: the feed is in the store, so this works across a node change too.
func TestARegistrationReplaysFromTheSupervisorsLastEventID(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})

	seen := appendEvent(t, st, EventNodeJoined)
	missed := appendEvent(t, st, EventHealthChanged)
	sup.lastID = seen

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go registration(t, svc, srv.URL, "c").Run(ctx)
	eventually(t, "the replayed event", func() bool {
		return slices.ContainsFunc(sup.lines(0), func(l Line) bool { return l.ID == missed })
	})
	ls := sup.lines(0)
	if slices.Contains(kinds(ls), "resync") {
		t.Fatalf("a resumable supervisor was resynced: %v", kinds(ls))
	}
	if slices.ContainsFunc(ls, func(l Line) bool { return l.ID == seen }) {
		t.Fatalf("the event the supervisor already had was sent again")
	}
}

// An id from another feed, or one pruned below the floor, is resync.
func TestAnUnresumableIDIsResync(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	sup.lastID = "evt-0000000000000000-000000000001"
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go registration(t, svc, srv.URL, "c").Run(ctx)
	eventually(t, "a resync", func() bool { return slices.Contains(kinds(sup.lines(0)), "resync") })
}

// Inbound carries liveness and a re-report request, and nothing that acts: a
// line of any other kind changes nothing and is not answered.
func TestInboundOnlyHeartbeatsAndRereports(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	sup.inbound = []Line{
		{Kind: "revoke_grant", Data: json.RawMessage(`{"grant":"g-1"}`)},
		{Kind: InboundRereport},
	}
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go registration(t, svc, srv.URL, "c").Run(ctx)
	eventually(t, "a report", func() bool { return slices.Contains(kinds(sup.lines(0)), "report") })
	for _, k := range kinds(sup.lines(0)) {
		if !slices.Contains([]string{"hello", "resync", "report", "heartbeat"}, k) {
			t.Fatalf("an inbound line produced %q", k)
		}
	}
}

// A REVOKED REGISTRATION SAYS SO. The connection's last line is `revoked`,
// with the store's instant, and the request body then ends cleanly; nothing
// dials while revoked; a restore registers again, and that connection
// carries no `revoked`.
func TestARevokedRegistrationSaysSo(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})
	sink := &logSink{}
	reg := registration(t, svc, srv.URL, "c", revokedLog(sink))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go reg.Run(ctx)
	eventually(t, "a heartbeat", func() bool { return slices.Contains(kinds(sup.lines(0)), "heartbeat") })

	setRevoked(t, st, true, firstRevokedAt)
	eventually(t, "the connection to end", func() bool { return sup.conn(0).ended && sup.openNow() == 0 })
	assertSaysRevoked(t, st, sup.conn(0))
	if !toldSupervisor(t, sink) {
		t.Fatalf("a supervisor that read the line and ended its response was not told")
	}

	time.Sleep(300 * time.Millisecond)
	if n := sup.connections(); n != 1 {
		t.Fatalf("a revoked registration dialled again: %d connections", n)
	}

	setRevoked(t, st, false, time.Time{})
	eventually(t, "a reconnection after restore, and its heartbeat", func() bool {
		return sup.openNow() == 1 && slices.Contains(kinds(sup.lines(1)), "heartbeat")
	})
	if got := sup.revoked(); len(got) != 1 {
		t.Fatalf("after a restore the supervisor read %d revoked lines, want the one: %v", len(got), got)
	}
}

// Revoking a revoked deployment changes nothing a supervisor sees: the line
// carries the first instant, which the store keeps, and the second
// revocation sends no line and dials nothing.
func TestRevokingTwiceKeepsTheFirstInstant(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go registration(t, svc, srv.URL, "c", revokedLog(&logSink{})).Run(ctx)
	eventually(t, "a heartbeat", func() bool { return slices.Contains(kinds(sup.lines(0)), "heartbeat") })

	setRevoked(t, st, true, firstRevokedAt)
	setRevoked(t, st, true, laterRevokedAt)
	eventually(t, "the connection to end", func() bool { return sup.conn(0).ended && sup.openNow() == 0 })
	assertSaysRevoked(t, st, sup.conn(0))
	if at := sup.lines(0)[len(sup.lines(0))-1].At; !at.Equal(firstRevokedAt) {
		t.Fatalf("the line says %v, want the first revocation's %v", at, firstRevokedAt)
	}

	setRevoked(t, st, true, laterRevokedAt.Add(time.Minute))
	time.Sleep(300 * time.Millisecond)
	if got := storedRevocation(t, st); !got.Equal(firstRevokedAt) {
		t.Fatalf("the store holds %v after a second revocation, want the first %v", got, firstRevokedAt)
	}
	if n, got := sup.connections(), sup.revoked(); n != 1 || len(got) != 1 {
		t.Fatalf("revoking again was seen: %d connections, revoked lines %v", n, got)
	}
}

// ONLY A LOCAL REVOCATION SENDS IT. A shutdown, a supervisor that ends the
// connection, one that goes silent (the liveness watchdog) and a lost
// singleton each end a session, and none of them writes `revoked`: its
// absence means nothing.
func TestOnlyALocalRevocationSendsTheRevokedLine(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*supervisor)
		// end ends a registration running under cancel, and waits until the
		// first connection has ended.
		end func(t *testing.T, sup *supervisor, cancel context.CancelFunc, e *election)
	}{
		{"shutdown", nil, func(t *testing.T, sup *supervisor, cancel context.CancelFunc, _ *election) {
			cancel()
			eventually(t, "the connection to end", func() bool { return sup.conn(0).ended })
		}},
		{"supervisor ends the connection", func(s *supervisor) { s.endAfter = 150 * time.Millisecond },
			func(t *testing.T, sup *supervisor, _ context.CancelFunc, _ *election) {
				eventually(t, "a redial", func() bool { return sup.connections() >= 2 && sup.conn(0).ended })
			}},
		{"supervisor goes silent", func(s *supervisor) { s.silent = true },
			func(t *testing.T, sup *supervisor, _ context.CancelFunc, _ *election) {
				eventually(t, "a redial", func() bool { return sup.connections() >= 2 && sup.conn(0).ended })
			}},
		{"singleton lost", nil, func(t *testing.T, sup *supervisor, _ context.CancelFunc, e *election) {
			e.handover()
			eventually(t, "the next holder's registration", func() bool {
				return sup.connections() >= 2 && sup.conn(0).ended && slices.Contains(kinds(sup.lines(1)), "heartbeat")
			})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := storetest.New(t)
			sup := newSupervisor("c")
			if c.setup != nil {
				c.setup(sup)
			}
			srv := httptest.NewServer(sup)
			t.Cleanup(srv.Close)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			e := &election{}
			nodes := startCluster(ctx, t, st, srv.URL, e, 30*time.Millisecond, "node-a", "node-b")
			eventually(t, "a registration", func() bool { return sup.openNow() == 1 && len(sup.lines(0)) >= 2 })

			c.end(t, sup, cancel, e)
			for _, n := range nodes {
				n.stop()
			}
			if got := sup.revoked(); len(got) != 0 {
				t.Fatalf("%s sent revoked: %v", c.name, got)
			}
		})
	}
}

// A supervisor that stops reading before the `revoked` line loses the
// connection at the bound. The line's write blocks on a body nobody reads,
// and one heartbeat after the check that saw the revocation the session
// gives it up: no wait on the supervisor beyond that, no dial afterwards,
// and the log says the supervisor was not told.
func TestARevokedLineIsBoundedWhenTheSupervisorStopsReading(t *testing.T) {
	st := storetest.New(t)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})
	const heartbeat = 250 * time.Millisecond
	stalled := &stalledSupervisor{
		between: func() { setRevoked(t, st, true, firstRevokedAt) },
		stopped: make(chan time.Time, 1),
		gone:    make(chan time.Time, 1),
	}
	sink := &logSink{}
	reg := registration(t, svc, "http://supervisor.invalid/register", "c", func(o *RegistrationOptions) {
		o.Client = &http.Client{Transport: stalled}
		// Only the bound can end this session: the watchdog never fires.
		o.Heartbeat, o.Liveness = heartbeat, time.Hour
		o.Logger = slog.New(slog.NewJSONHandler(sink, nil))
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go reg.Run(ctx)

	var stoppedAt time.Time
	select {
	case stoppedAt = <-stalled.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the registration never reached the supervisor")
	}
	select {
	case goneAt := <-stalled.gone:
		// The check runs within one heartbeat of the loop starting, and the
		// bound is one heartbeat after the check.
		if took := goneAt.Sub(stoppedAt); took > 3*heartbeat {
			t.Fatalf("the connection was given up %s after the supervisor stopped reading; the bound is %s after the check", took, heartbeat)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a supervisor that stopped reading kept a revoked registration open")
	}
	if toldSupervisor(t, sink) {
		t.Fatalf("a supervisor that never read the line was recorded as told")
	}
	time.Sleep(2 * heartbeat)
	if n := stalled.dials.Load(); n != 1 {
		t.Fatalf("a revoked registration dialled %d times, want the one", n)
	}
}

// stalledSupervisor is a transport that is a supervisor: it reads the hello,
// calls between, reads one more line (the resync a new supervisor gets) and
// never reads again, while it keeps its response alive with heartbeats. It
// records when it stopped reading and when the request was given up.
type stalledSupervisor struct {
	between       func()
	stopped, gone chan time.Time
	dials         atomic.Int64
}

func (s *stalledSupervisor) RoundTrip(req *http.Request) (*http.Response, error) {
	s.dials.Add(1)
	context.AfterFunc(req.Context(), func() {
		select {
		case s.gone <- time.Now():
		default:
		}
	})
	go func() {
		sc := bufio.NewScanner(req.Body)
		sc.Buffer(make([]byte, 0, 1<<16), 1<<22)
		if !sc.Scan() {
			return
		}
		s.between()
		if !sc.Scan() {
			return
		}
		s.stopped <- time.Now()
	}()
	pr, pw := io.Pipe()
	go func() {
		enc := json.NewEncoder(pw)
		_ = enc.Encode(Line{Kind: InboundResume})
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-req.Context().Done():
				_ = pw.CloseWithError(req.Context().Err())
				return
			case <-tick.C:
				if enc.Encode(Line{Kind: InboundHeartbeat}) != nil {
					return
				}
			}
		}
	}()
	return &http.Response{
		StatusCode: http.StatusOK, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"application/x-ndjson"}}, Body: pr, Request: req,
	}, nil
}

// The line arrives over HTTP/2 as it does over HTTP/1.1 full duplex: the two
// transports the default client uses. The registration's client is the
// default one's shape, trusting the test server.
func TestARevokedRegistrationSaysSoOverHTTP2(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	srv := httptest.NewUnstartedServer(sup)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})
	sink := &logSink{}
	reg := registration(t, svc, srv.URL, "c", revokedLog(sink), func(o *RegistrationOptions) {
		o.Client = &http.Client{Transport: &http.Transport{
			TLSClientConfig:   srv.Client().Transport.(*http.Transport).TLSClientConfig,
			ForceAttemptHTTP2: true,
		}}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go reg.Run(ctx)
	eventually(t, "a heartbeat", func() bool { return slices.Contains(kinds(sup.lines(0)), "heartbeat") })
	if p := sup.conn(0).proto; p != 2 {
		t.Fatalf("the registration arrived over HTTP/%d, so this test is not testing HTTP/2", p)
	}

	setRevoked(t, st, true, firstRevokedAt)
	eventually(t, "the connection to end", func() bool { return sup.conn(0).ended && sup.openNow() == 0 })
	assertSaysRevoked(t, st, sup.conn(0))
	if !toldSupervisor(t, sink) {
		t.Fatalf("a supervisor that read the line over HTTP/2 was not told")
	}
}

// --- the singleton ----------------------------------------------------------

// election is one cluster's singleton election, shared by its nodes.
type election struct {
	mu     sync.Mutex
	holder *lease
	queue  []*lease
}

type lease struct {
	e        *election
	changes  chan bool
	held     bool
	released bool
}

func (l *lease) Held() bool {
	l.e.mu.Lock()
	defer l.e.mu.Unlock()
	return l.held
}
func (l *lease) Changes() <-chan bool { return l.changes }
func (l *lease) Release(context.Context) error {
	e := l.e
	e.mu.Lock()
	defer e.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	l.held = false
	close(l.changes)
	e.queue = slices.DeleteFunc(e.queue, func(x *lease) bool { return x == l })
	if e.holder == l {
		e.holder = nil
		if len(e.queue) > 0 {
			e.holder = e.queue[0]
			e.queue = e.queue[1:]
			e.holder.held = true
			e.holder.changes <- true
		}
	}
	return nil
}

// handover moves the singleton to the next node in line, as an election does
// when its holder loses the lease while it keeps running.
func (e *election) handover() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.holder == nil || len(e.queue) == 0 {
		return
	}
	old := e.holder
	old.held = false
	old.changes <- false
	e.holder, e.queue = e.queue[0], append(e.queue[1:], old)
	e.holder.held = true
	e.holder.changes <- true
}

// electedCoord is one node's coordinator over a shared election.
type electedCoord struct {
	nodeCoord
	e *election
}

func (c electedCoord) Lead(ctx context.Context, name string) (ext.Leadership, error) {
	if name != SupervisionJob {
		return nil, errors.New("unexpected singleton")
	}
	l := &lease{e: c.e, changes: make(chan bool, 4)}
	c.e.mu.Lock()
	if c.e.holder == nil {
		c.e.holder, l.held = l, true
		l.changes <- true
	} else {
		c.e.queue = append(c.e.queue, l)
		l.changes <- false
	}
	c.e.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = l.Release(context.Background())
	}()
	return l, nil
}

// clusterNode is one node of a test cluster: its singleton runner and the
// registration it holds when it holds the singleton.
type clusterNode struct {
	cancel context.CancelFunc
	done   chan struct{}
	svc    *Service
}

// stop stops the node and waits for it; it may be called again.
func (n *clusterNode) stop() {
	n.cancel()
	<-n.done
}

func (n *clusterNode) holds() bool {
	n.svc.selfMu.Lock()
	defer n.svc.selfMu.Unlock()
	return n.svc.jobs[SupervisionJob]
}

// startCluster runs one node per id over one store and one election, each
// campaigning for the supervisory singleton (ext.ClusterCoordinator), until
// ctx ends or the test does.
func startCluster(ctx context.Context, t *testing.T, st *store.Store, url string, e *election, heartbeat time.Duration, ids ...string) []*clusterNode {
	t.Helper()
	m := &membership{}
	var nodes []*clusterNode
	for _, id := range ids {
		coord := electedCoord{nodeCoord: nodeCoord{self: ext.Node{ID: ext.NodeID(id)}, m: m}, e: e}
		svc := realService(t, st, coord)
		feed := NewFeed(svc, id, 16)
		reg, err := NewRegistration(svc, feed, RegistrationOptions{
			Endpoint: url, Credential: "c", Heartbeat: heartbeat, Liveness: max(500*time.Millisecond, 20*heartbeat),
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatal(err)
		}
		nctx, cancel := context.WithCancel(ctx)
		n := &clusterNode{cancel: cancel, done: make(chan struct{}), svc: svc}
		go func() {
			defer close(n.done)
			_ = NewSupervision(coord, svc, feed, reg, 30*time.Millisecond).Run(nctx)
		}()
		nodes = append(nodes, n)
	}
	t.Cleanup(func() {
		for _, n := range nodes {
			n.stop()
		}
	})
	return nodes
}

// holder is the one node that records it holds the singleton, or -1 while
// that is not exactly one.
func holder(nodes []*clusterNode) int {
	at := -1
	for i, n := range nodes {
		if n.holds() {
			if at >= 0 {
				return -1
			}
			at = i
		}
	}
	return at
}

// Three nodes, one registration; kill its holder and there is exactly one
// again, resuming from the supervisor's last event id rather than resyncing.
func TestTheRegistrationIsAClusterSingleton(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	nodes := startCluster(t.Context(), t, st, srv.URL, &election{}, 30*time.Millisecond, "node-a", "node-b", "node-c")

	eventually(t, "one registration", func() bool { return sup.openNow() == 1 })
	time.Sleep(150 * time.Millisecond)
	if sup.mostOpen() != 1 || sup.connections() != 1 {
		t.Fatalf("three nodes opened %d registrations (at most %d at once), want one", sup.connections(), sup.mostOpen())
	}
	held := holder(nodes)
	if held < 0 {
		t.Fatalf("not exactly one node records that it holds the singleton")
	}

	// Something to resume past, then the holder dies.
	id := appendEvent(t, st, EventCountsChanged)
	eventually(t, "the event to reach the supervisor", func() bool {
		return slices.ContainsFunc(sup.lines(0), func(l Line) bool { return l.ID == id })
	})
	nodes[held].stop()

	eventually(t, "a second registration", func() bool { return sup.connections() == 2 && sup.openNow() == 1 })
	time.Sleep(150 * time.Millisecond)
	if sup.mostOpen() != 1 {
		t.Fatalf("two registrations were open at once")
	}
	second := sup.lines(1)
	if slices.Contains(kinds(second), "resync") {
		t.Fatalf("the new holder resynced instead of resuming: %v", kinds(second))
	}
	sup.mu.Lock()
	instances := len(sup.instances)
	sup.mu.Unlock()
	if instances != 1 {
		t.Fatalf("the supervisor saw %d instance ids from one deployment", instances)
	}
}

// In a cluster the `revoked` line comes from the node holding
// hoplock.supervision, once, with the store's instant. The singleton moves
// before the revocation, so the node that says it is not the one that first
// registered, and no node dials while revoked.
func TestARevokedClusterSaysSoOnceFromTheHolder(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	e := &election{}
	nodes := startCluster(t.Context(), t, st, srv.URL, e, 100*time.Millisecond, "node-a", "node-b", "node-c")
	eventually(t, "one registration and one holder", func() bool {
		return sup.openNow() == 1 && len(sup.lines(0)) >= 2 && holder(nodes) >= 0
	})
	first := holder(nodes)

	e.handover()
	eventually(t, "the next holder's registration", func() bool {
		return sup.connections() == 2 && sup.openNow() == 1 && sup.conn(0).ended &&
			slices.Contains(kinds(sup.lines(1)), "heartbeat") && holder(nodes) >= 0
	})
	if second := holder(nodes); second == first {
		t.Fatalf("the singleton did not move: node %d holds it still", first)
	}

	setRevoked(t, st, true, firstRevokedAt)
	eventually(t, "the holder's connection to end", func() bool { return sup.conn(1).ended && sup.openNow() == 0 })
	assertSaysRevoked(t, st, sup.conn(1))
	time.Sleep(400 * time.Millisecond)
	if got := sup.revoked(); len(got) != 1 || !strings.HasPrefix(got[0], "1: ") {
		t.Fatalf("want one revoked line, on the holder's connection: %v", got)
	}
	if n := sup.connections(); n != 2 {
		t.Fatalf("a node dialled while the deployment was revoked: %d connections", n)
	}
}
