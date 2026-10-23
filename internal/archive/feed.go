// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package archive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/store"
)

// What the feed promises, and how it keeps the promise.
//
// AT LEAST ONCE, PER STREAM, IN ORDER. A batch is one stream's records after
// the archive's confirmed position, in sequence order, read from the store.
// Archive returning nil moves the position to the batch's last record, durably;
// an error moves nothing, and the same records are offered again after a
// backoff. The store's append lock makes a stream's sequence order its commit
// order (store.FeedRepository), so a read after the position never skips a
// record that commits later.
//
// ONE NODE PER STREAM, BY A LEASE IN THE POSITION ROW. Every node may run a
// feed; a node feeds a stream only while it holds that stream's lease, which it
// renews on each batch and which another node takes only once it expires, by
// the database's clock. A node that dies mid-batch confirms nothing, and its
// lease lapses. Two nodes whose turns overlap — a lease that lapsed under a slow
// archive — deliver one batch twice, which the archive tolerates (idempotent on
// RecordID); neither can move a position backwards or past a record it did not
// deliver. The lease is in the store rather than taken through
// ext.ClusterCoordinator because nothing else here depends on one, and rather
// than a session advisory lock because that would pin a database connection to
// each stream for as long as its archive is slow.
//
// NO STREAM HOLDS BACK ANOTHER. Each stream is delivered by a worker of its own,
// at most maxConcurrentStreams at once; a failing stream backs off alone, and a
// call that outlives its deadline is abandoned rather than waited on, so an
// archive that never returns holds no worker.
//
// THE TENANTS ARE THE ONES THIS PROCESS HAS SEEN. The store cannot list tenants
// (M18), so the feed delivers the tenants it is told of: the configured one,
// each a proxy subscribes in, each a record is written in on this node, and each
// a retention pass or an extension listing names. A tenant this node has not
// seen is delivered by the node that wrote its records, or by this one as soon
// as it sees it. Nothing is lost meanwhile: retention reads the confirmed
// position, not this set, and holds back whatever is not confirmed.

// maxConcurrentStreams bounds how many streams one node delivers at once. A
// worker holds a database connection only between its calls to the archive,
// but a fleet's worth of streams catching up after an archive outage must not
// take every connection ingest needs.
const maxConcurrentStreams = 4

// initialBackoff is the first wait after a failed attempt; it doubles up to the
// configured cap.
const initialBackoff = time.Second

// releaseTimeout bounds giving a lease back at shutdown, so that the next
// process to start need not wait for it to lapse.
const releaseTimeout = 5 * time.Second

// leaseAttempts is how many attempt deadlines a lease outlasts: a step reads,
// calls Archive, confirms, and may call ArchiveCaptures, and the lease is
// renewed before each step.
const leaseAttempts = 3

// Options configures a Feed.
type Options struct {
	// Store is where the records, the positions and the leases are.
	Store *store.Store
	// Archive is the registered archive, and Provider its registration's
	// name for the log.
	Archive  ext.ArchiveStore
	Provider string
	// BatchRecords bounds one Archive call, and the count of one
	// ArchiveCaptures call; CaptureBatchBytes bounds the second's bytes.
	BatchRecords      int
	CaptureBatchBytes int64
	// AttemptTimeout is the deadline on each call to the archive.
	AttemptTimeout time.Duration
	// IdleInterval is how long the feed waits with nothing to deliver.
	IdleInterval time.Duration
	// BackoffCap is the longest wait between attempts on a failing stream.
	BackoffCap time.Duration
	// Node names this process as a lease holder. Empty takes a random name,
	// unique per process.
	Node   string
	Logger *slog.Logger
}

// Feed delivers the audit store to a registered archive.
type Feed struct {
	st       *store.Store
	archive  ext.ArchiveStore
	provider string
	batch    int
	capBytes int64
	attempt  time.Duration
	idle     time.Duration
	capWait  time.Duration
	base     time.Duration
	lease    time.Duration
	node     string
	log      *slog.Logger

	mu       sync.Mutex
	tenants  map[store.Tenant]struct{}
	streams  map[streamKey]*streamState
	scanning map[store.Tenant]bool // a tenant whose stream list failed to read
	busy     int
	// starved is set when a stream waited for a free worker, so the next
	// worker to finish starts a pass rather than leaving it to the timer.
	starved bool

	wake      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
	workers   sync.WaitGroup
}

type streamKey struct {
	tenant store.Tenant
	stream string
}

// streamState is what this node remembers of a stream between passes. None of
// it changes an answer another node gives (M5): it is when to try again and
// whether the failure has been logged.
type streamState struct {
	busy     bool
	failures int
	retryAt  time.Time
	// outstanding is closed when an abandoned call to the archive returns;
	// the stream is not tried again before it has.
	outstanding chan struct{}
}

// New builds a feed. It delivers nothing until Start.
func New(o Options) (*Feed, error) {
	switch {
	case o.Store == nil:
		return nil, fmt.Errorf("archive: a store is required")
	case o.Archive == nil:
		return nil, fmt.Errorf("archive: an archive is required")
	case o.BatchRecords <= 0 || o.CaptureBatchBytes <= 0:
		return nil, fmt.Errorf("archive: a batch is bounded by a count and by bytes")
	case o.AttemptTimeout <= 0 || o.IdleInterval <= 0 || o.BackoffCap < initialBackoff:
		return nil, fmt.Errorf("archive: an attempt timeout, an idle interval and a backoff cap of at least %s are required", initialBackoff)
	}
	node := o.Node
	if node == "" {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("archive: name this node: %w", err)
		}
		node = "node-" + hex.EncodeToString(b[:])
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	provider := o.Provider
	if provider == "" {
		provider = "unnamed-archive"
	}
	return &Feed{
		st: o.Store, archive: o.Archive, provider: provider,
		batch: o.BatchRecords, capBytes: o.CaptureBatchBytes,
		attempt: o.AttemptTimeout, idle: o.IdleInterval, capWait: o.BackoffCap, base: initialBackoff,
		lease: leaseAttempts * o.AttemptTimeout, node: node, log: log,
		tenants:  map[store.Tenant]struct{}{},
		streams:  map[streamKey]*streamState{},
		scanning: map[store.Tenant]bool{},
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}, nil
}

// Node is this feed's name as a lease holder.
func (f *Feed) Node() string { return f.node }

// Track adds a tenant to the set the feed delivers. It is idempotent, never
// blocks, and is safe to call from the ingest path: it is a map insert, and the
// delivery it prompts happens on the feed's own goroutines.
func (f *Feed) Track(tenant store.Tenant) {
	if tenant == "" {
		return
	}
	f.mu.Lock()
	_, known := f.tenants[tenant]
	f.tenants[tenant] = struct{}{}
	f.mu.Unlock()
	if !known {
		select {
		case f.wake <- struct{}{}:
		default:
		}
	}
}

// Lag is how far behind the archive is in a tenant, and tracks the tenant: a
// tenant somebody asks about is one to deliver.
func (f *Feed) Lag(ctx context.Context, tenant store.Tenant) (store.FeedLag, error) {
	f.Track(tenant)
	return f.st.Feed().Lag(ctx, tenant, store.ArchiveConsumer)
}

// Start runs the feed until ctx is done or Stop is called.
func (f *Feed) Start(ctx context.Context) {
	f.startOnce.Do(func() {
		ctx, f.cancel = context.WithCancel(ctx)
		go f.run(ctx)
	})
}

// Stop ends the feed and waits for its workers, until ctx is done. A batch in
// flight is not confirmed, so it is offered again at the next start; a call to
// the archive that has not returned by then is abandoned, not waited on.
func (f *Feed) Stop(ctx context.Context) error {
	// A feed never started has nothing to wait for.
	f.startOnce.Do(func() { close(f.done) })
	f.stopOnce.Do(func() {
		if f.cancel != nil {
			f.cancel()
		}
	})
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("archive: the feed did not stop before the deadline: %w", ctx.Err())
	}
}

func (f *Feed) run(ctx context.Context) {
	defer close(f.done)
	defer f.workers.Wait()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-f.wake:
		}
		f.pass(ctx)
		timer.Reset(f.idle)
	}
}

// pass looks at every stream of every tenant this process has seen, and starts
// a worker for each that has something to deliver and may be tried now.
func (f *Feed) pass(ctx context.Context) {
	f.mu.Lock()
	tenants := slices.Sorted(maps.Keys(f.tenants))
	f.mu.Unlock()
	for _, tenant := range tenants {
		if ctx.Err() != nil {
			return
		}
		streams, err := f.st.Feed().Streams(ctx, tenant, store.ArchiveConsumer)
		if err != nil {
			if ctx.Err() == nil {
				f.scanFailed(tenant, err)
			}
			continue
		}
		f.scanRecovered(tenant)
		for _, s := range streams {
			if s.Behind() || s.CapturesReady > 0 {
				f.dispatch(ctx, streamKey{tenant, s.Stream})
			}
		}
	}
}

func (f *Feed) dispatch(ctx context.Context, key streamKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.streams[key]
	if st == nil {
		st = &streamState{}
		f.streams[key] = st
	}
	if st.busy || time.Now().Before(st.retryAt) {
		return
	}
	if f.busy >= maxConcurrentStreams {
		f.starved = true
		return
	}
	if st.outstanding != nil {
		select {
		case <-st.outstanding:
			st.outstanding = nil
		default:
			return
		}
	}
	st.busy = true
	f.busy++
	f.workers.Add(1)
	go func() {
		defer f.workers.Done()
		f.work(ctx, key)
		f.mu.Lock()
		st.busy = false
		f.busy--
		starved := f.starved
		f.starved = false
		f.mu.Unlock()
		if starved {
			select {
			case f.wake <- struct{}{}:
			default:
			}
		}
	}()
}

// work delivers one stream until it is caught up, it fails, or another node
// holds its lease.
func (f *Feed) work(ctx context.Context, key streamKey) {
	repo := f.st.Feed()
	defer func() {
		if ctx.Err() == nil {
			return
		}
		// Stopping: give the lease back, so a restart — a new node name —
		// feeds the stream at once rather than once the lease lapses.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer cancel()
		_ = repo.Release(rctx, key.tenant, store.ArchiveConsumer, key.stream, f.node)
	}()
	for ctx.Err() == nil {
		pos, held, err := repo.Claim(ctx, key.tenant, store.ArchiveConsumer, key.stream, f.node, f.lease)
		if err != nil {
			f.failed(ctx, key, err)
			return
		}
		if !held {
			return
		}
		delivered, err := f.step(ctx, key, pos)
		if err != nil {
			f.failed(ctx, key, err)
			return
		}
		f.recovered(key)
		if !delivered {
			_ = repo.Release(ctx, key.tenant, store.ArchiveConsumer, key.stream, f.node)
			return
		}
	}
}

// step delivers one batch of records and one of captures, and reports whether
// it delivered anything.
func (f *Feed) step(ctx context.Context, key streamKey, pos store.FeedPosition) (bool, error) {
	repo := f.st.Feed()
	// After the position: a record a retention pass removed before an
	// archive was registered is gone, so the batch starts after the
	// stream's marker without being told it.
	recs, err := f.st.Audit().Chain(ctx, key.tenant, key.stream, pos.ConfirmedSeq, f.batch)
	if err != nil {
		return false, err
	}
	if len(recs) > 0 {
		batch := make([]ext.AuditRecord, len(recs))
		for i, r := range recs {
			batch[i] = toExt(key.tenant, r)
		}
		if err := f.call(ctx, key, "archive", func(ctx context.Context) error {
			return f.archive.Archive(ctx, batch)
		}); err != nil {
			return false, err
		}
		last := recs[len(recs)-1]
		if pos, err = repo.Confirm(ctx, key.tenant, store.ArchiveConsumer, key.stream, last.ChainSeq, last.Hash); err != nil {
			return false, err
		}
	}

	caps, err := repo.PendingCaptures(ctx, key.tenant, key.stream, pos.ConfirmedSeq, f.batch, f.capBytes)
	if err != nil {
		return false, err
	}
	if len(caps) > 0 {
		batch := make([]ext.Capture, len(caps))
		ids := make([]string, len(caps))
		for i, c := range caps {
			batch[i] = ext.Capture{
				Tenant: ext.Tenant(key.tenant), RecordID: c.RecordID, SessionID: c.SessionID,
				Digest: c.Digest, Size: c.Size, Body: bytes.NewReader(c.Bytes),
			}
			ids[i] = c.RecordID
		}
		if err := f.call(ctx, key, "archive captures", func(ctx context.Context) error {
			return f.archive.ArchiveCaptures(ctx, batch)
		}); err != nil {
			return false, err
		}
		if _, err := repo.ConfirmCaptures(ctx, key.tenant, ids); err != nil {
			return false, err
		}
	}
	return len(recs) > 0 || len(caps) > 0, nil
}

// call runs one call to the archive under the attempt deadline. A call that
// outlives it is abandoned — its result, if it ever comes, confirms nothing —
// and the stream is not tried again until it has returned.
func (f *Feed) call(ctx context.Context, key streamKey, op string, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, f.attempt)
	defer cancel()
	result := make(chan error, 1)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		result <- fn(ctx)
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		// An answer that arrived with the deadline is still an answer.
		select {
		case err := <-result:
			return err
		default:
		}
		f.mu.Lock()
		if st := f.streams[key]; st != nil {
			st.outstanding = returned
		}
		f.mu.Unlock()
		kind := ext.KindUnavailable
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			kind = ext.KindInternal
		}
		return ext.Errorf(ext.PointArchiveStore, f.provider, op, kind,
			"no answer within %s: %v", f.attempt, ctx.Err())
	}
}

// failed backs a stream off, holds its lease for the wait so no other node
// retries it sooner, and logs the first failure of a run of them.
func (f *Feed) failed(ctx context.Context, key streamKey, err error) {
	if ctx.Err() != nil {
		// Shutting down: the batch is not confirmed, and that is all.
		return
	}
	f.mu.Lock()
	st := f.streams[key]
	st.failures++
	wait := f.base << min(st.failures-1, 30)
	if wait <= 0 || wait > f.capWait {
		wait = f.capWait
	}
	st.retryAt = time.Now().Add(wait)
	first := st.failures == 1
	f.mu.Unlock()

	_, _, _ = f.st.Feed().Claim(ctx, key.tenant, store.ArchiveConsumer, key.stream, f.node, wait+f.lease)
	if first {
		f.log.Warn("the archive feed is failing for a stream; it is retried with backoff and logged again when it recovers",
			"event", "archive_feed_failing",
			"provider", f.provider, "tenant", string(key.tenant), "stream", key.stream,
			"kind", ext.KindOf(err).String(), "error", err.Error(),
		)
	}
}

// recovered clears a stream's backoff, and logs the end of a run of failures.
func (f *Feed) recovered(key streamKey) {
	f.mu.Lock()
	st := f.streams[key]
	failures := st.failures
	st.failures, st.retryAt = 0, time.Time{}
	f.mu.Unlock()
	if failures > 0 {
		f.log.Info("the archive feed recovered for a stream",
			"event", "archive_feed_recovered",
			"provider", f.provider, "tenant", string(key.tenant), "stream", key.stream,
			"failed_attempts", failures,
		)
	}
}

func (f *Feed) scanFailed(tenant store.Tenant, err error) {
	f.mu.Lock()
	first := !f.scanning[tenant]
	f.scanning[tenant] = true
	f.mu.Unlock()
	if first {
		f.log.Warn("the archive feed cannot read a tenant's streams; it tries again on its interval",
			"event", "archive_feed_failing",
			"provider", f.provider, "tenant", string(tenant),
			"kind", ext.KindOf(err).String(), "error", err.Error(),
		)
	}
}

func (f *Feed) scanRecovered(tenant store.Tenant) {
	f.mu.Lock()
	was := f.scanning[tenant]
	delete(f.scanning, tenant)
	f.mu.Unlock()
	if was {
		f.log.Info("the archive feed reads a tenant's streams again",
			"event", "archive_feed_recovered", "provider", f.provider, "tenant", string(tenant))
	}
}

// toExt is a stored record as it crosses the seam: everything its link covers,
// so the archive can recompute ChainHash with ext.ChainLink.
func toExt(tenant store.Tenant, r store.AuditRecord) ext.AuditRecord {
	return ext.AuditRecord{
		Tenant: ext.Tenant(tenant), RecordID: r.RecordID,
		Stream: r.Stream, Sequence: uint64(r.ChainSeq), PrevHash: r.PrevHash, ChainHash: r.Hash, Body: r.Body,
		OccurredAt: r.RecordedAt.UTC(), Kind: r.Kind,
		SubjectID: r.Subject, TargetID: r.Target, SessionID: r.SessionID, DecisionID: r.DecisionID,
		Attributes: maps.Clone(r.Attributes),
	}
}
