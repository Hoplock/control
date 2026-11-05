# 0049 — No write after a revocation: the registration's writes are bounded and gated

> Found by 0047 (`docs/learnings/0047-revoked-registration-line-learnings.md`,
> "A gap noticed and not closed"), and queued at the user's direction in
> https://github.com/Hoplock/control/pull/141. The user also set a requirement:
> **once a revocation is issued, no write to the supervisor may be possible**,
> so that no event can leak after a revocation.
>
> **Where it sits.** After 0048, which `hoplock/enterprise` is waiting on and
> which this phase does not touch. Before the console (0050) and everything
> after it. It extends 0047 and depends on it alone. The revocation-gate
> renumbering note in `docs/PLAN.md` §10 gives the reasons for each
> neighbour.

## Read first
- `docs/PROTOCOL.md`: §3 for revising the plan in place and for the
  downstream hand-over this phase owes, then §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md`: **§4.1** and **§4.3**. This phase changes
  when the supervisory registration's connection ends, which a supervisor
  sees, so it owes `hoplock/enterprise` a sync (below).
- `docs/PLAN.md`:
  - **§2**. Read the register first, then **M19** in full, above all its first
    rule (registration is revocable locally, without the supervisor's
    cooperation) and its third (a supervisor is never on the decision path).
    Also read "The shapes, as they stand": this phase revises the revocation
    sentence of its credential bullet in place.
  - Then read **M5** (nothing the registration holds may come from the
    decision path's connections), **M11** (a supervisory failure is an outage
    in a management view, never a deny), **M25** (state is in the store, and
    the cluster bus only wakes; it is why the gate below is in the store and
    not on the bus) and **M23** (every phase is a release).
  - **§3**, "Component responsibilities", at `internal/instance`.
  - **§10**: this phase's row and the revocation-gate renumbering note.
- `docs/API-VERSIONING.md`, "The registration protocol". This phase adds no
  line and no member, so the version stays `2`; item 5 below says what to add
  to that section.
- `docs/learnings/`: read every summary. Open these two in full:
  - **0046**: "HTTP full duplex — the gotcha";
  - **0047**: the ending and why it closes the pipe itself, `told_supervisor`,
    the tests and the mutation each one fails, and "A gap noticed and not
    closed".
- Code. Read it before designing anything:
  - `internal/instance/register.go`, whole: `Run`, `revoked`, `session`
    (the hello goroutine, the watchdog, the loop, the deferred teardown),
    `endRevoked`, `resume`, `send`, `sendReport`, `lineWriter`;
  - `internal/instance/register_test.go`: the `supervisor` (the reference far
    end), `stalledSupervisor`,
    `TestARevokedLineIsBoundedWhenTheSupervisorStopsReading`,
    `TestOnlyALocalRevocationSendsTheRevokedLine`, `startCluster`;
  - `internal/store/deployment.go`: `SetSupervisionRevoked` (its `COALESCE`),
    `AppendSupervision` (the `deployment` row is the feed's publish lock),
    `SupervisionAfter`, `SupervisionStream`;
  - `internal/daemon/supervision.go` (the `revoke` command) and
    `internal/daemon/instance.go` (`buildInstance`: the registration's store
    is the instance pool, `instance.health_connections`, default 2);
  - `TestASupervisorCannotAffectASession` in
    `internal/daemon/supervision_test.go`, case (e).

## Context estimate
- **Reads:** this prompt, M19, M5, the summaries, the full 0046 and 0047
  learnings, and the code above (about 1 800 lines, most of them
  `register.go`, its test and `deployment.go`).
- **Changes:**
  - `internal/store/deployment.go`: the gate, and the revocation taking it;
  - `internal/instance/register.go`: every write through the gate, with a
    bound;
  - `internal/daemon/instance.go`: the gate's pool;
  - the tests in `internal/store`, `internal/instance` and `internal/daemon`;
  - PLAN M19, §3 and §10; `docs/API-VERSIONING.md`; `CHANGELOG.md`.
- **Why it fits:** no route, no migration (an advisory lock needs no table),
  no `ext` or `server` change, no new line kind, and nothing on the decision
  path.

## Objective
Two defects in the registration as built at `v0.34.0`. The guarantee below
closes both.

1. **A write can block forever, so a revocation can be missed.** The
   revocation check runs between writes in the session loop. A write to the
   supervisor blocks for as long as the supervisor does not read. The liveness
   watchdog drops a supervisor that stops *sending*, but not one that keeps
   sending and never *reads*. Once that supervisor's buffers fill, a write
   blocks indefinitely, the loop never reaches the check, and the connection
   outlives a revocation for as long as the supervisor likes. It is never told.
2. **Nothing stops a write after a revocation the node has not yet seen.**
   Between `supervision revoke` committing and the holding node's next check,
   the session can write feed events logged after the revocation, and a
   report computed after it. That window is up to one heartbeat, and under
   defect 1 it has no bound. A supervisor that stalled and then resumes
   reading is sent every event logged during its stall, up to the feed's
   newest 1024, before the line that says revoked.

What 1 exposes is a coarse feed (`node_joined`, `health_changed`,
`policy_published`, `counts_changed` and the rest) and reports of identity and
health. It is never access: nothing inbound acts (M19), and a supervisor's
north-bound token is a separate credential that this phase does not touch. But
the operator who revoked meant that feed to stop, and M19's first rule says it
stops without the supervisor's cooperation.

## The guarantee
After this phase:

- **G1: every write is bounded.** Every write the registration makes to a
  supervisor completes within the registration's liveness interval
  (`RegistrationOptions.Liveness`, 3 × heartbeat by default), or the session
  ends: the connection is dropped and the registration redials with backoff.
  No new configuration key.
- **G2: no write after a revocation.** "Issued" means committed. Once
  `supervision revoke`'s transaction has committed, no node *begins* any write
  to a supervisor except the one `revoked` line 0047 built. A write already in
  progress at that moment finishes or fails within the G1 bound, and the
  revocation does not commit until it has, so the command waits for it. Every
  node, every write: the hello, `resync`, replayed and live events, `report`,
  `heartbeat`.
- **G3: nothing observed after a revocation leaves.** No feed event logged
  after the revocation committed, and no report whose data was read after it,
  ever reaches a supervisor, by any node, whatever the timing, a stalled
  supervisor included. G3 follows from G2, because data is read before the
  write that carries it begins, but it is asserted on its own.

Revocation still takes hold within one heartbeat (M19). The holding node
writes a heartbeat every tick, and every write now checks. The `revoked` line,
its `at` and 0047's ending (one heartbeat from the check that saw the
revocation) are unchanged.

## The mechanism
This is decided here. Deviate only for a reason you write in the learnings.

### 1. The gate: an advisory lock, exclusive for the revocation, shared for every write
- One Postgres advisory lock, on a key that is a named constant in
  `internal/store`. It is used by nothing else.
- **The revocation takes it exclusively.** `SetSupervisionRevoked(ctx, true,
  …)` runs in one transaction that first takes the lock exclusively
  (`pg_advisory_xact_lock`) and then updates the row. Committing releases it.
  `restore` takes nothing.
- **Every write takes it shared, and checks under it.** The registration does
  each write inside a transaction that takes the lock shared
  (`pg_advisory_xact_lock_shared`), reads `supervision_revoked_at`, and only
  when that is empty writes to the supervisor. The transaction ends when the
  write returns.
  - If the read says revoked, nothing is written under the gate. The session
    goes to 0047's ending with the instant it read.
  - The data a gated write carries is read while the gate is held: the events
    for a batch, a report's snapshot, a heartbeat's position. A revocation
    cannot commit while the gate is held, so that data predates any
    revocation that commits later.
- **Why this holds G2.** Postgres queues lock requests, so a pending
  exclusive request blocks later shared ones. Once `revoke` is waiting, no
  write starts. `revoke` waits only for a write already holding the gate, and
  G1 bounds that write. Prove the queueing with a test against the real
  store; do not rely on this paragraph.
- **Granularity.** One gate per write call: the hello, one `resync` or
  `report`, one batch of up to 256 events, one heartbeat. The G1 bound
  applies to the whole call.
- **The one exception.** The `revoked` line is written outside the gate,
  after the gate's own read saw the revocation. It carries only that instant.
- **The gate's connection is its own.** It holds a transaction across a
  network write for up to the G1 bound, so it must never take a connection
  the decision path uses (M5). It must also not starve health, the node
  reports or the feed observer, which share the instance pool (2 connections
  by default, and it may be configured to 1).
  - `buildInstance` opens a **one-connection pool** for the gate, only when a
    supervisor is configured, and closes it with the instance.
  - `instance.Options` or `RegistrationOptions` carries it. Choose one and
    say which.
  - A test holds a blocked write and computes health meanwhile.
- **When the store cannot be read, nothing is written.** If the gate cannot
  be taken (the store is unreachable, or the context ends), the session ends
  as a failed write does, and `Run` redials with backoff. `Run`'s check
  before dialling fails closed the same way: an unreadable store does not
  dial.
  - This reverses 0046's "an unreadable store reads as not revoked for this
    check only". Revise that comment.
  - It is an outage in a management view (M11). Nothing on the decision path
    changes.

### 2. The bound
- Every gated write, and the `revoked` line, is bounded by `Liveness`. Use
  0047's mechanism: a timer that cancels the session, whose `context.AfterFunc`
  closes the pipe, so a write blocked in the pipe returns and the transport
  aborts the request.
- A session ended this way logs `supervision_disconnected` with a reason that
  says the supervisor stopped taking writes, distinct from a silent supervisor
  (`errSupervisorGone`). Add an error for it.
- 0047's ending keeps its own bound: one heartbeat from the check, which is
  shorter than `Liveness`.

### 3. `supervision revoke`
- It may now wait, at most the G1 bound plus the store's own timeout, for a
  write in progress. Say so in the command's doc comment and in
  `config.example.yaml`'s `supervisor` comment. Its output is unchanged.
- With no registration connected it returns as fast as it does today.

## In scope
1. **`internal/store/deployment.go`.** The gate, as one repository method so
   that no caller can write outside it, for example:

   ```go
   // SupervisionGate takes the supervision gate shared, reads the local
   // revocation and, only when the registration is not revoked, calls write
   // with the gate held. It returns the revocation instant it read (zero when
   // not revoked) and write's error. A revocation that commits after this
   // read waits until write has returned.
   SupervisionGate(ctx context.Context, write func(ctx context.Context) error) (revokedAt time.Time, err error)
   ```

   `SetSupervisionRevoked` takes the gate exclusively when it revokes. The
   method's name and signature are yours, but the doc comment states G2. Store
   tests cover:
   - a revocation waits for a gate in progress;
   - a gate requested while a revocation waits does not call `write`, and then
     reads it revoked;
   - after a revocation, `write` is never called;
   - a restore takes nothing.
2. **`internal/instance/register.go`.**
   - Every write goes through the gate and the bound: the hello goroutine,
     `resume`, `send`, `sendReport`, and the heartbeat.
   - The check in the loop becomes the gate's own read.
   - `revoked` and `Run` fail closed.
   - The head comment's "REVOKED" paragraph states G1–G3. The `revoked`
     paragraph names the gate.
   - `lineWriter` stays the only thing that writes to the pipe.
3. **`internal/daemon/instance.go`**: the gate's one-connection pool.
   **`internal/daemon/supervision.go`**: the doc comment.
4. **Tests**: see "Tests".
5. **Docs.**
   - **PLAN M19, "The shapes, as they stand"**: revise in place the credential
     bullet's revocation sentence. It takes hold within one heartbeat; no
     write begins after it commits; the command waits for a write in
     progress, bounded by the liveness interval; a supervisor that stops
     taking writes is dropped at that bound.
   - **PLAN §3**, `internal/instance`: one clause on the gate and its
     connection.
   - **PLAN §10**: this phase's row, from what was built, with its release.
     M19 stays `live`, and the register does not change.
   - **`docs/API-VERSIONING.md`**, "The registration protocol": one bullet
     saying that how long a write may wait, and when a connection is dropped,
     are not lines or members, so they do not move the version. This phase's
     change is named there as the example.
6. **`CHANGELOG.md`**: the next MINOR after the latest tag, with no
   `### Breaking` (no public package changes). List:
   - a supervisor that does not take a write within the liveness interval is
     disconnected;
   - nothing is written after a revocation commits;
   - `supervision revoke` may wait for a write in progress;
   - an unreadable store no longer keeps a registration open.

## Out of scope
- **A new line kind or member.** The registration protocol stays `2`, and
  `revoked` is unchanged.
- **The supervisor's north-bound token**
  (`POST …/instance/supervisors/{principal}/revoke`). It is a separate
  credential with its own revocation. `supervision revoke` does not touch it,
  and this phase does not change that.
- **A revocation the cluster bus announces.** The bus only wakes (M25). The
  gate is in the store because only the store can order a revocation against
  a write.
- **Feed retention, replay and resync.** They are unchanged, apart from being
  written through the gate.
- **A configurable write bound.** It is `Liveness`.
- **An asynchronous `revoke`.** The wait is the guarantee.

## Acceptance criteria
- **The gap is closed.** A supervisor that keeps sending heartbeats and never
  reads, with no revocation, loses the connection within the write bound of
  the first write it does not take, and the registration redials.
- **A revocation while stalled.** With that same supervisor connected and a
  write blocked, `supervision revoke` returns within the write bound. The
  connection ends, nothing more is written, and no node dials afterwards.
- **Nothing logged after a revocation leaks (G3).**
  - A supervisor stalls with a write blocked.
  - Feed events are appended.
  - The revocation commits, and more feed events are appended after it.
  - The supervisor resumes reading.

  It receives no event whose position is above the feed's head at the
  revocation's commit. If `revoked` arrives, it is the last line. If the
  supervisor resumes only after the write bound, the connection was cut at
  the bound and `revoked` never arrives.
- **Under contention.** At least 50 revocations are issued at random moments
  while events are appended continuously and a supervisor reads normally, with
  the race detector on. No event logged after a revocation committed is ever
  received, and wherever `revoked` arrives it is the connection's last line.
- **The gate's ordering (G2)**, proven at the store, as in item 1's tests.
- **A store that cannot be read writes nothing.** The session ends and
  redials with backoff, and the decision path is unaffected.
- **The gate's connection is its own.** It is never the decision path's, and
  health is computed while a gated write is blocked.
- **0047's guarantees still hold**: the line, its `at`, its clean end, its
  bound, `told_supervisor`, and only a local revocation sends it. Name any 0047
  test this phase had to change, and say why.
- **`TestASupervisorCannotAffectASession`.** Case (e) now also asserts that
  `supervision revoke` returned within the write bound. A new case (f) is a
  supervisor that heartbeats and never reads, with no revocation: it is
  dropped at the write bound, it redials, and authorize is unaffected
  throughout. This test may never be weakened.

## Tests
- `internal/store`: the gate's ordering (item 1), against the real store.
- `internal/instance/register_test.go`:
  - the never-reading supervisor without a revocation;
  - the revocation while stalled;
  - the stall, revoke and resume scenario for G3;
  - the contention run;
  - an unreadable store;
  - health computed while a gated write is blocked.

  `stalledSupervisor` can be extended to keep heartbeating and to resume
  reading on a signal.
- `internal/daemon/supervision_test.go`: case (e)'s extra assertion and case
  (f).
- Run `go test ./...`, and both packages with `-race -count=3`.
- Every new assertion fails against a mutation that breaks it: no bound, no
  gate, a gate checked before reading but not held across the write, a
  revocation that does not take the gate, and a fail-open store. List each
  mutation in the learnings.

## Release (M23)
The next MINOR after the latest tag, with nothing under `### Breaking`.
`make release-check` reports no public API change.

## Cross-repo impact
This phase changes when the supervisory registration's connection ends, and
what can arrive after a revocation. `hoplock/enterprise`'s plane is the far
end of both, so the PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1).

### `hoplock/enterprise`
State at least:
1. Its gateway must read continuously. A supervisor that does not take a
   write within the liveness interval (3 × heartbeat by default) is
   disconnected and redialled.
2. After a revocation commits, nothing but `revoked` arrives, so no event or
   report the gateway receives postdates a revocation. Its "revoked" state
   needs no filtering of later lines.
3. The registration protocol stays `2`, with no new line or member.
4. Its pin: the release this PR cuts, by version.

Queue the "Downstream sync" kickoff from `docs/KICKOFF.md`, verbatim except
for its blanks, as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`. Commit it
once the PR is open.

### `hoplock/proxy`
Write **"None"**. The contract is untouched (M1).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0049-no-write-after-revocation-learnings.md`. Its summary
block MUST give:
- G1–G3 as built;
- the gate (the lock, who takes it, and how), and its connection;
- the bound, and what a supervisor sees at it;
- that an unreadable store writes nothing;
- how long `supervision revoke` may wait;
- the release, by version.
