# 0026 — Audit sink delivery: Control delivers every record to every registered sink

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/26, under `## Upstream request`,
> item 2, from its request file
> `prompts/upstream/queued/enterprise-PR#26-audit-sink-delivery.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2).
>
> **Where it sits.** Right after the archive feed (0025), whose feed it reuses
> and whose `ext.AuditRecord` fields it needs. Before the shared event plane
> (0027), bounded lists (0028), host namespaces (0029) and the action executor
> (0030), none of which changes what it builds on. Before instance identity
> (0033), the console (0034) and the E2E topology (0035). The sink-delivery
> renumbering note in `docs/PLAN.md` §10 gives the reasons. It depends on
> 0025, and otherwise only on merged phases: 0010 (the audit store and its
> chain) and 0019 (the extensions listing and the retention routes).

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `ext/` as a
  compatibility promise, for migrations, for revising the plan in place, for
  the register, and for the downstream hand-over this phase owes. Then §4 and
  §5.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a
  request and changes what `ext/` says Control does, so it owes a downstream
  sync (below).
- `docs/PLAN.md`:
  - **§2**, the register first, then:
    - **M8**, in full. The audit store is the system of record and the SIEM
      export a downstream consumer, never read back as truth. Retention
      deletes a prefix and is signed (0024), and with an archive registered it
      waits on the archive (0025). This phase adds delivery to sinks and
      settles that retention does **not** wait on them.
    - **M15**, in full, for the three forms a point's answer takes.
      `AuditSink` is **core** when absent: records stay in the local store
      and go nowhere else, and that stays true.
    - **M5**: nothing on the decision path or ingest waits on anything
      outside Control, and no fact that changes an answer lives on one node
      only. A sink's positions are such facts.
    - **M12** and **M18** (tenancy), and **M23** (the release, and why a
      method added to an `ext` interface would be a `### Breaking`; this phase
      adds none).
  - **§3**: the layout's `internal/export/` line, and "Component
    responsibilities" for `ext`, `internal/audit`, `internal/store`,
    `internal/httpapi/north`, `internal/daemon` and `internal/config`.
  - **§7**, the Export bullet and the retention bullets.
  - **§10**: this phase's row and the sink-delivery renumbering note.
    **§11**: SIEM export itself stays Enterprise's.
- `docs/learnings/` — read every summary. Open these in full:
  - **`0025`**, whole: the feed, where its positions live, which mechanism
    keeps one node per stream, its configuration keys, the fields it added to
    `ext.AuditRecord`, and the archive's lag on `GET …/extensions`. This phase
    is built on all of it.
  - **`0004`**: `AuditSink` and `AuditRecord` as designed, a point that
    accepts several registrations, and the catalogue's three forms.
  - **`0010`**: ingest, the per-tenant-per-stream chain and the append lock.
  - **`0019`**: `GET …/extensions`, the retention routes and
    `operator.audit.retained`.
  - **`0021`**: item 7, "Delivery without reconnect", the precedent for
    durable delivery positions in the store.
- Code. Read it before designing anything:
  - `ext/audit.go`: `AuditRecord` (with 0025's fields), `AuditSink` and their
    doc comments.
  - `ext/point.go` (`PointAuditSink`'s catalogue entry), `ext/registry.go`
    (`RegisterAuditSink`, `Extensions.AuditSinks`, `Bound`, and the rule that
    a provider registers once per point), `ext/errors.go` (`Kind`, `KindOf`),
    and the out-of-tree sink in `ext/example_test.go` and `ext/README.md`.
  - `internal/archive` (0025's feed) and the positions table its migration
    added, with the store code that reads and moves them.
  - `internal/export/doc.go`: an empty package from the scaffold, documented
    as Splunk, Sentinel and Elastic sinks.
  - `internal/audit/retention.go` and `internal/store/retention.go`: where a
    retention pass reads the archive's confirmation (0025).
  - `internal/httpapi/north/extensions.go` (`handleExtensions`, and the lag
    0025 added to the archive's row).
  - `internal/daemon/main.go` and `serve.go`: where the registry is sealed,
    where 0025 starts the feed, and how shutdown drains.
  - `internal/config`: the `audit:` section, and the keys 0025 added.
- The request itself, for the requester's own words. The Enterprise decisions
  it turns on are **E4**, **E6** and **E7**. The Enterprise phase that
  consumes this one is its 0006. Read any of them in `hoplock/enterprise` only
  if a choice here turns on it. All are cited by id and never restated.

## Objective
Deliver every record this store commits to every registered sink.

`ext` declares the sink, and nothing in Control delivers to it. Outside tests,
nothing in `internal/`, `server/` or `cmd/` calls `Extensions.AuditSinks` or
`AuditSink.Export`. A registered sink is listed by `GET …/extensions` and is
never offered a batch. `AuditSink`'s own doc comment says Control "retries a
batch it could not confirm" and "will offer it again", and neither is true,
because Control never offers the first one.

After this phase, with sinks registered, Control hands each of them every
committed record, keeps a durable position per sink, and offers again what a
sink did not confirm. A sink that is down or slow slows nothing but its own
delivery. With no sink registered, nothing changes.

## The request, in this repository's vocabulary
- *Asked for*, verbatim from enterprise#26, item 2:

  > Control delivers every committed record to every registered
  > `ext.AuditSink` through `Export(ctx, batch)`, per tenant and stream in
  > ascending `Sequence`, at least once. It keeps a durable position per sink
  > and offers an unconfirmed batch again, as `ext.AuditSink`'s doc comment
  > already says. A sink that is down or slow never slows ingest or a decision
  > (M5, E4), and one sink's backlog never stalls another's. Each sink's
  > delivered-through position against the stream head is observable, for
  > example in `GET …/extensions`, so lag is visible. No `ext` signature
  > changes. With no sink registered, nothing changes.

  In this repository's words: each registered sink is one more consumer of
  the feed 0025 built. The feed reads each stream of each tenant from the
  store after a position the store keeps per consumer, tenant and stream, and
  moves that position only when the consumer returns nil. The archive is one
  consumer. Each sink is another, named by its `Registration.Provider`. The
  delivery code lives in `internal/export`, which the scaffold reserved for
  exactly this and documented as something it is not.
- *Until it exists:* with Enterprise's Splunk, Sentinel and Elastic sinks
  registered, each appears in the listing and receives nothing. A SOC sees no
  Hoplock events at all, including the `operator.<noun>.<verb>` records an
  export is meant to carry. Enterprise's 0006 builds its sinks, export schema,
  mapping, retries and lag against a fake feed, and skips every end-to-end
  criterion. A customer on Control alone loses nothing, because the point is
  core and the local store is the record.

## What this phase answers, and where it differs from the ask
The shape is **met as asked**: every committed record, to every registered
sink, per tenant and stream, in ascending sequence, at least once; a durable
position per sink; an unconfirmed batch offered again; delivery off ingest and
the decision path; one sink's backlog never stalling another's; each sink's
lag observable on `GET …/extensions`; no `ext` signature changes; nothing
changed with no sink registered.

The request was written against `v0.6.0` and leaves four things to Control.
Reading this repository adds them here, each with why and then what to build.

1. **Per-stream order needs 0025 first.**
   - *Why.* The chain is per tenant **per stream** (M8), and `Sequence` is a
     position in one stream. At `v0.6.0`, `ext.AuditRecord` names no stream,
     so "ascending `Sequence` per stream" cannot be said to a sink, and a sink
     holding two streams cannot tell their sequence 7s apart. 0025 adds
     `Stream`, `PrevHash` and the hashed `Body`, and corrects `Sequence`'s doc
     comment. That is why this phase runs after it.
   - *What to build.* Nothing more on `AuditRecord`. A sink receives the
     same record the archive does, with every field 0025 added. A sink checks
     what it was handed with the link function 0025 put in `ext`.
2. **Retention does not wait on a sink.**
   - *Why.* M8 makes a sink a downstream consumer that is never read back as
     truth. Keeping records past local retention is the archive's job (0025,
     and Enterprise's E6), and with an archive registered retention already
     waits on it. If every sink could hold retention, any registered
     integration would hold a veto over an administrator's lawful deletion
     from the system of record, and a SIEM down for a week would stop it.
     The request does not ask for that. But the doc comment on `Sequence`
     calls a gap the sink's proof that something was dropped. So a sink that
     falls behind a retention pass has to be told what happened, never left
     to read the gap as a loss.
   - *What to build.* A retention pass never consults a sink's position. When
     a stream's chain start passes a sink's position, delivery resumes at the
     chain start, after the marker. The skipped range is counted in the
     position row and shown on the sink's lag (§4), and logged once at
     `WARN` with the provider, tenant, stream and range. The sink always also
     receives the control stream's `operator.audit.retained` record for that
     pass, which names the stream, the last deleted sequence and its hash.
     That record is what explains the gap. `AuditSink`'s and `Sequence`'s doc
     comments say so.
3. **Concurrency is stated, not implied.**
   - *Why.* "One sink's backlog never stalls another's" is asked. A stream
     that fails must not stall another stream or tenant either, as 0025 holds
     for the archive. Both need delivery to one sink to be independent per
     tenant and stream. The doc comment says only that a sink ignoring its
     deadline "delays every later batch behind it". That implies one call at
     a time, and no implementation can know which to expect.
   - *What to build.* Per sink, Control never has two batches of one stream
     in flight, and offers a stream's next batch only after the previous one
     returned. Batches of different streams or tenants may be in flight to one
     sink at once, up to a per-sink bound in configuration. So `Export` must
     be safe for concurrent use, and the doc comment says so. A sink that
     ignores its deadline holds one slot until it returns. With every slot
     held, it stalls only itself.
4. **Where a sink's position lives, and what names it.**
   - *Why.* Enterprise's 0006 left this for Control to settle, and its
     criteria include resuming after a restart with no gap. A cursor a sink
     kept for itself would be a second delivery path beside the seam.
   - *What to build.* The position lives in Control's store, per sink, tenant
     and stream, and a sink keeps no cursor of its own. A sink is named by
     its `Registration.Provider`, which the registry already holds unique
     within the point. A provider that stops registering keeps its rows,
     which gate nothing. If it registers again, it resumes from them. A
     provider registering under a new name is a new sink, and starts at each
     stream's chain start. A newly registered sink starts there too, as the
     archive does. The doc comment says what names a sink and where it starts.

## In scope

### 1. One feed, two kinds of consumer
- **Generalise 0025's feed** so the archive and each sink are consumers of one
  implementation of reading, positions, leases, batching and backoff. Do not
  copy it. Where the shared part lives is your call: generalised in
  `internal/archive`, or moved to a package both use. Say which in the
  learnings. What is the archive's alone stays the archive's: captures, and
  the confirmation retention reads.
- **Positions.** If 0025 keyed its positions by consumer, sinks add rows. If
  it did not, the next migration adds the consumer to the key, forward-only,
  with every existing row keyed as the archive's. Either way, a sink's row
  carries the tenant column (M12), the delivered sequence, its hash, when it
  was confirmed, and the count of records skipped by retention (item 2). The
  archive's behaviour, rows and tests do not change.
- **Several nodes.** The same mechanism 0025 chose keeps one node per stream
  per consumer, and never `ext.ClusterCoordinator`. Two nodes that overlap
  cost a duplicate batch, which `Export` already tolerates (idempotent on
  `RecordID`), and never a skipped record.

### 2. Delivery (`internal/export`)
- **Started by `internal/daemon`** for each sink `Extensions.AuditSinks`
  returns, after the registry is sealed and before the listeners open, with
  the server's lifetime as its context, as the archive's feed is. Stopped on
  shutdown before the store closes, bounded by the shutdown deadline. A batch
  in flight at shutdown is not confirmed, so it is offered again at the next
  start. With no sink registered, no delivery exists, and the tests prove it.
- **What a sink receives.** One tenant's and one stream's records per batch,
  in ascending sequence, mapped to `ext.AuditRecord` exactly as the archive
  receives them. Batches are bounded by a record count, and each attempt by a
  deadline. Captures never cross to a sink. If a host needs capture bytes in a
  SIEM, that is a new request.
- **Every tenant to every sink.** Control offers every tenant's records to
  every registered sink. Which tenants a host's sink forwards, and where, is
  the host's.
- **Confirmation.** `Export` returning nil confirms the batch. The position
  moves to the batch's last sequence, durably, and never backwards. An error
  confirms nothing, and the same records are offered again after a backoff
  that grows to a cap. Each sink, tenant and stream backs off on its own.
- **Isolation.** One sink that fails, hangs or lags never delays another
  sink's delivery. One tenant or stream that fails never delays another
  tenant's or stream's delivery to the same sink (item 3).
- **Off ingest and the decision path (M5).** Ingest never calls a sink, never
  waits on delivery, and never reads a position. A sink that never returns
  slows nothing but its own delivery.
- **Delivery writes no audit record.** Neither a delivery nor a failure is
  recorded on the chain, because a record about delivery is itself delivered:
  a failing sink would grow the very stream it cannot drain. The lag view and
  the log are where a failure shows.
- **Logging.** A sink that starts failing for a tenant and stream is logged
  once at `WARN`, with the provider, tenant, stream and `ext.KindOf(err)`, and
  once when it recovers. A failure is never logged once per attempt.
- **Configuration**, in the `audit:` section, beside the archive's keys and
  separate from them, because a SIEM and an archive are tuned differently. As
  a guide: a batch size no larger than `audit.max_batch_records`, an attempt
  timeout, an idle interval, a backoff cap, and the per-sink bound on batches
  in flight. Name and default each in `config.go`, bound it at start-up as
  its neighbours are, and document it where they are documented. These apply
  to every sink. A sink's own settings are the host's configuration.

### 3. What `ext/` says (`ext/audit.go`)
No signature changes. The doc comments say what is now true:
- `AuditSink`:
  - every committed record, per tenant and stream, in ascending sequence, at
    least once;
  - a nil return is a confirmation, and an error is a retry;
  - the position is Control's, durable and per sink, and a sink keeps no
    cursor of its own;
  - a sink is named by its `Registration.Provider`, and a new name starts at
    the chain's start;
  - the concurrency contract (item 3), and that `Export` must be safe for
    concurrent use;
  - retention does not wait on a sink, and a gap after a retained prefix is
    explained by `operator.audit.retained` (item 2);
  - delivery runs off ingest and the decision path.
- `AuditRecord.Sequence`: a gap is proof of a drop **except** across a
  retained prefix, which the control stream records.
- `PointAuditSink`'s catalogue entry stays **core**. Its `Absent` line still
  holds. Revise it only if it no longer does.
- `ext/README.md`'s out-of-tree sink still compiles and still reads true.

### 4. What an operator sees (`internal/httpapi/north`)
- `GET …/extensions`: each registration at the audit sink point gains its
  delivery lag for the caller's tenant. Use the same field names 0025 gave the
  archive's lag, so one renderer reads both: how many streams are behind their
  head, the `recorded_at` of the oldest record not yet delivered, and whether
  delivery is failing. Add the records skipped by retention since the sink
  last caught up (item 2). These are scalars, never a list of streams, so
  bounded lists (0028) inherits nothing unbounded.
- With no sink registered, the point's row is exactly what it is today.

### 5. The documents
- `docs/PLAN.md`, revised in place:
  - M8: Control delivers to registered sinks, at least once, off ingest and
    the decision path, and retention does not wait on a sink. Revise M8 in
    place, or record a new decision if you find this settles something M8
    does not, numbered when it is written. Either way, the register row says
    so;
  - §3: the layout's `internal/export/` line, which says Control ships
    Splunk, Sentinel and Elastic sinks, and the component list. Control
    delivers to registered sinks; the sinks are a host's (§11);
  - §7's Export bullet, revised to what is built;
  - §10's row, revised to what was built.

  §11 is unchanged: SIEM export itself stays Enterprise's.
- `internal/export/doc.go`, revised to what the package does.
- The console prompt (0034). Its extensions screen shows each sink's lag, and
  a sink that is failing or skipped by retention.
- `CHANGELOG.md`: the release this phase cuts, a MINOR (M23). No
  `### Breaking`, since no signature changes. The entry names what
  `AuditSink`'s doc comment now promises, including the concurrency contract,
  because an implementation written to the old comment may assume one call at
  a time.

## Out of scope
- **Enterprise's sinks** (its 0006): Splunk, Sentinel, Elastic, the export
  schema, mapping and saved searches.
- **Any Control-shipped sink.** The point stays core, and the local store is
  the record. A file or webhook sink here would be a capability M15 and §11
  give to Enterprise.
- **Retention that waits on a sink** (item 2). If a host needs that, it is a
  new request, and the archive (0025) is where it would belong.
- **Captures to a sink.**
- **Filtering per sink**, by kind, tenant or stream. A sink filters what it
  forwards.
- **A starting point other than the chain's start** for a new sink.
- **Delivery through `ext.ClusterCoordinator`**, and anything 0027 builds.
- **`contract/` (M1).** Nothing on the wire changes.

## Acceptance criteria
Each criterion is a test against Postgres, in the packages' own tests and over
the wire in `internal/httpapi/north`, with fake sinks registered through
`server.Options.Registry` where the daemon is involved.

**Delivery**
- Every record committed in two tenants, across the control stream and two
  proxy streams, reaches each of two fake sinks exactly once per stream in
  ascending sequence while they confirm. Each record is the one the archive
  receives, with its stream, predecessor's hash and hashed body, and the
  `ext` link function reproduces its `ChainHash`.
- A fake sink fails three batches and then accepts. The same records are
  offered again, its position moves only on success, and nothing is skipped
  or reordered.
- One fake sink never returns. The other sink's delivery is not delayed. A
  batch ingest with that sink registered completes within the bound it meets
  with none.
- A sink that fails only one tenant's batches does not delay the other
  tenant's delivery to it.
- A fake sink that detects overlap never sees two batches of one stream in
  flight, and does see batches of different streams in flight together, up
  to the configured bound.
- Concurrent ingest and delivery over many interleaved commits never skip a
  sequence.
- Two deliveries over one store, standing in for two nodes, deliver every
  record at least once to each sink and never move a position backwards.
- Killing delivery mid-batch and restarting it re-offers the unconfirmed
  batch.
- Positions are per sink. Two sinks hold different positions. A sink
  unregistered and registered again resumes from its own. A provider
  registered under a new name starts at the chain's start.
- Delivery writes nothing to the chain. A sink failing for a minute leaves the
  control stream's head where ingest put it.
- With no sink registered, no delivery exists and no sink position is
  written. With the archive registered and no sink, every 0025 test passes
  unchanged. The existing audit, retention and extension tests pass
  unchanged.

**Retention**
- With a sink behind, a retention pass deletes what it asked for. A sink does
  not gate it, and with an archive registered too, only the archive does.
- The sink then resumes after the marker. The skipped count is in its row and
  on its lag, the `WARN` is logged once, and the sink has received the
  `operator.audit.retained` record that names the range.

**What an operator sees**
- `GET …/extensions` shows each sink's lag for the caller's tenant, and only
  that tenant's, under the field names the archive's lag uses.

**The release**
- `make release-check` passes with no `### Breaking` entry, because no public
  signature changed. If generalising the feed changed one, it is declared.
  `go build`, `go vet`, `go test ./...` and `-race` on the changed packages
  pass, and `golangci-lint` at CI's pin reports 0 issues.
- `make contract-check`, `license-check` and `make conform` pass.

## Cross-repo impact
This phase answers an upstream request and changes what `ext/` says Control
does. So it owes a downstream sync. Its PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what
was built, and the session there that rewrites its prompts will be a fresh
one. State at least these obligations:

1. **Its 0006, "What Control does not do yet".** Rewrite it from what merged,
   and stop building around the gap. It needs:
   - the delivery semantics: per tenant and stream, ascending, at least once,
     and what a nil return and an error each mean;
   - that the position is Control's, per sink, tenant and stream, so a sink
     keeps no cursor and "resumption after a restart" is Control's to prove;
   - that a sink is named by its `Registration.Provider`, and where a new one
     starts;
   - the concurrency contract, and that `Export` must be safe for concurrent
     use;
   - that retention does not wait on a sink, and how a gap after a retained
     prefix is explained;
   - that delivery writes no audit record;
   - the lag on `GET …/extensions`, and its field names.

   Turn its skipped end-to-end criteria into real ones.
2. **Its plan.** §4's `AuditSink` row and its misreadings, and wherever E6 or
   E7 says what reaches a SIEM and how.
3. **Its pin.** Every obligation holds from the release this PR cuts (M23),
   never from `main`. Name that release by version.

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
`docs/learnings/0026-audit-sink-delivery-learnings.md`. Enterprise's sync is
written from its summary block, which MUST give:

- the delivery semantics, and where the shared feed now lives;
- where a sink's position lives, what names a sink, and where a new one
  starts;
- the concurrency contract;
- retention's relation to a sink, and how a skipped range is counted and
  explained;
- the lag fields, the configuration keys and their defaults;
- the release, and any decision added or amended, in one line.
