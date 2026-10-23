// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The feed as a sink's (0026): the behaviour a sink's delivery has and the
// archive's does not, with the short backoff only this package's tests can set.
// internal/export's tests drive the same feed through the delivery the daemon
// builds.

// fakeSink records what it was handed and checks, on every call, what
// ext.AuditSink's doc comment promises: one tenant's stream per batch,
// contiguous and ascending, never two batches of one stream in flight, and
// never past what it holds plus one except across a retained prefix.
type fakeSink struct {
	mu sync.Mutex
	// refuse, when set, decides a call's answer before the batch is taken.
	// It is called without the lock.
	refuse func(batch []ext.AuditRecord) error

	held     map[key]uint64
	offered  map[key][]uint64
	records  map[key][]ext.AuditRecord
	busy     map[key]bool
	inFlight int
	most     int
	problems []string
}

func newFakeSink() *fakeSink {
	return &fakeSink{
		held: map[key]uint64{}, offered: map[key][]uint64{}, records: map[key][]ext.AuditRecord{},
		busy: map[key]bool{},
	}
}

func (s *fakeSink) Export(_ context.Context, batch []ext.AuditRecord) error {
	if len(batch) == 0 {
		s.problem("an empty batch")
		return nil
	}
	k := key{batch[0].Tenant, batch[0].Stream}
	s.mu.Lock()
	if s.busy[k] {
		s.problems = append(s.problems, fmt.Sprintf("%v: two batches of one stream in flight", k))
	}
	s.busy[k] = true
	s.inFlight++
	s.most = max(s.most, s.inFlight)
	refuse := s.refuse
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy[k] = false
		s.inFlight--
		s.mu.Unlock()
	}()

	for i, r := range batch {
		if r.Tenant != k.tenant || r.Stream != k.stream {
			s.problem(fmt.Sprintf("%v: a batch mixes in %s/%s", k, r.Tenant, r.Stream))
		}
		if i > 0 && r.Sequence != batch[i-1].Sequence+1 {
			s.problem(fmt.Sprintf("%v: a batch is not contiguous at %d", k, r.Sequence))
		}
	}
	s.mu.Lock()
	for _, r := range batch {
		s.offered[k] = append(s.offered[k], r.Sequence)
	}
	s.mu.Unlock()
	if refuse != nil {
		if err := refuse(batch); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	held := s.held[k]
	for _, r := range batch {
		switch {
		case r.Sequence <= held:
			// A duplicate, which Export tolerates by RecordID.
		case held != 0 && r.Sequence != held+1:
			s.problems = append(s.problems, fmt.Sprintf("%v: handed %d while holding through %d", k, r.Sequence, held))
		default:
			s.records[k] = append(s.records[k], r)
			held = r.Sequence
		}
	}
	s.held[k] = held
	return nil
}

func (s *fakeSink) problem(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.problems = append(s.problems, p)
}

func (s *fakeSink) snapshot() (map[key][]ext.AuditRecord, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[key][]ext.AuditRecord{}
	for k, v := range s.records {
		out[k] = append([]ext.AuditRecord(nil), v...)
	}
	return out, append([]string(nil), s.problems...)
}

// sinkFeed is a sink's feed with the bounds a test wants.
func sinkFeed(t *testing.T, st *store.Store, s ext.AuditSink, provider string, inFlight int, log *slog.Logger) *Feed {
	t.Helper()
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	f, err := New(Options{
		Store: st,
		Consumer: &Consumer{
			Key: store.SinkConsumer(provider), Point: ext.PointAuditSink, Provider: provider, Deliver: s.Export,
		},
		BatchRecords: 4, AttemptTimeout: 2 * time.Second, IdleInterval: 20 * time.Millisecond, BackoffCap: time.Second,
		InFlight: inFlight, Node: "node-1", Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.base = 10 * time.Millisecond
	return f
}

// sinkCaughtUp waits until a sink's position in every stream of a tenant is
// the stream's head.
func sinkCaughtUp(t *testing.T, st *store.Store, consumer string, within time.Duration, tenant store.Tenant) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		streams, err := st.Feed().Streams(context.Background(), tenant, consumer)
		if err != nil {
			t.Fatal(err)
		}
		behind := ""
		for _, s := range streams {
			if s.Behind() {
				behind = fmt.Sprintf("%s at %d of %d", s.Stream, s.Confirmed, s.Head)
			}
		}
		if behind == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not catch up within %s: %s", consumer, within, behind)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// lockedLog is a JSON log a test reads while the feed writes it.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// events returns the log lines whose event is the one named.
func (l *lockedLog) events(event string) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for line := range strings.SplitSeq(l.buf.String(), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m["event"] == event {
			out = append(out, m)
		}
	}
	return out
}

// A sink that fails three batches and then accepts is offered the same
// records again each time, its position moves only on a success, and it ends
// holding every record once, in order, with nothing skipped.
func TestAFailedSinkBatchIsOfferedAgainAndOnlyASuccessMovesThePosition(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	s := newFakeSink()
	var calls atomic.Int32
	positions := make(chan int64, 8)
	s.refuse = func([]ext.AuditRecord) error {
		pos, _ := st.Feed().Position(context.Background(), tenantA, store.SinkConsumer("test/sink"), "proxy:p1")
		if n := calls.Add(1); n <= 3 {
			positions <- pos.ConfirmedSeq
			return ext.Errorf(ext.PointAuditSink, "test/sink", "export", ext.KindUnavailable, "the SIEM is down")
		}
		return nil
	}
	f := sinkFeed(t, st, s, "test/sink", 2, nil)
	in := newIngester(t, st, f.Track)
	ingest(t, in, tenantA, "p1", "", many(3, "s")...)
	start(t, f)
	sinkCaughtUp(t, st, store.SinkConsumer("test/sink"), 10*time.Second, tenantA)

	close(positions)
	for p := range positions {
		if p != 0 {
			t.Errorf("a refused batch moved the position to %d", p)
		}
	}
	held, problems := s.snapshot()
	for _, p := range problems {
		t.Error(p)
	}
	s.mu.Lock()
	offered := fmt.Sprint(s.offered[key{ext.Tenant(tenantA), "proxy:p1"}])
	s.mu.Unlock()
	if offered != "[1 2 3 1 2 3 1 2 3 1 2 3]" {
		t.Errorf("offered %s; want the same batch four times", offered)
	}
	if got := held[key{ext.Tenant(tenantA), "proxy:p1"}]; len(got) != 3 || got[0].Sequence != 1 || got[2].Sequence != 3 {
		t.Errorf("the sink holds %+v", got)
	}
	if pos, _ := st.Feed().Position(context.Background(), tenantA, store.SinkConsumer("test/sink"), "proxy:p1"); pos.ConfirmedSeq != 3 || !pos.FailingSince.IsZero() {
		t.Errorf("after the success the position is %+v", pos)
	}
}

// Delivery writes no audit record: a sink failing over and over, for as long
// as the test lets it, leaves the control stream's head where ingest put it,
// and the failure shows on the lag instead — and is logged once, not per
// attempt.
func TestASinkFailingWritesNothingToTheChain(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	s := newFakeSink()
	var failing atomic.Bool
	failing.Store(true)
	var attempts atomic.Int32
	s.refuse = func([]ext.AuditRecord) error {
		if failing.Load() {
			attempts.Add(1)
			return errors.New("connection refused")
		}
		return nil
	}
	log := &lockedLog{}
	f := sinkFeed(t, st, s, "test/sink", 2, slog.New(slog.NewJSONHandler(log, nil)))
	f.capWait = 20 * time.Millisecond // retried often, to give a record every chance to appear
	in := newIngester(t, st, f.Track)
	ingest(t, in, tenantA, "", audit.StreamControl, many(2, "")...)
	ingest(t, in, tenantA, "p1", "", many(2, "s")...)
	head, err := st.Audit().ChainHead(context.Background(), tenantA, audit.StreamControl)
	if err != nil {
		t.Fatal(err)
	}
	start(t, f)
	deadline := time.Now().Add(10 * time.Second)
	for attempts.Load() < 40 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d failed attempts", attempts.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lag, err := f.Lag(context.Background(), tenantA); err != nil || !lag.Failing || lag.StreamsBehind != 2 {
		t.Errorf("while failing the lag is %+v, %v", lag, err)
	}
	after, err := st.Audit().ChainHead(context.Background(), tenantA, audit.StreamControl)
	if err != nil || after.ChainSeq != head.ChainSeq || after.Hash != head.Hash {
		t.Errorf("the control stream's head moved from %d to %d while a sink failed", head.ChainSeq, after.ChainSeq)
	}
	if n := len(log.events("audit_sink_failing")); n != 2 {
		t.Errorf("%d failing lines for %d failed attempts on two streams; want one per stream", n, attempts.Load())
	}

	failing.Store(false)
	sinkCaughtUp(t, st, store.SinkConsumer("test/sink"), 10*time.Second, tenantA)
	if lag, _ := f.Lag(context.Background(), tenantA); lag.Failing {
		t.Errorf("caught up, the lag is %+v", lag)
	}
	if n := len(log.events("audit_sink_recovered")); n != 2 {
		t.Errorf("%d recovery lines; want one per stream", n)
	}
	if after, _ := st.Audit().ChainHead(context.Background(), tenantA, audit.StreamControl); after.ChainSeq != head.ChainSeq {
		t.Errorf("delivery wrote to the control stream: head %d, was %d", after.ChainSeq, head.ChainSeq)
	}
}

// A sink is never handed two batches of one stream at once, and is handed
// batches of different streams together, up to its bound and never past it.
func TestASinkSeesStreamsInFlightTogetherUpToItsBoundAndNeverOneStreamTwice(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	s := newFakeSink()
	s.refuse = func([]ext.AuditRecord) error {
		time.Sleep(30 * time.Millisecond)
		return nil
	}
	f := sinkFeed(t, st, s, "test/sink", 2, nil)
	in := newIngester(t, st, f.Track)
	for p := range 5 {
		ingest(t, in, tenantA, fmt.Sprintf("p%d", p), "", many(12, "s")...)
	}
	start(t, f)
	sinkCaughtUp(t, st, store.SinkConsumer("test/sink"), 20*time.Second, tenantA)
	_, problems := s.snapshot()
	for _, p := range problems {
		t.Error(p)
	}
	s.mu.Lock()
	most := s.most
	s.mu.Unlock()
	if most != 2 {
		t.Errorf("at most %d batches were in flight together; want the bound, 2", most)
	}
}

// A sink's call abandoned at its deadline keeps its place until it returns: a
// sink bounded to one batch in flight, hung on one stream, is handed nothing
// else until that call returns — it stalls only itself — and then everything.
func TestAHungSinkCallHoldsItsPlaceUntilItReturns(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	s := newFakeSink()
	release := make(chan struct{})
	hung := make(chan struct{}, 1)
	var once sync.Once
	s.refuse = func(batch []ext.AuditRecord) error {
		if batch[0].Stream != "proxy:p1" {
			return nil
		}
		var block bool
		once.Do(func() { block = true })
		if block {
			hung <- struct{}{}
			<-release // ignores its deadline
			return errors.New("too late")
		}
		return nil
	}
	f := sinkFeed(t, st, s, "test/sink", 1, nil)
	f.attempt = 50 * time.Millisecond
	in := newIngester(t, st, f.Track)
	ingest(t, in, tenantA, "p1", "", many(2, "s")...)
	start(t, f)
	<-hung
	ingest(t, in, tenantA, "p2", "", many(2, "s")...)
	time.Sleep(300 * time.Millisecond) // six deadlines
	if pos, err := st.Feed().Position(context.Background(), tenantA, store.SinkConsumer("test/sink"), "proxy:p2"); err == nil && pos.ConfirmedSeq > 0 {
		t.Errorf("proxy:p2 was delivered at %+v while the only place was held by a hung call", pos)
	}
	close(release)
	sinkCaughtUp(t, st, store.SinkConsumer("test/sink"), 10*time.Second, tenantA)
	_, problems := s.snapshot()
	for _, p := range problems {
		t.Error(p)
	}
}

// Retention does not wait on a sink. A pass deletes what it asked for while a
// sink is behind; the sink is then moved past the deleted records once, which
// is counted in its row and on its lag and logged once at WARN with the range;
// it resumes after the marker; and it holds the control stream's
// operator.audit.retained record that names the range.
func TestARetentionPassOvertakingASinkIsSkippedCountedAndExplained(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	s := newFakeSink()
	var down atomic.Bool
	down.Store(true)
	refused := make(chan struct{}, 1)
	s.refuse = func(batch []ext.AuditRecord) error {
		if batch[0].Stream == "proxy:p1" && down.Load() {
			select {
			case refused <- struct{}{}:
			default:
			}
			return errors.New("the SIEM is down")
		}
		return nil
	}
	log := &lockedLog{}
	consumer := store.SinkConsumer("test/sink")
	f := sinkFeed(t, st, s, "test/sink", 2, slog.New(slog.NewJSONHandler(log, nil)))
	f.capWait = 20 * time.Millisecond
	in := newIngester(t, st, f.Track)
	ingest(t, in, tenantA, "p1", "", many(6, "s")...)
	start(t, f)
	<-refused

	emitter, err := audit.NewEmitter(in, audit.WithRetentionSigner(retentionKeys(t, st)))
	if err != nil {
		t.Fatal(err)
	}
	pass, err := emitter.RetainPrefix(context.Background(), tenantA, audit.PrefixPass{Stream: "proxy:p1", ThroughSeq: 3},
		audit.OperatorActor{Principal: "tok-admin"})
	if err != nil || pass.Deleted != 3 || pass.HeldByArchive {
		t.Fatalf("the pass behind a sink = %+v, %v; want 3 deleted and nothing held", pass, err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		pos, _ := st.Feed().Position(context.Background(), tenantA, consumer, "proxy:p1")
		if pos.SkippedRecords == 3 {
			if pos.SkippedBehind != 3 || pos.ConfirmedSeq != 3 || pos.ConfirmedHash != pass.ThroughHash {
				t.Errorf("after the skip the row is %+v", pos)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sink was never moved past the pass: %+v", pos)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lag, err := f.Lag(context.Background(), tenantA); err != nil || lag.SkippedByRetention != 3 || !lag.Failing {
		t.Errorf("behind and failing, the lag is %+v, %v; want 3 skipped", lag, err)
	}

	down.Store(false)
	sinkCaughtUp(t, st, consumer, 10*time.Second, tenantA)
	pos, _ := st.Feed().Position(context.Background(), tenantA, consumer, "proxy:p1")
	if pos.SkippedRecords != 3 || pos.SkippedBehind != 0 {
		t.Errorf("caught up, the row is %+v; want 3 skipped in all and none since", pos)
	}
	if lag, _ := f.Lag(context.Background(), tenantA); lag.SkippedByRetention != 0 {
		t.Errorf("caught up, the lag is %+v", lag)
	}
	lines := log.events("audit_sink_skipped_by_retention")
	if len(lines) != 1 {
		t.Fatalf("%d skip lines, want one", len(lines))
	}
	if l := lines[0]; l["level"] != "WARN" || l["provider"] != "test/sink" || l["tenant"] != string(tenantA) ||
		l["stream"] != "proxy:p1" || l["from_seq"] != float64(1) || l["through_seq"] != float64(3) || l["records"] != float64(3) {
		t.Errorf("the skip line is %v", l)
	}

	held, problems := s.snapshot()
	for _, p := range problems {
		t.Error(p)
	}
	p1 := held[key{ext.Tenant(tenantA), "proxy:p1"}]
	if len(p1) != 3 || p1[0].Sequence != 4 || p1[0].PrevHash != pass.ThroughHash {
		t.Fatalf("proxy:p1 arrived as %+v; want 4..6, the first chaining onto the marker", p1)
	}
	var explained bool
	for _, r := range held[key{ext.Tenant(tenantA), audit.StreamControl}] {
		explained = explained || (r.Attributes["event"] == audit.EventAuditRetained &&
			r.Attributes["operator.stream"] == "proxy:p1" && r.Attributes["operator.through_hash"] == pass.ThroughHash)
	}
	if !explained {
		t.Errorf("the sink holds no record explaining the gap: %+v", held[key{ext.Tenant(tenantA), audit.StreamControl}])
	}
}

// A sink registered after a pass begins each stream at its chain start, after
// the marker, and counts nothing as skipped: those records were never its.
func TestASinkRegisteredAfterAPassBeginsAtTheMarkerAndSkipsNothing(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st, nil)
	ingest(t, in, tenantA, "p1", "", many(5, "s")...)
	emitter, err := audit.NewEmitter(in, audit.WithRetentionSigner(retentionKeys(t, st)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emitter.RetainPrefix(context.Background(), tenantA, audit.PrefixPass{Stream: "proxy:p1", ThroughSeq: 2},
		audit.OperatorActor{Principal: "tok-admin"}); err != nil {
		t.Fatal(err)
	}
	s := newFakeSink()
	f := sinkFeed(t, st, s, "test/late", 2, nil)
	f.Track(tenantA)
	start(t, f)
	sinkCaughtUp(t, st, store.SinkConsumer("test/late"), 10*time.Second, tenantA)
	held, _ := s.snapshot()
	if p1 := held[key{ext.Tenant(tenantA), "proxy:p1"}]; len(p1) != 3 || p1[0].Sequence != 3 {
		t.Errorf("proxy:p1 arrived as %+v; want 3..5", p1)
	}
	if pos, _ := st.Feed().Position(context.Background(), tenantA, store.SinkConsumer("test/late"), "proxy:p1"); pos.SkippedRecords != 0 {
		t.Errorf("a sink registered after the pass counts %d skipped", pos.SkippedRecords)
	}
}

// New refuses a feed that names both an archive and a consumer, or neither, a
// consumer under the archive's key, and a consumer with no bound in flight.
func TestNewRefusesAConsumerItCannotDeliverTo(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ok := Options{
		Store: st, BatchRecords: 1, AttemptTimeout: time.Second, IdleInterval: time.Second, BackoffCap: time.Second,
		InFlight: 1,
		Consumer: &Consumer{Key: store.SinkConsumer("p"), Point: ext.PointAuditSink, Provider: "p",
			Deliver: func(context.Context, []ext.AuditRecord) error { return nil }},
	}
	if _, err := New(ok); err != nil {
		t.Fatalf("a well-formed consumer: %v", err)
	}
	for name, mutate := range map[string]func(*Options){
		"both":            func(o *Options) { o.Archive, o.CaptureBatchBytes = newFakeArchive(), 1 },
		"neither":         func(o *Options) { o.Consumer = nil },
		"the archive key": func(o *Options) { c := *o.Consumer; c.Key = store.ArchiveConsumer; o.Consumer = &c },
		"no provider":     func(o *Options) { c := *o.Consumer; c.Provider = ""; o.Consumer = &c },
		"no delivery":     func(o *Options) { c := *o.Consumer; c.Deliver = nil; o.Consumer = &c },
		"nothing in flight": func(o *Options) {
			o.InFlight = 0
		},
	} {
		o := ok
		mutate(&o)
		if _, err := New(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := New(Options{
		Store: st, Archive: newFakeArchive(), BatchRecords: 1, CaptureBatchBytes: 1,
		AttemptTimeout: time.Second, IdleInterval: time.Second, BackoffCap: time.Second, InFlight: 2,
	}); err == nil {
		t.Error("the archive's feed accepted a bound in flight")
	}
}
