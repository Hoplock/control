# 0047 — A revoked registration says so: the registration's last line names a local revocation

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/64, under `## Upstream request`,
> item 1, from its request file
> `prompts/upstream/queued/enterprise-PR#64-registration-revoked-line.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2). The same
> PR's item 2, the supervising operator named in the instance's trail, waits
> in Enterprise's queue as a request of its own; whichever session answers it
> places it.
>
> **Where it sits.** Right after instance identity and supervisory
> registration (0046), which built everything this phase extends, and before
> the console (0048) and everything after it. Nothing queued depends on it and
> it depends on nothing queued, so the requesting repository's wait decides.
> The revoked-line renumbering note in `docs/PLAN.md` §10 gives one line of
> reasons for each neighbour.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for revising the plan in
  place and for the downstream hand-over this phase owes, then §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a
  request and changes the supervisory registration's protocol, so it owes a
  downstream sync (below).
- `docs/PLAN.md`:
  - **§2**, the register first, then **M19** in full, above all its four
    rules (the first: registration is opt-in, configured locally and
    revocable locally; the third: a supervisor is never on the decision
    path), its point 2 (the client names what it can read and the server
    never answers outside it), and "The shapes, as they stand", whose
    registration bullet this phase revises in place. Then **M2** (a
    supervisor's administrative acts are north-bound calls, never this
    channel), **M11** (a supervisory failure is an outage in a management
    view, never a deny), **M15** (this is the honest default; the plane that
    consumes it is Enterprise's, its E14) and **M23** (every phase is a
    release).
  - **§3**, "Component responsibilities", at `internal/instance`.
  - **§10**: this phase's row and the revoked-line renumbering note.
- `docs/API-VERSIONING.md` — whole (it is short). It is the north-bound
  API's promise. This phase writes the registration protocol's beside it, on
  the same principle, and must not move the north-bound number.
- `docs/learnings/` — read every summary. Open
  **`0046-instance-identity-and-supervisory-registration-learnings.md`** in
  full: "HTTP full duplex — the gotcha" is the one this phase is most likely
  to meet again, and "Deviations" says why revocation is a CLI.
- Code. Read it before designing anything:
  - `internal/instance/register.go` whole: the protocol comment at its head,
    `ProtocolVersion`, `Line`, `Registration.Run`, `revoked`, `session`, the
    deferred teardown at the top of `session` and the order it runs in, and
    `lineWriter`;
  - `internal/instance/register_test.go`: the test `supervisor` (the
    reference far end), `TestInboundOnlyHeartbeatsAndRereports`,
    `TestLocalRevocationEndsTheRegistration` and
    `TestTheRegistrationIsAClusterSingleton`;
  - `internal/store/deployment.go`: `DeploymentIdentity`
    (`SupervisionRevokedAt`, `SupervisionRevokedBy`) and
    `SetSupervisionRevoked`, whose `COALESCE` keeps the first instant when a
    revoked registration is revoked again;
  - `internal/daemon/supervision.go` (`supervision revoke|restore|status`)
    and `TestASupervisorCannotAffectASession` in
    `internal/daemon/supervision_test.go`.

## Context estimate
- **Reads:** this prompt, M19, `docs/API-VERSIONING.md`, the summaries, 0046's
  learnings, and the code above (~1 300 lines, `register.go` and its test most
  of it).
- **Changes:** `internal/instance/register.go` (one out line, the negotiated
  protocol, the session's ending) and its tests; one case in
  `internal/daemon/supervision_test.go`; `docs/API-VERSIONING.md`; PLAN M19's
  shapes, §3 and §10's row; `CHANGELOG.md`.
- **Why it fits:** no route, no migration, no `ext` or `server` change, no new
  decision, and nothing near the decision path. The instant the line carries is
  already in the store. What is new is one line kind, one inbound member, and
  an ending that delivers a last line instead of dropping the connection.

## Objective
Let a supervisor tell a registration its supervised deployment's operator
revoked from one that ended for any other reason.

Today, at `v0.33.0` and on `main`:

- `hoplock-control supervision revoke` records the instant
  (`deployment.supervision_revoked_at`). The node holding the registration
  reads it at its next heartbeat tick, `session` returns `errRevoked`, and
  the connection ends. No node dials again until `supervision restore`;
- nothing on the connection says why. The request body simply ends, which is
  exactly what a deployment stopping, shutting down cleanly, losing its
  singleton or losing its network looks like from the far end;
- so a supervisor cannot show a revoked deployment as **revoked**, distinct
  from **unreachable**. Enterprise's 0012 has to (its E14, the fourth rule:
  supervision is revocable by the supervised, and "the customer revoked us" is
  routed to a different person than "the customer is down"). Until this lands
  it builds that state behind a seam, unwired, and shows a revoked instance as
  unreachable.

After this phase a node that ends a registration **because it was revoked
locally** first writes one last line saying so, to a supervisor that declared
it reads it, and then ends the request body cleanly. Nothing else changes: who
decides, how soon it takes hold, and what a supervisor can do about it.

## The request, in this repository's vocabulary
Enterprise asked for one out line, written before the body ends and only for
a local revocation:

```
{"kind":"revoked","id":"evt-<epoch>-<seq>.<line>","at":"<RFC 3339: when supervision revoke recorded it>"}
```

- `id` takes the heartbeat's form: the line names a position without taking
  one. It is not an event of the feed (`supervision_log`), is never logged
  there, and is never replayed.
- `at` is the instant the deployment record holds, so whichever node holds the
  registration sends the same value. It is `DeploymentIdentity.SupervisionRevokedAt`,
  which `SetSupervisionRevoked` keeps at the first revocation when the
  operator revokes again.
- No `by`. Who revoked is the deployment's own record (`--by`, shown by
  `supervision status`), and the supervisor learns only that it was revoked,
  and when. Enterprise left this to Control. Control agrees: M19's first rule
  makes revocation the supervised operator's act, and nothing about it is the
  supervisor's to know beyond the fact.
- Only a local revocation sends it. A shutdown, a lost connection, a failed
  write, a lost singleton and a supervisor that went silent send nothing, so
  its absence means nothing.
- A deployment revoked while it is not connected sends nothing. No node dials
  while revoked, and this phase makes no exception (M19's first rule). A
  supervisor shows that instance unreachable, and says it cannot know why.
- On the version: "if an added out kind is a new registration protocol version
  by Control's rules, `hello` says `"protocol":2`. A supervisor at `1` that
  ignores unknown kinds sees what it sees today."

## What this phase answers, and where it differs from the ask
The line is **met as asked**: kind, `id`, `at`, no `by`, and when it is and is
not sent. Two things the request left to Control are settled here.

### 1. The registration protocol gets a written promise, and it is negotiated
Control has no rules for the registration protocol's version yet.
`ProtocolVersion` is `1` on `hello`, and nothing says what moves it or what a
supervisor at an older version is sent. So this phase writes them, on the
principle M19 already settled for the north-bound API: **the reader names what
it reads, and the writer never sends outside it.** On the registration the
supervisor is the reader.

- **`resume` gains `protocol`**, an integer: the newest registration protocol
  the supervisor reads. Absent means `1`. That is the opposite default to the
  north-bound header, and deliberately so. An undeclared north-bound client is
  served the current version because every undeclared client ships with the
  server. No supervisor ships with a deployment, so a supervisor that declares
  nothing was written against `1`.
- **`hello`'s `protocol` is the newest this deployment speaks**, now `2`. At
  `1` that and "the session's protocol" were the same number, so no reader
  could tell them apart. `hello` is written before the supervisor's `resume`
  is read, and nothing on it differs by version.
- **The session speaks the lower of the two.** Every line that differs
  between versions comes after `resume`. A supervisor that declares `1`, or
  nothing, is sent exactly what `v0.33.0` sends, whether or not it would have
  ignored an unknown kind. That is stronger than the request's last bullet,
  and it is what the request's "by Control's rules" was asking Control to
  decide.
- **A `protocol` below `1`** ends the session as a missing `resume` does
  (`errNoResume`), logged with what was declared, and redialled with backoff.
  A value above this deployment's newest is not an error: the session speaks
  this deployment's newest.
- **What moves it**, written into `docs/API-VERSIONING.md` (below): a new line
  kind in either direction, a new member on a line, a new value of a closed set
  a line carries (an event kind), and a changed meaning. Each moves it by one.
  It moves independently of `north_api`, `contract` and the module release, the
  same lesson that file's "Three numbers" table records, now four.

The alternative was the request's literal reading: `hello` says `2`, and every
supervisor must ignore unknown kinds. It needs no inbound member. But the
version would then be a label, not a promise: a supervisor written against a
closed set of kinds, which is what `docs/API-VERSIONING.md` says moves a version
on the north-bound side, would be sent a kind it never agreed to read. The
answer would differ by surface for no reason either reader could see.
Enterprise's 0012 already negotiates the north-bound version per instance, and
its gateway already writes `resume` first, so the cost to it is one member on a
line it sends today.

### 2. The ending delivers the line, and is still bounded
Today `session` returns `errRevoked` and its deferred teardown closes the
response body **before** it closes the request body's pipe. That is harmless
when nothing more is to be said. Now it would race the last line: closing the
response can tear the connection down before the transport has written the
line and the end of the body. So, on a local revocation with a session at
protocol `2` or above:

1. write the `revoked` line;
2. close the request body cleanly (`pw.Close()`, never `CloseWithError`), so
   the supervisor reads the line and then the end of the body;
3. only then close the response: wait for the supervisor to end it, which
   the reference supervisor does when it reads the end of the request body,
   or for the bound below, whichever comes first.

Steps 1 to 3 together are bounded by **one heartbeat interval**. A supervisor
that does not take the line within it loses the connection at that bound,
exactly as it loses it at the check today, with no line. Nothing about the line
can keep a revoked registration open past that bound, make it wait on the
supervisor, or make any node dial again. M19's "on every node within one
heartbeat" is about when revocation takes hold, and that is unchanged: the
check still runs on every tick. Revise the sentence so it also states the
delivery bound.

The `supervision_revoked` log event gains whether the line was written to the
supervisor (`told_supervisor`), so an operator can see that the supervisor was
told.

## In scope

### 1. `internal/instance/register.go`: the line, the negotiation, the ending
- `ProtocolVersion` becomes `2`, and `MinProtocolVersion` is `1`. An out kind
  constant for `revoked`, beside the inbound ones.
- `revoked(ctx)` returns the instant it read, beside the bool. The line
  carries that instant, never `time.Now()` and never a second read: a restore
  between the check and the write must not produce a line with no time.
- `session` reads `protocol` from the `resume` line (`Line.Protocol` already
  decodes it; it is ignored today), computes the session's protocol, and keeps
  it for the session. It is in the `supervision_registered` log line.
- On `errRevoked` at protocol `2` or above: the ending in item 2 above. At
  protocol `1`: today's ending, byte for byte.
- The protocol comment at the head of the file lists `revoked` and `resume`'s
  `protocol`, and says which version each line arrived in.

### 2. `internal/instance/register_test.go` and `internal/daemon/supervision_test.go`
The test `supervisor` gains a declared protocol (default: none), records
whether each connection's request body ended cleanly, and stays the reference
far end the 0046 learnings point Enterprise at. See "Tests".

### 3. `docs/API-VERSIONING.md`: the registration protocol
A section of its own, after the north-bound one: what the number versions (the
lines of one registration, both ways, and their members); the negotiation
(item 1 above); what moves it; and a history table — `1`, `v0.33.0`, the
registration as 0046 built it; `2`, this release, `revoked` and `resume`'s
`protocol`. The "Three numbers" table gains the registration protocol as a
fourth row: `hello`'s `protocol`, never `GET …/instance`. Its heading changes
to match. The file's title and opening say it now holds two promises.

### 4. The plan, revised in place
- **M19, "The shapes, as they stand"**, the registration bullet: the `revoked`
  out line and when it is sent, `resume`'s `protocol` and the session's
  protocol, and the delivery bound. Point at `docs/API-VERSIONING.md` for the
  promise. M19 stays `live`. No decision is added, amended or withdrawn, so the
  register does not change.
- **§3**, `internal/instance`: one clause saying the registration protocol is
  negotiated and where its promise is.
- **§10**: this phase's row, from what was built, with its release.

### 5. `CHANGELOG.md`
The next MINOR after the latest tag. Nothing breaks: `ext`, `server`, the
north-bound API and its version are untouched, and a supervisor at protocol `1`
is sent what it was sent. Under the release's changes: registration protocol
`2`, the `revoked` line, `resume`'s `protocol`, and that a supervisor declaring
nothing is at `1`.

## Out of scope
- **Who revoked.** No `by` on the line (above).
- **A revocation made while disconnected.** No node dials while revoked, and
  nothing here makes it (M19's first rule).
- **A "restored" line.** After `supervision restore` the next registration is
  an ordinary one: its `hello` is what says supervision resumed.
- **The supervisor credential's revocation**
  (`POST …/instance/supervisors/{principal}/revoke`). It is a north-bound
  token, refused `401` afterwards, and it leaves the registration alone. That
  is visible to a supervisor at `v0.33.0` already.
- **The registration protocol on the north-bound API.** `versions` on
  `GET …/instance` does not gain it: that would move `north_api` for a number a
  supervisor reads on `hello`.
- **Versioning the report body by the session's protocol.** `hello`'s,
  `resync`'s and `report`'s `data` are rendered as `0046` renders them, the
  north-bound views at the current north-bound version, which `hello`'s
  identity names (`versions.north_api`). Unchanged here.
- **Anything a supervisor can do in reply.** Nothing inbound acts (M19), and
  `revoked` invites no answer.
- **Enterprise's item 2**, the supervising operator named in the instance's
  trail (`prompts/upstream/queued/enterprise-PR#64-supervising-operator-attribution.md`
  in `hoplock/enterprise`). It is a request of its own.

## Acceptance criteria
- A supervisor that declares `"protocol":2` on `resume`, connected when
  `supervision revoke` runs, reads one `revoked` line as the last line of that
  connection's request body, and then the end of the body, not an error.
  `revoked` has the heartbeat's id form, the feed's current epoch, and `at`
  equal to `supervision_revoked_at` as the store holds it.
- No node dials while the deployment is revoked. After `supervision restore`
  the next connection's `hello` says `"protocol":2` and carries no `revoked`.
- Revoking an already-revoked deployment changes nothing a supervisor sees:
  no line, and the store's instant is the first one.
- A supervisor that declares nothing, or `"protocol":1`, is sent no `revoked`
  line and no line of a kind `v0.33.0` does not send, before, during or at the
  end of a revoked registration. Its connection ends as it does at `v0.33.0`.
- A shutdown, a supervisor that drops the connection, a supervisor that stops
  sending (the liveness watchdog), a lost singleton and a failed write each end
  a session at protocol `2` with no `revoked` line.
- A supervisor at protocol `2` that has stopped reading when the deployment is
  revoked loses the connection within one heartbeat interval of the check that
  saw the revocation, and nothing dials afterwards. The decision path is
  unaffected throughout (M19's third rule, M11).
- In a clustered deployment the `revoked` line comes only from the node holding
  `hoplock.supervision`, once, with the store's instant, after the singleton
  has moved at least once before the revocation.
- The line arrives over HTTP/1.1 full duplex and over HTTP/2, the two
  transports the default client uses.
- A `resume` with a `protocol` below `1` ends the session as a missing
  `resume` does and is redialled with backoff. One above this deployment's
  newest is served at this deployment's newest.
- `docs/API-VERSIONING.md` states the registration protocol's promise and its
  history. M19's shapes, §3 and §10 say what was built.

## Tests
- `internal/instance/register_test.go`:
  - a revoked registration says so, at protocol `2`: the line's kind, id form,
    epoch and `at`; a clean end of the body after it; no dial while revoked; a
    restore registers again with no `revoked` on the new connection;
  - revoking twice keeps the first instant on the line;
  - at protocol `1` and undeclared: `kinds(sup.lines(n))` stays within
    `hello`, `resync`, the feed's event kinds, `report` and `heartbeat`
    through a revocation, as `TestInboundOnlyHeartbeatsAndRereports` checks
    for inbound lines today;
  - every non-revocation ending sends no `revoked`: cancelled context,
    supervisor-closed connection, liveness timeout, lost singleton;
  - across a singleton handover, using `TestTheRegistrationIsAClusterSingleton`'s
    election: one `revoked`, from the holder, with the store's instant;
  - the line over HTTP/2: an `httptest.NewUnstartedServer` with
    `EnableHTTP2` and `StartTLS`, the registration's client trusting it;
  - a `resume` declaring `0`, `-1` and `99`.
- `internal/daemon/supervision_test.go`, `TestASupervisorCannotAffectASession`:
  a case revoked while the supervisor has stopped reading, at protocol `2`,
  asserting the bound and that authorize is unaffected while it runs.
- `go test ./...` with the race detector on the two packages, since the ending
  adds a wait on the transport.

## Release (M23)
The next MINOR after the latest tag, with nothing under `### Breaking`.
`make release-check` reports no public API change: `internal/instance` is
internal, and `ext` and `server` are untouched.

## Cross-repo impact
This phase answers an upstream request and changes the supervisory
registration's protocol, which Enterprise's plane is the far end of, so it owes
a downstream sync. Its PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what
was built, and the session there that rewrites its prompts will be a fresh one.
State at least these obligations:

1. **Its 0012, the gateway.** Its `resume` declares `"protocol":2` — the
   newest both the plane and the instance speak, read from the instance's
   `hello` — and it reads `revoked`: kind, id form, `at`, and the clean end of
   the body after it, on which it ends its response, as Control's reference
   supervisor does. Its "That end is three lines and no more" still holds:
   `resume` gains a member, not a sibling. An instance whose `hello` says `1`
   (`v0.33.0`) never sends `revoked`, and a revoked one still looks unreachable.
2. **Its 0012, "What `v0.33.0` does not give this phase".** The seam for the
   `revoked` state is wired from this release: `revoked`, with `at`, sets it,
   and a later `hello` from the same `instance_id` clears it. Its rule against
   inferring revocation from how a connection ended stands unchanged: only the
   line says revoked. A deployment revoked while not connected still shows as
   unreachable. The acceptance criterion that skips "local revocation wins"'s
   second half, naming the request, asserts it from this release.
3. **What Control decided that it did not ask for**: the negotiated protocol
   and its absent-means-`1` default, and why (item 1); the delivery bound, and
   that an instance revoked while this plane is not reading ends with no line
   (item 2); no `by`, and the reason.
4. **Its plan.** PLAN §5's "Supervising one of Control's deployments": the
   registration's lines at protocol `2`, and the gap it names closed from this
   release. Its 0013 and 0017 where they assert the revocation.
5. **Its pin.** Every obligation holds from the release this PR cuts (M23),
   never from `main`. Name the release by version.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks, as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`. Commit it
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"**. The contract is untouched (M1), and the proxy is neither end
of the supervisory registration. "None" is a finding.

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0047-revoked-registration-line-learnings.md`. Enterprise's sync
is written from its summary block, which MUST give:

- the `revoked` line as built, and when it is and is not sent;
- the registration protocol's negotiation: `resume`'s `protocol`, its
  absent value, `hello`'s meaning, and the session's protocol;
- what moves the registration protocol, and where the promise is written;
- the delivery bound;
- the release, by version.
