// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/extdefault"
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

// fakeArchive records what it was handed and checks, on every call, the
// promises ext.ArchiveStore's doc comment makes: one tenant's stream per batch,
// contiguous and ascending, never past what it already holds plus one, and a
// capture only after its record.
type fakeArchive struct {
	mu sync.Mutex
	// refuse, when set, decides a call's answer before the batch is taken.
	refuse         func(call int, batch []ext.AuditRecord) error
	refuseCaptures func(call int, batch []ext.Capture) error
	calls          int
	captureCalls   int

	held     map[key]uint64 // the highest sequence held, contiguous from the first
	first    map[key]uint64 // the first sequence ever held
	offered  map[key][]uint64
	records  map[key][]ext.AuditRecord // held, once each, in order
	captures map[string]ext.Capture    // by tenant/record id, with Body read into bodies
	bodies   map[string][]byte
	problems []string
}

func newFakeArchive() *fakeArchive {
	return &fakeArchive{
		held: map[key]uint64{}, first: map[key]uint64{}, offered: map[key][]uint64{},
		records: map[key][]ext.AuditRecord{}, captures: map[string]ext.Capture{}, bodies: map[string][]byte{},
	}
}

func (a *fakeArchive) Archive(_ context.Context, batch []ext.AuditRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if len(batch) == 0 {
		a.problems = append(a.problems, "an empty batch")
		return nil
	}
	k := key{batch[0].Tenant, batch[0].Stream}
	for i, r := range batch {
		if r.Tenant != k.tenant || r.Stream != k.stream {
			a.problems = append(a.problems, fmt.Sprintf("a batch for %v carried %s/%s", k, r.Tenant, r.Stream))
		}
		if i > 0 && r.Sequence != batch[i-1].Sequence+1 {
			a.problems = append(a.problems, fmt.Sprintf("%v: a batch jumped from %d to %d", k, batch[i-1].Sequence, r.Sequence))
		}
		a.offered[k] = append(a.offered[k], r.Sequence)
	}
	if held, ok := a.held[k]; ok && batch[0].Sequence > held+1 {
		a.problems = append(a.problems, fmt.Sprintf("%v: holding through %d, offered from %d: a record was skipped", k, held, batch[0].Sequence))
	}
	if a.refuse != nil {
		if err := a.refuse(a.calls, batch); err != nil {
			return err
		}
	}
	if _, ok := a.held[k]; !ok {
		a.first[k] = batch[0].Sequence
		a.held[k] = batch[0].Sequence - 1
	}
	for _, r := range batch {
		if r.Sequence == a.held[k]+1 {
			a.records[k] = append(a.records[k], r)
			a.held[k] = r.Sequence
		}
	}
	return nil
}

func (a *fakeArchive) ArchiveCaptures(_ context.Context, batch []ext.Capture) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.captureCalls++
	for _, c := range batch {
		found := false
		for k, recs := range a.records {
			if k.tenant != c.Tenant {
				continue
			}
			for _, r := range recs {
				found = found || r.RecordID == c.RecordID
			}
		}
		if !found {
			a.problems = append(a.problems, fmt.Sprintf("capture %s/%s offered before its record", c.Tenant, c.RecordID))
		}
	}
	if a.refuseCaptures != nil {
		if err := a.refuseCaptures(a.captureCalls, batch); err != nil {
			return err
		}
	}
	for _, c := range batch {
		b, err := io.ReadAll(c.Body)
		if err != nil {
			return err
		}
		id := string(c.Tenant) + "/" + c.RecordID
		a.captures[id], a.bodies[id] = c, b
	}
	return nil
}

func (a *fakeArchive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

func (a *fakeArchive) snapshot() (map[key][]ext.AuditRecord, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[key][]ext.AuditRecord, len(a.records))
	for k, v := range a.records {
		out[k] = append([]ext.AuditRecord(nil), v...)
	}
	return out, append([]string(nil), a.problems...)
}

// testFeed is a feed with the bounds a test wants: small batches, a short idle
// interval and a short first backoff.
func testFeed(t *testing.T, st *store.Store, a ext.ArchiveStore, node string) *Feed {
	t.Helper()
	f, err := New(Options{
		Store: st, Archive: a, Provider: "test/archive",
		BatchRecords: 4, CaptureBatchBytes: 64,
		AttemptTimeout: 2 * time.Second, IdleInterval: 20 * time.Millisecond, BackoffCap: time.Second,
		Node: node, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.base = 10 * time.Millisecond
	return f
}

func start(t *testing.T, f *Feed) {
	t.Helper()
	f.Start(context.Background())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.Stop(ctx); err != nil {
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

func logRecord(session string) contract.LogRecord {
	return contract.LogRecord{
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

// captureRecord is a session capture, recorded at an instant.
func captureRecord(session string, at time.Time, bytes string) contract.LogRecord {
	r := logRecord(session)
	r.Kind = "stream"
	r.Timestamp = at.UTC().Format(time.RFC3339Nano)
	r.Payload = base64.StdEncoding.EncodeToString([]byte(bytes))
	return r
}

// ingest writes n records into a stream: a proxy's, or the control stream's.
func ingest(t *testing.T, in *audit.Ingester, tenant store.Tenant, proxy, stream string, recs ...contract.LogRecord) {
	t.Helper()
	if _, err := in.Ingest(context.Background(), audit.Submission{Tenant: tenant, ProxyID: proxy, Stream: stream, Records: recs}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

func many(n int, session string) []contract.LogRecord {
	out := make([]contract.LogRecord, n)
	for i := range out {
		out[i] = logRecord(session)
	}
	return out
}

// caughtUp waits until the archive's position in every stream of the tenants
// is the stream's head.
func caughtUp(t *testing.T, st *store.Store, within time.Duration, tenants ...store.Tenant) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		behind := ""
		for _, tenant := range tenants {
			streams, err := st.Feed().Streams(context.Background(), tenant, store.ArchiveConsumer)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range streams {
				if s.Behind() || s.CapturesReady > 0 {
					behind = fmt.Sprintf("%s/%s at %d of %d, %d captures", tenant, s.Stream, s.Confirmed, s.Head, s.CapturesReady)
				}
			}
		}
		if behind == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the archive did not catch up within %s: %s", within, behind)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// holdsEverything asserts the archive holds every stored record of the tenants'
// streams exactly once, in order, from the stream's chain start, each one
// carrying what its link covers — and that ext.ChainLink reproduces its hash.
func holdsEverything(t *testing.T, st *store.Store, a *fakeArchive, tenants ...store.Tenant) {
	t.Helper()
	held, problems := a.snapshot()
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
				t.Errorf("%s/%s: the archive holds %d records, the store %d", tenant, stream, len(got), len(stored))
				continue
			}
			for i, r := range got {
				want := stored[i]
				if r.RecordID != want.RecordID || r.Sequence != uint64(want.ChainSeq) || r.ChainHash != want.Hash ||
					r.PrevHash != want.PrevHash || r.Body != want.Body || r.Stream != stream || r.Tenant != ext.Tenant(tenant) {
					t.Errorf("%s/%s at %d: the archive holds %+v, the store %+v", tenant, stream, i, r, want)
				}
				if link := ext.ChainLink(r.Tenant, r.Stream, r.Sequence, r.PrevHash, r.Body); link != r.ChainHash {
					t.Errorf("%s/%s at %d: ext.ChainLink gives %s, the record says %s", tenant, stream, r.Sequence, link, r.ChainHash)
				}
				if i > 0 && r.PrevHash != got[i-1].ChainHash {
					t.Errorf("%s/%s at %d does not chain onto the record before it", tenant, stream, r.Sequence)
				}
			}
		}
		result, err := audit.NewVerifier(st, audit.WithRetentionKeys(retentionKeys(t, st)), audit.WithCaptureVerification(true)).
			VerifyTenant(context.Background(), tenant)
		if err != nil || !result.OK() {
			t.Errorf("audit-verify on %s: %v %+v", tenant, err, result.Streams)
		}
	}
}

// Every record committed in two tenants, across the control stream and two
// proxy streams, reaches the archive exactly once per stream in ascending
// sequence, each with its stream, its predecessor's hash and its hashed body,
// and never in another tenant's batch.
func TestEveryCommittedRecordReachesTheArchiveOncePerStreamInOrder(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newFakeArchive()
	f := testFeed(t, st, a, "node-1")
	in := newIngester(t, st, f.Track)
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		ingest(t, in, tenant, "p1", "", many(5, "s1")...)
		ingest(t, in, tenant, "p2", "", many(3, "s2")...)
		ingest(t, in, tenant, "", audit.StreamControl, many(2, "")...)
	}
	start(t, f)
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		ingest(t, in, tenant, "p1", "", many(6, "s1")...)
		ingest(t, in, tenant, "", audit.StreamControl, many(1, "")...)
	}
	caughtUp(t, st, 10*time.Second, tenantA, tenantB)
	holdsEverything(t, st, a, tenantA, tenantB)

	a.mu.Lock()
	defer a.mu.Unlock()
	for k, offered := range a.offered {
		if len(offered) != len(a.records[k]) {
			t.Errorf("%v was offered %d records for %d held, with an archive that never failed", k, len(offered), len(a.records[k]))
		}
	}
	if len(a.records) != 6 {
		t.Errorf("the archive holds %d streams, want three in each of two tenants", len(a.records))
	}
}

// An archive that fails three batches and then accepts is offered the same
// records again, its position moves only on success, and nothing is skipped
// or reordered.
func TestAFailedBatchIsOfferedAgainAndConfirmsNothing(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newFakeArchive()
	var positions []int64
	a.refuse = func(call int, batch []ext.AuditRecord) error {
		pos, err := st.Feed().Position(context.Background(), tenantA, store.ArchiveConsumer, batch[0].Stream)
		if err == nil {
			positions = append(positions, pos.ConfirmedSeq)
		}
		if call <= 3 {
			return ext.Errorf(ext.PointArchiveStore, "test/archive", "archive", ext.KindUnavailable, "down")
		}
		return nil
	}
	f := testFeed(t, st, a, "node-1")
	in := newIngester(t, st, f.Track)
	ingest(t, in, tenantA, "p1", "", many(7, "s")...)
	start(t, f)
	caughtUp(t, st, 10*time.Second, tenantA)
	holdsEverything(t, st, a, tenantA)

	a.mu.Lock()
	defer a.mu.Unlock()
	offered := a.offered[key{ext.Tenant(tenantA), "proxy:p1"}]
	if fmt.Sprint(offered[:16]) != fmt.Sprint([]uint64{1, 2, 3, 4, 1, 2, 3, 4, 1, 2, 3, 4, 1, 2, 3, 4}) {
		t.Errorf("offered %v; want the first batch four times", offered)
	}
	if fmt.Sprint(positions[:4]) != "[0 0 0 0]" {
		t.Errorf("the position at each attempt was %v; a refused batch moved it", positions)
	}
}

// An archive that never returns slows no ingest, and a stream that fails does
// not delay another stream's delivery.
func TestAnArchiveThatNeverReturnsHoldsBackNothingButItsStream(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st, nil)
	// What a batch ingest costs with no archive at all.
	var baseline time.Duration
	for range 3 {
		began := time.Now()
		ingest(t, in, tenantA, "p0", "", many(50, "s")...)
		baseline = max(baseline, time.Since(began))
	}

	forever := make(chan struct{})
	t.Cleanup(func() { close(forever) })
	a := newFakeArchive()
	stuck := make(chan struct{}, 1)
	a.refuse = func(_ int, batch []ext.AuditRecord) error {
		if batch[0].Stream == "proxy:p1" {
			select {
			case stuck <- struct{}{}:
			default:
			}
			a.mu.Unlock()
			<-forever // ignores its deadline, as a broken archive does
			a.mu.Lock()
			return errors.New("never")
		}
		return nil
	}
	f := testFeed(t, st, a, "node-1")
	f.attempt = 200 * time.Millisecond
	in = newIngester(t, st, f.Track)
	ingest(t, in, tenantA, "p1", "", many(3, "s")...)
	start(t, f)
	select {
	case <-stuck:
	case <-time.After(5 * time.Second):
		t.Fatal("the archive was never called")
	}

	for range 3 {
		began := time.Now()
		ingest(t, in, tenantA, "p1", "", many(50, "s")...)
		if took := time.Since(began); took > 4*baseline+time.Second {
			t.Errorf("a batch ingest took %s with a hung archive registered, and at most %s with none", took, baseline)
		}
	}
	ingest(t, in, tenantA, "p2", "", many(9, "s")...)
	deadline := time.Now().Add(5 * time.Second)
	for {
		pos, err := st.Feed().Position(context.Background(), tenantA, store.ArchiveConsumer, "proxy:p2")
		if err == nil && pos.ConfirmedSeq == 9 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proxy:p2 stood at %+v (%v) behind a hung proxy:p1", pos, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pos, err := st.Feed().Position(context.Background(), tenantA, store.ArchiveConsumer, "proxy:p1"); err != nil || pos.ConfirmedSeq != 0 {
		t.Errorf("proxy:p1, whose archive never answered, stands at %+v, %v", pos, err)
	}
}

// Concurrent ingest and feeding, over many interleaved commits on one stream,
// never skip a sequence: the store's append lock makes commit order sequence
// order, so a read after the position never misses a record that commits
// after it.
func TestConcurrentIngestAndFeedingNeverSkipASequence(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newFakeArchive()
	f := testFeed(t, st, a, "node-1")
	f.idle = time.Millisecond
	in := newIngester(t, st, f.Track)
	start(t, f)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := in.Ingest(context.Background(), audit.Submission{
					Tenant: tenantA, ProxyID: "p1", Records: many(1+w%3, "s"),
				}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	caughtUp(t, st, 20*time.Second, tenantA)
	holdsEverything(t, st, a, tenantA)
	a.mu.Lock()
	defer a.mu.Unlock()
	if n := a.calls; n < 5 {
		t.Errorf("only %d batches: the feed did not interleave with ingest, so this proves nothing", n)
	}
}

// Two feeds over one store, standing in for two nodes, deliver every record at
// least once and never move a position backwards.
func TestTwoNodesDeliverEverythingAndNeverMoveAPositionBack(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newFakeArchive()
	one, two := testFeed(t, st, a, "node-1"), testFeed(t, st, a, "node-2")
	// A short lease, so the two hand streams to each other while they run.
	one.lease, two.lease = 50*time.Millisecond, 50*time.Millisecond
	inOne, inTwo := newIngester(t, st, one.Track), newIngester(t, st, two.Track)
	start(t, one)
	start(t, two)

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
			for _, s := range []string{"proxy:p1", "proxy:p2", "proxy:p3"} {
				pos, err := st.Feed().Position(context.Background(), tenantA, store.ArchiveConsumer, s)
				if err != nil {
					continue
				}
				if pos.ConfirmedSeq < last[s] {
					backwards = append(backwards, fmt.Sprintf("%s from %d to %d", s, last[s], pos.ConfirmedSeq))
				}
				last[s] = pos.ConfirmedSeq
			}
		}
	}()
	for i := range 30 {
		in := inOne
		if i%2 == 1 {
			in = inTwo
		}
		ingest(t, in, tenantA, fmt.Sprintf("p%d", 1+i%3), "", many(3, "s")...)
	}
	caughtUp(t, st, 20*time.Second, tenantA)
	close(stop)
	if backwards := <-watched; len(backwards) > 0 {
		t.Errorf("a position moved backwards: %v", backwards)
	}
	holdsEverything(t, st, a, tenantA)
}

// Stopping the feed mid-batch confirms nothing, gives the lease back, and the
// next feed — a new process, under a new name — offers the batch again at once.
func TestAFeedStoppedMidBatchOffersItAgainOnRestart(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newFakeArchive()
	inFlight := make(chan struct{}, 1)
	forever := make(chan struct{})
	t.Cleanup(func() { close(forever) })
	var once sync.Once
	a.refuse = func(_ int, batch []ext.AuditRecord) error {
		var block bool
		once.Do(func() { block = true })
		if !block {
			return nil
		}
		inFlight <- struct{}{}
		// The fake's lock is released while the call hangs, as a real
		// archive holds none of Control's; the stop does not wait for it.
		a.mu.Unlock()
		<-forever
		a.mu.Lock()
		return nil
	}
	first := testFeed(t, st, a, "node-1")
	first.attempt = time.Hour // only the stop ends the call
	in := newIngester(t, st, first.Track)
	ingest(t, in, tenantA, "p1", "", many(3, "s")...)
	first.Start(context.Background())
	<-inFlight
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := first.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	pos, err := st.Feed().Position(context.Background(), tenantA, store.ArchiveConsumer, "proxy:p1")
	if err != nil || pos.ConfirmedSeq != 0 || pos.LeaseUntil.After(time.Now().Add(time.Second)) {
		t.Fatalf("after a stop mid-batch the position is %+v, %v; want nothing confirmed and the lease given back", pos, err)
	}

	second := testFeed(t, st, a, "node-2")
	second.Track(tenantA)
	start(t, second)
	caughtUp(t, st, 5*time.Second, tenantA)
	a.mu.Lock()
	offered := append([]uint64(nil), a.offered[key{ext.Tenant(tenantA), "proxy:p1"}]...)
	a.mu.Unlock()
	if fmt.Sprint(offered) != "[1 2 3 1 2 3]" {
		t.Errorf("offered %v; want the interrupted batch offered again", offered)
	}
	holdsEverything(t, st, a, tenantA)
}

// A capture is offered only after the archive confirmed its record, with the
// record's digest and size, and the digest is the bytes'. A refused capture
// batch is offered again, and the records stay confirmed.
func TestACaptureFollowsItsRecordWithTheDigestTheChainCovers(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	a := newFakeArchive()
	a.refuseCaptures = func(call int, _ []ext.Capture) error {
		if call <= 2 {
			return errors.New("bucket unavailable")
		}
		return nil
	}
	f := testFeed(t, st, a, "node-1")
	in := newIngester(t, st, f.Track)
	at := time.Now().Add(-time.Hour)
	// Records with no capture first, so a capture offered before its record
	// is confirmed would arrive while the archive holds only these.
	recs := many(4, "sess-cap")
	for i := range 6 {
		recs = append(recs, captureRecord("sess-cap", at.Add(time.Duration(i)*time.Second), fmt.Sprintf("$ ls\r\nfile-%d\r\n%s", i, "padding-padding-padding")))
	}
	ingest(t, in, tenantA, "p1", "", recs...)
	ingest(t, in, tenantB, "p1", "", captureRecord("sess-b", at, "tenant b's bytes"))
	start(t, f)
	caughtUp(t, st, 10*time.Second, tenantA, tenantB)
	holdsEverything(t, st, a, tenantA, tenantB)

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.captures) != 7 {
		t.Fatalf("the archive holds %d captures, want 7", len(a.captures))
	}
	for id, c := range a.captures {
		sum := sha256.Sum256(a.bodies[id])
		if c.Digest != "sha256:"+hex.EncodeToString(sum[:]) || c.Size != int64(len(a.bodies[id])) {
			t.Errorf("capture %s: digest %s size %d, but the bytes hash to %x over %d", id, c.Digest, c.Size, sum, len(a.bodies[id]))
		}
		stored, err := st.Audit().Get(context.Background(), store.Tenant(c.Tenant), c.RecordID)
		if err != nil || stored.CaptureSHA256 != c.Digest || stored.SessionID != c.SessionID {
			t.Errorf("capture %s does not carry its record's digest: %+v, %v", id, stored, err)
		}
	}
	if a.captureCalls < 4 {
		t.Errorf("%d capture calls: the bytes bound should split six captures, and two were refused", a.captureCalls)
	}
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		if lag, err := st.Feed().Lag(context.Background(), tenant, store.ArchiveConsumer); err != nil || lag.CapturesWaiting != 0 {
			t.Errorf("%s: %+v %v, want nothing waiting", tenant, lag, err)
		}
	}
}

// An archive registered after a retention pass is fed each stream from its
// chain's start after the marker, and the control stream it receives carries
// the operator.audit.retained record that explains the gap.
func TestAnArchiveRegisteredAfterAPassStartsAfterTheMarker(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st, nil)
	ingest(t, in, tenantA, "p1", "", many(6, "s")...)
	keys := retentionKeys(t, st)
	before, err := audit.NewEmitter(in, audit.WithRetentionSigner(keys))
	if err != nil {
		t.Fatal(err)
	}
	pass, err := before.RetainPrefix(context.Background(), tenantA, audit.PrefixPass{Stream: "proxy:p1", ThroughSeq: 3},
		audit.OperatorActor{Principal: "tok-admin"})
	if err != nil {
		t.Fatal(err)
	}

	a := newFakeArchive()
	f := testFeed(t, st, a, "node-1")
	f.Track(tenantA)
	start(t, f)
	caughtUp(t, st, 10*time.Second, tenantA)
	holdsEverything(t, st, a, tenantA)

	held, _ := a.snapshot()
	p1 := held[key{ext.Tenant(tenantA), "proxy:p1"}]
	if len(p1) != 3 || p1[0].Sequence != 4 || p1[0].PrevHash != pass.ThroughHash {
		t.Fatalf("proxy:p1 arrived as %+v; want 4..6, the first chaining onto the marker's hash", p1)
	}
	var explained bool
	for _, r := range held[key{ext.Tenant(tenantA), audit.StreamControl}] {
		explained = explained || (r.Attributes["event"] == audit.EventAuditRetained &&
			r.Attributes["operator.stream"] == "proxy:p1" && r.Attributes["operator.through_hash"] == pass.ThroughHash)
	}
	if !explained {
		t.Errorf("the control stream the archive holds does not explain the gap: %+v", held[key{ext.Tenant(tenantA), audit.StreamControl}])
	}
}

// The feed's backoff doubles from its base to its cap, and a recovery clears
// it.
func TestTheBackoffDoublesToItsCap(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	f := testFeed(t, st, newFakeArchive(), "node-1")
	f.base, f.capWait = time.Second, 5*time.Second
	k := streamKey{tenantA, "proxy:p1"}
	f.streams[k] = &streamState{}
	var waits []time.Duration
	for range 5 {
		f.failed(context.Background(), k, errors.New("down"))
		waits = append(waits, time.Until(f.streams[k].retryAt).Round(time.Second))
	}
	if fmt.Sprint(waits) != "[1s 2s 4s 5s 5s]" {
		t.Errorf("waits = %v", waits)
	}
	f.recovered(k)
	if f.streams[k].failures != 0 || !f.streams[k].retryAt.IsZero() {
		t.Errorf("a recovery left %+v", f.streams[k])
	}
}

// testKEK is the key-encryption key the tests' software custodian uses.
var testKEK = bytes.Repeat([]byte{0x24}, extdefault.KeyEncryptionKeySize)

// retentionKeys is Control's own custody over the test database, as a
// deployment with a key-encryption key and no ext.KeyStore runs it.
func retentionKeys(t *testing.T, st *store.Store) *audit.RetentionKeys {
	t.Helper()
	ks, err := extdefault.NewSoftwareKeyStore(st, testKEK)
	if err != nil {
		t.Fatal(err)
	}
	return audit.NewRetentionKeys(ks)
}
