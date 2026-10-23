# 0025 — archive before retention — Learnings

## Summary
- **Feed** (`internal/archive`): with an `ext.ArchiveStore` registered, every committed record goes to `Archive`, one tenant's stream per batch, ascending `Sequence`, contiguous from the last confirmed record, **at least once**. nil = confirmed, so the position (`audit_feed_positions`, migration `0014`) moves forward durably. An error confirms nothing, and that stream alone backs off from 1 s, doubling to the cap. Captures go through `ArchiveCaptures` only after their record is confirmed (`audit_captures.archived_at`). A stream retained before registration starts after its marker. With no archive: no feed, no rows.
- **One node per stream:** a lease in the position row (`lease_holder`, `lease_until`, the database's clock, 3× the attempt timeout, renewed each batch, released on stop). Not `ext.ClusterCoordinator`, not the event plane. Overlap costs a duplicate batch, never a skip.
- **`ext` (additive):** `AuditRecord.Stream`, `.PrevHash`, `.Body` (the hashed canonical JSON); `ext.ChainLink(tenant, stream, sequence, prevHash, body) string` + `ext.ChainVersion` (`hoplock.chain/1`). `audit.Link` calls `ChainLink`, so there is one implementation, and the encoding is in its doc. **Breaking:** `ArchiveStore.ArchiveCaptures(ctx, []ext.Capture) error`; `Capture{Tenant, RecordID, SessionID, Digest (capture_sha256, "sha256:<hex>"), Size, Body io.Reader}`.
- **Retention gate:** a pass deletes no further than `ArchivedThrough`, the last seq whose records and captures are all confirmed. By `through_seq` beyond it: **`409 retention_not_archived`** {`stream`, `archived_through_seq`}, and nothing changes. By `before`: clamped. A capture pass runs to the earliest unconfirmed capture's `recorded_at`, signs there, and empties only confirmed captures. Both answers have `held_by_archive`; the prefix answer has `archived_through_seq` (archive only); `operator.audit.retained` has `operator.held_by_archive` / `.archived_through_seq` (archive only).
- **Lag:** the archive's row on `GET …/extensions` has `lag {streams_behind, oldest_unconfirmed_at, captures_waiting}`, for the caller's tenant only.
- **Config** `audit.archive.*`: `batch_records` 500 (≤ `max_batch_records`), `capture_batch_bytes` 16 MiB (≥ `max_capture_bytes`), `attempt_timeout` 30s, `idle_interval` 5s, `backoff_cap` 5m (≥ 1s).
- **Release `v0.12.0`**, MINOR. `### Breaking`: `ArchiveCaptures` added, `ArchiveStore` now called, retention waits on it. Decisions: **M8 revised in place** (register row reworded, still `live`); none added. Owes `hoplock/enterprise` a sync; `hoplock/proxy` none.
- **NEXT (0026):** generalise this feed. Positions are already keyed by consumer (`store.ArchiveConsumer` = `archive`), so a sink is one more key (`sink:<provider>`). Captures and the retention gate stay archive-only. `AuditSink`'s doc comment still says "ascending Sequence" without "per stream"; that sentence is 0026's to fix.

## Details

### What was answered, item by item

The request (enterprise#26, item 1) is **met as asked**: every committed
record, per tenant and stream, ascending, at least once; the confirmed position
kept per tenant and stream; retention never past it; capture bytes across the
seam before a capture pass empties them, through `ArchiveCaptures` exactly as
sketched; delivery off ingest and the decision path; nothing changed with no
archive. The prompt's three additions:

1. **Refused by sequence, held back by instant.** Built as the prompt framed
   it. A pass by instant that the archive holds at or behind the marker
   deletes nothing and, like any pass that found nothing to take, is **not
   recorded**: there is no act to state. A refused pass by sequence writes
   nothing either.
2. **`AuditRecord.Stream`.** Added. `Sequence`'s doc comment no longer says
   "the tenant's hash chain".
3. **Verifiable outside Control.** Taken as asked rather than the "not like
   that" path. The chain's encoding is now a public promise. `ext.ChainLink`
   is the computation, and `audit.Link` is one line that calls it. A pinned
   vector (`ext/chain_test.go`, computed in Python) fails on any change.
   `TestTheSealEncoderIsTheChainLinksEncoding` holds the seal encoder
   (`writeParts`, still `internal/audit`'s) to `ChainLink`, so the two cannot
   drift. `ChainVersion` is exported so an archive can name the encoding it
   verified. A later encoding is a new version and a new function.

### One gap the request did not name: a prefix pass deletes captures too

A prefix pass deletes its records' captures with them (`store.RetainPrefix`).
A record confirmed by `Archive` may still have a capture `ArchiveCaptures` has
not taken, so "through the confirmed position" alone would delete an unarchived
capture. That breaks the promise "never empties a capture the archive has not
confirmed". So the gate is `ArchivedThrough`: LEAST(position, first sequence
with an unarchived capture − 1). It is read over a partial index of unarchived
captures (`audit_captures_unarchived_idx`), which is small in steady state.
`archived_through_seq` reports this number, not the bare position.

### Why each half only moves forward

The position moves forward only (an upsert `WHERE confirmed_seq < new`). A
capture only ever gains `archived_at`. A new unarchived capture belongs to a
record committed after every confirmed one, because the capture row is
written before its record and the record's sequence is beyond the head. So a
read before the lock can only under-delete. The by-sequence check is done
again under the stream's append lock (`RetainPrefixOptions.Check`), and the
capture bound under the capture-retention lock (`RetainCapturesOptions.Bound`).
The capture pass also empties only `archived_at IS NOT NULL`
(`ArchivedOnly`), because `recorded_at` is the client's clock. A capture
recorded an hour ago can arrive after the bound was read, and without the
guard the `UPDATE` would empty it. The pass then signs its watermark over the
bounded instant, never the instant asked for.

### Commit order is sequence order — proven, not assumed

Ingest takes the stream's transaction-scoped advisory lock and holds it to
commit, and reads the head only once it holds it. So N+1 is assigned only
after N committed, and Postgres makes a commit visible before it releases its
locks. `TestConcurrentIngestAndFeedingNeverSkipASequence` runs 8 writers × 20
batches against a feed polling every millisecond. The fake archive flags any
batch that starts past what it holds + 1.

### The lease, and the alternatives

- **Lease in the position row**: chosen. It needs no connection held across
  the archive call, so a slow archive costs nothing but its own lease. Nodes
  share the database's clock. The holder renews before each step. On failure
  it extends the lease over its backoff, so another node does not retry
  sooner. On a clean stop it releases the lease, so a restart, which is a new
  random node name, resumes at once. A crashed node's lease lapses after 3×
  `attempt_timeout` (90 s by default).
- **Transaction advisory lock across the call**: rejected. It holds a pooled
  connection and an open transaction for up to the attempt timeout per
  stream, which competes with ingest for connections.
- **Session advisory lock**: rejected. It pins a connection per stream.
- **`ext.ClusterCoordinator.Lead`**: excluded by the prompt.

### Tenants: the ones this process has seen

The store cannot list tenants (M18; `TestRepositoryMethodsTakeATenant`). So
the feed walks a set it is told of: the configured tenant, each tenant a proxy
subscribes in (`trackSubscriptions`), each tenant the ingester writes in
(`audit.Options.Track`, a map insert after the write), and each tenant a
retention pass or the extension listing names (`north.ArchiveFeed.Track`).
This is the workflow poller's precedent (0016). A tenant no node has seen
since a restart is delivered once one does. Nothing is lost meanwhile:
retention reads positions, not this set, and holds back whatever is not
confirmed.

### Bounds that are constants, not configuration

- `maxConcurrentStreams = 4` workers per node. A worker holds a connection
  only between archive calls, but a fleet's streams catching up after an
  outage must not take every connection ingest needs. A worker that finds no
  free slot wakes the scheduler when one frees.
- A call past `attempt_timeout` is **abandoned**, not waited on. Its result
  confirms nothing, and the stream is not tried again until that call has
  returned. So an archive that never returns holds no worker, and leaks at
  most one goroutine per stream.
- The first backoff is 1 s, which is why `backoff_cap` must be ≥ 1 s.

### Mapping to `ext.AuditRecord`

`Kind` is the record's contract kind (`command`, `policy_decision`, …). The
event name is in `Attributes["event"]`, as it already was, and an operator
act's own fields are under `operator.`. `OccurredAt` is `recorded_at`, the
client's instant. `Attributes` is a copy.

### A bug the tests found

`call` cancelled the attempt context inside the goroutine after `fn`
returned. That made `ctx.Done()` ready together with the result, and
`select` picked the deadline branch about half the time. So a successful
batch was retried, a duplicate rather than a loss.
`TestEveryCommittedRecordReachesTheArchiveOncePerStreamInOrder` caught it
under `-race`. The fix cancels after the select, and prefers a result that
arrived with the deadline.

### Mutations (each applied, the named test run, reverted; all 14 caught)

| Mutation | Caught by |
| --- | --- |
| by-sequence gate removed | `TestAPassBySequenceBeyondTheArchiveIsRefusedWhole` |
| by-instant clamp removed | `TestAPassByInstantIsHeldBackByTheArchive` |
| capture pass not bounded | `TestACapturePassIsHeldBackByTheArchive` |
| `ArchivedOnly` guard removed | `TestACapturePassForAnArchiveEmptiesOnlyConfirmedCaptures` |
| `ArchivedThrough` ignores captures | `TestAPrefixPassWaitsForTheCapturesOfWhatItDeletes` |
| position may move back | `TestAFeedPositionNeverMovesBackwards` |
| lease ignored | `TestAFeedLeaseIsHeldByOneNodeAtATime` |
| confirm on an archive error | `TestAFailedBatchIsOfferedAgainAndConfirmsNothing` |
| capture offered before its record | `TestACaptureFollowsItsRecordWithTheDigestTheChainCovers` |
| no lease release at stop | `TestAFeedStoppedMidBatchOffersItAgainOnRestart` |
| ingest does not report tenants | `TestEveryCommittedRecordReachesTheArchiveOncePerStreamInOrder` |
| hung call waited on | `TestAnArchiveThatNeverReturnsHoldsBackNothingButItsStream` |
| lag read across tenants | `TestTheExtensionListingShowsTheArchivesLagForTheCallersTenant` |
| daemon builds no feed | `server.TestTheDaemonFeedsTheArchiveAHostRegisters` |

### Verified by hand

- `make conform`: 58/58 against a local server with migration `0014`
  applied.
- `make release-check` prints `v0.12.0 (MINOR … 1 incompatible change)`, and
  refuses the entry with its `### Breaking` removed.
- `golangci-lint` v2.14.0 (CI's pin, built with Go 1.27): 0 issues.
- `go test -race` on `internal/archive`: 15 runs clean after the fix.

### Observations, not built

- **Position table growth** is one row per tenant, consumer and stream, the
  same order as `audit_retention`. Nothing bounds it, and nothing needs to.
- `oldest_unconfirmed_at` is the earliest `recorded_at` of each behind
  stream's *first* unconfirmed record. It is one index probe per stream, not
  a scan of every unconfirmed record, so a later record with an earlier
  client clock is not considered.
- A capture row whose record does not claim a capture (`capture_bytes = 0`),
  left by a refused or duplicate batch, is ignored by the feed and by the
  gate.

### Cross-repo impact

`hoplock/enterprise` (the requester) owes a sync. Its 0005 must be rewritten
against the feed, the gate, the lag, `ArchiveCaptures` and `ChainLink`, and
its archive must implement `ArchiveCaptures` to compile against `v0.12.0`.
Its 0006 sees the new `AuditRecord` fields. Its plan's `ArchiveStore` row and
E6 text change too. The PR's `## Cross-repo impact` lists every obligation,
and the queued kickoff names them. `hoplock/proxy`: none, because the contract
is untouched.
