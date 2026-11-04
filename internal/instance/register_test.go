// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// supervisor is a fake supervisor: it remembers the last event id it was sent,
// across connections, as a real one would, and resumes from it.
type supervisor struct {
	mu        sync.Mutex
	lastID    string
	open      int // connections open now
	maxOpen   int
	conns     [][]Line
	inbound   []Line // extra lines to send after the resume
	instances map[string]bool
	cred      string
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
	_ = rc.EnableFullDuplex()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	s.mu.Lock()
	s.open++
	s.maxOpen = max(s.maxOpen, s.open)
	idx := len(s.conns)
	s.conns = append(s.conns, nil)
	s.instances[r.Header.Get("Hoplock-Instance-Id")] = true
	resume := Line{Kind: InboundResume, LastEventID: s.lastID}
	extra := slices.Clone(s.inbound)
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
			s.conns[idx] = append(s.conns[idx], l)
			if _, _, ok := ParseEventID(l.ID); ok && l.Kind != "heartbeat" && l.Kind != "hello" && l.Kind != "report" {
				s.lastID = l.ID
			}
			s.mu.Unlock()
		}
	}()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case <-tick.C:
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
	return slices.Clone(s.conns[conn])
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

func registration(t *testing.T, svc *Service, url, cred string) *Registration {
	t.Helper()
	reg, err := NewRegistration(svc, NewFeed(svc, "node-a", 16), RegistrationOptions{
		Endpoint: url, Credential: cred, Heartbeat: 30 * time.Millisecond, Liveness: 500 * time.Millisecond,
		DialTimeout: 500 * time.Millisecond, MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
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
	if first[0].Protocol != ProtocolVersion {
		t.Fatalf("hello names protocol %d", first[0].Protocol)
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

// Local revocation ends the registration without a restart and without the
// supervisor; restoring it registers again.
func TestLocalRevocationEndsTheRegistration(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	svc := realService(t, st, nodeCoord{self: ext.Node{ID: "node-a"}, m: &membership{}})
	reg := registration(t, svc, srv.URL, "c")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go reg.Run(ctx)
	eventually(t, "a connection", func() bool { return sup.openNow() == 1 })

	if _, err := st.Deployment().SetSupervisionRevoked(t.Context(), true, "ops", time.Now()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the connection to end", func() bool { return sup.openNow() == 0 })
	n := sup.connections()
	time.Sleep(200 * time.Millisecond)
	if sup.connections() != n {
		t.Fatalf("a revoked registration reconnected")
	}

	if _, err := st.Deployment().SetSupervisionRevoked(t.Context(), false, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a reconnection after restore", func() bool { return sup.openNow() == 1 })
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

// Three nodes, one registration; kill its holder and there is exactly one
// again, resuming from the supervisor's last event id rather than resyncing.
func TestTheRegistrationIsAClusterSingleton(t *testing.T) {
	st := storetest.New(t)
	sup := newSupervisor("c")
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)

	e := &election{}
	m := &membership{}
	type runner struct {
		cancel context.CancelFunc
		done   chan struct{}
		svc    *Service
	}
	var runners []runner
	for _, id := range []string{"node-a", "node-b", "node-c"} {
		coord := electedCoord{nodeCoord: nodeCoord{self: ext.Node{ID: ext.NodeID(id)}, m: m}, e: e}
		svc := realService(t, st, coord)
		feed := NewFeed(svc, id, 16)
		reg, err := NewRegistration(svc, feed, RegistrationOptions{
			Endpoint: srv.URL, Credential: "c", Heartbeat: 30 * time.Millisecond, Liveness: 500 * time.Millisecond,
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = NewSupervision(coord, svc, feed, reg, 30*time.Millisecond).Run(ctx)
		}()
		runners = append(runners, runner{cancel, done, svc})
	}
	t.Cleanup(func() {
		for _, r := range runners {
			r.cancel()
			<-r.done
		}
	})

	eventually(t, "one registration", func() bool { return sup.openNow() == 1 })
	time.Sleep(150 * time.Millisecond)
	if sup.maxOpen != 1 || sup.connections() != 1 {
		t.Fatalf("three nodes opened %d registrations (at most %d at once), want one", sup.connections(), sup.maxOpen)
	}
	holder := -1
	for i, r := range runners {
		r.svc.selfMu.Lock()
		if r.svc.jobs[SupervisionJob] {
			holder = i
		}
		r.svc.selfMu.Unlock()
	}
	if holder < 0 {
		t.Fatalf("no node records that it holds the singleton")
	}

	// Something to resume past, then the holder dies.
	id := appendEvent(t, st, EventCountsChanged)
	eventually(t, "the event to reach the supervisor", func() bool {
		return slices.ContainsFunc(sup.lines(0), func(l Line) bool { return l.ID == id })
	})
	runners[holder].cancel()
	<-runners[holder].done

	eventually(t, "a second registration", func() bool { return sup.connections() == 2 && sup.openNow() == 1 })
	time.Sleep(150 * time.Millisecond)
	if sup.maxOpen != 1 {
		t.Fatalf("two registrations were open at once")
	}
	second := sup.lines(1)
	if slices.Contains(kinds(second), "resync") {
		t.Fatalf("the new holder resynced instead of resuming: %v", kinds(second))
	}
	if len(sup.instances) != 1 {
		t.Fatalf("the supervisor saw %d instance ids from one deployment", len(sup.instances))
	}
}
