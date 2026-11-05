# 0048 — The supervising operator named in the instance's trail: a supervisor's call says who it acts for

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/64, under `## Upstream request`,
> item 2, from its request file
> `prompts/upstream/queued/enterprise-PR#64-supervising-operator-attribution.md`,
> and answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2). The same
> PR's item 1, a revoked registration says so, is Control's 0047, queued just
> before this one.
>
> **Where it sits.** Right after the revoked registration line (0047), and
> before the console (0049) and everything after it. It depends on nothing
> queued. It follows 0047 because both write `docs/API-VERSIONING.md` and were
> raised together, and 0047 already holds the earlier place. It goes before the
> console because the console shows what a supervisor did to a deployment, and
> it should show who the supervisor said did it from its first release. The
> operator-attribution renumbering note in `docs/PLAN.md` §10 gives one line of
> reasons for each neighbour.

## Read first
- `docs/PROTOCOL.md`: the session workflow. Read §3 for revising the plan in
  place, the `ext`/`server` promise, and the downstream hand-over this phase
  owes. Then read §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md`: **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a request
  and changes the north-bound API and `server.Caller`, so it owes a downstream
  sync (below).
- `docs/PLAN.md`:
  - **§2**: the register first, then **M19** in full. Above all read its
    second rule (the supervisor's credential is scoped, and every act through
    it is in this deployment's own trail, attributed to the supervisor) and
    "The shapes, as they stand", whose credential and version bullets this
    phase revises in place. Then read:
    - **M2** (a supervisor's acts are north-bound calls, OIDC or scoped
      tokens, nothing else);
    - **M8** (the trail is append-only and tamper-evident: the name is
      written into the hashed body, never beside it);
    - **M18** (tenancy is resolved from the credential, never asserted). The
      name is the first thing this surface takes from a caller's word, and it
      must stay outside everything M18 guards;
    - **M15** (`server.Caller` is a compatibility promise, and this is the
      honest default; the plane that sends the name is Enterprise's, its E14);
    - **M23** (every phase is a release).
  - **§3**, "Component responsibilities", at `internal/httpapi/north` and
    `internal/audit`, whose paragraph lists what an inbound action's records
    carry. This phase adds what a supervisor's call carries beside it.
  - **§6**, the RBAC paragraphs: "**RBAC is enforced in one place**", and
    "Own" (`AccessSelf`), the rule that a subject is resolved from the
    principal and never asserted.
  - **§7**: "Standing denials and inbound actions are audited on this
    server's own chain". It is the precedent for a key every record of one
    kind of act carries.
  - **§10**: this phase's row and the operator-attribution renumbering note.
- `docs/API-VERSIONING.md`: read it whole (it is short). It is the north-bound
  API's promise. This phase moves the version to `2`, and writes down two rules
  the promise does not yet state.
- `docs/learnings/`: read every summary. Open
  **`0046-instance-identity-and-supervisory-registration-learnings.md`** in
  full for the version negotiation, and **0047's** summary for what it wrote
  into `docs/API-VERSIONING.md`.
- Code. Read it before designing anything:
  - `internal/httpapi/north/middleware.go`: `enforce` (where a credential is
    authenticated and the principal put on the context), `requestFacts`,
    `logRequest` and `logRefusal`;
  - `internal/httpapi/north/apiversion.go` whole, and
    `TestTheAPIVersionIsNegotiated` in `instance_test.go`;
  - `internal/httpapi/north/grants.go`: `actorFrom`. Every actor on this
    surface is built from it, by `actorOf` (`policy.go`) and `standingActor`
    (`standing.go`);
  - `internal/audit/operatorevent.go` (`OperatorActor`, and the rule at its
    head that the actor comes from the credential and never from the body),
    `grantevent.go`, `contextevent.go` and `authevent.go` (`AttrPrincipalID`);
  - `internal/access/events.go` (`Actor`) and `internal/standing/service.go`
    (`Actor`, `audited`);
  - `internal/identity/federation.go`: `TokenRequest.Supervisor`,
    `SupervisorPrincipalPrefix` and `IsSupervisor`;
  - `internal/httpapi/north/host.go` (`HostCaller`) and `server/caller.go`
    (`Caller`, `CallerFrom`);
  - `internal/httpapi/north/audited_test.go`:
    `TestEveryWriteCommitsWithItsRecordOrNotAtAll`, whose list of every write
    on this surface this phase's test reuses;
  - `TestASupervisorIsAttributedListedAndRevocable` in `instance_test.go`.

## Context estimate
- **Reads:** this prompt, M19, `docs/API-VERSIONING.md`, the summaries, 0046's
  learnings, and the code above (~1 600 lines, most of it the middleware, the
  emitters and the audited-writes test).
- **Changes:**
  - code: one check in `enforce`, one context value, one field on four actor
    types and on `HostCaller` and `server.Caller`, and one attribute in three
    emitters; `north.APIVersion`;
  - tests: one test over every write, the middleware's cases, and the version
    test;
  - docs: `docs/API-VERSIONING.md`, PLAN M19's shapes, §3, §7 and §10's row,
    and `CHANGELOG.md`.
- **Why it fits:** no route, no migration, no new decision, nothing near the
  decision path, and no `ext` change. `server` gains one struct field, which is
  additive. The name is carried from one place to the records that already
  name the credential.

## Objective
Let a supervisor say which of its operators made a call, so the supervised
deployment's own trail can answer "who at our provider did this" without the
customer asking the provider.

Today, at `v0.33.0` and on `main`:

- every act through a supervisor's credential is in the deployment's own trail,
  attributed to its `sup-` principal (`principal_id`). That is M19's second
  rule, and it is met;
- nothing on a call can name the person at the supervisor who made it, and no
  record carries one. With one credential per supervisor, the trail answers
  what the provider's credential did, and not which of the provider's people
  did it;
- so Enterprise's 0013 cannot meet its E14's fourth rule: the customer answers
  "what did our provider do last Tuesday" from their own trail, and has to ask
  the provider "and who did it". Until this lands, 0013 builds the naming behind
  a seam, unwired, and keeps the plane's own record of which operator made each
  call. 0013 and 0017 assert the `sup-` attribution and skip the named half,
  naming the request.

After this phase a supervisor's call may carry the name of the operator it acts
for. Control records that name, as the supervisor's word, on every audit record
the call writes, beside the credential it verified. Nothing else changes: who
may call, what a call may do, and what the trail says about the credential.

## The request, in this repository's vocabulary
Enterprise asked for one request header, on a call authenticated by a
supervisor's token:

```
Hoplock-Acting-Operator: <the supervising operator's name, as the supervisor names them>
```

- It is accepted only when the caller is a `sup-` principal
  (`identity.IsSupervisor`). On any other caller it is `400 invalid_request`
  with `{field: "Hoplock-Acting-Operator", problem: "not_supervisor"}`, so an
  ordinary token cannot put a name in a record.
- The value is 1 to 128 characters of printable UTF-8 with no control
  characters. Anything else is `400 invalid_request`
  `{field: "Hoplock-Acting-Operator", problem: "invalid"}`.
- It is recorded as `acting_operator` on every audit record the call writes,
  beside `principal_id`, and is absent when the header was.
- It is **the supervisor's assertion, not Control's verification**. Control
  verified the `sup-` principal and records the name as that principal's word.
  A reader of the record sees both and can tell which is which.
- It is optional: a supervisor that sends no header is recorded as today.
- It is readable wherever the record is read, `GET …/audit/records` included.
  The request proposed this: "a new member with a documented absent value
  carries `since:"2"`, so the north-bound version moves to `2` under
  `docs/API-VERSIONING.md`."

Enterprise offered an alternative and left the choice to Control: one supervisor
token per supervising operator (`identity token-issue --supervisor --name
<operator>`), which `v0.33.0` already supports.

## What this phase answers
The request is **met as asked**: the header, its name, who may send it, the
two refusals, `acting_operator` beside `principal_id`, and the north-bound
version at `2`. Three things it left to Control are settled here.

### 1. The header, not a token per operator
The alternative was weighed and is not the answer. It is still available to
anyone who wants it, and the header works with it.

- **It verifies nothing more.** The plane holds every operator's token and
  chooses which one to present, so a per-person token records the plane's
  choice of person, exactly as the header does. The token would present that
  choice as a credential Control verified. The header presents it as what it
  is.
- **It costs the supervised operator, not the provider.** Under M19's first
  rule a supervisor's credential is minted locally, by the supervised operator
  (`identity token-issue --supervisor`). Then every change to a provider's
  staff is an act at every customer's deployment. The provider's own operators
  become credentials each customer must issue, list and revoke, and "the
  instance's own credential" (Enterprise's 0013 ceiling on an operator's scope)
  becomes one per person.
- **What it buys is kept.** A customer who wants a credential per person can
  still issue them. The header then names the operator on each, and the trail
  carries both.

### 2. What the name is, and what it is never
- **An assertion recorded, never an input.** The name is written down and
  shown. It is never a principal or a subject, never matched, and never read by
  RBAC, tenant resolution, the decision path, or any filter that widens what a
  caller reaches. A route's answer does not depend on it. The actor of every act
  is still built from the credential, never from the request
  (`operatorevent.go`'s rule). The name is the credential's word about who the
  actor acted for, and it is carried beside the actor and never in its place.
  So it is not `actor_subject`, which names a subject this deployment knows. It
  is also never the record's `subject`, which names whom the act concerned.
- **In the hashed body.** `acting_operator` is a key in the record's
  `attributes`, which the chain hashes (M8). The name is as tamper-evident as
  `principal_id`, and it reaches every reader of the record without any of them
  changing. The query routes render `attributes` whole, and a sink and the
  archive receive the hashed body verbatim (`ext.AuditRecord.Body`).
- **How it travels.** A header is bytes, and few operators' names are ASCII.
  RFC 9110 treats a non-ASCII octet in a field value as opaque, and an ingress
  may refuse it. So:
  - the value is the name's UTF-8, with every octet outside printable ASCII,
    and `%` itself, percent-encoded (RFC 3986 §2.1);
  - Control decodes the value, then checks it. Decoded, it must be valid
    UTF-8, 1 to 128 characters (runes), every one of them `unicode.IsPrint`,
    and with no leading or trailing space;
  - `unicode.IsPrint` takes letters, marks, numbers, punctuation, symbols and
    the ASCII space, and no control or format characters. So a bidirectional
    override, which would make a record display a name it does not carry, is
    refused;
  - over 128 characters is `problem: "too_long"` with `limit: "128"`, as
    `access.ProblemTooLong` is written today. Anything else wrong, including a
    header sent twice, an empty one, or one that does not decode, is
    `problem: "invalid"`;
  - the name is recorded decoded and exactly as sent: no case folding, no
    Unicode normalisation, no trimming.
- **Refused before the handler, after authentication.** `enforce` checks it
  right after the credential is authenticated, so a bad credential is `401`
  whatever the header says, and a refused header changes nothing. A header on
  an anonymous route has no supervisor behind it either, so it is
  `not_supervisor` there too.
- **Logged where the credential is.** The access log line and
  `northbound_refused` gain `acting_operator` beside `principal_id`, so an
  operator reading logs during an incident sees what the trail sees.

### 3. The version moves for the header, and a record's attributes are open
The request reached `2` through a `since` member. On this surface the name is
not one: it is a key of `attributes`, and the keys of that map have never been
members.

- **`attributes` is the contract's open map** (`LogRecord.attributes`,
  `additionalProperties: string`). A proxy adds keys as the contract moves, and
  the contract's version and the north-bound one move independently
  (`docs/API-VERSIONING.md`, "Three numbers"; M1). So a proxy's new key cannot be
  a north-bound member. Control's own records add keys the same way, as 0039's
  `action_id` and its four companions did. Treating a key as a `since` member
  would answer `500
  api_version_unexpressible` to every reader declaring `1` for any page that
  holds a named act. That includes the customer's own SIEM, for a record about
  them.
- **So this phase writes the rule down** in `docs/API-VERSIONING.md`, under
  "What 'additive' means":
  - a record's `attributes` keys are its writer's (the proxy's under the
    contract, or this server's for its own records), never members of this
    surface;
  - a reader skips a key it does not know;
  - a new key moves nothing;
  - a key whose meaning changes is a changed meaning.

  This is the same skip rule 0047 writes for the registration's lines.
- **The version still moves to `2`, for the header.** A new request header is
  additive, like a new route: an older client does not send it. It moves the
  version so that a client can tell whether a deployment honours it.
  - A plane that reads `versions.north_api` ≥ `2` sends the header and knows
    the name is recorded.
  - Against `1` (`v0.33.0`), the header would be ignored as an unknown header,
    and the act recorded with no name. Nothing would say so. So a plane sends
    no header to such an instance, and shows that the instance cannot record
    who acted.
  - The header is honoured whatever version a call declares, since it changes
    no answer. `north_api_min` stays `1`, so no deprecation window starts.
- **The refusals need no new code.** `invalid_request` exists. The problems
  `not_supervisor`, `too_long` and `invalid` are reached only by a client
  sending the header, which is a client written against `2`.

## In scope

### 1. `internal/httpapi/north`: the header, checked once
- `ActingOperatorHeader = "Hoplock-Acting-Operator"`, beside
  `APIVersionHeader`, and a function that decodes and checks a value. Unit-test
  it on its own (see "Tests").
- In `enforce`, after authentication and before the access class branches:
  - when the header is present, refuse it unless `identity.IsSupervisor` holds
    for the principal;
  - check it;
  - put the decoded name on the context under a key of its own, with
    `ActingOperatorFrom(ctx)` beside `PrincipalFrom`;
  - record it on `requestFacts` for the log line.

  On an `AccessAnonymous` route, a present header is `not_supervisor`.
- `actorFrom` copies the name onto `access.Actor`. `actorOf` and
  `standingActor` carry it on, so every actor this surface builds has it, from
  one read.
- `HostCaller` gains `ActingOperator`, filled in the same place as `Principal`.

### 2. The actors and the emitters
- `audit.OperatorActor`, `access.Actor` and `standing.Actor` each gain
  `ActingOperator string`, documented as the supervisor's assertion, empty
  unless the principal is a supervisor's and the call named one.
  `standing.Actor.audited` copies it.
- `AttrActingOperator = "acting_operator"`, beside `AttrPrincipalID`. Each
  emitter puts it from its actor with `put`, which already leaves an empty
  value out:
  - `OperatorEvent`;
  - `GrantEvent`;
  - the access-context events in `contextevent.go`.
- Revise the comment at the head of `OperatorActor` so it still says the actor
  comes from the credential, and adds that the acting operator is the
  credential's word, carried beside it.
- **What the call does not write is not in scope.** Some records are written
  after the call, by work it started: the claim mapping run's
  `operator.claim_mapping.applied`, whose actor is the version's author, and a
  sink's delivery. They stay as they are. The call's own record carries the
  name.

### 3. `server.Caller`
`Caller.ActingOperator string`, copied in `CallerFrom`. Document it as above,
plus one line for a host: record it as the supervisor's word, and never
authorise on it. It is a new field on a struct, with no method added to any
interface. `make release-check` must report it as compatible, which makes the
release a MINOR with nothing under `### Breaking` (M23, `docs/PROTOCOL.md` §3).

### 4. `docs/API-VERSIONING.md`
- Under "What 'additive' means", two rules:
  - the open `attributes` map (item 3 above);
  - a new request header is additive, like a new route, and moves the
    version.
- A section of its own, after negotiation, for the header: who may send it,
  its encoding and its checks, the refusals, that it is recorded as
  `acting_operator` as the supervisor's word, that it changes no answer, and
  that it is honoured at any declared version.
- The history table gains row `2`: this release, `Hoplock-Acting-Operator`
  and the `acting_operator` attribute.
- If 0047's registration-protocol section and "Three numbers" row are there,
  leave them as they are. This phase does not move the registration protocol.

### 5. The version
`north.APIVersion` becomes `2`, and `MinAPIVersion` stays `1`. Revise every
test that pins `1` as the current version (`TestTheAPIVersionIsNegotiated`
among them). `GET …/instance` reports `north_api: 2` and `north_api_min: 1`.

### 6. The plan, revised in place
- **M19, "The shapes, as they stand"**:
  - the north-bound version bullet says `2`, and why;
  - the credential bullet gains the header: who may send it, that it is
    recorded beside the `sup-` principal as the supervisor's word, and what it
    is never.

  M19's second rule stays as written: the act is attributed to the supervisor,
  and the name is the supervisor's word beside that attribution. M19 stays
  `live`. No decision is added, amended or withdrawn, so the register does not
  change.
- **§3**, `internal/audit`: one clause beside the inbound action's keys,
  saying a supervisor's call that names an operator carries `acting_operator`
  on every record it writes.
- **§7**: the same, one sentence, in the paragraph on standing denials and
  inbound actions or beside it.
- **§10**: this phase's row, from what was built, with its release.

### 7. `CHANGELOG.md`
The next MINOR after the latest tag. Under the release's changes:
- north-bound version `2`;
- `Hoplock-Acting-Operator` and its refusals;
- `acting_operator` on the records;
- `server.Caller.ActingOperator`;
- the open-`attributes` rule a reader is held to.

Nothing is under `### Breaking`.

## Out of scope
- **Verifying the name.** Control cannot, and does not pretend to. Its
  verification is the `sup-` principal, which is unchanged.
- **A credential per operator.** It is supported already (item 1), and this
  phase adds nothing to it.
- **The name on rows.** A grant's `created_by` and `ended_by`, a standing
  denial's actor, and a binding's `updated_by` stay the credential. So does the
  copy of a grant's actor in a decision record. Each would be a migration, and
  a `since` member on a view. The record of the act is the trail E14 asks for,
  and a reader reaches it from the row by the grant's, denial's or binding's
  id.
- **A filter on it.** `GET …/audit/records` gains no `acting_operator`
  parameter. A reader filters by the credential, and reads the name on the
  records.
- **Recording reads.** A supervisor's reads write no record today, and naming
  an operator does not make them start.
- **Any caller other than a supervisor naming anyone.** An ordinary token,
  a session or an inbound action handler cannot send the header.
- **The registration.** The supervisory registration carries no acts (M19),
  so nothing on it names an operator.
- **Enterprise's item 1**, the revoked registration line. That is 0047.

## Acceptance criteria
- A supervisor's call carrying `Hoplock-Acting-Operator` writes
  `acting_operator`, decoded and exactly as sent, on **every** audit record it
  writes, beside its `sup-` `principal_id`. That holds for every write in
  `TestEveryWriteCommitsWithItsRecordOrNotAtAll`'s list, through a credential
  scoped to the write.
- The same call without the header writes records identical to today's: no
  `acting_operator` key.
- The name is inside the record's hashed body: the chain verifies, and a stored
  record whose `acting_operator` is edited no longer does.
- A session, an ordinary `tok-` token or any other non-supervisor caller
  sending the header is answered `400 invalid_request`
  `{field: "Hoplock-Acting-Operator", problem: "not_supervisor"}`, on a read
  and on a write. Nothing is changed or recorded, and so is a header on an
  anonymous route.
- A supervisor sending a value that does not decode, is not valid UTF-8, is
  empty, has a leading or trailing space, carries a control or format
  character, or is sent twice is answered `problem: "invalid"`. Over 128
  characters is `problem: "too_long"`. Nothing is changed or recorded.
- A bad credential with the header is `401`, never `400`.
- A name that needed encoding (non-ASCII letters, a `%`) is recorded decoded.
  A name of exactly 128 characters is accepted.
- The name changes no answer. The same supervisor's call returns the same
  status and body with and without it. It is reachable from no RBAC, tenant,
  subject or decision path.
- `GET …/instance` reports `north_api: 2`, `north_api_min: 1`. A client
  declaring `1` is served as before, including a record carrying
  `acting_operator`, never `500 api_version_unexpressible`.
- A host route's `server.CallerFrom` returns the name. A report provider's
  call does too, on a call that named one.
- The access log line and `northbound_refused` carry `acting_operator` when
  the call named one.
- `docs/API-VERSIONING.md` states the header, the open-`attributes` rule and
  version `2`. M19's shapes, §3, §7 and §10 say what was built.

## Tests
- `internal/httpapi/north`:
  - **the check, on its own:** table cases for each refusal, the 128 boundary,
    a percent-encoded name, a raw `%` that does not decode, and a U+202E
    override;
  - **every write names the operator:** a test that reuses
    `TestEveryWriteCommitsWithItsRecordOrNotAtAll`'s list, or that list
    extracted so both tests share it. It runs each write with a supervisor
    token and the header, and asserts every record the write committed carries
    `acting_operator`. A write added later fails it until listed, as it fails
    the first test;
  - **the refusals:** `not_supervisor` for a session, a `tok-` token and an
    anonymous route; ordering against `401`; nothing recorded on a refusal;
  - **the version:** `TestTheAPIVersionIsNegotiated` at `2`; a client
    declaring `1` reads a record carrying `acting_operator`;
  - **the answer does not depend on the name:** one write and one read,
    compared with and without it.
- `internal/audit`: each of the three emitters writes the key from its actor
  and leaves it out when empty. A chain over a named record verifies, and fails
  once the stored body's `acting_operator` is changed.
- `server`: a host route reads `Caller.ActingOperator`.
- `go test ./...`, and `make release-check` reporting the `server` change as
  compatible.

## Release (M23)
The next MINOR after the latest tag, with nothing under `### Breaking`.
`make release-check` reports one compatible change to `server`
(`Caller.ActingOperator`), and none to `ext`.

## Cross-repo impact
This phase answers an upstream request and changes the north-bound API, its
version and `server.Caller`, all of which Enterprise consumes, so it owes a
downstream sync. Its PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what was
built, and the session there that rewrites its prompts will be a fresh one.
State at least these obligations:

1. **Its 0013, the actions.** The plane sends `Hoplock-Acting-Operator` on
   every call it makes for a named operator, encoded as built. It sends it only
   to an instance whose `versions.north_api` is at least `2`. To an instance at
   `1`, it sends no header and shows that the instance cannot record who acted.
   The seam is wired from this release. Its rule against writing a name into a
   `reason` or other free text stands unchanged. The plane keeps its own record
   of which operator made each call: it is the provider's own answer, and the
   instance's record is the customer's.
2. **Its 0013 and 0017, the criteria.** The criteria that skip the named half
   assert it from this release: the instance's records carry `acting_operator`
   beside `sup-`. They still skip it against an instance below this release.
3. **What Control decided that it did not ask for:**
   - the header rather than a token per operator, and why (item 1);
   - percent-encoding, and `too_long` as a third problem (item 2);
   - the name never on rows and never a filter (out of scope);
   - the version at `2` because of the header, with `attributes` an open map
     whose keys move nothing and which a reader skips when it does not know
     one (item 3);
   - `server.Caller.ActingOperator`, for its own host routes' records.
4. **Its plan.** PLAN §5's "Supervising one of Control's deployments": the
   credential's paragraph gains the header, the version is `2`, and the gap
   it names is closed from this release.
5. **Its pin.** Every obligation holds from the release this PR cuts (M23),
   never from `main`. Name the release by version. Name `server`'s new field
   as the release's only public API change.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks, as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`. Commit it
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"**. "None" is a finding:
- the contract is untouched (M1);
- the proxy sends no north-bound calls;
- the attribute is Control's own, on Control's own records.

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0048-supervising-operator-attribution-learnings.md`.
Enterprise's sync is written from its summary block, which MUST give:

- the header as built: who may send it, its encoding, its checks and its
  refusals;
- `acting_operator`: on which records, and what it is never;
- north-bound version `2`, why, and the open-`attributes` rule;
- `server.Caller.ActingOperator`;
- the release, by version.
