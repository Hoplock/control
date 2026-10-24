# 0026 — audit sink delivery — Learnings

## Summary
- **Delivery:** with sinks registered, each gets every committed record of every tenant through `Export`, one tenant's stream per batch, ascending `Sequence`, at least once. nil confirms and moves the sink's durable position; an error confirms nothing, and that stream alone backs off from 1 s, doubling to the cap. Off ingest and the decision path. Delivery writes no audit record. Captures never cross. With no sink: no delivery, no rows.
- **Where the feed lives:** generalised **in `internal/archive`**: one `Feed` per consumer. `Options.Consumer` (`Key`, `Point`, `Provider`, `Deliver`) makes a sink's feed; `Options.Archive` still makes the archive's. `internal/export.Delivery` builds one feed per sink (`export.New`, `Track`, `Lag(tenant, provider)`, `Start`, `Stop`); `internal/daemon/export.go` wires it.
- **Position:** in `audit_feed_positions` under `store.SinkConsumer(provider)` = `sink:<provider>`, per tenant and stream; the sink keeps no cursor. Named by `Registration.Provider`: re-registering resumes, a new name is a new sink. A sink's row is begun at the stream's chain start (after the marker) the first time it is delivered (`FeedRepository.Begin`).
- **Concurrency:** per consumer, never two calls of one stream in flight, and the next only after the previous returned; different streams/tenants in flight together up to `audit.sinks.max_in_flight` (or `audit.archive.max_in_flight`), so `Export` — and `Archive`/`ArchiveCaptures`, whose doc now says so — must be safe for concurrent use. A call past its deadline holds its place until it returns, for the archive too (changed in review). Across nodes, a call held past the lease can see a duplicate, never a skip.
- **Retention:** never consults a sink. When a batch read after a sink's position starts past the next sequence, `Skip` moves the position to the marker once (row lock), adds the run to `skipped_records` and `skipped_behind`, and logs `audit_sink_skipped_by_retention` once at WARN (`provider`, `tenant`, `stream`, `from_seq`, `through_seq`, `records`). `operator.audit.retained`, delivered like any record, explains the gap. A confirm that reaches the head zeroes `skipped_behind`.
- **Lag** (`GET …/extensions`, on each sink's registration, caller's tenant): `streams_behind`, `oldest_unconfirmed_at`, `failing`, `skipped_by_retention`. The archive's row-level `lag` gains `failing`; `captures_waiting` stays archive-only.
- **Config** `audit.sinks.*`: `batch_records` 500 (≤ `max_batch_records`), `attempt_timeout` 30s, `idle_interval` 5s, `backoff_cap` 5m (≥ 1s), `max_in_flight` 4 (≥ 1). New for the archive: `audit.archive.max_in_flight` 4 (≥ 1).
- **Migration `0015`:** `audit_feed_positions` gains `skipped_records`, `skipped_behind`, `failing_since`. No row rewritten.
- **Release `v0.13.0`**, MINOR, no `### Breaking` (no signature changed). **M8 revised in place** (register row reworded, still `live`); no decision added. Owes `hoplock/enterprise` a sync; `hoplock/proxy` none.
- **NEXT:** a new feed consumer is an `archive.Consumer` and a key; failures are durable (`failing_since`), so a lag read on any node agrees.

## Details

### What was answered, item by item

The request (enterprise#26, item 2) is **met as asked**. The prompt's four
additions:

1. **Per-stream order needs 0025 first.** Nothing added to `ext.AuditRecord`.
   A sink receives exactly what the archive receives (`toExt` is shared), and
   `TestEveryRecordReachesEverySinkOncePerStreamInOrder` compares the two
   record for record.
2. **Retention does not wait on a sink.** `audit.Emitter` is untouched: the
   retention gate still reads `ArchivedThrough`, which keys on
   `store.ArchiveConsumer` only. How a skip is counted is below.
3. **Concurrency is stated.** `AuditSink`'s doc comment states the contract,
   and `Export`'s says "concurrently for different streams, never for one
   stream at once".
4. **Where a position lives.** In the store, keyed by provider. Begun at the
   chain start, as item 4 asks.

### Generalised in `internal/archive`, not moved

The 0025 tests reach into the feed's internals (`f.base`, `f.attempt`,
`f.capWait`, `f.streams`, `streamState`, `f.failed`, `f.recovered`), and the
prompt requires them to pass unchanged. Moving the feed to a new package
would have meant rewriting them. So the shared part stayed where it was, and
`internal/export` is a thin layer of one feed per sink. What differs by
consumer:

| | archive | sink |
| --- | --- | --- |
| step | records, then captures | records, after any skip |
| first position | 0, created by `Claim` | the chain start, created by `Begin` |
| retention | gates it | moved past it (`Skip`) |
| log events | `archive_feed_*` | `audit_sink_*` |

**One bound for every consumer, changed in review.** The prompt asks that "a
sink that ignores its deadline holds one slot until it returns", and that a
fake sink never sees more than the configured bound. If an abandoned call
freed its slot, the bound would be a promise about workers, not about calls
the consumer is serving.

The first version kept 0025's archive semantics, where an abandoned call freed
its worker, only because the prompt froze the archive's behaviour. The user
was asked whether that difference made sense on its merits, and had the
archive unified in this PR. On the merits:
- the old semantics let an archive that hangs gather one stuck call per
  stream, unbounded across a fleet;
- holding the place fails safe, because a stalled archive feed only holds
  retention back;
- freeing the place helps only an archive that hangs for ever on a few
  streams while serving the rest.

So `Options.InFlight` bounds every feed (zero takes 4, 0025's worker count).
`audit.archive.max_in_flight` configures the archive's. When a call is
abandoned, a goroutine waiting on it frees the place (`freeSlot`) once it
returns. `ext.ArchiveStore`'s doc comment now states the concurrency it always
had: four workers called it at once from `v0.12.0`, and it never said so.

### How a skip is detected: the gap, not every step

`Skip` takes a row lock in a transaction. Calling it on every batch would cost
two round trips per batch on the steady path. Instead `stepSink` reads the
batch after the position, and only when the first record is not position + 1
does it call `Skip`. The chain is contiguous above its marker, and retention
is the only deleter, so a gap means a pass. The loop re-reads after each moved
skip, so a second pass between the read and the skip is also counted. A gap
`Skip` does not move is one of two cases:
- another node moved the position first; its records follow;
- a gap no pass explains, such as tampering.

In both cases the batch is delivered as it stands. A gap is the sink's proof
of a loss, and hiding it would be wrong.

The first version returned after a moved skip and let the next step deliver.
`-count=5` caught it: `recovered` ran on the skip step, cleared
`failing_since`, then the next failure set it again. That logged a
recovered/failing pair and briefly showed `failing: false`. Now the skip and
the delivery are one step.

### Why a sink's row begins at the marker

Without `Begin`, a new sink's row would start at 0. It could not then tell "I
was registered after a pass" (not a skip) from "a pass overtook me" (a skip).
Starting at the marker makes the first case a position, so every later jump is
a real skip. That holds even for a sink that failed from its very first
attempt: its row was begun before the pass, so the pass's records count as
skipped. `Fail` was made **update-only** for the same reason. 0025's failure
path used `Claim`, which inserts at 0, so a `Begin` that failed on a database
error would have left a row at 0 for the next `Begin` to keep. `Begin` and
`Skip` refuse the archive's key.

### `skipped_records` and `skipped_behind`: two counts, deliberately

The prompt asks for a skipped count "in the position row", and on the lag "the
records skipped by retention since the sink last caught up". One counter
cannot be both:
- reset at catch-up, the row loses its history;
- never reset, the lag shows an old skip for ever.

So `skipped_records` is cumulative, and `skipped_behind` is zeroed by a
`Confirm` that reaches the stream's head (one index probe per confirm). The lag
sums `skipped_behind`. Once a sink catches up, the lag reads 0. The WARN line
and `operator.audit.retained` remain the record that it happened.

### `failing` is durable

"Whether delivery is failing" is in the store (`failing_since`), not in the
node's backoff state, because `GET …/extensions` may land on any node (M5).
- `Fail` sets it (COALESCE, so a run keeps its start).
- `Confirm` clears it.
- `Recovered` clears it after a success that confirmed nothing, which is the
  archive's captures-only step.

The archive's failure path writes it too. That is the one change to the
archive's rows: a column added, nothing rewritten. It is why the archive's
`lag` gains `failing`.

### What is wired where

- `internal/daemon/serve.go` composes one `trackFeeds` from the archive's
  feed and the sink delivery. It hands that to the ingester, to
  `trackSubscriptions` and to the configured tenant.
- `north.Server.trackFeeds` (was `trackArchive`) tells both of a retention pass.
- Sink delivery starts after the archive's feed and stops after it, both
  bounded by the shutdown grace.

### Tests and mutations

The acceptance criteria are tested in four places:
- `internal/archive/sink_test.go`: the sink feed with a 10 ms backoff, which
  only that package can set;
- `internal/export/delivery_test.go`: delivery as the daemon builds it;
- `internal/httpapi/north/sink_test.go`: over the wire;
- `server/sink_test.go`: through `server.Options.Registry`.

Store tests cover `Begin`, `Skip`, `Fail`, `Recovered` and the lag. Each
mutation below was applied, its test run, then reverted; all 12 were caught.

| Mutation | Caught by |
| --- | --- |
| no `Begin` for a sink | `TestASinkRegisteredAfterAPassBeginsAtTheMarkerAndSkipsNothing` |
| no skip on a gap | `TestARetentionPassOvertakingASinkIsSkippedCountedAndExplained` |
| an abandoned call frees its place | `TestAHungSinkCallHoldsItsPlaceUntilItReturns`, `TestAHungArchiveCallHoldsItsPlaceUntilItReturns` |
| bound ignored | `TestASinkSeesStreamsInFlightTogetherUpToItsBoundAndNeverOneStreamTwice` |
| confirm on an error | `TestAFailedSinkBatchIsOfferedAgainAndOnlyASuccessMovesThePosition` |
| `skipped_behind` never reset | `TestASinkBeginsAtTheChainStartAndSkipsWhatRetentionRemovedBehindIt` |
| lag aggregate across consumers | the same, and `TestAFailingStreamIsMarkedUntilDeliverySucceeds` |
| `Fail` does not mark | `TestASinkFailingWritesNothingToTheChain` |
| daemon builds no delivery | `server.TestTheDaemonDeliversToEverySinkAHostRegisters` |
| the ingester does not track | the same (it writes in a second tenant, `acme`) |
| a lag from the wrong tenant | `TestTheExtensionListingShowsEachSinksLagForTheCallersTenant` |
| two sinks share one feed's lag | `TestPositionsArePerSinkAndNamedByTheProvider` |

Two mutations were missed on the first run and the tests were strengthened:
- The ingester-tracking mutation passed because the configured tenant is
  always tracked at start.
- The per-sink lag lookup was only checked over the wire.

The restart test first flagged "two batches of one stream in flight". The
first process's abandoned call was still hanging while the second process ran.
That is the cross-process overlap the contract allows (a duplicate), so the
test now ends the first call before restarting, as a process exit would.

### Verified by hand

- `go build`, `go vet`, `go test ./...` against Postgres 16: all pass.
- `-race` on `internal/archive`, `internal/export`, `internal/store`,
  `internal/daemon`, `internal/config`, `internal/httpapi/north` and `server`:
  all pass. The sink and retention tests also ran `-count=8` under `-race`.
- `golangci-lint` v2.14.0 (CI's pin, built with Go 1.27): 0 issues.
- `make license-check` and `make contract-check` pass.
- `make conform`: 58/58 against a local server with migration `0015` applied.
- `make release-check`: `v0.13.0 (MINOR: 0026 implemented; no change to the
  public API)`.
- Migration `0015` on a populated `0014` table (1000 rows): the same
  `relfilenode`, the same xmins, the same values. Every row reads 0 / 0 / NULL.

### Observations, not built

- `oldest_unconfirmed_at` for a sink is computed as 0025's is: the first
  unconfirmed record of each behind stream.
- Rows of a provider that stopped registering are kept, and gate nothing.
  Nothing prunes them; the order is one row per tenant, stream and provider
  ever registered.
- The archive's prefix gate and the sink skip both read `audit_retention`'s
  one row per stream, so neither needs a history of markers.

### Cross-repo impact

`hoplock/enterprise`, the requester, owes a sync. Its 0006 must be rewritten
against the delivery semantics, the position, the naming, the concurrency
contract, retention's relation to a sink, the absence of delivery records, and
the lag fields. Its skipped end-to-end criteria become real ones. Its plan's
`AuditSink` row and E6/E7 text change, and every obligation holds from
`v0.13.0`. The PR's `## Cross-repo impact` lists each obligation, and the
queued kickoff names them. `hoplock/proxy`: none, because the contract is
untouched.
