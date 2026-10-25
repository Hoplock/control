# Changelog

What each release of `github.com/hoplock/control` contains, newest first, for
anyone who depends on the module or runs the binary. Hoplock Enterprise reads it
before every bump of its pin (its E3).

What a release is, when one is cut, and what its number promises is decision
**M23** in `docs/PLAN.md` §2. It is not restated here.

**Adding an entry.**

- List what someone consuming the module or running the binary can observe. Do
  not list prompts, plans, learnings or CI.
- A phase releases in its own PR. Add a `## vX.Y.Z` section above the newest
  one, and move anything under `## Unreleased` into it. In the section:
  - `### Breaking`, when the release changes a public package incompatibly.
    `make release-check` finds those changes and prints them;
  - the phase or phases it contains, by number;
  - the changes to public packages, by identifier;
  - a behaviour change that no API diff can see, which nothing else catches.
- A change that does not release goes under `## Unreleased` until one does.
- A released section is what its tag says. Never edit one; correct it in the
  next release's section.
- `make release-check` says what merging the tree releases, and refuses a
  version number that does not match the change.

## Unreleased

## v0.20.0

Phase 0033, bounded lists 5: grants and grant requests.
`GET /api/v1/tenants/{tenant}/grants` and
`GET /api/v1/tenants/{tenant}/me/grant-requests` now answer in decision M26's
shape (`docs/PLAN.md` §2), one page at a time, newest first, and a grant's view
says when its sample of decisions was cut.

### Breaking

No signature changes; `make release-check` finds none. Five behaviour
changes:

- **Both lists answer at most 500 items by default, and refuse above
  10 000.** They answered 100 by default and clamped silently at 1 000.
  `?limit=` asks for 1 to 10 000; a page holds exactly that many unless it
  is the last. Above 10 000 is `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`; zero, a
  negative or a non-integer is `{field: limit, problem: invalid}`, as
  before.
- **Both answers gain `truncated` and, when cut, `next`.**
  `grants` is `{"tenant": "...", "grants": [...], "truncated": bool, "next": "<cursor>"}`,
  ordered by creation and then grant id, newest first;
  `me/grant-requests` is `{"tenant": "...", "requests": [...], "truncated": bool, "next": "<cursor>"}`,
  ordered by when each request was raised and then request id, newest
  first. Pass `next` back as `?after=` with the same `subject` and `state`
  to read the following page; a cursor under other filters is
  `400 invalid_request` `{field: after, problem: invalid}`. A client that
  read a whole list from one answer must now follow `next` until
  `truncated` is false. A requester's cursor names neither them nor the
  tenant: whoever presents it reads their own requests from that place.
- **A state filter is judged on every page.** `?state=` on `grants` is
  judged at the server's instant when each page is read, so a grant that
  expires or starts during a walk is listed if it matches when the walk
  reaches it, and not otherwise. It is never listed twice.
- **`GET …/grants/{grant}` gains `decisions_truncated`.** The view still
  samples the newest 50 decisions the grant supplied; the member is `true`
  when the grant supplied more. It takes no `limit` and no `after`: every
  record a grant backed is in the audit query.
- **Migration `0017` locks `grants` and `grant_requests` while it runs.** It
  builds three indexes in one transaction, which blocks writes to both
  tables (creating, revoking and deciding grants and requests) until it
  commits, and drops the three it replaces at the end, which blocks reads,
  the decision path's included, from the drops to the commit. Run
  `hoplock-control migrate` when that pause is acceptable; its length grows
  with the two tables.

- **Phase:** 0033, bounded lists 5: grants and grant requests.
- **Public packages:** none changed.
- **Migration:** `0017_grant_list_order` replaces `grants_by_created_idx`,
  `grants_by_subject_idx` and `grant_requests_by_subject_idx` with
  `grants_created_order_idx`, `grants_subject_order_idx` and
  `grant_requests_subject_order_idx`, each ending in the id, descending with
  the time before it. No row is rewritten.

## v0.19.0

Phase 0032, bounded lists 4: algorithm declarations and target observations.
`GET /api/v1/tenants/{tenant}/fleet/algorithms` now answers in decision M26's
shape (`docs/PLAN.md` §2), one page at a time, and the target observations,
which answered two lists side by side, are now two lists of their own.

### Breaking

No signature changes; `make release-check` finds none. Four behaviour
changes:

- **`GET …/fleet/target-observations` is withdrawn.** It answers
  `404 not_found`. Its two lists are now
  `GET /api/v1/tenants/{tenant}/fleet/target-observations/rungs`
  (`{"rungs": [...], "ttl_seconds": N, "truncated": bool, "next": "<cursor>"}`,
  by hostname, port and platform) and
  `GET /api/v1/tenants/{tenant}/fleet/target-observations/key-exchanges`
  (`{"key_exchanges": [...], "ttl_seconds": N, "floor_levels": [...], "truncated": bool, "next": "<cursor>"}`,
  by hostname and port), each taking `fleet:read` as the old route did. Each
  item is the one the old answer held, with its own `fresh`. `ttl_seconds`
  and `floor_levels` describe the list and are the same on every page.
- **A host route on either new path is refused at start-up**, as on every
  path Control serves: a host that served `GET` or `POST` on
  `fleet/target-observations/rungs` or `…/key-exchanges` no longer starts.
- **The three lists answer at most 500 items by default.** They answered the
  whole tenant. `?limit=` asks for 1 to 10 000; a page holds exactly that
  many unless it is the last. Above 10 000 is `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`; zero, a
  negative or a non-integer is `{field: limit, problem: invalid}`.
- **Each answer gains `truncated` and, when cut, `next`.** `fleet/algorithms`
  is `{"declarations": [...], "truncated": bool, "next": "<cursor>"}`. Pass
  `next` back as `?after=` to read the following page. A client that read a
  whole list from one answer must now follow `next` until `truncated` is
  false. A cursor whose port is outside a 32-bit integer is
  `400 invalid_request` `{field: after, problem: invalid}`.

- **Phase:** 0032, bounded lists 4: algorithm declarations and target
  observations.
- **Public packages:** none changed.
- **Behaviour no API diff shows:** a stored declaration on a page that does
  not decode fails that page with a `500` and its correlation id, as it
  failed the whole list before; a page never skips it.

## v0.18.0

Phase 0031, bounded lists 3: proxies and configuration rollouts.
`GET /api/v1/tenants/{tenant}/proxies` and
`GET /api/v1/tenants/{tenant}/fleet/config/rollouts` now answer in decision
M26's shape (`docs/PLAN.md` §2), one page at a time, as the target and subject
lists have since `v0.16.0`.

### Breaking

No signature changes; `make release-check` finds none. Three behaviour
changes on the two lists:

- **They answer at most 500 items by default.** They answered the whole
  tenant. `?limit=` asks for 1 to 10 000; a page holds exactly that many
  unless it is the last. Above 10 000 is `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`; zero, a
  negative or a non-integer is `{field: limit, problem: invalid}`.
- **Both answers gain `truncated` and, when cut, `next`.** They are
  `{"proxies": [...], "liveness": {...}, "truncated": bool, "next": "<cursor>"}`
  and
  `{"rollouts": [...], "fleet_owned_keys": [...], "truncated": bool, "next": "<cursor>"}`.
  `liveness` and `fleet_owned_keys` describe the list and are the same on
  every page. Pass `next` back as `?after=` to read the following page. A
  client that read the whole fleet from one answer must now follow `next`
  until `truncated` is false.
- **`?drifted=true` is now a cursor filter.** A cursor from the drifted view
  continues only the drifted view, and one from the whole view only the whole
  view; either presented to the other is `400 invalid_request`
  `{field: after, problem: invalid}`. Any other value of `drifted` is the
  whole view, as before.

- **Phase:** 0031, bounded lists 3: proxies and configuration rollouts.
- **Public packages:** none changed.
- **Behaviour no API diff shows:**
  - The drifted view is filtered as it is read, so a page of it holds its
    limit unless it is the last. It holds exactly the proxies with a
    published document (desired version not 0) that have not reported
    `applied` on that document. For every state Control writes, that is the
    set it answered before.
  - A page of the fleet view reads relay registrations and configuration
    states for its own proxies only, and a proxy's event subscription, on
    any node, for each proxy of the page. A failure in any of those reads is
    a `500` with its correlation id, never a shorter page.

## v0.17.0

Phase 0030, bounded lists 2: the shape for a host's lists. A host's list
can now answer in decision M26's shape (`docs/PLAN.md` §2) through the same
code Control's lists use, so one client reads both. Additive only; nothing
Control serves changes.

- **Phase:** 0030, bounded lists 2: the shape for a host's lists.
- **Public packages:** `server` gains, compatibly:
  - `server.DefaultListLimit` (500) and `server.MaxListLimit` (10 000), the
    bounds of every north-bound list;
  - `server.List`, one page a caller asked of a host's list (`Limit`,
    `After`);
  - `server.ReadList(w, r, name, keyLen, filters...)`, which reads `?limit=`
    and `?after=` and answers Control's `400 invalid_request` refusals;
  - `(server.List).Write(w, r, member, items, more, last, extra)`, which
    answers `{<member>: [...], "truncated": bool, "next": "<cursor>"}`.
  - `server.Route`'s doc comment now says a list is expected to answer
    through them. Nothing enforces it: Control cannot see which route is a
    list.
- **Behaviour no API diff shows:**
  - A host list's cursor names `<provider>:<name>`, and binds the filters the
    host passes. A cursor from one of Control's lists, from another of the
    host's, or under other filters is refused on a host list as
    `400 invalid_request` `{field: after, problem: invalid}`, and a host
    list's cursor is refused the same way on Control's lists. It never carries
    the tenant, nor on a cross-tenant read the set.
  - `ReadList` and `List.Write` fail closed as `server.WriteError` does: a
    call outside a host route, an empty list name, an order key of no
    components, a `List` that `ReadList` did not return, a member that is
    empty, `truncated` or `next`, an extra member colliding with any of the
    three, items that are not a JSON array, or `more` with no `last` or a
    `last` of the wrong length answers `500 internal` with its correlation id
    and logs `northbound_host_list_refused` at error, naming the problem.

## v0.16.0

Phase 0029, bounded lists 1: the mechanism, proven on targets and subjects.
`GET /api/v1/tenants/{tenant}/targets` and
`GET /api/v1/tenants/{tenant}/subjects` are the first lists to answer in
decision M26's shape (`docs/PLAN.md` §2), one page at a time, and every later
list takes the same path.

### Breaking

No signature changes; `make release-check` finds none. Three behaviour
changes on the two lists:

- **They answer at most 500 items by default.** They answered the whole
  tenant. `?limit=` asks for 1 to 10 000; a page holds exactly that many
  unless it is the last.
- **Both answers gain `truncated` and, when cut, `next`.** The answer is
  `{"targets": [...], "truncated": bool, "next": "<cursor>"}` (and the same
  with `subjects`). `truncated` is always present and `next` exactly when it
  is true. Pass `next` back as `?after=` to read the following page, under the
  same filters (`?label=` for targets). An empty page is
  `{"targets": [], "truncated": false}`. A client that read the whole list from
  one answer must now follow `next` until `truncated` is false.
- **`?limit=` above 10 000 is refused**, as `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`. Zero, a negative
  or a non-integer is `{field: limit, problem: invalid}`.

- **Phase:** 0029, bounded lists 1: the mechanism, targets and subjects.
- **Public packages:** none changed. `server` offers the shape to a host in
  phase 0030.
- **Behaviour no API diff shows:**
  - A cursor is opaque, never expires, and holds nothing the caller was not
    handed. It continues only the list, filters and order it was minted for;
    any other is `400 invalid_request` `{field: after, problem: invalid}`,
    and the client starts the list again. It never carries the tenant: under
    another tenant's path it continues that tenant's list, which the
    caller's credential must already reach.
  - A page is stable under writes: an item added behind the position is not
    seen, one added ahead of it is, and the item the position names may be
    gone.
  - A failure while reading a page is a `500` with its correlation id, never
    a shorter page.

## v0.15.0

Phase 0028, bounded lists: audit and plan. **This release changes no
behaviour.** No handler, store method, response shape or public identifier
changed, and `make release-check` finds no API difference.

What it plans, for anyone reading Control's north-bound lists or building a
host's:

- **Every north-bound list is to be bounded by its caller and say where it
  stopped** (decision M26 in `docs/PLAN.md` §2).
  - `?limit=` defaults to 500 and is refused above 10 000.
  - `?after=` continues from an opaque cursor.
  - The answer is `{<list>, truncated, next}`.
- Phases 0029–0037 implement it, each in a release of its own. Each lists its
  own `### Breaking`, and between them those breaks will include:
  - lists cut at 500 by default;
  - limits above 10 000 refused where they were clamped;
  - `GET …/fleet/target-observations` split into `…/rungs` and
    `…/key-exchanges`.
- Phase 0030 adds the same shape to `server` for a host's lists.

## v0.14.0

The shared event plane. The event stream (`GET /v1/proxies/{proxy_id}/events`)
is the same whichever node a proxy reaches: a revocation or a `config_changed`
published on one node reaches a proxy whose stream another node holds, a proxy
reconnecting to any node — or after a restart — is replayed what it missed, and
the cache-hint gate sees a stream held on any node. Control on one node takes
the same path, through its single-node coordinator.

### Breaking

No signature changes; `make release-check` finds none. These change what an
implementation of `ext.ClusterCoordinator` is relied on for:

- **`ext.ClusterCoordinator`'s bus is now called.** Every node publishes a
  wake-up on topic `hoplock.events.logged` (kind `logged`, the tenant set, a
  small JSON payload naming the position) each time it logs an event, and
  every node subscribes to that topic for its lifetime. Nothing called
  `Publish` or `Subscribe` before.
- **Its doc comment now states what the bus must deliver**, under "What the
  bus must deliver": at least once to every subscription open when `Publish`
  was called, on every node including the publisher's; in any order, with
  duplicates allowed; a subscription the bus cannot keep up with ended by
  closing its channel after what it already holds, never left open with
  events skipped; closed when its context ends or it is cancelled; payloads
  delivered as published, `Origin` included; `Publish` failing promptly
  rather than hanging. `Node` must return an id unique among live members and
  stable for the process, because Control keys the streams a node holds by
  it. An implementation that silently drops events to a slow subscriber
  delays revocations reaching that node by up to `events.sync_interval`.
- **Control's single-node coordinator closes a subscriber it drops.** It used
  to discard the event and keep the subscriber; a subscriber now sees its
  channel close once it falls 64 events behind.
- **Event ids changed shape**, from `evt-<run>-<sequence>` to
  `evt-<epoch>-<position>` (and `evt-<epoch>-<position>.<line>` on a heartbeat
  and on the `resync` answering a reconnect). Ids are opaque to a proxy, which
  only echoes one back, so nothing breaks; every proxy reconnecting for the
  first time after the upgrade presents an id the new log did not mint and is
  answered `resync` once, as it was after every restart before this release.

- **Phase:** 0027, the shared event plane.
- **Public packages:** `ext.ClusterCoordinator` and `ext.ClusterEvent` doc
  comments, above. `server` is unchanged.
- **Behaviour no API diff shows:**
  - Migration `0016`: `event_log` (every event a proxy may be replayed, with
    its audience), `event_streams` (each tenant's epoch, head and floor; its
    row is the tenant's publish lock), `event_subscriptions` (which proxy
    holds a stream on which node), and the sequence `event_log_seq`. Run
    `hoplock-control migrate` before starting this release.
  - A publication answers once the event is committed to the log. A node that
    dies after answering loses nothing; one whose wake-up is lost delays the
    other nodes by at most `events.sync_interval`.
  - A restart replays rather than resyncs: a proxy reconnecting with an id
    still in the log is replayed what it missed, on any node.
  - `events.replay_buffer` (default 1024) is now per tenant and kept in the
    store rather than one ring per process. A `config_changed` to every proxy
    of a large zone is one event per proxy, so size it to cover that.
  - New configuration: `events.sync_interval` (default 1s, or
    `events.heartbeat_interval` when that is shorter; refused above
    `events.heartbeat_interval`): how often a node reads the log for events no
    wake-up told it of, and writes and reads the deployment's subscriptions.
  - The cache-hint gate and the fleet's liveness read whether a proxy holds a
    stream on any node: this node's own and a copy of every other node's, at
    most two sync intervals late, from memory and never the store. The gate's
    read is now one lookup per hinted answer; it used to copy the tenant's
    whole subscription map per call.
  - `POST …/revocations` answers `delivered` as the subscriptions on the node
    that took the request; proxies whose streams other nodes hold receive the
    event from the log and are not counted.
  - A `config_changed` is settled (`announced_hash`) once the stream logs it,
    wherever the proxy's stream is held, and any node's announcer retries one
    the stream never took. Before, only a delivery to a stream on the
    announcing node settled it.
  - A clean shutdown removes the node's rows from `event_subscriptions`. New
    `WARN` lines: `revoke_wake_failed`, `revoke_wake_dropped`,
    `revoke_wake_subscribe_failed`, `revoke_fanout_deferred`,
    `revoke_catch_up_failed`, `revoke_presence_read_failed`,
    `revoke_presence_write_failed`, `revoke_presence_remove_failed`.

## v0.13.0

Audit sink delivery. With one or more `ext.AuditSink`s registered, Control
hands each of them every record it commits, keeps a durable position per sink,
and offers again what a sink did not confirm. A sink that is down or slow
slows nothing but its own delivery. Retention does not wait on a sink. With no
sink registered, nothing changes. The archive's feed now bounds the calls in
flight to the archive the same way.

- **Phase:** 0026, audit sink delivery.
- **Public packages:** no signature changes. `ext.AuditSink`'s doc comment now
  says what Control does with one, and `ext.AuditRecord.Sequence`'s says when a
  gap is not a drop. An implementation written to the old comment should check
  these:
  - **`Export` is now called, concurrently.** Control may have batches of
    different streams or tenants in flight to one sink at once, up to
    `audit.sinks.max_in_flight` (default 4), so `Export` must be safe for
    concurrent use. It never has two batches of one stream in flight, and
    offers a stream's next batch only after the previous one returned. The
    old comment implied one call at a time.
  - Each batch is one tenant's and one stream's records, in ascending
    `Sequence`, at least once; every tenant's records go to every sink. A nil
    return confirms the batch; an error confirms nothing, and the same
    records are offered again after a backoff. `Export` must be idempotent
    on `RecordID`.
  - The position is Control's, in its store per sink, tenant and stream; a
    sink keeps no cursor. A sink is named by its `Registration.Provider`: one
    that registers again under the same name resumes from its positions, and
    a new name is a new sink, starting each stream at its chain start.
  - A call past its deadline confirms nothing and keeps one of the sink's
    places in flight until it returns, so a sink that ignores its deadline
    stalls only itself.
  - **Retention does not wait on a sink.** When a retention pass removes
    records a sink has not confirmed, the sink resumes after the pass's
    marker. The gap is explained by the pass's `operator.audit.retained`
    record on the control stream, which the sink is handed too.
  - Delivery writes no audit record, and captures' bytes never cross to a
    sink.
  - **`ext.ArchiveStore`'s doc comment now states its concurrency**, which it
    left out: `Archive` and `ArchiveCaptures` may be called concurrently for
    different streams, never two calls for one stream at once, so both must
    be safe for concurrent use. Control has called them from up to four
    workers at once since `v0.12.0`; this says so, and adds no requirement.
- **Behaviour no API diff shows:**
  - Delivery runs off ingest and the decision path, on the archive's feed
    with positions of its own (migration `0015`: `audit_feed_positions` gains
    `skipped_records`, `skipped_behind` and `failing_since`; a sink's rows are
    keyed `sink:<provider>`). Each stream is delivered by one node at a time,
    by the lease 0025 added, so it is correct on several nodes. One sink,
    tenant or stream that fails, hangs or lags slows no other.
  - A sink that starts failing for a stream is logged once at `WARN`
    (`audit_sink_failing`, with `provider`, `tenant`, `stream` and `kind`)
    and once when it recovers (`audit_sink_recovered`). A sink a retention
    pass overtakes is logged once at `WARN`
    (`audit_sink_skipped_by_retention`, with `from_seq`, `through_seq` and
    `records`). The start-up line `audit_sink_delivery_configured` names
    every sink.
  - New configuration, applied to every sink alike, each with a default and
    bounded at start-up: `audit.sinks.batch_records` (500, at most
    `audit.max_batch_records`), `attempt_timeout` (30s), `idle_interval`
    (5s), `backoff_cap` (5m, at least 1s), `max_in_flight` (4, at least 1).
  - `GET …/extensions`: each registration at the audit sink point gains `lag`
    for the caller's tenant — `streams_behind`, `oldest_unconfirmed_at`,
    `failing`, and `skipped_by_retention` (records a pass removed before the
    sink confirmed them, since it last caught up). The archive's `lag` gains
    `failing`. With no sink registered, the sink point's row is unchanged.
  - **The archive's feed is bounded the way a sink's is.** Its four workers
    are now a bound on calls in flight to the archive,
    `audit.archive.max_in_flight` (4, at least 1), and a call past
    `audit.archive.attempt_timeout` keeps its place until it returns. Before,
    an abandoned call freed its worker, so an archive that hung could gather
    one stuck call per stream. Now an archive that hangs on every place
    stalls its own feed, and retention waits on it as it always did.
  - Recording a failure of the archive's feed now also marks its position
    row failing, which is what the archive's `failing` reads.

## v0.12.0

An archive fed before retention deletes. With an `ext.ArchiveStore`
registered, Control hands it every record it commits and every session
capture, keeps how far the archive has confirmed each stream, and never
deletes a record, or empties a capture, that the archive has not confirmed.
With no archive registered, nothing changes.

### Breaking

- **`ext.ArchiveStore` gains `ArchiveCaptures(ctx context.Context, batch
  []ext.Capture) error`.** An implementation must add it, or it does not
  compile against this release. It must store each capture's bytes durably
  before it returns nil, and be idempotent on `Capture.RecordID`: Control
  offers a capture only after `Archive` confirmed its record, offers it again
  after an error, and empties the local bytes only once it has been
  confirmed. Each `Body` is valid only until the call returns.
- **`ext.ArchiveStore` is now called.** A registered archive was handed
  nothing before; now `Archive` receives every committed record, one stream's
  records per batch in ascending `Sequence`, at least once, and a nil return
  confirms the batch. An implementation that answered nil without storing
  lets Control delete what it never kept.
- **With an archive registered, retention waits on it.**
  `POST …/audit/retention` by `through_seq` beyond what the archive holds —
  every record through it, and every capture of those records — is
  `409 retention_not_archived` with `stream` and `archived_through_seq`, and
  deletes, seals and records nothing. By `before`, the pass deletes no
  further than the archive holds. `POST …/audit/captures/retention` runs no
  further than the earliest capture the archive has not confirmed, signs its
  watermark over the instant it ran to, and empties only confirmed captures.

- **Phase:** 0025, an archive fed before retention deletes.
- **Public packages:**
  - `ext.AuditRecord` gains `Stream` (the chain the record belongs to:
    `control`, `proxy:<id>`, `unattributed`), `PrevHash` and `Body` (the
    canonical JSON the chain hashed). `Sequence` is a position in that
    stream, not in the tenant's chain as its doc comment said, and
    `ChainHash` is now verifiable from the record alone.
  - `ext.ChainLink(tenant, stream, sequence, prevHash, body) string` computes
    a record's link exactly as Control's chain does, and is the code Control
    computes it with; `ext.ChainVersion` (`hoplock.chain/1`) is inside every
    link. The encoding is in `ChainLink`'s doc comment, and a later one is a
    new version, never a change to this one.
  - `ext.Capture`: `Tenant`, `RecordID`, `SessionID`, `Digest` (the record's
    `capture_sha256`, `sha256:<hex>`), `Size` (its `capture_bytes`) and
    `Body`.
  - `ext.ArchiveStore`'s doc comment says what Control does with one.
    `Search` is still called nowhere.
- **Behaviour no API diff shows:**
  - Delivery runs off ingest and the decision path. Each stream is fed by
    one node at a time, by a lease in its position row (migration `0014`:
    `audit_feed_positions`), so every node may run the feed, and positions
    and leases are correct on several nodes without the shared event plane.
    A call to the archive that outlives `audit.archive.attempt_timeout` is
    abandoned and retried, a failing stream backs off from a second to
    `audit.archive.backoff_cap` and holds back no other, and a stream that
    starts failing is logged once at `WARN` (`archive_feed_failing`) and once
    when it recovers (`archive_feed_recovered`).
  - A stream retained before an archive was registered is fed from after its
    marker; the `operator.audit.retained` record on the control stream says
    where the gap is.
  - Captures carry a confirmation of their own (`audit_captures.archived_at`).
  - New configuration, each with a default and bounded at start-up:
    `audit.archive.batch_records` (500, at most `audit.max_batch_records`),
    `capture_batch_bytes` (16 MiB, at least `audit.max_capture_bytes`),
    `attempt_timeout` (30s), `idle_interval` (5s), `backoff_cap` (5m, at
    least 1s).
  - Both retention answers gain `held_by_archive` (`false` with no archive
    registered); the prefix pass's gains `archived_through_seq` while one is.
    `operator.audit.retained` carries `operator.held_by_archive` and, for a
    prefix pass, `operator.archived_through_seq`, while one is.
  - `GET …/extensions`: the archive's row gains `lag` for the caller's
    tenant while one is registered — `streams_behind`,
    `oldest_unconfirmed_at` and `captures_waiting`.
  - New code: `retention_not_archived` (409).

## v0.11.0

Signed retention. A retention pass signs what it leaves behind with a
per-tenant key the database does not hold in usable form, and `audit-verify`
refuses a marker, or a removed session capture, whose signature does not
check. A writer of the database alone can no longer delete a stream's oldest
records — or empty a capture — and make it verify as lawful retention.

### Breaking

- **Retention is refused without a key store.** With no
  `credential.key_encryption_key_env` and no registered `ext.KeyStore`,
  `POST …/audit/retention` and `POST …/audit/captures/retention` answer
  `503 retention_key_not_configured` with `config_key:
  credential.key_encryption_key_env`, and delete nothing. Both passes ran
  unsigned before.
- **Retention written by `v0.6.0`–`v0.10.0` no longer verifies, and nothing
  builds on it.** Nothing can tell an unsigned marker from a forged one, so it
  is never silently trusted:
  - `hoplock-control audit-verify` reports a stream whose marker is unsigned
    as `BROKEN`, a `retention-marker` break naming the marker's sequence and
    hash, and exits 1. With `--captures` it does the same for each capture
    emptied by a pass that signed nothing.
  - A retention pass over such a marker, or over such captures, is
    `409 retention_unverified` (`stream`, or `captures: true`) and deletes
    nothing. So is a pass over a stream that no longer starts where its marker
    says.
  - No path signs them after the fact. Nothing is deployed yet, so a
    database carrying such retention is a development or test one, and is
    rebuilt rather than migrated.
- **`audit-verify` and `audit-retention` hand a host's configuration sections
  to `HostConfig` and seal its registry,** as the daemon does, so they check
  and sign with a host's `ext.KeyStore`. Every other subcommand still never
  calls `HostConfig`.

- **Phase:** 0024, signed retention markers.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `server.Main` and `server.Options.HostConfig` name the
  subcommands that call `HostConfig`.
- **Behaviour no API diff shows:**
  - A registered `ext.KeyStore` now holds keys: one Ed25519 key per tenant,
    `audit-retention:<generation>`, generated on the first pass. Without one,
    Control's software custodian holds them, encrypted under the
    key-encryption key. The certificate authority still uses the software
    custodian only.
  - Each marker stores the key that signed it and the signature
    (migration `0013`: `audit_retention.key_name`, `signature`), so a marker
    signed before a rotation still verifies after it. A destroyed key makes
    every marker it signed a `retention-marker` break naming the key.
  - Capture passes keep a signed per-tenant watermark
    (`audit_capture_retention`). A capture counts as retained only when its
    record's own timestamp, read from the hashed body, is before it.
  - A capture pass whose `before` is ahead of the clock runs to now, and its
    answer's `before` says so. Both retention answers gain `key`, the key that
    signed. `operator.audit.retained` carries `operator.key` and
    `operator.signature`, and a capture pass's also `operator.watermark`.
  - `hoplock-control audit-retention key | rotate-key`: show the tenant's
    retention key and its public half, and make a new generation (recorded as
    `operator.audit.retention_key_rotated`).
  - New codes: `retention_key_not_configured` (503) and
    `retention_unverified` (409).

## v0.10.0

The fourth and last audit of `v0.6.0`: the north-bound surface — its routes,
roles, error codes, the record every write leaves, each handler — and what
`v0.6.0` and its pull request said, checked against the code, with every
defect fixed under a test that failed first.

### Breaking

- **A host route on a path Control serves through a wildcard is refused at
  start-up.** `server.Main` and `server.Run` refused a host pattern equal to
  one of Control's, a method beside one, or one the mux found ambiguous, but
  accepted a literal under one of Control's wildcards — `targets/special`
  beside `targets/{target}`, `access-context/bindings/push` beside
  `access-context/{provider}/push` — and the mux then routed that path to the
  host. No identifier changed.
- **`PUT …/subjects/{subject}` refuses a claim the claim mapping would
  refuse** (`400`, `field: claims.<name>`, `problem: reserved` or `invalid`):
  `chain_hop_proxy_id`, any `hoplock.*` name, and a name outside the attribute
  pattern. A stored claim is a policy attribute on every decision for the
  subject, so a reserved name stored here stood beside every login as if a
  proxy's key had authenticated a chain leg.
- **Refusals that changed status or field:**
  - a body past the listener's limit is `413 payload_too_large` with
    `limit`; it was `400 invalid_request`, `field: body`;
  - `POST …/revocations`: a `host_key_withdraw` with a blank `target` or
    `fingerprint` is `400` naming `host_key.target` or
    `host_key.fingerprint`, where it was `409 host_key_unresolved`; a
    selector, reason or audience refusal names `session_ids`, `keys`,
    `reason` or `proxy_ids` rather than `body`;
  - `POST …/targets/relabel`: a change naming no target, or a target a
    second time, is `400` naming `changes[<i>].target`; a target that is not
    there is `404` with `target`;
  - `POST …/audit/retention` with a negative `through_seq` is `400`; it was
    read as absent.
- **The fleet configuration records drop `operator.unannounced`.** The
  record is now written in the publish's transaction, before anything is
  announced; which announcements the stream took is in the answer, as it
  was. The record gains `before`: the version the publish displaced.

- **Phase:** 0023, the audit of 0019's north-bound surface and of the record.
- **Public packages:** no identifier is added, removed or changed. The doc
  comment of `server.Route` names the paths a host may not take.
- **Behaviour no API diff shows:**
  - Three writes now commit with their record: `PUT …/identity/claim-mapping`
    (which also stored and activated the version in two statements),
    `POST …/proxies/enrollments`, and `PUT …/fleet/config/{scope}/{id}` with
    its `rollback`. Each was recorded after it committed, so a failed record
    left a change the chain did not state.
  - A target, subject or claim-mapping write reads what it replaces under the
    tenant's lock for that collection, so concurrent writes record a chain of
    befores. A relabel's write re-reads each target under that lock and
    changes only its labels: a hostname, zone or credential method written
    while it simulated is kept, where it was overwritten with the values the
    preview read.
  - `GET …/audit/negotiated` answers `truncated`, true when its limit cut the
    list short: the runbook's next step ends exactly the sessions listed. An
    `axis` without its `value`, or a `ban_axis` without its `ban_name`, is
    `400` naming the missing one; it was a `500`.
  - No refusal quotes a Go operation, a package's error text, or the
    database's: `this query cannot be answered: store.Audit.RetainPrefix:
    invalid: …` is `this query cannot be answered: the head of stream … is
    never retained`.
- **What `v0.6.0` stated wrongly or left out:**
  - its route list omitted `PUT …/identity/claim-mapping` (`identity:write`);
  - its `### Breaking` did not say that a host route on a path it began
    serving, or a host code equal to one of its eight new codes, is refused at
    start-up — a host binary that started under `v0.5.0` could fail to start;
  - "every operator act is recorded" held, but three of those records were
    written outside the act's transaction, against PLAN §3's rule (above).

## v0.9.0

The third audit of `v0.6.0`: the policy lifecycle — the publish-time report,
upload, activation, simulation, explain and `policyctl` — checked against the
code, with every defect fixed under a test that failed first.

### Breaking

- **A validator finding whose `ext.FindingSeverity` is none of the three is an
  outage.** Validate, upload and activate answer it as a validator error
  does (`5xx`), naming the provider, the code and the value. It was rendered
  `advice` (`FindingSeverity.String`'s default), which gates nothing, so a
  governance rule could pass silently.
- **Two activation-gating warnings arise where they did not.** A cache hint on
  a rule matching `context.source_cidrs` is the warning
  `authoring.cache_hint_spans_sources`, whatever bounds the session: a proxy
  reuses a cached decision by identity, target, port, method and hop trail,
  never by client address. And `algorithm.policy_unmet` now warns every allow
  route reaching a target with a stored `target.algorithm_policy_unmet` under
  its profile, including a route naming no floor and no ban (it warned only
  those that named one). A bundle that activated under `v0.8.0` may now need
  these accepted by id to activate again.
- **`POST …/policy/simulate` refuses a dry-run case that would ask a
  different question** (`400`): a grant with no `expires_at`, which was never
  live, and a grant `origin` or a `subject.auth_method` outside the engine's
  vocabulary, which matched no rule naming one.

- **Phase:** 0022, the audit of 0019's policy lifecycle.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `ext.PolicyBundleRef.Version`, `ext.PolicyDocument` (and its
  `AuthorID`, `ProposedAt`, `ActiveVersion`), `ext.FindingSeverity` and
  `ext.PolicyValidator` now say what Control does: a validator runs only on a
  source that compiles, at validate, upload and activation; `Version` is
  empty until a version is stored; `AuthorID` is the subject asking for that
  judgement, empty for an API token; `ProposedAt` is that call's time;
  `ActiveVersion` is the version serving when asked; Control's own
  publish-time checks run beside the compiler's when no validator is
  registered.
- **Behaviour no API diff shows:**
  - Uploads and activations are serialised per tenant. Identical sources
    uploaded at once are one version, found by digest rather than by reading
    every stored source. Concurrent activations each record the version they
    replaced as `before`, and one that finds its version already active
    records nothing; the loser of a race was a `5xx` from a unique index.
  - A ban is a typo warning (`algorithm.ban_unmatched`) only when at least one
    build has declared what it offers. With none, the ban is advice
    (`algorithm.declaration_unknown`) and the emergency runbook's ban
    publishes without an acceptance.
  - A read of the stored unmet records that stops at its limit (1000, newest
    first) says so: advice, `algorithm.policy_unmet_truncated`.
  - Replay lists a record whose recorded instant is missing, or whose source
    address or grant instant does not parse, as unreplayable with its reason;
    it was replayed with zero values in their place.
  - Explain answers `500 decision_unexplainable` for five more partial
    stories: inputs with no instant, an effect this server never writes, an
    allow the engine did not make, an allow with no snapshot, and an unserved
    record that does not say why.
  - The all-added text diff no longer counts a line after the final line
    break.
  - `policyctl` prints a tab or a line break inside a field as a space, so a
    record is one line. `simulate` with neither `-from` nor `-cases`, or with
    `-to` alone, is a usage error (exit `2`); it exited `1`.

## v0.8.0

The second audit of `v0.6.0`: the audit store, retention and fleet
configuration delivery, checked against the code, with every defect fixed
under a test that failed first.

- **Phase:** 0021, the audit of 0019's audit store, retention, fleet
  configuration, store, configuration and daemon wiring.
- **Public packages:** no identifier is added, removed or changed. `ext` and
  `server` are unchanged.
- **Migration:** none. The audit columns and `audit_retention` are as `v0.6.0`
  left them.
- **Behaviour no API diff shows:**
  - A retention pass (`POST …/audit/retention`, `POST
    …/audit/captures/retention`) and its `operator.audit.retained` record
    commit in one transaction. A pass whose record cannot be written deletes
    nothing and moves no marker. Before, the deletion committed first, so a
    failure between the two left a deletion nothing recorded.
  - `POST …/audit/retention` answers `through_hash` on every answer. A pass
    by `before` that took nothing now carries it too: the marker's hash, or
    empty when the stream was never retained.
  - `GET …/audit/negotiated`'s `limit` counts sessions. It counted records,
    so a session with several matching records could crowd others out of
    the answer the runbook hands to `session_kill`.
  - `GET …/audit/sessions/{session}/certificates` and `…/algorithms` read
    every record of the session that carries a serial or an algorithm. They
    read the session's first 500 records and missed anything after them.
  - The negotiated-value, ban, refused-record-id and certificate-serial
    filters of the audit query use the indexes `v0.6.0` built for them.
    Before, each one read every record in the tenant.
  - A proxy whose zone and own scope have nothing published desires no
    version. Its fetch answers `204`, it is sent no `config_changed`, and the
    rollout view does not show it as drift, even after it reports its
    bootstrap file applied. Its first document is `v1`. `v0.6.0` stored the
    empty composition as `v1`: it announced that version, which the fetch
    then denied, and showed the proxy behind it for good. A proxy enrolled
    under `v0.6.0` or `v0.7.0` with nothing published keeps that `v1` until
    its scope is published. Its fetch now answers `200` with no settings,
    which the contract treats as the bootstrap file.
  - A tenant in which a fleet configuration is published or rolled back is
    one the announcer retries in. A lost announcement there was retried only
    once a proxy of that tenant subscribed on the same node.
  - A `config_changed` counts as announced only when a proxy's live event
    stream on the node took it, or the proxy's report names the document.
    Each node's announcer delivers to the proxies whose streams it holds, so
    a publish on one node reaches a proxy subscribed on another within one
    announce interval (15 s). A publish answers `announced: false` for a proxy
    not subscribed on the publishing node, saying why, where it answered
    `true` because the event was retained. Revocations still reach only the
    publishing node's subscribers, so Control still runs as one node.

## v0.7.0

The first audit of `v0.6.0`: the south-bound surface and the decision path,
checked against the code, with every defect fixed under a test that failed
first.

### Breaking

- **`cmd/pdpconform` expectation files must name `algorithms.bans`:** the ban
  route's bans as authored, one list per axis. A file without it is refused
  at start-up, as any missing key is, because the case that compares the
  answer with it would otherwise grade nothing.
- **`south.max_log_body_bytes` below one storable record is refused at
  start-up:** below `audit.max_capture_bytes` as base64, plus
  `audit.max_record_bytes`, plus 4 KiB (`config.MinLogBodyBytes`). A body over
  the limit is a `400` the proxy cures by halving the batch, which a batch of
  one record cannot be, so such a limit lost that record. The shipped bounds
  need about 1.4 MiB under the 4 MiB default, so a configuration that leaves
  all three keys at their defaults is unchanged.

- **Phase:** 0020, the audit of 0019's south-bound surface and decision path.
- **Public packages:** no identifier is added, removed or changed. `ext` and
  `server` are unchanged.
- **Behaviour no API diff shows:**
  - A policy bundle may ban an algorithm name made of blanks. It was refused
    as `route.algorithm_ban_empty`, which the proxy reserves for the empty
    name; it is now a name no build offers, published with the
    `algorithm.ban_unmatched` warning like any other.
  - `POST /v1/credentials/certificate` mints only on a decision inside the
    window a session may be served under it: its cache hint's TTL, or none,
    plus two minutes (`credential.DecisionSetupAllowance`). An older
    `decision_id` is `401 unknown_decision`. A conforming proxy cites a
    decision only while it may reuse it, so it never sees this.
  - A proxy's algorithm declaration, as `GET …/fleet/algorithms` and the
    publish-time report read it, follows the build the proxy runs within a
    minute on every node. A node used to remember its own last write for as
    long as it ran, so after a rollback the stored copy could keep another
    node's newer build indefinitely.
- **Conformance suite:** the error-discipline group grades the certificate
  endpoint and both fleet configuration routes for a rejected token. New
  cases: a ban answered exactly as authored, and unchanged when the request
  declares `capabilities.algorithm_profiles`; a key-exchange report and a
  session-less priority record answering `accepted: true`; a certificate's
  `valid_before` inside the route's `lifetime_seconds`; and a `logging.gap`
  accepted on the path its severity picks.

## v0.6.0

The north-bound surface is complete: an operator, a pipeline or `policyctl`
authors, simulates, publishes and explains policy, manages the inventory,
queries and retains audit, and acts — and a proxy can be sent brokered
certificates, an algorithm floor and bans, and fleet configuration.

### Breaking

- **Configuration keys removed:** `events.publish_listener`,
  `events.publish_token`, `audit.read_listener` and `audit.read_token`. The
  two debug listeners they bound are deleted, and a configuration naming any
  of the four is refused at start-up as an unknown key. What they served is
  on the north-bound listener under a scoped API token:
  `POST /api/v1/tenants/{tenant}/revocations` (`session:revoke`) and
  `GET /api/v1/tenants/{tenant}/audit/records/{record_id}` (`audit:read`).
- **`ext.FindingWarning` stops activation until it is accepted.** It used to
  block nothing. A warning — from Control's own checks or a registered
  `ext.PolicyValidator` — now holds activation until the author accepts it by
  the finding's id, and the acceptance is on the activation's audit record. A
  validator that meant a finding as information returns `FindingAdvice`. No
  identifier changed, so no API diff shows this.
- **Audit columns renamed:** `audit_records.target_auth_method` and
  `target_auth_rung` are `credential_method` and `credential_rung`, the only
  names the proxy emits (migration `0012`). SQL naming the old columns fails.

- **Phase:** 0019, the north-bound API, inventory and policy lifecycle.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `ext.FindingSeverity`, `ext.FindingWarning`,
  `ext.FindingBlocking` and `ext.Finding.Code` say what each severity does at
  activation and that `Code`, with the provider and `Path`, forms the id a
  warning is accepted by.
- **North-bound routes**, each under `/api/v1/tenants/{tenant}/`: the policy
  lifecycle (`policy/validate`, `policy/bundles`, `policy/bundles/{version}`,
  `…/diff`, `…/activate`, `policy/diff`, `policy/active`, `policy/apply`,
  `policy/simulate`); explain (`decisions/{decision}`,
  `sessions/{session}/decisions`); inventory (`targets`, `targets/relabel`,
  `subjects`, `role-bindings`, `proxies`, `proxies/enrollments`,
  `proxies/{proxy}/withdraw`, `fleet/config/{scope}/{id}`, its `rollback`,
  `fleet/config/rollouts`, `fleet/algorithms`, `fleet/target-observations`);
  the audit query (`audit/records`, `audit/blocked-commands`, `audit/drift`,
  `audit/algorithm-policy-unmet`, `audit/negotiated`, `audit/gaps`,
  `audit/sessions/{session}/loss`, `…/algorithms`, `…/certificates`,
  `audit/certificates/{serial}/records`) and retention (`audit/retention`,
  `audit/captures/retention`); `revocations`; `extensions`; and
  `access-context/providers`, `access-context/bindings` and
  `access-context/providers/{provider}/binding`.
- **Permissions:** `target:write`, `audit:retain`, `session:revoke`,
  `access-context:read`, `access-context:write` and `extension:read` are
  added, and a host route may name any of them. Every reading role gains
  `access-context:read` and `extension:read`; `policy-author` and
  `fleet-admin` gain `target:write`; `grant-admin` gains `session:revoke`;
  `admin` holds all. `audit:retain` and `access-context:write` are the
  administrator's alone.
- **Error codes:** `policy_invalid` (`422`), `findings_not_accepted` (`409`),
  `policy_blocked` (`422`), `decision_unexplainable` (`500`),
  `config_key_not_fleet_owned` and `config_value_invalid` (`422`),
  `host_key_unresolved` (`409`) and `unavailable` (`503`).
- **South-bound:** the contract is vendored at `4.7.0` (`hoplock/proxy@976faaa`)
  and this server speaks `policy_version` `6`. `POST /v1/credentials/certificate`
  issues a brokered certificate per session; `GET /v1/proxies/{proxy_id}/config`
  and `POST …/config/report` are served, and a publish announces
  `config_changed`. A route naming `brokered-certificate` is refused to a proxy
  declaring below `5`, and one with a floor or a ban below `6` — a `5xx`
  naming both numbers, never a thinned answer.
- **Binary:** `bin/policyctl` is built beside the server: `validate`, `diff`,
  `apply`, `activate`, `versions`, `simulate`, `explain`, exiting `0`, `1` for
  a refusal and `2` for a usage or transport error.
- **Configuration added:** `policy.interpreters` (the executables a restricted
  allow-list warns on; empty takes the shipped list, and a list replaces it) and
  `south.max_log_body_bytes` (the two log paths' body limit, 4 MiB).
- **Migration:** `0012` adds the algorithm declarations, the key-exchange
  observations, the configuration report and announcement columns, the audit
  record projections, `audit_retention`, and `audit_captures.retained_at`.
- **Behaviour no API diff shows:** `session_id: ""` is accepted on every
  record kind and never looked up as a session. Every operator act is recorded
  as `operator.<noun>.<verb>` on the control stream. A retention pass deletes
  a prefix of a stream and `audit-verify` resumes after it. A proxy's running
  configuration comes only from its config report; the heartbeat no longer
  carries one.

## v0.5.0

A host serves a read that spans tenants, and Control resolves which tenants it
covers.

- **Phase:** 0018, the cross-tenant access class.
- **Public packages:** `server.Access` is added, with `server.AccessTenant`
  (the zero value: what every host route was before) and
  `server.AccessCrossTenant`. `server.Route.Access` is added; a `Route` that
  does not set it is unchanged. `server.Caller.Tenants` is added: the set
  Control resolved on a cross-tenant route, sorted and never empty, and nil on
  a tenant route, where `Caller.Tenant` is the one; `Caller.Tenant` is empty
  on a cross-tenant route. `ext` is unchanged.
- **North-bound routes:** Control adds none of its own. A host's
  `AccessCrossTenant` route is mounted at `/api/v1/cross-tenant/<Pattern>`.
  With no selector it covers every tenant in the caller's scope where the
  caller holds the route's permission, and `403 forbidden` naming the
  permission when there is none. A `tenant` query value or an
  `X-Hoplock-Tenant` header narrows that set, never widens it: a named tenant
  out of scope is `403 tenant_out_of_scope`, and one in scope without the
  permission `403 forbidden` naming both — refused, never dropped. No error
  code is added, and no role gains a permission.
- **Start-up refusals:** a host route whose `Access` is neither class; a
  cross-tenant route whose permission the auditor role does not hold
  (`grant:write`, `report:write`, `access-context:push`, ...); and a
  cross-tenant pattern naming `{tenant}`. Each names the route, before a file
  is read or a port bound.
- **Behaviour no API diff shows:** a session covers the one tenant it signed
  in through, so on a cross-tenant route it covers that tenant; a principal
  spans tenants only as a token minted with `identity token-issue --scope`
  naming several. The north-bound access log line of a cross-tenant request
  carries `tenants`, the sorted set, and no `tenant`.

## v0.4.0

An external system ends a window it opened, and the sessions it backed end
with it.

- **Phase:** 0017, ending an external window.
- **Public packages:** `ext.WindowAssertion.Ended` is added. With it, a push
  ends the window its `ID` opened: Control reads the `ID`, records `Reference`,
  `IssuedAt` and `AdditionalContext` when set, and ignores every other field.
  The zero value is an open, as every push was before. The documentation of
  `WindowAssertion`, its `ID`, `Subject`, `Targets` and `Window`, and
  `AccessContextProvider`'s push direction says what an end does. `server` is
  unchanged.
- **North-bound routes:** none added. The push receiver answers an end `200`
  with `{assertion_id, outcome, grant, state}`, where `outcome` is `revoked`,
  `already_ended` or `recorded`, after the settle pass when it revokes. An end
  whose revocation could not be published is `503
  grant_revocation_undelivered` with `grant_id` and `revoked: true`, as the
  revoke route answers one; pushing the end again re-sends. An open under an
  id its system already ended is `400 window_closed`, `field: id`. No error
  code is added, and no role gains a permission.
- **Behaviour no API diff shows:** an end revokes the grant its window became,
  audited as `grant.revoked` with the integration's credential and
  `grant_external_ended: "true"`, and the holder is shown "The access window
  this session depended on has ended." — never the vendor's text. An end is
  never refused for its age, scope or window. An end of a window already
  revoked, ended or expired changes nothing; an expired one is not rewritten
  as revoked. An end for an id with no window is kept and recorded as
  `access_context.window_ended`, and a later open under that id is refused. A
  probe that stops confirming a window still revokes nothing.
- **Configuration:** a declarative provider's `push.ended` — assertions over
  the push body in `probe.confirm`'s vocabulary, all of which must hold, with
  no placeholders — makes a push an end. Absent, every push is an open.
- **Migration:** `0011_external_window_ends.sql` adds one table,
  `access_context_window_ends`. Run `hoplock-control migrate` before starting
  this release.

## v0.3.0

A person asks for access for themselves, through a registered approval
workflow.

- **Phase:** 0016, self-service grant requests.
- **Public packages:** `ext.GrantRequest.BreakGlass` is added: true when the
  credential that raised the request was minted on the break-glass path,
  copied from the stored request, so a resubmission after an outage keeps it.
  `ext.GrantWorkflow`'s documentation names the two doors a request reaches
  `Submit` through: an administrator's create, and a requester's own, where
  `Subject` and `RequestedBy` are one person. In `ext.Points()`, the
  `GrantWorkflow` point's `Absent` text adds that a requester's own request is
  refused while nothing is registered. `server` is unchanged.
- **Permissions and roles:** `grant:request` is added to the closed set, held
  by a new role, `requester`, and by `admin`. `requester` holds nothing else:
  no read set, and so not `license:read`, which is now held by every role
  except `integration` and `requester`.
- **North-bound routes:** four, in a new self-scoped class whose subject is
  the caller's and never a field the caller sends, all under `grant:request`:
  `POST` and `GET /api/v1/tenants/{tenant}/me/grant-requests`, and `GET …/{request}`
  and `POST …/{request}/cancel` beneath it. The create takes the
  administrator's body without `subject` and answers as the administrator's
  create does with a workflow registered (`202`, `201`, `403
  grant_request_denied`, `502 grant_workflow_failed`). With no workflow
  registered it answers `403 grant_workflow_not_registered` and stores
  nothing. Somebody else's request is `404`. A credential with no person
  behind it is refused on every one of them, `403 subject_required`. Two error
  codes are added: `subject_required` and `grant_workflow_not_registered`. A
  host route pattern beginning `me/grant-requests` now collides with
  Control's and is refused at start-up.
- **Behaviour no API diff shows:** the approval workflow's poller now learns a
  tenant when a proxy of that tenant opens its event subscription, so after a
  restart a decision is applied in every tenant with a live proxy within one
  `grants.workflow_poll_interval`, with nobody reading the request.
- **Migration:** `0010_grant_requests_by_subject.sql` adds one index. Run
  `hoplock-control migrate` before starting this release.
- Two texts name Control's phases by their new numbers, after the queue was
  renumbered to fit phase 0018: `ReportProvider`'s `ControlShips` in
  `ext.Points()` now reads "(phases 0010, 0019)", and the start-up warning on
  the two temporary operator listeners now calls each "a pre-0019 operator
  path". Nothing else changes.

## v0.2.0

A host binary can start Control.

- **Phase:** 0015, the public server package and host routes.
- **Public packages:** `server` is added, beside `ext`, and is the same
  compatibility promise. `server.Main` is Control's whole command line and
  `server.Run` the daemon alone; `server.Options` carries what a host adds:
  `Provider`, `Registry`, `HostSections`, `HostConfig`, `Routes` (`server.Route`)
  and `ErrorCodes`. A host route reads its caller with `server.CallerFrom`
  (`server.Caller`) and answers a failure with `server.WriteError`. `ext` is
  unchanged.
- **Permissions:** two codes are added to the closed set. `report:write` is
  held by `admin` alone; `license:read` by every role except `integration`.
  Control serves no route that needs either: a host's routes name them.
- **`hoplock-control`** is one call to `server.Main` and otherwise unchanged:
  the same flags, subcommands, output, configuration and routes. Two things a
  build or an operator can see:
  - an interrupt or `SIGTERM` now cancels a running subcommand, which stops
    with an error, where it used to end the process outright;
  - the `VERSION=` override lands in `internal/daemon`, so an `-X
    main.version=…` passed by hand no longer sets anything. Use
    `make build VERSION=…`.

## v0.1.0

The first release.

- **Phases:** 0001–0013, and 0014, which cut this release. `docs/PLAN.md` §10
  says what each one delivered.
- **Public packages:** `ext`, as `ext/README.md` describes it. `ext.Points()` is
  the authoritative list of its extension points. It is the module's only
  public package.
- **`hoplock-control --version`** reports Control's module version from the
  build's own information: the tag at a release, a pseudo-version after one, a
  `+dirty` suffix for a modified tree, and `dev` when the build has no version.
  Built into another program, it reports the version of Control that program
  depends on, never the program's own. A Control swapped in by a `replace`
  directive reports `dev` and names its replacement.
