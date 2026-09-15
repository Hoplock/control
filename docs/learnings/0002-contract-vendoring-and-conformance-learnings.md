# 0002 — contract vendoring & conformance harness — Learnings

## Summary
- **What shipped:** `contract/` vendored read-only + a checksum drift check
  (`make contract-check`, its own CI job) + `make contract-sync REF=<ref>`;
  `internal/contract` (wire types, enum constants, absent-value resolvers,
  handler interfaces); and `cmd/pdpconform`, a black-box conformance suite —
  **38 assertions, green in CI against Hoplock Proxy's `cmd/mock-control`.**
- **Vendored:** `hoplock/proxy@37359c5`, `api/control.yaml`. `info.version`
  **4.0.0**, `policy_version` **4** — two independent numbers; the drift check
  keys off the SHA-256 in `contract/UPSTREAM` and reads nothing inside the file.
  **This is now one revision behind upstream:** `Hoplock/proxy#56` (merged,
  `7c2a356`) took the document to **4.1.0** — `policy_version` unmoved at `4` —
  and **phase 0009 re-vendors** at that commit. Nothing breaks meanwhile: CI
  pins `cmd/mock-control` to the commit in `contract/UPSTREAM`, so the suite
  grades this repository against the document it actually holds.
- **Types are HAND-WRITTEN** (open `params`/`device_field.` namespaces, a
  `oneOf`, and a prose-only absent-value discipline all defeat a generator —
  Details). To change one: edit `internal/contract/types.go`, then
  `go test ./internal/contract/` — the enum test reads the vendored document and
  compares **both directions**. There is no generate step to re-run.
- **Types per endpoint:** `Authenticate{Cert,Password}Request`/`MFAPollRequest`
  → `AuthenticateResponse`; `AuthorizeRequest`→`AuthorizeResponse`; and the
  `{HostKeyReport,CapabilityReport,UIDLease,LogBatch,LogPriority}Request`
  /`Response` pairs, plus `RevocationEvent`. Handler seams: `Authenticator`,
  `Authorizer`, `HostKeyReporter`, `CapabilityReporter`, `UIDLeaser`,
  `LogIngester`, `EventPublisher`, `Southbound`.
- **Read absent-value defaults through the resolvers, never the fields:**
  `Ladder`, `Profile`, `EnforcedExecution`, `EnforcedReach`, `CaptureRequired`,
  `Deadline`, `Caps`, `FilterPolicy.Tier`, `Direction`, `Cacheable`, `Policed`.
- **Run it:** `make conform BASE_URL= TOKEN= EXPECT=` (`CONFORM_FLAGS=-v` shows
  that a passing case graded something). Expectation-file format:
  `cmd/pdpconform/README.md`. **0017 needs a new expectation file and no Go.**
- **Migrations added:** none. **Decisions:** none added/amended/withdrawn, so the
  §2 register is unchanged; PLAN §3 revised (contract types are hand-written).
- **Contract ambiguities (findings, both now ANSWERED upstream by
  `Hoplock/proxy#56`, merged):** the heartbeat interval gained a field —
  `RevocationEvent.heartbeat_interval_seconds` — and the missing publish/read-back
  surfaces were confirmed as deliberate, since neither operation is proxy-facing.
  The contract is still vendored at the older commit, so **phase 0009 re-vendors
  and lands both consequences**; until then the suite grades the heartbeat bound
  from its expectation file. Details below.
- **Cross-repo:** none owed. This PR consumes `api/control.yaml` and leaves
  `ext/` untouched.
- **NEXT session:** `contract/` is generated output — editing it fails CI by
  design. Every endpoint you build is graded by `cmd/pdpconform`; run it before
  claiming an endpoint works, and fix the case, not the expectation file.

## Details

### Vendoring, and why the drift check is a checksum

`contract/UPSTREAM` records repository, path, ref, commit, commit date, sync
date, filename, and SHA-256. `scripts/contract-check.sh` recomputes the digest
and fails with the two hashes and the remedy; `scripts/contract-sync.sh` does a
blobless partial clone, resolves the ref, writes the file, and regenerates
`UPSTREAM`. Both are shell rather than Go so they run before anything compiles.

The check reads **nothing inside the document**. That is a decision worth
keeping: `info.version` and `policy_version` are independent numbers, and the
document's version does not only ever rise — upstream `#53` moved it *down*,
4.3.0 → 4.0.0, while the vocabulary stood still at 4. A drift check keyed on a
version would have called that sync a downgrade, and would have called a
hand-edit that left the version alone no drift at all.

`contract_test.go` proves the check rather than asserting it: it copies
`contract/` and `scripts/` to a temp tree, changes `openapi: 3.0.3` to `3.0.4`
— one plausible byte, in a place that still parses — and requires the script to
fail and to name the recorded hash, the actual hash, and `contract-sync`.

Syncing twice in a week is cheap: `make contract-sync REF=<sha>` is the whole
procedure, and re-running it against the same ref is a no-op that reproduces the
same checksum (verified).

### Generated or hand-written: hand-written, and why

The prompt allows either. Generation loses on three specific shapes:

- `TargetAuth.params` is an **open** `map[string]string`, and the
  `device_field.<name>` namespace inside it is open too. A generator emits the
  map and nothing else; the shape rules (name pattern, ≤64 chars; value
  non-empty, ≤256; ≤16 per entry) would have to be hand-written beside it
  anyway, which is where they are now (`DeviceField*` in `enums.go`).
- `grant_context.additional_context` is a `oneOf` of **string or object, and
  nothing else** — a number or a list is a contract violation rather than
  something to coerce. Generators render that as `any` or as a generated union
  nobody enjoys; `AdditionalContext` with its own `UnmarshalJSON` says the rule
  in six lines.
- The absent-value discipline is the contract's core rule and is expressed in
  *prose*, not in the schema. Absent `permitted_requests` is "not policed" and
  `{}` is "deny everything"; no generator produces `Policed()`. Leaving each
  caller to get that right is how a truncated response ends up failing open.

What generation would genuinely have bought — catching an enum drift, a renamed
field, a removed path — is bought instead by `enums_test.go`, which reads the
vendored document and compares it with the constants **in both directions**. The
reverse direction is the one that matters more than it looks: a value *deleted*
upstream that this repository still offers is how a server ends up serving a
rung the proxy has stopped implementing. Proved by mutating a constant and
watching both halves fail.

Cost of the choice: adding a payload field is a manual edit in two places
(`types.go`, and a line in `enums.go` if it carries an enum). The enum test
catches the second; nothing catches a *non-enum* field that upstream added and
nobody mirrored, other than a conformance failure — which is what the suite is
for.

### The suite's shape

`cmd/pdpconform` is one `package main`: `runner.go` (HTTP, results, the
`Case`/`abort` mechanism that lets one assertion fail without ending the run),
`expectations.go` (strict YAML, validated against vacuity), and one
`checks_*.go` per endpoint group.

It **does** import `internal/contract`, which is a deliberate softening of the
old `cmd/pdpconform/README.md`'s "it does not import this server". The
alternative is a second copy of every payload type, which diverges and then
grades the divergence. What keeps the import from becoming
agreement-with-itself is that those types are themselves tested against
`contract/control.yaml`, and that every absent-field assertion reads raw JSON
rather than the decoded struct. Nothing else from this server is imported, and
nothing should be: the suite has no database, no in-process handler, and no
knowledge of how any answer was reached.

Two mechanics are worth knowing before extending it:

- **`response.has(key)` reads the wire, not the struct.** Every absent-value
  assertion rests on it: a decoded `AuthorizeResponse` cannot tell
  `require_session_capture: false` from an omitted key, and those are exactly
  the two readings the contract insists are different.
- **Request bodies are built as maps, not structs**, so a case can send a body
  the Go types cannot express — an authorize request with `policy_version`
  omitted *entirely* is the whole of the 400 case.

### Three assertions written against a rule rather than a literal

These are the ones a later session is most likely to "simplify" into a literal
and thereby break:

1. **Vocabulary negotiation.** The suite sends the same request at the current
   version and at `1`. A `5xx` passes (the correct answer when the policy needs
   vocabulary the caller cannot read). A `200` passes **only if it dropped no
   policy field the current-version answer carried**; a `401` never passes. The
   comparison is against the current-version *answer* rather than against a
   table of which field arrived in which revision — `#53` deleted every such
   table, both documents now read in the present tense, and an assertion citing
   a revision by number would be describing a section that does not exist.
   Against the mock this takes the `5xx` branch, which is the meaningful one.
2. **Device fields demand no bump.** The suite finds the lowest version at which
   a comparable route *without* device fields is served, and asserts the route
   *with* them is served at the same one. Pinning it to `4` makes it stale at
   the next revision, and expecting a bump asserts a rule the contract does not
   have.
3. **Absent-value defaults are graded in PAIRS** — a route that answers without
   the field and one that answers with it. The one-sided version passes
   vacuously against a server that cannot express the field at all, and a
   vacuous pass is worse than a failure because it reads as coverage.

### The uid endpoint is the reason the suite is not a request-per-assertion tool

Everything about `/v1/uids/lease` looks fine on a server that hands the same uid
out twice. So the suite leases **repeatedly** against one target and asserts no
grant ever intersects an earlier one — including a block it abandons, a block it
lets expire past the term the server itself stated, and blocks taken by a
**second `proxy_id`**, because exclusivity is per target and a server keying its
cursor by proxy would look perfect to a single-proxy suite.

**The suite suffixes every uid target with a token unique to the run**
(`uid-exhaust.company.com` → `uid-exhaust-1a2b3c4d.company.com`). This was
found rather than designed: the first version reused fixed names, and the second
run against a live server reported "exhausted after **0** grants" — a pass that
graded nothing, because the cursor was already at the top. The exhaustion case
now also requires at least one grant before the `409`.

`observed_floor` is graded in both directions on a third target: a floor above
the last grant must raise the cursor, and a floor *below* it must not pull the
cursor down — a lowered floor is uid reuse.

### Contract ambiguities (cross-repo findings) — both answered upstream

Raised here rather than resolved unilaterally (PROTOCOL §3, M1) and recorded in
`cmd/pdpconform/README.md` too. **Upstream `Hoplock/proxy#56` (merged, `7c2a356`)
answered both**, in opposite directions: the first became a field, the second
became a stated boundary. This section records the answers; the work of adopting
them is phase 0009's, because the contract here is still vendored at an older
commit and a sync vendors nothing (`docs/CROSS-REPO-PROTOCOL.md` §3.1).

1. **The heartbeat interval had no field — it has one now.**
   `RevocationEvent.heartbeat_interval_seconds` names the interval the server is
   **currently keeping**: normally on `heartbeat` events, legal on any event, and
   a later event carrying a different value re-states the interval rather than
   contradicting an earlier claim. Three rules travel with it and matter more
   than the field does — **absent means what every server did before it existed**
   (the reader stays on its own timers, the same absent-value discipline as
   `HostKeyReportResponse.cache`); **it may only ever tighten detection, never
   loosen it** (sooner always, later never — the `cache.ttl_seconds` and
   `report_after_seconds` rule, because otherwise a hostile server silences
   itself by announcing that it intends to); and **the ceiling stands and the
   field does not replace it**, at 10 seconds or less
   (`control.MaxHeartbeatIntervalSeconds`), so two consecutive intervals fit
   inside the proxy's 20s reconnect timeout.

   So PLAN §4's "arrive within the interval the server advertises" is gradeable
   from the wire, and it is **two** assertions: the server keeps the interval it
   advertises, *and* that interval is inside the ceiling. A server advertising
   600s and honestly keeping to it passes the first and breaks the fleet.
   `events.heartbeat_interval_seconds` survives as the fallback for a server that
   advertises nothing, which is still a conformant server.

   The document version moved `4.0.0` → `4.1.0` for this while `policy_version`
   stood still at `4` — the field is on the event stream, not on `/v1/authorize`,
   and only the latter is governed by the number. It is a second worked example
   of the independence 0018 audits.

2. **Nothing publishes an event or reads a record back — and that is the answer.**
   The suspicion recorded here ("this is probably correct — neither is a
   proxy-facing operation") was right, and upstream now says so outright. A proxy
   writes audit records and never queries them, and an event originates from an
   operator action on a surface `/v1` does not describe; an endpoint for either
   would oblige every Hoplock Control to implement an API no proxy calls. An
   implementation that wants the durability and gap-recovery guarantees **graded**
   therefore exposes paths of its own **outside `/v1`**, and a harness takes them
   as inputs — which is exactly what `logs.read_url` and `events.publish_url`
   already are. `cmd/mock-control`'s `GET /debug/logs` and `POST /debug/revoke`
   are the reference shapes and stay mock-only.

   Two of PLAN §4's six obligations still cannot be graded against a server that
   serves only the contract. The difference is that this is now a documented
   boundary with a reason rather than something the contract forgot — so the
   obligation to expose those paths is **this repository's**, and it lands in the
   phases that own them: 0009 for the publish path, 0010 for the read path.

### Running it, and what CI does

CI gains two jobs. `contract` runs `make contract-check` alone, so the failure
names itself. `conform` reads the commit out of `contract/UPSTREAM`, checks out
`hoplock/proxy` **at exactly that commit** (pinning it to `main` would make an
upstream revision look like a conformance failure here), builds and starts
`cmd/mock-control` against `cmd/pdpconform/testdata/mock-fixtures.yaml`, and
runs the suite with `-v` so a reader of the log can see each case graded
something.

The mock passed every assertion on the first complete run. That is a fact about
the mock, not a licence to relax: the suite's failure modes were checked the
other way round, by pointing the expectation file at the wrong routes (a
"default" route that sets the field, a deny route the server allows, an unknown
"known" host key, a ladder with no `brokered-key` entry) and confirming each
produces a named failure and a non-zero exit.

### Follow-ups, and what is deliberately not here

- No endpoint is implemented (0007 onward). The suite was written before the
  server exists, on purpose.
- `internal/contract` carries no `Clone`, no `Validate`, and no cache/revocation
  client. The proxy's `internal/control` has those because it is a *client* with
  a decision cache; this side is the server and needs none of them yet. 0008 may
  want a `Validate` — the fail-closed gate the mock uses on its fixtures — and
  it belongs in `internal/contract` when it arrives.
- `ErrCode*` in `enums.go` are conventions rather than an enum: `error.code` is
  a free string in the document. 0007/0008 should keep using them rather than
  inventing codes at each call site.
- Nothing here touches a shared surface in the outbound direction
  (`docs/CROSS-REPO-PROTOCOL.md` §1): this PR *consumes* `api/control.yaml` and
  leaves `ext/` untouched, so no downstream sync is owed to Enterprise.
