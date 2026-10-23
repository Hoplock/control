// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// The audit feed's positions (0025, 0026, PLAN §7, M5).
//
// A consumer of the audit store — the registered archive, and each registered
// sink — is handed every committed record, per tenant and stream in ascending
// sequence, and confirms what it took. How far it confirmed is kept here, one
// row per tenant, consumer and stream, because it changes an answer: with an
// archive registered, retention never deletes past the archive's, and a sink
// resumes from its own after a restart on any node. A node may remember nothing
// of it that another node could not read back (M5).
//
// ORDER IS THE APPEND LOCK'S. Every append takes its stream's transaction-scoped
// lock (lockStream) and holds it to its commit, and the next append reads the
// head only once it holds the lock, so sequence N+1 is assigned only after N
// committed — and Postgres makes a commit visible before it releases its locks.
// A read that sees N+1 therefore sees N: a feed reading "after my position, in
// sequence order" never skips a record that commits later.

// ArchiveConsumer is the position key of the registered ext.ArchiveStore. It is
// the only consumer whose confirmations gate retention, and the only one whose
// captures cross the seam (audit_captures.archived_at).
const ArchiveConsumer = "archive"

// sinkConsumerPrefix keeps a sink's position key apart from the archive's.
const sinkConsumerPrefix = "sink:"

// SinkConsumer is the position key of the registered ext.AuditSink its
// Registration.Provider names (0026). The registry holds a provider unique
// within the point, so the name is the sink: a provider that registers again
// resumes from its positions, and one under a new name is a new sink.
func SinkConsumer(provider string) string { return sinkConsumerPrefix + provider }

// FeedPosition is how far one consumer has confirmed one stream, and who holds
// the lease to feed it next.
type FeedPosition struct {
	Stream string
	// ConfirmedSeq and ConfirmedHash are the last record the consumer
	// confirmed; zero and empty when it has confirmed nothing.
	ConfirmedSeq  int64
	ConfirmedHash string
	// ConfirmedAt is when it last moved; zero when it never has.
	ConfirmedAt time.Time
	// LeaseHolder and LeaseUntil are the node feeding the stream and until
	// when, by the database's clock.
	LeaseHolder string
	LeaseUntil  time.Time
	// SkippedRecords counts the records a retention pass removed before the
	// consumer confirmed them, over the row's life, and SkippedBehind those
	// since it last confirmed through the stream's head (Skip). Both are
	// zero for the archive, which retention waits on.
	SkippedRecords int64
	SkippedBehind  int64
	// FailingSince is the first failed attempt of the current run of them,
	// and zero while delivery succeeds.
	FailingSince time.Time
}

// FeedSkip is a run of records a retention pass removed before a consumer
// confirmed them: the consumer stood at From, and the stream now starts after
// Through, the pass's marker, whose record hashed to Hash.
type FeedSkip struct {
	From    int64
	Through int64
	Hash    string
}

// Records is how many records the run held.
func (s FeedSkip) Records() int64 { return s.Through - s.From }

// FeedStream is one stream of a tenant as a feed sees it: how far the chain
// reaches, where it starts, and how far the consumer has confirmed.
type FeedStream struct {
	Stream string
	// Head is the stream's last sequence.
	Head int64
	// Start is the stream's retention marker (the last sequence a pass
	// deleted), zero with none: its chain starts at Start+1.
	Start int64
	// Confirmed is the consumer's position, zero with none.
	Confirmed int64
	// CapturesReady is how many captures of records the archive has
	// confirmed are not confirmed themselves. It is counted for
	// ArchiveConsumer only, because only the archive is handed captures.
	CapturesReady int64
}

// From is the sequence the next batch starts after: the later of the
// consumer's position and the chain's start, because a record retained before a
// consumer was registered is gone and is never offered.
func (s FeedStream) From() int64 { return max(s.Confirmed, s.Start) }

// Behind reports whether the stream has records the consumer has not
// confirmed.
func (s FeedStream) Behind() bool { return s.Head > s.From() }

// FeedCapture is one session capture waiting for the archive, with its bytes.
type FeedCapture struct {
	RecordID  string
	SessionID string
	ChainSeq  int64
	// Digest is the record's capture_sha256, which the chain covers, and
	// Size its capture_bytes.
	Digest string
	Size   int64
	Bytes  []byte
}

// FeedLag is how far behind a consumer is in one tenant, as scalars: it is
// shown on the extension listing, and a list of streams there would be a list
// nobody bounds.
type FeedLag struct {
	// StreamsBehind counts the streams with records not yet confirmed.
	StreamsBehind int
	// OldestUnconfirmed is the recorded_at of the first record not yet
	// confirmed, the earliest across those streams; zero when none is.
	OldestUnconfirmed time.Time
	// CapturesWaiting counts the captures not yet confirmed, whether or
	// not their records are (ArchiveConsumer only).
	CapturesWaiting int64
	// Failing reports that delivery to the consumer is failing for at
	// least one stream.
	Failing bool
	// SkippedByRetention counts the records retention removed before the
	// consumer confirmed them, since it last caught up with each stream
	// (FeedPosition.SkippedBehind, summed). Always zero for the archive.
	SkippedByRetention int64
}

type feedRepo struct{ s *Store }

// feedPositionColumns is what scanFeedPosition reads, in its order.
const feedPositionColumns = `confirmed_seq, confirmed_hash, confirmed_at, lease_holder, lease_until,
	skipped_records, skipped_behind, failing_since`

// Feed returns the audit feed's position repository (0025).
func (s *Store) Feed() FeedRepository { return feedRepo{s} }

// feedStreamsSQL lists a tenant's streams with their heads, markers and the
// consumer's positions. The streams are walked by skipping along
// audit_records_chain_key (tenant, stream, chain_seq) one stream at a time, and
// each head is one probe of the same index, so the cost is the number of
// streams rather than the number of records.
const feedStreamsSQL = `
	WITH RECURSIVE streams(stream) AS (
		(SELECT stream FROM audit_records WHERE tenant = $1 ORDER BY stream LIMIT 1)
		UNION ALL
		SELECT (SELECT r.stream FROM audit_records r
		        WHERE r.tenant = $1 AND r.stream > s.stream ORDER BY r.stream LIMIT 1)
		FROM streams s WHERE s.stream IS NOT NULL
	)
	SELECT s.stream, h.chain_seq, COALESCE(m.through_seq, 0), COALESCE(p.confirmed_seq, 0)
	FROM streams s
	CROSS JOIN LATERAL (
		SELECT chain_seq FROM audit_records
		WHERE tenant = $1 AND stream = s.stream ORDER BY chain_seq DESC LIMIT 1) h
	LEFT JOIN audit_retention m ON m.tenant = $1 AND m.stream = s.stream
	LEFT JOIN audit_feed_positions p ON p.tenant = $1 AND p.consumer = $2 AND p.stream = s.stream
	WHERE s.stream IS NOT NULL
	ORDER BY s.stream`

// unarchivedCapturesFrom selects every capture still waiting for the archive, with
// its record's stream and position, over the partial index that holds only
// those. A capture whose bytes a pass already removed has nothing to hand over,
// and a capture row its record does not claim is not the record's.
const unarchivedCapturesFrom = `
	FROM audit_captures c
	JOIN audit_records a ON a.tenant = c.tenant AND a.record_id = c.record_id
	WHERE c.tenant = $1 AND c.archived_at IS NULL AND c.retained_at IS NULL AND a.capture_bytes > 0`

func (r feedRepo) Streams(ctx context.Context, tenant Tenant, consumer string) ([]FeedStream, error) {
	const op = "store.Feed.Streams"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if consumer == "" {
		return nil, invalid(op, "consumer is required")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, feedStreamsSQL, tenant, consumer)
	if err != nil {
		return nil, wrap(op, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (FeedStream, error) {
		var s FeedStream
		err := row.Scan(&s.Stream, &s.Head, &s.Start, &s.Confirmed)
		return s, err
	})
	if err != nil {
		return nil, wrap(op, err)
	}
	if consumer != ArchiveConsumer || len(out) == 0 {
		return out, nil
	}

	ready := map[string]int64{}
	byStream := make(map[string]int64, len(out))
	for _, s := range out {
		byStream[s.Stream] = s.Confirmed
	}
	crows, err := r.s.db.Query(ctx, `SELECT a.stream, a.chain_seq`+unarchivedCapturesFrom, tenant)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer crows.Close()
	for crows.Next() {
		var (
			stream string
			seq    int64
		)
		if err := crows.Scan(&stream, &seq); err != nil {
			return nil, wrap(op, err)
		}
		if confirmed, ok := byStream[stream]; ok && seq <= confirmed {
			ready[stream]++
		}
	}
	if err := crows.Err(); err != nil {
		return nil, wrap(op, err)
	}
	for i := range out {
		out[i].CapturesReady = ready[out[i].Stream]
	}
	return out, nil
}

func (r feedRepo) Position(ctx context.Context, tenant Tenant, consumer, stream string) (FeedPosition, error) {
	const op = "store.Feed.Position"
	if err := checkTenant(op, tenant); err != nil {
		return FeedPosition{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	return scanFeedPosition(op, stream, r.s.db.QueryRow(ctx, `
		SELECT `+feedPositionColumns+`
		FROM audit_feed_positions WHERE tenant = $1 AND consumer = $2 AND stream = $3`,
		tenant, consumer, stream))
}

// Claim takes, or renews, the lease to feed a stream for ttl: the row is
// created at position zero if it has none, and the lease is taken when nobody
// holds it, holder already does, or it expired. It answers the position and
// whether holder now holds the lease; false means another node does.
func (r feedRepo) Claim(ctx context.Context, tenant Tenant, consumer, stream, holder string, ttl time.Duration) (FeedPosition, bool, error) {
	const op = "store.Feed.Claim"
	if err := checkTenant(op, tenant); err != nil {
		return FeedPosition{}, false, err
	}
	switch {
	case consumer == "" || stream == "" || holder == "":
		return FeedPosition{}, false, invalid(op, "a consumer, a stream and a holder are required")
	case ttl <= 0:
		return FeedPosition{}, false, invalid(op, "a lease must last")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	pos, err := scanFeedPosition(op, stream, r.s.db.QueryRow(ctx, `
		INSERT INTO audit_feed_positions (tenant, consumer, stream, lease_holder, lease_until)
		VALUES ($1, $2, $3, $4, now() + $5::bigint * interval '1 microsecond')
		ON CONFLICT (tenant, consumer, stream) DO UPDATE
		SET lease_holder = EXCLUDED.lease_holder, lease_until = EXCLUDED.lease_until
		WHERE audit_feed_positions.lease_holder = EXCLUDED.lease_holder
		   OR audit_feed_positions.lease_until <= now()
		RETURNING `+feedPositionColumns,
		tenant, consumer, stream, holder, ttl.Microseconds()))
	if IsNotFound(err) {
		return FeedPosition{}, false, nil
	}
	if err != nil {
		return FeedPosition{}, false, err
	}
	return pos, true, nil
}

// Release gives a lease up early, when holder holds it, so the next node to
// look need not wait for it to expire.
func (r feedRepo) Release(ctx context.Context, tenant Tenant, consumer, stream, holder string) error {
	const op = "store.Feed.Release"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		UPDATE audit_feed_positions SET lease_until = now()
		WHERE tenant = $1 AND consumer = $2 AND stream = $3 AND lease_holder = $4 AND lease_until > now()`,
		tenant, consumer, stream, holder)
	return wrap(op, err)
}

// Confirm moves a consumer's position in a stream to a record it confirmed. It
// never moves it backwards: a confirmation at or behind the position changes
// nothing, which is what lets two nodes whose turns overlapped both confirm.
// A confirmation that moves it ends a run of failures, and one that reaches
// the stream's head sets SkippedBehind back to zero: the consumer has caught
// up. It answers the position after the call.
func (r feedRepo) Confirm(ctx context.Context, tenant Tenant, consumer, stream string, seq int64, hash string) (FeedPosition, error) {
	const op = "store.Feed.Confirm"
	if err := checkTenant(op, tenant); err != nil {
		return FeedPosition{}, err
	}
	switch {
	case consumer == "" || stream == "":
		return FeedPosition{}, invalid(op, "a consumer and a stream are required")
	case seq <= 0 || hash == "":
		return FeedPosition{}, invalid(op, "a confirmation names a record's sequence and hash")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	if _, err := r.s.db.Exec(ctx, `
		INSERT INTO audit_feed_positions (tenant, consumer, stream, confirmed_seq, confirmed_hash, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (tenant, consumer, stream) DO UPDATE
		SET confirmed_seq = EXCLUDED.confirmed_seq, confirmed_hash = EXCLUDED.confirmed_hash,
		    confirmed_at = EXCLUDED.confirmed_at, failing_since = NULL,
		    skipped_behind = CASE
		        WHEN EXCLUDED.confirmed_seq >= (
		            SELECT chain_seq FROM audit_records
		            WHERE tenant = $1 AND stream = $3 ORDER BY chain_seq DESC LIMIT 1)
		        THEN 0 ELSE audit_feed_positions.skipped_behind END
		WHERE audit_feed_positions.confirmed_seq < EXCLUDED.confirmed_seq`,
		tenant, consumer, stream, seq, hash); err != nil {
		return FeedPosition{}, wrap(op, err)
	}
	return scanFeedPosition(op, stream, r.s.db.QueryRow(ctx, `
		SELECT `+feedPositionColumns+`
		FROM audit_feed_positions WHERE tenant = $1 AND consumer = $2 AND stream = $3`,
		tenant, consumer, stream))
}

// Begin creates a sink's position in a stream at the stream's chain start —
// after its retention marker, whose record's hash it keeps — when it has none,
// and changes nothing when it has one. A sink starts where the chain does when
// Control first delivers the stream to it, so a prefix retained before then
// was never the sink's to receive and is not counted by Skip; one retained
// after then is.
//
// The archive never begins this way: its position is what retention waits on,
// and a position at a marker would claim it holds records it was never handed.
func (r feedRepo) Begin(ctx context.Context, tenant Tenant, consumer, stream string) error {
	const op = "store.Feed.Begin"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	switch {
	case consumer == "" || stream == "":
		return invalid(op, "a consumer and a stream are required")
	case consumer == ArchiveConsumer:
		return invalid(op, "the archive's position starts at zero, because retention waits on it")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		INSERT INTO audit_feed_positions (tenant, consumer, stream, confirmed_seq, confirmed_hash)
		SELECT $1, $2, $3, COALESCE(m.through_seq, 0), COALESCE(m.through_hash, '')
		FROM (VALUES (1)) AS one(x)
		LEFT JOIN audit_retention m ON m.tenant = $1 AND m.stream = $3
		ON CONFLICT (tenant, consumer, stream) DO NOTHING`,
		tenant, consumer, stream)
	return wrap(op, err)
}

// Skip moves a consumer that retention does not wait on past the records a
// retention pass removed before it confirmed them: when the stream's marker is
// beyond its position, the position moves to the marker and the run is added
// to SkippedRecords and SkippedBehind, in one transaction under the row's lock.
// It answers the run and true when it moved the position — exactly one caller
// does, however many nodes look at once — and false when the marker is at or
// behind it, or the consumer has no position in the stream.
//
// It is refused for the archive, whose position a pass never passes.
func (r feedRepo) Skip(ctx context.Context, tenant Tenant, consumer, stream string) (FeedSkip, bool, error) {
	const op = "store.Feed.Skip"
	if err := checkTenant(op, tenant); err != nil {
		return FeedSkip{}, false, err
	}
	switch {
	case consumer == "" || stream == "":
		return FeedSkip{}, false, invalid(op, "a consumer and a stream are required")
	case consumer == ArchiveConsumer:
		return FeedSkip{}, false, invalid(op, "retention waits on the archive, so it never skips")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	var (
		skip  FeedSkip
		moved bool
	)
	err := r.s.inTx(ctx, op, func(ctx context.Context, q querier) error {
		var from, through *int64
		var hash *string
		if err := q.QueryRow(ctx, `
			SELECT p.confirmed_seq, m.through_seq, m.through_hash
			FROM audit_feed_positions p
			LEFT JOIN audit_retention m ON m.tenant = p.tenant AND m.stream = p.stream
			WHERE p.tenant = $1 AND p.consumer = $2 AND p.stream = $3
			FOR UPDATE OF p`, tenant, consumer, stream).Scan(&from, &through, &hash); err != nil {
			if IsNotFound(wrap(op, err)) {
				return nil
			}
			return wrap(op, err)
		}
		if from == nil || through == nil || hash == nil || *through <= *from {
			return nil
		}
		skip = FeedSkip{From: *from, Through: *through, Hash: *hash}
		if _, err := q.Exec(ctx, `
			UPDATE audit_feed_positions
			SET confirmed_seq = $4, confirmed_hash = $5,
			    skipped_records = skipped_records + ($4 - confirmed_seq),
			    skipped_behind = skipped_behind + ($4 - confirmed_seq)
			WHERE tenant = $1 AND consumer = $2 AND stream = $3`,
			tenant, consumer, stream, skip.Through, skip.Hash); err != nil {
			return wrap(op, err)
		}
		moved = true
		return nil
	})
	if err != nil {
		return FeedSkip{}, false, err
	}
	return skip, moved, nil
}

// Fail records that holder's attempt to deliver a stream failed: it holds the
// lease for ttl — the backoff and a lease beyond it — so no other node tries
// the stream sooner, and marks the stream failing from now unless it already
// is. It changes nothing when another node holds a live lease, and creates no
// position: a sink's begins at the chain start (Begin), never here.
func (r feedRepo) Fail(ctx context.Context, tenant Tenant, consumer, stream, holder string, ttl time.Duration) error {
	const op = "store.Feed.Fail"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	switch {
	case consumer == "" || stream == "" || holder == "":
		return invalid(op, "a consumer, a stream and a holder are required")
	case ttl <= 0:
		return invalid(op, "a lease must last")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		UPDATE audit_feed_positions
		SET lease_holder = $4, lease_until = now() + $5::bigint * interval '1 microsecond',
		    failing_since = COALESCE(failing_since, now())
		WHERE tenant = $1 AND consumer = $2 AND stream = $3
		  AND (lease_holder = $4 OR lease_until <= now())`,
		tenant, consumer, stream, holder, ttl.Microseconds())
	return wrap(op, err)
}

// Recovered ends a stream's run of failures for a consumer. A confirmation
// ends one too; this is for an attempt that succeeded with nothing to confirm.
func (r feedRepo) Recovered(ctx context.Context, tenant Tenant, consumer, stream string) error {
	const op = "store.Feed.Recovered"
	if err := checkTenant(op, tenant); err != nil {
		return err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	_, err := r.s.db.Exec(ctx, `
		UPDATE audit_feed_positions SET failing_since = NULL
		WHERE tenant = $1 AND consumer = $2 AND stream = $3 AND failing_since IS NOT NULL`,
		tenant, consumer, stream)
	return wrap(op, err)
}

// PendingCaptures returns a stream's captures waiting for the archive whose
// records are at or before throughSeq — the records it confirmed — in sequence
// order, at most limit of them and no more than maxBytes in all, except that
// the first is returned whatever its size so that no capture waits for ever.
// The bytes are read only for the captures returned.
func (r feedRepo) PendingCaptures(ctx context.Context, tenant Tenant, stream string, throughSeq int64, limit int, maxBytes int64) ([]FeedCapture, error) {
	const op = "store.Feed.PendingCaptures"
	if err := checkTenant(op, tenant); err != nil {
		return nil, err
	}
	if limit <= 0 || maxBytes <= 0 {
		return nil, invalid(op, "a capture batch is bounded by a count and by bytes")
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()

	rows, err := r.s.db.Query(ctx, `
		SELECT a.record_id, a.session_id, a.chain_seq, a.capture_sha256, a.capture_bytes`+unarchivedCapturesFrom+`
		  AND a.stream = $2 AND a.chain_seq <= $3
		ORDER BY a.chain_seq
		LIMIT $4`, tenant, stream, throughSeq, limit)
	if err != nil {
		return nil, wrap(op, err)
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (FeedCapture, error) {
		var c FeedCapture
		var size int32
		err := row.Scan(&c.RecordID, &c.SessionID, &c.ChainSeq, &c.Digest, &size)
		c.Size = int64(size)
		return c, err
	})
	if err != nil {
		return nil, wrap(op, err)
	}
	var (
		out   []FeedCapture
		total int64
	)
	for _, c := range all {
		if len(out) > 0 && total+c.Size > maxBytes {
			break
		}
		total += c.Size
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, nil
	}

	ids := make([]string, len(out))
	for i, c := range out {
		ids[i] = c.RecordID
	}
	brows, err := r.s.db.Query(ctx, `
		SELECT record_id, bytes FROM audit_captures
		WHERE tenant = $1 AND record_id = ANY($2) AND retained_at IS NULL`, tenant, ids)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer brows.Close()
	got := make(map[string][]byte, len(ids))
	for brows.Next() {
		var (
			id string
			b  []byte
		)
		if err := brows.Scan(&id, &b); err != nil {
			return nil, wrap(op, err)
		}
		got[id] = b
	}
	if err := brows.Err(); err != nil {
		return nil, wrap(op, err)
	}
	// A capture retained between the two reads has no bytes to hand over
	// and is dropped from the batch: it is no longer waiting.
	kept := out[:0]
	for _, c := range out {
		if b, ok := got[c.RecordID]; ok {
			c.Bytes = b
			kept = append(kept, c)
		}
	}
	return kept, nil
}

// ConfirmCaptures marks captures as confirmed by the archive, and answers how
// many it marked. A capture already confirmed keeps its first confirmation.
func (r feedRepo) ConfirmCaptures(ctx context.Context, tenant Tenant, recordIDs []string) (int64, error) {
	const op = "store.Feed.ConfirmCaptures"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	if len(recordIDs) == 0 {
		return 0, nil
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	tag, err := r.s.db.Exec(ctx, `
		UPDATE audit_captures SET archived_at = now()
		WHERE tenant = $1 AND record_id = ANY($2) AND archived_at IS NULL`, tenant, recordIDs)
	if err != nil {
		return 0, wrap(op, err)
	}
	return tag.RowsAffected(), nil
}

// ArchivedThrough is how far the archive holds everything of a stream: the last
// sequence such that the archive confirmed every record through it and every
// capture of those records whose bytes are still here. It is what a retention
// pass may delete through while an archive is registered, and zero when the
// archive has confirmed nothing. Both halves only ever move forward — a
// position never moves back, and a capture that arrives later belongs to a
// record after every one already confirmed — so a read inside a pass can only
// under-state it.
func (r feedRepo) ArchivedThrough(ctx context.Context, tenant Tenant, stream string) (int64, error) {
	const op = "store.Feed.ArchivedThrough"
	if err := checkTenant(op, tenant); err != nil {
		return 0, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	var through int64
	err := r.s.db.QueryRow(ctx, `
		SELECT LEAST(
			COALESCE((SELECT confirmed_seq FROM audit_feed_positions
			          WHERE tenant = $1 AND consumer = $2 AND stream = $3), 0),
			COALESCE((SELECT MIN(a.chain_seq) - 1`+unarchivedCapturesFrom+` AND a.stream = $3), $4))`,
		tenant, ArchiveConsumer, stream, int64(math.MaxInt64)).Scan(&through)
	if err != nil {
		return 0, wrap(op, err)
	}
	return max(through, 0), nil
}

// EarliestUnarchivedCapture is the earliest recorded_at among the records whose
// capture still has its bytes and is not confirmed by the archive, and false
// when there is none. A capture pass with an archive registered runs no further
// than it.
func (r feedRepo) EarliestUnarchivedCapture(ctx context.Context, tenant Tenant) (time.Time, bool, error) {
	const op = "store.Feed.EarliestUnarchivedCapture"
	if err := checkTenant(op, tenant); err != nil {
		return time.Time{}, false, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	var at *time.Time
	if err := r.s.db.QueryRow(ctx, `SELECT MIN(a.recorded_at)`+unarchivedCapturesFrom, tenant).Scan(&at); err != nil {
		return time.Time{}, false, wrap(op, err)
	}
	if at == nil {
		return time.Time{}, false, nil
	}
	return at.UTC(), true, nil
}

// Lag is how far behind a consumer is in a tenant.
func (r feedRepo) Lag(ctx context.Context, tenant Tenant, consumer string) (FeedLag, error) {
	const op = "store.Feed.Lag"
	streams, err := r.Streams(ctx, tenant, consumer)
	if err != nil {
		return FeedLag{}, err
	}
	ctx, cancel := r.s.withTimeout(ctx)
	defer cancel()
	var lag FeedLag
	for _, s := range streams {
		if !s.Behind() {
			continue
		}
		lag.StreamsBehind++
		var at time.Time
		if err := r.s.db.QueryRow(ctx, `
			SELECT recorded_at FROM audit_records
			WHERE tenant = $1 AND stream = $2 AND chain_seq > $3
			ORDER BY chain_seq LIMIT 1`, tenant, s.Stream, s.From()).Scan(&at); err != nil {
			if IsNotFound(wrap(op, err)) {
				continue
			}
			return FeedLag{}, wrap(op, err)
		}
		if lag.OldestUnconfirmed.IsZero() || at.Before(lag.OldestUnconfirmed) {
			lag.OldestUnconfirmed = at.UTC()
		}
	}
	if consumer == ArchiveConsumer {
		if err := r.s.db.QueryRow(ctx, `SELECT count(*)`+unarchivedCapturesFrom, tenant).Scan(&lag.CapturesWaiting); err != nil {
			return FeedLag{}, wrap(op, err)
		}
	}
	if err := r.s.db.QueryRow(ctx, `
		SELECT COALESCE(sum(skipped_behind), 0), COALESCE(bool_or(failing_since IS NOT NULL), false)
		FROM audit_feed_positions WHERE tenant = $1 AND consumer = $2`,
		tenant, consumer).Scan(&lag.SkippedByRetention, &lag.Failing); err != nil {
		return FeedLag{}, wrap(op, err)
	}
	return lag, nil
}

func scanFeedPosition(op, stream string, row pgx.Row) (FeedPosition, error) {
	p := FeedPosition{Stream: stream}
	var confirmedAt, failingSince *time.Time
	if err := row.Scan(&p.ConfirmedSeq, &p.ConfirmedHash, &confirmedAt, &p.LeaseHolder, &p.LeaseUntil,
		&p.SkippedRecords, &p.SkippedBehind, &failingSince); err != nil {
		return FeedPosition{}, wrap(op, err)
	}
	if confirmedAt != nil {
		p.ConfirmedAt = confirmedAt.UTC()
	}
	if failingSince != nil {
		p.FailingSince = failingSince.UTC()
	}
	return p, nil
}
