# 0025 — An archive fed before retention deletes: Control feeds a registered archive, and retention waits on it

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/26, under `## Upstream request`,
> item 1, from its request file
> `prompts/upstream/queued/enterprise-PR#26-archive-before-retention.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2).
>
> **Where it sits.** First in the queue, before the shared event plane (0026),
> bounded lists (0027), host namespaces (0028) and the action executor (0029),
> none of which changes what it builds on. Before instance identity (0030), the
> console (0031) and the E2E topology (0032). The archive-before-retention
> renumbering note in `docs/PLAN.md` §10 gives the reasons. It depends only on
> merged phases: 0010 (the audit store and its chain), 0019 (the retention
> routes and the operator records) and 0024 (signed retention).

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `ext/` as a
  compatibility promise, for migrations, for revising the plan in place, for
  the register, and for the downstream hand-over this phase owes. Then §4 and
  §5.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a
  request and changes what `ext/` carries and says, so it owes a downstream
  sync (below).
- `docs/PLAN.md`:
  - **§2**, the register first, then:
    - **M8**, in full. Retention deletes a prefix and leaves the chain
      verifiable, and retention is signed (0024). This phase adds the rule
      that, with an archive registered, retention never removes what the
      archive has not confirmed.
    - **M15**, in full, for the three forms a point's answer takes.
      `ArchiveStore` is **disabled** when absent: "a long-term archive" is a
      capability Control never claimed. Nothing here changes that, and Control
      gains no archive of its own (§11).
    - **M5**: nothing on the decision path or ingest waits on anything
      outside Control, and no fact that changes an answer lives on one node
      only. The feed's positions are such facts.
    - **M12** and **M18** (tenancy), **M11** (a refusal is never a `401`, and
      an outage is a `5xx`), and **M23** (the release, and why a method added
      to an `ext` interface is a `### Breaking`).
  - **§3**, "Component responsibilities": `ext`, `internal/audit`,
    `internal/store`, `internal/httpapi/north`, `internal/daemon` and
    `internal/config`.
  - **§7**, in full: Storage, "What the chain does and does not defend
    against", "Retention holds to the same guarantee because it is signed",
    "Retention has to remove a chain's TAIL", and Export.
  - **§10**: this phase's row and the archive-before-retention renumbering
    note. **§11**: the archive itself stays Enterprise's.
- `docs/learnings/` — read every summary. Open these in full:
  - **`0004`**: `ArchiveStore`, `AuditSink` and `AuditRecord` as designed,
    and the catalogue's three forms.
  - **`0010`**: ingest, the per-tenant-per-stream chain, the append lock, and
    session capture's own table.
  - **`0019`**: the retention routes, `audit_retention`, and
    `operator.audit.retained`.
  - **`0021`**: its verdicts on retention, and its item 7, "Delivery without
    reconnect". That item is how configuration delivery became correct on
    several nodes with durable markers in the store, which is the precedent
    for this phase's positions.
  - **`0024`**, whole: the signed marker and capture watermark, what each
    seal covers, `409 retention_unverified` and `503
    retention_key_not_configured`.
- Code. Read it before designing anything:
  - `ext/audit.go`: `AuditRecord`, `AuditSink`, `ArchiveQuery`,
    `ArchivePage`, `ArchiveStore` and their doc comments.
  - `ext/point.go` (`PointArchiveStore`'s catalogue entry), `ext/registry.go`
    (`RegisterArchiveStore`, `Extensions.ArchiveStore`) and `ext/errors.go`
    (`Kind`, `KindOf`).
  - `internal/audit/chain.go`: `Link`, `writeParts`, `chainVersion`,
    `StreamFor` and `StreamControl`.
  - `internal/audit/retention.go` (`RetainPrefix`, `RetainCaptures`),
    `seal.go` and `verify.go`.
  - `internal/store/retention.go` (`RetainPrefix`, `RetainCaptures`,
    `ThroughSeqBefore`), `internal/store/audit.go` (the append lock,
    `pg_advisory_xact_lock`, and `Streams`) and `internal/store/types.go`
    (`AuditRecord`).
  - Migrations `0006` (`audit_captures`, `capture_bytes`, `capture_sha256`),
    `0012` (`audit_retention`, `audit_captures.retained_at`) and `0013` (the
    seals), and the latest migration's number.
  - `internal/httpapi/north/audit.go` (`handleRetention`,
    `handleCaptureRetention`, `auditFailure`), `errors.go` (the code
    registry) and `extensions.go` (`handleExtensions`).
  - `internal/daemon/main.go` and `serve.go`: where the registry is sealed,
    where the listeners open, and how shutdown drains.
  - `internal/config`: the `audit:` section and how a key is bounded at
    start-up.
- The request itself, for the requester's own words. The Enterprise decisions
  it turns on are **E4** (nothing Enterprise adds sits on the decision path)
  and **E6** (the archive never becomes a second system of record). The
  Enterprise phase that consumes this one is its 0005. Read any of them in
  `hoplock/enterprise` only if a choice here turns on it. All are cited by id
  and never restated.

## Objective
Feed a registered archive everything this store records, and make retention
wait for it.

`ext` declares the archive, and nothing in Control feeds it:

- nothing calls `Extensions.ArchiveStore`, so a registered archive is listed by
  `GET …/extensions` and handed nothing;
- a retention pass does not consult it, so a pass deletes records, and a
  capture pass empties captures, that no archive ever received;
- capture bytes cannot cross the seam at all. `ext.AuditRecord` carries a
  record's fields, never its capture.

So `ArchiveStore`'s own doc comment, "Control will not delete a local record
that an archive was asked for and has not confirmed", holds only because
Control never asks. "Seven years of retention" then holds only for what nobody
retained.

After this phase, with an archive registered, Control hands it every committed
record and every capture, durably tracks what it confirmed, and never deletes
or empties anything it has not. With no archive registered, nothing changes.

## The request, in this repository's vocabulary
- *Asked for*, verbatim from enterprise#26, item 1:

  > With an `ext.ArchiveStore` registered, Control:
  > - hands it every committed record through `Archive(ctx, batch)`, per tenant
  >   and stream in ascending `Sequence`, at least once, and keeps the last
  >   sequence the archive confirmed per tenant and stream;
  > - deletes only through that confirmed sequence on `POST …/audit/retention`.
  >   A pass asking for more is refused or clamped (Control's call), and the
  >   answer says how far it went. This is what `ext.ArchiveStore`'s doc comment
  >   already promises ("Control will not delete a local record that an archive
  >   was asked for and has not confirmed");
  > - hands capture bytes across the seam before
  >   `POST …/audit/captures/retention` empties them, which today they cannot
  >   cross at all. A sketch, which is a break at this repository's next bump
  >   (M23):
  >
  >   ```go
  >   // on ext.ArchiveStore
  >   ArchiveCaptures(ctx context.Context, batch []Capture) error
  >
  >   // Capture is one session capture's bytes as they cross the seam.
  >   type Capture struct {
  >       Tenant    Tenant
  >       RecordID  string    // the record whose digest covers these bytes
  >       SessionID string
  >       Digest    string    // the digest the chain covers, so the archive can verify
  >       Body      io.Reader
  >   }
  >   ```
  >
  > Delivery runs off ingest and the decision path (M5, E4). An archive that is
  > down holds retention back and never holds ingest. With no archive
  > registered, nothing changes.

  In this repository's words: a feed in a new `internal/archive` package,
  started by `internal/daemon` when an archive is registered. It reads each
  stream of each tenant from the store after a position the store keeps per
  tenant and stream, and moves that position only when `Archive` returns nil.
  The retention pass in `internal/audit` reads the position inside its own
  transaction and never goes past it. A capture's bytes cross through
  `ArchiveCaptures` once its record has been confirmed, and a capture pass
  empties only captures confirmed that way.
- *Until it exists:* with Enterprise's archive registered, the archive is
  empty, so archive search finds nothing, and an administrator's retention
  pass destroys records and captures that were never copied anywhere.
  Enterprise's 0005 builds its archive, tiers, search, legal hold,
  verification and replay against a fake feed, and skips every end-to-end
  criterion. A customer on Control alone loses nothing, because the point is
  disabled and retention without an archive is a deletion Control documents.

## What this phase answers, and where it differs from the ask
The shape is **met as asked**: every committed record, per tenant and stream,
in ascending sequence, at least once; the confirmed position kept per tenant
and stream; retention never past it; capture bytes across the seam before a
capture pass empties them, through `ArchiveCaptures` as sketched; delivery off
ingest and the decision path; nothing changed with no archive registered.

The request left one call to Control, and reading this repository's code adds
two things the shape needs and `ext` does not carry. Each item below says why,
then what to build.

1. **A pass by sequence is refused, a pass by instant is held back.**
   - *Why.* The two forms already mean different things. A pass by
     `through_seq` names exactly what to delete, and the store refuses one it
     cannot do whole (at or behind the marker, or at the head). A pass by
     `before` already means "as far as is lawful", and is answered with where
     it stopped. Clamping a pass by sequence would delete less than an
     operator named and call it success; refusing a pass by instant would make
     every scheduled pass fail while the archive is a minute behind.
   - *What to build.* With an archive registered, a pass by sequence beyond
     the stream's confirmed position is refused whole with `409
     retention_not_archived`, naming the stream and the confirmed sequence,
     and deletes nothing. A pass by instant deletes through the lesser of what
     the instant reaches and the confirmed position, and its answer says the
     archive held it back. A capture pass runs to the earliest instant before
     which every capture is confirmed, and its answer's `before` says so, as
     it already does for an instant ahead of the clock.
2. **`ext.AuditRecord` names its stream.**
   - *Why.* The chain is per tenant **per stream** (M8, `chain.go`), and
     `Sequence` is a position in one stream. `AuditRecord` has no stream, so
     "ascending `Sequence` per stream" cannot be expressed to the archive, and
     an archive holding two streams cannot tell their sequence 7s apart.
     `Sequence`'s doc comment says "the tenant's hash chain", which is wrong.
   - *What to build.* `AuditRecord.Stream`, the chain the record belongs to
     (`control`, `proxy:<id>`, `unattributed`), and `Sequence`'s doc comment
     corrected. Adding a field is not a break.
3. **An archived record is verifiable outside Control, as `ext` already
   claims.**
   - *Why.* `ChainHash`'s doc comment says a sink can "verify the chain it was
     handed without asking Control to vouch for it". It cannot. The link
     covers the tenant, the stream, the position, the predecessor's hash and
     the record's exact bytes, under `chainVersion`, encoded by `writeParts`.
     `AuditRecord` carries neither the bytes nor the stream, and the function
     is in `internal/`. Enterprise's 0005 has to verify across the retention
     boundary: the archive's copy up to the marker, Control's after it. It has
     been told never to reimplement Control's walk of its own chain, and
     never to read Control's tables.
   - *What to build.* `AuditRecord.PrevHash` and `AuditRecord.Body`, the
     canonical JSON the chain hashed, verbatim. Add a function in `ext` that
     computes a record's link from its fields, exactly as Control's chain
     does, and make `audit.Link` call it, so there is one implementation and
     it cannot drift. Its doc comment states the encoding, and says that a
     later encoding is a new `chainVersion`, never a changed one, because an
     archive verifies records written years earlier. Adding the fields and
     the function is not a break.

   If you judge that the chain's encoding must not become a public promise,
   that is the "not like that, like this" outcome for this item alone. Then
   correct `ChainHash`'s doc comment to say what an archive can verify and
   how, say so in the PR, and name it as an obligation under `## Cross-repo
   impact`. Never leave the claim as it stands.

## In scope

### 1. The feed (`internal/archive`, new)
- **Started by `internal/daemon`** when `Extensions.ArchiveStore` returns
  one. Start it after the registry is sealed and before the listeners open,
  with the server's lifetime as its context. Stop it on shutdown, before the
  store closes, bounded by the shutdown deadline. A batch in flight at
  shutdown is not confirmed, so it is offered again at the next start. With no
  archive registered, no feed exists, and the tests prove it.
- **What it reads.** For each tenant and each of its streams, the records
  after the stream's position, in ascending sequence, from the store. The
  position starts at the stream's chain start: after its retention marker
  when it has one, and at sequence 1 when it does not. Ingest takes the
  stream's lock per transaction, so a stream's sequence order is its commit
  order. Prove it rather than assume it: a test with concurrent ingest shows
  the feed never skips a sequence that commits after a later read.
- **What it hands over.** One stream's records per batch, mapped to
  `ext.AuditRecord` with every field (§2 below). Batches are bounded by a
  record count, and each attempt by a deadline (configuration, below).
- **Confirmation.** `Archive` returning nil confirms the batch: the position
  moves to the batch's last sequence, durably, and never backwards. An error
  confirms nothing, and the same records are offered again after a backoff
  that grows to a cap. A stream that fails never stalls another stream or
  another tenant.
- **Positions are rows in the store**, one per tenant and stream, added by the
  next migration, carrying the tenant column (M12). They hold the confirmed
  sequence, its hash and when it was confirmed. Nothing about the feed lives
  only in memory (M5).
- **Several nodes.** Every node may run the feed, and it must be correct
  without the shared event plane (0026). A stream is fed by one node at a
  time, by a lease in its position row or a lock in the store, and never by
  `ext.ClusterCoordinator`, which this phase does not depend on. Two nodes
  that overlap cost a duplicate batch, which `Archive` already tolerates
  (idempotent on `RecordID`), and never a skipped record. Write down which
  mechanism you chose and why.
- **Off ingest and the decision path (M5).** Ingest never calls the archive,
  never waits on the feed, and never reads a position. An archive that never
  returns slows nothing but the feed.
- **Logging.** A stream that starts failing is logged once at `WARN` with
  the provider, the stream and `ext.KindOf(err)`, and once when it recovers.
  A failure is never logged once per attempt.
- **Configuration**, in the `audit:` section, each bounded at start-up the way
  its neighbours are. As a guide: a batch size no larger than
  `audit.max_batch_records`, a capture batch's total bytes, an attempt
  timeout, an idle interval, and a backoff cap. Name and default them in
  `config.go`, and document each where the others are documented.
- **Reuse.** Enterprise has also queued sink delivery
  (`enterprise-PR#26-audit-sink-delivery.md`), which needs a feed of the same
  shape with a position per sink. Build no sink delivery here. If keying a
  position by a consumer's name costs nothing, do it, and say so in the
  learnings. The phase that answers that request decides whether it reuses
  this feed.

### 2. What crosses the seam (`ext/`)
- `AuditRecord` gains `Stream`, `PrevHash` and `Body` (item 2 and item 3
  above). `Sequence`'s and `ChainHash`'s doc comments say what is now true.
- The link function in `ext` (item 3 above), with `audit.Link` delegating to
  it. `TestExtImportsNothingInternal` must still pass, so the code moves into
  `ext` and `internal/audit` calls it, never the reverse.
- `ArchiveStore` gains `ArchiveCaptures(ctx, batch []Capture) error`, and
  `ext` gains `Capture`:
  - `Tenant`, `RecordID` and `SessionID`, as sketched;
  - `Digest`: the record's `capture_sha256`, hex, which the chain covers;
  - `Size`: the record's `capture_bytes`, so an archive can bound a read
    before it starts;
  - `Body`: the bytes, as an `io.Reader`.

  A batch is bounded by its total bytes as well as by its count. A capture
  is offered only after its record has been confirmed, so the archive always
  holds the record whose digest covers it. Its confirmation is durable, in a
  column on `audit_captures` added by the same migration.
- `ArchiveStore`'s doc comment says what Control now does:
  - every record, per stream, in ascending sequence, at least once;
  - a nil return is a confirmation, and an error is a retry;
  - Control never deletes a record, or empties a capture, that the archive
    has not confirmed, whether or not it was asked for it;
  - a pass beyond the confirmation is refused or held back (item 1);
  - delivery runs off ingest and the decision path;
  - Control calls `Search` nowhere. It is the host's, for its own routes.
- `PointArchiveStore`'s catalogue entry stays **disabled**. Revise its
  `Absent` line only if it no longer holds.
- Fix every implementation of `ArchiveStore` in this repository's tests and
  examples (`ext`, `server`). `make release-check` reports the added method as
  a break. Declare it under `### Breaking` in the release's changelog entry,
  naming the method and what an implementation must do.

### 3. Retention waits on the archive (`internal/audit`, `internal/store`, `internal/httpapi/north`)
- **Read inside the pass.** `RetainPrefix` and `RetainCaptures` read the
  archive's confirmation inside the pass's own transaction, under the lock the
  pass already takes. A confirmation only ever moves forward, so a read there
  can only under-delete, never over-delete.
- **Only with an archive registered.** The emitter is told whether one is.
  With none, both passes are exactly what they are today, positions or no
  positions. A deployment that unregisters its archive keeps its positions
  unused. One that registers an archive after a pass has run feeds each stream
  from the later of its stored position and the chain's current start. The
  archive learns of the deleted prefix from the `operator.audit.retained`
  record it receives on the control stream.
- **A pass by sequence beyond the confirmation:** `409
  retention_not_archived`, with `stream` and `archived_through_seq` in its
  parameters, and nothing deleted, sealed or recorded. Add the code to the
  registry in `errors.go`. It is a refusal on purpose, so it is never a `5xx`,
  and never a `401` (M11).
- **A pass by instant** deletes through the lesser of the two, and is sealed
  and recorded as any pass is. When the archive's confirmation is at or behind
  the marker, it deletes nothing, as a pass that found nothing to take does
  today.
- **A capture pass** runs to the earliest `recorded_at` among the records
  whose capture still has its bytes and is not confirmed, when that is earlier
  than the instant asked for. It is the same column the pass already selects
  on (`store.Audit().RetainCaptures`). The watermark is signed over the instant it actually ran
  to, never the one asked for, because a watermark vouches for every capture
  before it.
- **The answers.** Both answers gain `held_by_archive`, a boolean that is
  `false` with no archive registered. The prefix pass's answer also gains
  `archived_through_seq` when an archive is registered. The capture pass's
  `before` already says how far it ran. The `operator.audit.retained` record
  carries the same facts, so the control stream says that a pass was held
  back.

### 4. What an operator sees (`internal/httpapi/north`)
- `GET …/extensions`: the archive point's row gains the caller's tenant's lag
  when an archive is registered: how many streams are behind their head, the
  `recorded_at` of the oldest record not yet confirmed, and how many captures
  are waiting. These are scalars, never a list of streams, so bounded lists
  (0027) inherits nothing unbounded.
- Nothing new is required of `audit-verify`. It still resumes at the marker.

### 5. The documents
- `docs/PLAN.md`, revised in place:
  - M8's retention paragraphs: with an archive registered, retention never
    removes what the archive has not confirmed, and the feed is off ingest and
    the decision path. Revise M8 in place, or record a new decision if you
    find this settles something M8 does not, numbered when it is written.
    Either way, the register row says so;
  - §3's components (`internal/archive`);
  - §7's retention and export bullets, and the new refusal code;
  - §10's row, revised to what was built.

  §11 is unchanged: the archive itself stays Enterprise's.
- The console prompt (0031). Its audit view shows the archive's lag, and says
  when a retention pass was held back.
- `CHANGELOG.md`: the release this phase cuts, a MINOR with `### Breaking`
  (M23).

## Out of scope
- **Enterprise's archive** (its 0005, E6): storage, tiers, the per-kind and
  per-label policies, legal hold, search, its verifier and replay.
- **Serving archive search.** Control calls `Search` nowhere and adds no route
  for it. If Enterprise needs Control to serve it, that is a new request.
- **Sink delivery** (`enterprise-PR#26-audit-sink-delivery.md`), beyond
  leaving the feed reusable (§1).
- **Scheduled retention.** A pass stays an operator's act on the two routes.
- **An external anchor** for the chain (§7 names it as future work).
- **Delivery through `ext.ClusterCoordinator`**, and anything 0026 builds.
- **`contract/` (M1).** Nothing on the wire changes.
- **Bounding the position table's growth** beyond one row per tenant and
  stream. Note it in the learnings if it matters.

## Acceptance criteria
Each criterion is a test against Postgres, in the packages' own tests and over
the wire in `internal/httpapi/north`, with a fake archive registered through
`server.Options.Registry` where the daemon is involved.

**The feed**
- Every record committed in two tenants, across the control stream and two
  proxy streams, reaches the fake archive exactly once per stream in ascending
  sequence while it confirms. Each carries its stream, its predecessor's hash
  and its hashed body, and the `ext` link function reproduces its
  `ChainHash`. `audit-verify` passes.
- The fake archive fails three batches and then accepts. The same records are
  offered again, the position moves only on success, and nothing is skipped
  or reordered.
- A fake archive that never returns slows no ingest. A batch ingest with that
  archive registered completes within the bound it meets with none. A failing
  stream does not delay another stream's delivery.
- Concurrent ingest and feeding over many interleaved commits never skip a
  sequence.
- Two feeds over one store, standing in for two nodes, deliver every record at
  least once and never move a position backwards.
- Killing the feed mid-batch and restarting it re-offers the unconfirmed
  batch.
- With no archive registered, no feed exists and no position is written. The
  existing audit, retention and extension tests pass unchanged.
- Tenancy: one tenant's records and captures never reach the archive in
  another tenant's batch, and one tenant's positions never gate another
  tenant's retention.

**Captures**
- A capture is offered only after its record is confirmed. It arrives with the
  record's digest and size, and the digest matches the bytes.
- A capture pass with the archive behind runs to the earliest unconfirmed
  capture's instant, says so, and signs its watermark over that instant.
  `audit-verify --captures` passes afterwards.

**Retention**
- With the archive behind, a pass by sequence beyond its confirmation is `409
  retention_not_archived` and changes nothing: no row deleted, no marker
  moved, no record written.
- A pass by instant deletes only through the confirmation, says
  `held_by_archive: true` with `archived_through_seq`, is sealed, and is
  recorded with the same facts. `audit-verify` passes afterwards.
- Once the archive catches up, the same passes delete what they asked for.
- With no archive registered, every retention test from 0019, 0021 and 0024
  passes unchanged, and both answers say `held_by_archive: false`.
- An archive registered after a pass feeds from the chain's start after the
  marker, and the control stream it receives carries the
  `operator.audit.retained` record that explains the gap.

**What an operator sees**
- `GET …/extensions` shows the lag for the caller's tenant, and only that
  tenant's.
- The new code is in the registry, and the error-code tests list it.

**The release**
- `make release-check` passes with the `### Breaking` entry, and fails
  without it. `go build`, `go vet`, `go test ./...` and `-race` on the
  changed packages pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `license-check` and `make conform` pass.

## Cross-repo impact
This phase answers an upstream request and changes what `ext/` carries and
does, including an incompatible change to `ext.ArchiveStore`. So it owes a
downstream sync. Its PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what
was built, and the session there that rewrites its prompts will be a fresh
one. State at least these obligations:

1. **Its 0005, "What Control does not do yet".** Rewrite it from what merged,
   and stop building around the gap. It needs:
   - the feed's semantics: per stream, ascending, at least once, and what a
     nil return and an error each mean;
   - `ArchiveCaptures` and `Capture`, and that a capture follows its
     record;
   - the new `AuditRecord` fields and the link function, or, if the phase
     took the other path in item 3, what an archive can verify and how;
   - the retention gate: `409 retention_not_archived`, `held_by_archive`,
     `archived_through_seq`, and the capture pass held back;
   - the lag on `GET …/extensions`.

   Turn its skipped end-to-end criteria into real ones, and replace its
   verifier's plan where it assumed it must reimplement Control's link.
2. **Its 0006 (SIEM export).** `ext.AuditRecord` gains fields every sink sees
   too. Sink delivery itself is still the separate request, unanswered by this
   phase.
3. **Its plan.** §4's `ArchiveStore` row and its misreadings, and wherever E6
   says what reaches the archive.
4. **Its pin, and the break.** Every obligation holds from the release this PR
   cuts (M23), never from `main`. Name that release by version. Its archive
   implements the new method, or it does not compile against that release.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks, as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`. Commit it
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"**. The contract is untouched (M1), and the proxy consumes
neither `ext/` nor the north-bound surface. "None" is a finding.

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0025-archive-before-retention-learnings.md`. Enterprise's sync
is written from its summary block, which MUST give:

- the feed's semantics, and which mechanism keeps one node per stream;
- the new `ext` fields, the link function's name, and `Capture` with
  `ArchiveCaptures`;
- the retention gate: the refusal code, the answer fields, and the capture
  pass's held-back instant;
- the configuration keys and their defaults;
- the release, its `### Breaking` line, and any decision added or amended, in
  one line.
