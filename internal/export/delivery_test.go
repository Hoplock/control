// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package export_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/archive"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/export"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

const (
	tenantA store.Tenant = "tenant-a"
	tenantB store.Tenant = "tenant-b"
)

type key struct {
	tenant ext.Tenant
	stream string
}

// sink is a host's sink as a test registers it: it records what it was handed
// and checks what ext.AuditSink's doc comment promises on every call — one
// tenant's stream per batch, contiguous and ascending, never past what it
// holds plus one, never two batches of one stream in flight.
type sink struct {
	mu sync.Mutex
	// refuse, when set, decides a call's answer; it is called without the
	// lock, so it may block.
	refuse func(batch []ext.AuditRecord) error

	held     map[key]uint64
	records  map[key][]ext.AuditRecord
	offered  map[key][]uint64
	busy     map[key]bool
	problems []string
}

func newSink() *sink {
	return &sink{held: map[key]uint64{}, records: map[key][]ext.AuditRecord{}, offered: map[key][]uint64{}, busy: map[key]bool{}}
}

func (s *sink) Export(_ context.Context, batch []ext.AuditRecord) error {
	k := key{batch[0].Tenant, batch[0].Stream}
	s.mu.Lock()
	if s.busy[k] {
		s.problems = append(s.problems, fmt.Sprintf("%v: two batches of one stream in flight", k))
	}
	s.busy[k] = true
	for i, r := range batch {
		s.offered[k] = append(s.offered[k], r.Sequence)
		if r.Tenant != k.tenant || r.Stream != k.stream || (i > 0 && r.Sequence != batch[i-1].Sequence+1) {
			s.problems = append(s.problems, fmt.Sprintf("%v: batch not one contiguous stream at %d", k, r.Sequence))
		}
	}
	refuse := s.refuse
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy[k] = false
		s.mu.Unlock()
	}()
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

// inFlight reports whether a call is still inside Export.
func (s *sink) inFlight() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, busy := range s.busy {
		if busy {
			return true
		}
	}
	return false
}

func (s *sink) snapshot() (map[key][]ext.AuditRecord, map[key][]uint64, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, offered := map[key][]ext.AuditRecord{}, map[key][]uint64{}
	for k, v := range s.records {
		recs[k] = append([]ext.AuditRecord(nil), v...)
	}
	for k, v := range s.offered {
		offered[k] = append([]uint64(nil), v...)
	}
	return recs, offered, append([]string(nil), s.problems...)
}

// archiveOf is an archive registered beside the sinks, to compare what each
// is handed.
type archiveOf struct{ *sink }

func (a archiveOf) Archive(ctx context.Context, batch []ext.AuditRecord) error {
	return a.Export(ctx, batch)
}
func (archiveOf) ArchiveCaptures(context.Context, []ext.Capture) error { return nil }
func (archiveOf) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

func bound(provider string, s ext.AuditSink) ext.Bound[ext.AuditSink] {
	return ext.Bound[ext.AuditSink]{Registration: ext.Registration{Provider: provider}, Impl: s}
}

// newDelivery is delivery to the sinks with the bounds a test wants.
func newDelivery(t *testing.T, st *store.Store, node string, sinks ...ext.Bound[ext.AuditSink]) *export.Delivery {
	t.Helper()
	d, err := export.New(export.Options{
		Store: st, Sinks: sinks, BatchRecords: 4, AttemptTimeout: 2 * time.Second,
		IdleInterval: 20 * time.Millisecond, BackoffCap: time.Second, MaxInFlight: 4,
		Node: node, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func start(t *testing.T, d *export.Delivery) {
	t.Helper()
	d.Start(context.Background())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.Stop(ctx); err != nil {
			t.Errorf("stop: %v", err)
		}
	})
}

func newIngester(t *testing.T, st *store.Store, track func(store.Tenant)) *audit.Ingester {
	t.Helper()
	in, err := audit.New(audit.Options{Store: st, Track: track})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

var recordCounter atomic.Int64

func many(n int, session string) []contract.LogRecord {
	out := make([]contract.LogRecord, n)
	for i := range out {
		out[i] = contract.LogRecord{
			RecordID:  fmt.Sprintf("rec-%d", recordCounter.Add(1)),
			SessionID: session,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Kind:      "command",
			Severity:  contract.SeverityInfo,
			Message:   "a command ran",
			Subject:   "alice@example.invalid",
			Target:    "db-1.example.invalid",
		}
	}
	return out
}

// ingest writes records into a stream: a proxy's, or the control stream's.
func ingest(t *testing.T, in *audit.Ingester, tenant store.Tenant, proxy, stream string, recs ...contract.LogRecord) {
	t.Helper()
	if _, err := in.Ingest(context.Background(), audit.Submission{Tenant: tenant, ProxyID: proxy, Stream: stream, Records: recs}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

// caughtUp waits until the consumer's position in every stream of the tenants
// is the stream's head.
func caughtUp(t *testing.T, st *store.Store, consumer string, within time.Duration, tenants ...store.Tenant) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		behind := ""
		for _, tenant := range tenants {
			streams, err := st.Feed().Streams(context.Background(), tenant, consumer)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range streams {
				if s.Behind() {
					behind = fmt.Sprintf("%s/%s at %d of %d", tenant, s.Stream, s.Confirmed, s.Head)
				}
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

// holdsEverything asserts the sink holds every stored record of the tenants'
// streams exactly once, in order, each carrying what its link covers, and that
// ext.ChainLink reproduces its hash.
func holdsEverything(t *testing.T, st *store.Store, s *sink, tenants ...store.Tenant) {
	t.Helper()
	held, _, problems := s.snapshot()
	for _, p := range problems {
		t.Error(p)
	}
	for _, tenant := range tenants {
		streams, err := st.Audit().Streams(context.Background(), tenant)
		if err != nil {
			t.Fatal(err)
		}
		for _, stream := range streams {
			stored, err := st.Audit().Chain(context.Background(), tenant, stream, 0, 10000)
			if err != nil {
				t.Fatal(err)
			}
			got := held[key{ext.Tenant(tenant), stream}]
			if len(got) != len(stored) {
				t.Errorf("%s/%s: the sink holds %d records, the store %d", tenant, stream, len(got), len(stored))
				continue
			}
			for i, r := range got {
				want := stored[i]
				if r.RecordID != want.RecordID || r.Sequence != uint64(want.ChainSeq) || r.ChainHash != want.Hash ||
					r.PrevHash != want.PrevHash || r.Body != want.Body || r.Stream != stream || r.Tenant != ext.Tenant(tenant) {
					t.Errorf("%s/%s at %d: the sink holds %+v, the store %+v", tenant, stream, i, r, want)
				}
				if link := ext.ChainLink(r.Tenant, r.Stream, r.Sequence, r.PrevHash, r.Body); link != r.ChainHash {
					t.Errorf("%s/%s at %d: ext.ChainLink gives %s, the record says %s", tenant, stream, r.Sequence, link, r.ChainHash)
				}
			}
		}
	}
}

// Every record committed in two tenants, across the control stream and two
// proxy streams, reaches each of two sinks exactly once per stream in
// ascending sequence while they confirm, and each is the record the archive
// receives: its stream, its predecessor's hash and its hashed body.
func TestEveryRecordReachesEverySinkOncePerStreamInOrder(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	one, two, arch := newSink(), newSink(), archiveOf{newSink()}
	d := newDelivery(t, st, "node-1", bound("example.com/one", one), bound("example.com/two", two))
	feed, err := archive.New(archive.Options{
		Store: st, Archive: arch, Provider: "example.com/archive", BatchRecords: 3, CaptureBatchBytes: 1 << 20,
		AttemptTimeout: 2 * time.Second, IdleInterval: 20 * time.Millisecond, BackoffCap: time.Second,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	in := newIngester(t, st, func(tenant store.Tenant) { d.Track(tenant); feed.Track(tenant) })
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		ingest(t, in, tenant, "p1", "", many(5, "s1")...)
		ingest(t, in, tenant, "p2", "", many(3, "s2")...)
		ingest(t, in, tenant, "", audit.StreamControl, many(2, "")...)
	}
	start(t, d)
	feed.Start(context.Background())
	t.Cleanup(func() { _ = feed.Stop(context.Background()) })
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		ingest(t, in, tenant, "p1", "", many(6, "s1")...)
		ingest(t, in, tenant, "", audit.StreamControl, many(1, "")...)
	}
	for _, consumer := range []string{store.SinkConsumer("example.com/one"), store.SinkConsumer("example.com/two"), store.ArchiveConsumer} {
		caughtUp(t, st, consumer, 10*time.Second, tenantA, tenantB)
	}
	holdsEverything(t, st, one, tenantA, tenantB)
	holdsEverything(t, st, two, tenantA, tenantB)

	archived, _, _ := arch.snapshot()
	for _, s := range []*sink{one, two} {
		held, offered, _ := s.snapshot()
		if len(held) != 6 {
			t.Errorf("a sink holds %d streams, want three in each of two tenants", len(held))
		}
		for k, recs := range held {
			if len(offered[k]) != len(recs) {
				t.Errorf("%v was offered %d records for %d held, by a sink that never failed", k, len(offered[k]), len(recs))
			}
			if fmt.Sprint(recs) != fmt.Sprint(archived[k]) {
				t.Errorf("%v: a sink holds other records than the archive", k)
			}
		}
	}
}

// One sink that never returns delays neither another sink's delivery nor
// ingest: a batch ingest with it registered completes within the bound it
// meets with none.
func TestASinkThatNeverReturnsDelaysNoOtherSinkAndNoIngest(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st, nil)
	var baseline time.Duration
	for range 3 {
		began := time.Now()
		ingest(t, in, tenantA, "p0", "", many(50, "s")...)
		baseline = max(baseline, time.Since(began))
	}

	forever := make(chan struct{})
	t.Cleanup(func() { close(forever) })
	hung, healthy := newSink(), newSink()
	called := make(chan struct{}, 1)
	hung.refuse = func([]ext.AuditRecord) error {
		select {
		case called <- struct{}{}:
		default:
		}
		<-forever // ignores its deadline, as a broken SIEM client does
		return errors.New("never")
	}
	d, err := export.New(export.Options{
		Store: st, Sinks: []ext.Bound[ext.AuditSink]{bound("example.com/hung", hung), bound("example.com/healthy", healthy)},
		BatchRecords: 50, AttemptTimeout: 100 * time.Millisecond, IdleInterval: 20 * time.Millisecond,
		BackoffCap: time.Second, MaxInFlight: 2, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	in = newIngester(t, st, d.Track)
	ingest(t, in, tenantA, "p1", "", many(3, "s")...)
	start(t, d)
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("the hung sink was never called")
	}
	for range 3 {
		began := time.Now()
		ingest(t, in, tenantA, "p1", "", many(50, "s")...)
		if took := time.Since(began); took > 4*baseline+time.Second {
			t.Errorf("a batch ingest took %s with a hung sink registered, and at most %s with none", took, baseline)
		}
	}
	caughtUp(t, st, store.SinkConsumer("example.com/healthy"), 10*time.Second, tenantA)
	if lag, ok, err := d.Lag(context.Background(), tenantA, "example.com/hung"); err != nil || !ok || lag.StreamsBehind == 0 {
		t.Errorf("the hung sink's lag = %+v %v %v; want it behind", lag, ok, err)
	}
}

// A sink that fails only one tenant's batches does not delay the other
// tenant's delivery to it.
func TestASinkFailingOneTenantDelaysNoOtherTenant(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	s := newSink()
	s.refuse = func(batch []ext.AuditRecord) error {
		if batch[0].Tenant == ext.Tenant(tenantA) {
			return ext.Errorf(ext.PointAuditSink, "example.com/siem", "export", ext.KindDenied, "tenant A's index is read-only")
		}
		return nil
	}
	d := newDelivery(t, st, "node-1", bound("example.com/siem", s))
	in := newIngester(t, st, d.Track)
	start(t, d)
	for range 3 {
		ingest(t, in, tenantA, "p1", "", many(4, "s")...)
		ingest(t, in, tenantB, "p1", "", many(4, "s")...)
	}
	caughtUp(t, st, store.SinkConsumer("example.com/siem"), 10*time.Second, tenantB)
	if lag, _, err := d.Lag(context.Background(), tenantA, "example.com/siem"); err != nil || lag.StreamsBehind != 1 || !lag.Failing {
		t.Errorf("tenant A's lag = %+v, %v; want it behind and failing", lag, err)
	}
	if lag, _, _ := d.Lag(context.Background(), tenantB, "example.com/siem"); lag.StreamsBehind != 0 || lag.Failing {
		t.Errorf("tenant B's lag = %+v; want it caught up", lag)
	}
}

// Concurrent ingest and delivery over many interleaved commits on one stream
// never skip a sequence.
func TestConcurrentIngestAndDeliveryNeverSkipASequence(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	s := newSink()
	d, err := export.New(export.Options{
		Store: st, Sinks: []ext.Bound[ext.AuditSink]{bound("example.com/siem", s)},
		BatchRecords: 7, AttemptTimeout: 2 * time.Second, IdleInterval: time.Millisecond,
		BackoffCap: time.Second, MaxInFlight: 4, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	in := newIngester(t, st, d.Track)
	start(t, d)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				ingest(t, in, tenantA, "p1", "", many(3, "s")...)
			}
		})
	}
	wg.Wait()
	caughtUp(t, st, store.SinkConsumer("example.com/siem"), 20*time.Second, tenantA)
	holdsEverything(t, st, s, tenantA)
}

// Two deliveries over one store, standing in for two nodes, deliver every
// record at least once to each sink, and never move a position backwards.
func TestTwoNodesDeliverEverythingToEachSinkAndNeverMoveAPositionBack(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	one, two := newSink(), newSink()
	nodeA := newDelivery(t, st, "node-a", bound("example.com/one", one), bound("example.com/two", two))
	nodeB := newDelivery(t, st, "node-b", bound("example.com/one", one), bound("example.com/two", two))
	inA, inB := newIngester(t, st, nodeA.Track), newIngester(t, st, nodeB.Track)
	start(t, nodeA)
	start(t, nodeB)

	consumers := []string{store.SinkConsumer("example.com/one"), store.SinkConsumer("example.com/two")}
	stop := make(chan struct{})
	watched := make(chan []string)
	go func() {
		var backwards []string
		last := map[string]int64{}
		for {
			select {
			case <-stop:
				watched <- backwards
				return
			default:
			}
			for _, c := range consumers {
				for _, s := range []string{"proxy:p1", "proxy:p2", "proxy:p3"} {
					pos, err := st.Feed().Position(context.Background(), tenantA, c, s)
					if err != nil {
						continue
					}
					if pos.ConfirmedSeq < last[c+s] {
						backwards = append(backwards, fmt.Sprintf("%s %s from %d to %d", c, s, last[c+s], pos.ConfirmedSeq))
					}
					last[c+s] = pos.ConfirmedSeq
				}
			}
		}
	}()
	for i := range 30 {
		in := inA
		if i%2 == 1 {
			in = inB
		}
		ingest(t, in, tenantA, fmt.Sprintf("p%d", 1+i%3), "", many(3, "s")...)
	}
	for _, c := range consumers {
		caughtUp(t, st, c, 20*time.Second, tenantA)
	}
	close(stop)
	if backwards := <-watched; len(backwards) > 0 {
		t.Errorf("a position moved backwards: %v", backwards)
	}
	holdsEverything(t, st, one, tenantA)
	holdsEverything(t, st, two, tenantA)
}

// Stopping delivery mid-batch confirms nothing, and the next delivery — a new
// process, under a new name — offers the batch again at once.
func TestADeliveryStoppedMidBatchOffersItAgainOnRestart(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	s := newSink()
	inFlight := make(chan struct{}, 1)
	ended := make(chan struct{})
	returned := make(chan struct{})
	var once sync.Once
	s.refuse = func([]ext.AuditRecord) error {
		var block bool
		once.Do(func() { block = true })
		if !block {
			return nil
		}
		inFlight <- struct{}{}
		<-ended // the call dies with the process that made it
		defer close(returned)
		return errors.New("the process ended")
	}
	first, err := export.New(export.Options{
		Store: st, Sinks: []ext.Bound[ext.AuditSink]{bound("example.com/siem", s)},
		BatchRecords: 4, AttemptTimeout: time.Hour, IdleInterval: 20 * time.Millisecond,
		BackoffCap: time.Second, MaxInFlight: 1, Node: "node-1", Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	in := newIngester(t, st, first.Track)
	ingest(t, in, tenantA, "p1", "", many(3, "s")...)
	first.Start(context.Background())
	<-inFlight
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := first.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	pos, err := st.Feed().Position(context.Background(), tenantA, store.SinkConsumer("example.com/siem"), "proxy:p1")
	if err != nil || pos.ConfirmedSeq != 0 || pos.LeaseUntil.After(time.Now().Add(time.Second)) {
		t.Fatalf("after a stop mid-batch the position is %+v, %v; want nothing confirmed and the lease given back", pos, err)
	}
	close(ended)
	<-returned
	for s.inFlight() {
		time.Sleep(time.Millisecond)
	}

	second := newDelivery(t, st, "node-2", bound("example.com/siem", s))
	second.Track(tenantA)
	start(t, second)
	caughtUp(t, st, store.SinkConsumer("example.com/siem"), 5*time.Second, tenantA)
	_, offered, _ := s.snapshot()
	if got := fmt.Sprint(offered[key{ext.Tenant(tenantA), "proxy:p1"}]); got != "[1 2 3 1 2 3]" {
		t.Errorf("offered %s; want the interrupted batch offered again", got)
	}
	holdsEverything(t, st, s, tenantA)
}

// Positions are per sink: two sinks hold different positions; a sink
// unregistered and registered again under its provider resumes from its own;
// a provider registering under a new name is a new sink, and starts at the
// chain's start.
func TestPositionsArePerSinkAndNamedByTheProvider(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	fast, slow := newSink(), newSink()
	var slowDown atomic.Bool
	slowDown.Store(true)
	slow.refuse = func([]ext.AuditRecord) error {
		if slowDown.Load() {
			return errors.New("slow is down")
		}
		return nil
	}
	d := newDelivery(t, st, "node-1", bound("example.com/fast", fast), bound("example.com/slow", slow))
	in := newIngester(t, st, d.Track)
	ingest(t, in, tenantA, "p1", "", many(6, "s")...)
	d.Start(context.Background())
	caughtUp(t, st, store.SinkConsumer("example.com/fast"), 10*time.Second, tenantA)
	fastPos, _ := st.Feed().Position(context.Background(), tenantA, store.SinkConsumer("example.com/fast"), "proxy:p1")
	slowPos, _ := st.Feed().Position(context.Background(), tenantA, store.SinkConsumer("example.com/slow"), "proxy:p1")
	if fastPos.ConfirmedSeq != 6 || slowPos.ConfirmedSeq != 0 {
		t.Errorf("fast at %d, slow at %d; want 6 and 0", fastPos.ConfirmedSeq, slowPos.ConfirmedSeq)
	}
	fastLag, _, _ := d.Lag(context.Background(), tenantA, "example.com/fast")
	slowLag, _, _ := d.Lag(context.Background(), tenantA, "example.com/slow")
	if fastLag.StreamsBehind != 0 || fastLag.Failing || slowLag.StreamsBehind != 1 || !slowLag.Failing {
		t.Errorf("fast's lag %+v, slow's %+v; want each its own", fastLag, slowLag)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	// The next start registers fast alone; then fast and slow again, and
	// fast under a new name.
	in = newIngester(t, st, nil)
	ingest(t, in, tenantA, "p1", "", many(2, "s")...)
	alone := newDelivery(t, st, "node-2", bound("example.com/fast", fast))
	alone.Track(tenantA)
	start(t, alone)
	caughtUp(t, st, store.SinkConsumer("example.com/fast"), 10*time.Second, tenantA)
	// Fast never failed, so each record was offered once: the second start
	// resumed after 6 rather than at the chain's start.
	_, offered, _ := fast.snapshot()
	if got := fmt.Sprint(offered[key{ext.Tenant(tenantA), "proxy:p1"}]); got != "[1 2 3 4 5 6 7 8]" {
		t.Errorf("fast was offered %s over two starts; want each of 1..8 once", got)
	}

	slowDown.Store(false)
	renamed := newSink()
	again := newDelivery(t, st, "node-3", bound("example.com/slow", slow), bound("example.com/fast-v2", renamed))
	again.Track(tenantA)
	start(t, again)
	caughtUp(t, st, store.SinkConsumer("example.com/slow"), 10*time.Second, tenantA)
	caughtUp(t, st, store.SinkConsumer("example.com/fast-v2"), 10*time.Second, tenantA)
	holdsEverything(t, st, slow, tenantA)
	held, _, _ := renamed.snapshot()
	if p1 := held[key{ext.Tenant(tenantA), "proxy:p1"}]; len(p1) != 8 || p1[0].Sequence != 1 {
		t.Errorf("a provider under a new name holds %d records from %v; want all 8 from the chain's start", len(p1), p1)
	}
}

// New refuses delivery with no sink, and two sinks under one provider, which
// would share one position.
func TestNewRefusesNoSinkAndASharedName(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	o := export.Options{
		Store: st, BatchRecords: 1, AttemptTimeout: time.Second, IdleInterval: time.Second, BackoffCap: time.Second, MaxInFlight: 1,
	}
	if _, err := export.New(o); err == nil {
		t.Error("delivery to no sink was built")
	}
	o.Sinks = []ext.Bound[ext.AuditSink]{bound("example.com/a", newSink()), bound("example.com/a", newSink())}
	if _, err := export.New(o); err == nil {
		t.Error("two sinks under one provider were built")
	}
	if d, ok, err := newDelivery(t, st, "n", bound("example.com/a", newSink())).Lag(context.Background(), tenantA, "example.com/b"); ok || err != nil {
		t.Errorf("the lag of a provider not registered = %+v %v %v", d, ok, err)
	}
}
