# 0047 — A revoked registration says so: the registration's last line names a local revocation

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/64, under `## Upstream request`,
> item 1, from its request file
> `prompts/upstream/queued/enterprise-PR#64-registration-revoked-line.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2). The same
> PR's item 2, the supervising operator named in the instance's trail, is
> answered as a phase of its own: 0048, queued right after this one.
>
> **Where it sits.** Right after instance identity and supervisory
> registration (0046), which built everything this phase extends, and before
> the console (0049) and everything after it. Nothing queued depends on it and
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
    path) and "The shapes, as they stand", whose registration bullet this
    phase revises in place. Then **M2** (a supervisor's administrative acts
    are north-bound calls, never this channel), **M11** (a supervisory
    failure is an outage in a management view, never a deny), **M15** (this
    is the honest default; the plane that consumes it is Enterprise's, its
    E14) and **M23** (every phase is a release).
  - **§3**, "Component responsibilities", at `internal/instance`.
  - **§10**: this phase's row and the revoked-line renumbering note.
- `docs/API-VERSIONING.md` — whole (it is short). It is the north-bound
  API's promise. This phase writes a short one for the registration protocol
  beside it, and must not move the north-bound number.
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
    reference far end), `TestLocalRevocationEndsTheRegistration` and
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
- **Changes:** `internal/instance/register.go` (one out line, the protocol
  number, the session's ending) and its tests; one case in
  `internal/daemon/supervision_test.go`; `docs/API-VERSIONING.md`; PLAN M19's
  shapes, §3 and §10's row; `CHANGELOG.md`.
- **Why it fits:** no route, no migration, no `ext` or `server` change, no new
  decision, and nothing near the decision path. The instant the line carries is
  already in the store. What is new is one line kind and an ending that
  delivers a last line instead of dropping the connection.

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
locally** first writes one last line saying so, and then ends the request body
cleanly. Nothing else changes: who decides, how soon it takes hold, and what a
supervisor can do about it.

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

## What this phase answers
The request is **met as asked**: the line's kind, `id`, `at`, no `by`, when it
is and is not sent, and `hello` saying `"protocol":2`. Two things it left to
Control are settled here.

### 1. The registration protocol's rule, written down
Control has no written rules for the registration protocol's version yet:
`ProtocolVersion` is `1` on `hello`, and nothing says what moves it or what a
supervisor must do with a line it does not know. This phase writes the rule
the request assumed, and nothing more:

- **A supervisor skips what it does not recognise.** A line of a kind it does
  not know, and a member it does not know on a line it does, are ignored,
  never refused. This is the same rule the deployment already keeps inbound:
  a supervisor line of a kind it does not act on is logged and ignored.
- **`hello`'s `protocol` is the version this deployment speaks.** It is
  informational: nothing is negotiated, and the deployment sends every line
  its version has. A supervisor reads it to know which lines can arrive. An
  instance whose `hello` says `1` (`v0.33.0`) never sends `revoked`, so a
  supervisor can show that such an instance cannot report a revocation.
- **What moves it:** a new line kind or a new member on a line, in either
  direction. Each moves it by one, so `hello` keeps saying what can arrive.
  The north-bound version, the contract version and the module release do not
  move with it, and it does not move with them.
- **What it does not cover:** a changed meaning of an existing line or member.
  Skipping cannot make that safe. It would need the supervisor to say which
  version it reads, and nothing does. The phase that first needs such a
  change designs that negotiation; none is planned.

**Why no negotiation now.** No deployment is installed and no supervisor
exists: Enterprise's plane (its 0012) is not built. Negotiation would protect
nobody today. For this line in particular it would buy nothing even later: it
is the last line before the body ends, so a supervisor that skipped it loses
nothing it had before. Adding a member to `resume`, a default for its absence
and a session version costs code and tests for a case that cannot occur yet.

### 2. The ending delivers the line, and is still bounded
Today `session` returns `errRevoked` and its deferred teardown closes the
response body **before** it closes the request body's pipe. That is harmless
when nothing more is to be said. Now it would race the last line: closing the
response can tear the connection down before the transport has written the
line and the end of the body. So, on a local revocation:

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

### 1. `internal/instance/register.go`: the line and the ending
- `ProtocolVersion` becomes `2`. An out kind constant for `revoked`.
- `revoked(ctx)` returns the instant it read, beside the bool. The line
  carries that instant, never `time.Now()` and never a second read: a restore
  between the check and the write must not produce a line with no time.
- On `errRevoked`: the ending in item 2 above. Every other ending is
  unchanged.
- The protocol comment at the head of the file lists `revoked`, says which
  version each line arrived in, and states the skip rule.

### 2. `internal/instance/register_test.go` and `internal/daemon/supervision_test.go`
The test `supervisor` records whether each connection's request body ended
cleanly, ends its response when it does, and stays the reference far end the
0046 learnings point Enterprise at. See "Tests".

### 3. `docs/API-VERSIONING.md`: the registration protocol
A short section of its own, after the north-bound one: what the number
versions (the lines of one registration, both ways, and their members); that
`hello` carries it and nothing negotiates it; the skip rule; what moves it and
what it does not cover (item 1 above); and a history table — `1`, `v0.33.0`,
the registration as 0046 built it; `2`, this release, `revoked`. The "Three
numbers" table gains the registration protocol as a fourth row: `hello`'s
`protocol`, never `GET …/instance`. Its heading changes to match, and the
file's opening says it holds both.

### 4. The plan, revised in place
- **M19, "The shapes, as they stand"**, the registration bullet: the `revoked`
  out line and when it is sent, `hello`'s protocol `2`, the skip rule, and the
  delivery bound. Point at `docs/API-VERSIONING.md` for the rule. M19 stays
  `live`. No decision is added, amended or withdrawn, so the register does not
  change.
- **§3**, `internal/instance`: one clause saying where the registration
  protocol's rule is written.
- **§10**: this phase's row, from what was built, with its release.

### 5. `CHANGELOG.md`
The next MINOR after the latest tag. Nothing breaks: `ext`, `server`, the
north-bound API and its version are untouched. Under the release's changes:
registration protocol `2`, the `revoked` line, and the skip rule a supervisor
is held to.

## Out of scope
- **Negotiating the registration protocol** (item 1, "Why no negotiation
  now"). `resume` gains nothing.
- **Who revoked.** No `by` on the line.
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
- **The report body.** `hello`'s, `resync`'s and `report`'s `data` are
  rendered as 0046 renders them, the north-bound views at the current
  north-bound version, which `hello`'s identity names (`versions.north_api`).
- **Anything a supervisor can do in reply.** Nothing inbound acts (M19), and
  `revoked` invites no answer.
- **Enterprise's item 2**, the supervising operator named in the instance's
  trail. It is a request of its own, answered by 0048.

## Acceptance criteria
- A supervisor connected when `supervision revoke` runs reads one `revoked`
  line as the last line of that connection's request body, and then the end
  of the body, not an error. `revoked` has the heartbeat's id form, the feed's
  current epoch, and `at` equal to `supervision_revoked_at` as the store holds
  it.
- Every `hello` says `"protocol":2`.
- No node dials while the deployment is revoked. After `supervision restore`
  the next connection carries no `revoked`.
- Revoking an already-revoked deployment changes nothing a supervisor sees:
  no line, and the store's instant is the first one.
- A shutdown, a supervisor that drops the connection, a supervisor that stops
  sending (the liveness watchdog), a lost singleton and a failed write each end
  a session with no `revoked` line.
- A supervisor that has stopped reading when the deployment is revoked loses
  the connection within one heartbeat interval of the check that saw the
  revocation, and nothing dials afterwards. The decision path is unaffected
  throughout (M19's third rule, M11).
- In a clustered deployment the `revoked` line comes only from the node holding
  `hoplock.supervision`, once, with the store's instant, after the singleton
  has moved at least once before the revocation.
- The line arrives over HTTP/1.1 full duplex and over HTTP/2, the two
  transports the default client uses.
- `docs/API-VERSIONING.md` states the registration protocol's rule and its
  history. M19's shapes, §3 and §10 say what was built.

## Tests
- `internal/instance/register_test.go`:
  - a revoked registration says so: the line's kind, id form, epoch and `at`;
    a clean end of the body after it; no dial while revoked; a restore
    registers again with no `revoked` on the new connection;
  - `hello`'s `protocol` is `2`;
  - revoking twice keeps the first instant on the line;
  - every non-revocation ending sends no `revoked`: cancelled context,
    supervisor-closed connection, liveness timeout, lost singleton;
  - across a singleton handover, using `TestTheRegistrationIsAClusterSingleton`'s
    election: one `revoked`, from the holder, with the store's instant;
  - the line over HTTP/2: an `httptest.NewUnstartedServer` with
    `EnableHTTP2` and `StartTLS`, the registration's client trusting it.
- `internal/daemon/supervision_test.go`, `TestASupervisorCannotAffectASession`:
  a case revoked while the supervisor has stopped reading, asserting the bound
  and that authorize is unaffected while it runs.
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

1. **Its 0012, the gateway.** It reads `revoked`: kind, id form, `at`, and the
   clean end of the body after it, on which it ends its response, as Control's
   reference supervisor does. It skips any line kind or member it does not
   recognise, never refusing one. It reads `hello`'s `protocol`: an instance at
   `1` (`v0.33.0`) never sends `revoked`, so a revoked one still looks
   unreachable, and the plane can say that instance cannot report a
   revocation. What the gateway sends is unchanged: "That end is three lines
   and no more" still holds.
2. **Its 0012, "What `v0.33.0` does not give this phase".** The seam for the
   `revoked` state is wired from this release: `revoked`, with `at`, sets it,
   and a later `hello` from the same `instance_id` clears it. Its rule against
   inferring revocation from how a connection ended stands unchanged: only the
   line says revoked. A deployment revoked while not connected still shows as
   unreachable. The acceptance criterion that skips "local revocation wins"'s
   second half, naming the request, asserts it from this release.
3. **What Control decided that it did not ask for**: the skip rule written
   down as Control's, and that the registration protocol is not negotiated
   (item 1); the delivery bound, and that an instance revoked while this plane
   is not reading ends with no line (item 2); no `by`, and the reason.
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
- the registration protocol at `2` on `hello`, the skip rule, and that nothing
  negotiates it;
- what moves the registration protocol, and where the rule is written;
- the delivery bound;
- the release, by version.
