# 0048 — the supervising operator named in the instance's trail — Learnings

## Summary
- **Shipped (`v0.35.0`, MINOR, nothing under `### Breaking`; no migration; `ext`/`contract` untouched; `server` gains `Caller.ActingOperator`, the release's only public API change):** the `Hoplock-Acting-Operator` request header, the `acting_operator` attribute, north-bound API version `2`. Answers `Hoplock/enterprise#64`, item 2, **as asked**.
- **The header:** only from a `sup-` principal (`identity.IsSupervisor`); anyone else — a `tok-` token, a session, an anonymous route — is `400 invalid_request {field: "Hoplock-Acting-Operator", problem: "not_supervisor"}`. Value = the name's UTF-8, every octet outside printable ASCII and `%` percent-encoded (RFC 3986 §2.1); a raw octet outside 0x20–0x7E is refused. Decoded: valid UTF-8, 1–128 runes, all `unicode.IsPrint` (no control/format char, so no bidi override; no NBSP), no leading/trailing space → else `invalid`; over 128 → `too_long`, `limit: "128"`. Sent twice, empty, undecodable → `invalid`. Checked once in `enforce`, after authentication (bad credential = `401` first), before anything changes: a refusal records nothing.
- **`acting_operator`** (`audit.AttrActingOperator`): beside `principal_id`, inside the hashed body, on every record the call writes — `OperatorEvent`, `GrantEvent`, `ContextEvent` and (beyond the prompt's three) the `logout` `AuthEvent` of a supervisor ending its own token. Recorded decoded, exactly as sent. Never `actor_subject`, the record's `subject`, a row (`created_by`, `updated_by`, standing actor), a filter, or an input to RBAC/tenancy/decisions; changes no answer. Not on records written later by work the call started (`operator.claim_mapping.applied`, sink delivery). Logged on `northbound_request` and `northbound_refused`.
- **Carried by:** `north.ActingOperatorFrom(ctx)` → one read in `actorFrom` → `access.Actor`, `audit.OperatorActor`, `standing.Actor` `.ActingOperator`; `north.HostCaller` and `server.Caller` `.ActingOperator` (host routes and report-provider calls).
- **Version `2`, `north_api_min` `1`:** moved for the header (a new request header is additive, like a new route, and moves the version so a client can tell it is honoured). Honoured at any declared version. `docs/API-VERSIONING.md` now states the open-`attributes` rule: keys are their writer's, never members; a reader skips an unknown key; a new key moves nothing; a changed key meaning is a changed meaning.
- **Gotcha:** with `2` current, every client declaring `1` is now answered through the reflective `project` in production. `answer_names_test.go`'s probe now projects every body the package's tests render to every served version and fails the package unless it encodes as encoding/json does. **The first phase to add a `since` member must revise that check** (older versions then differ by exactly the members they omit).
- **NEXT:** a new write must enter `everyWrite` (`audited_test.go`); both `TestEveryWriteCommitsWithItsRecordOrNotAtAll` and `TestEveryWriteNamesTheActingOperator` run it. Downstream sync for `hoplock/enterprise` is queued (see the PR).

## Details

### Why the header, and not a token per operator
The prompt settled this and the build follows it: a per-person token verifies
nothing more (the plane holds every token and chooses one, exactly as it
chooses a header value), and it turns every change to a provider's staff into
an act at every customer's deployment, since a supervisor's credential is
minted locally (M19's first rule). A customer who wants a token per person can
still issue them; the header then names the operator on each.

### Where the check sits, and why there
`enforce` (`middleware.go`) authenticates, puts the principal on the context,
then calls `actingOperator(r)` (`acting.go`). A present header from a
non-supervisor becomes `not_supervisor` regardless of its value: the caller's
identity is the first problem. On an `AccessAnonymous` route the header is
refused before the handler runs, because there is nobody to have said it. The
decoded name goes on the context under `ctxKeyActingOperator` and on
`requestFacts.actingOperator` for the access log. Because it is set before
the tenant and permission checks, `logRefusal` reads it from the context and
`northbound_refused` carries it.

`DecodeActingOperator` is exported (within `internal/`) so the decode/check is
unit-tested on its own (`acting_internal_test.go`). It refuses raw octets
outside printable ASCII before decoding, uses `url.PathUnescape` (so `+` stays
`+`), then checks UTF-8, printability, edges, and length — "invalid" checks
before `too_long`, so a 200-character name with a bidi override is `invalid`.
Go's HTTP server strips leading/trailing OWS from field values, so a raw
leading space never reaches the check; an encoded one (`%20`) does, and is
refused.

### Why `logout` too (a deviation, additive)
The prompt named three emitters. `DELETE /session` is `AccessAuthenticated`,
and a supervisor's token can call it to end itself; that writes a `logout`
`AuthEvent`. Leaving it out would make "every record the call writes" false
for one call, so `identity.AuthEvent` gained `ActingOperator`, and
`Federation.EndSession` takes it as a fourth argument (internal signature; one
caller, plus one test). No other `AuthEvent` is written by an authenticated
call: logins are anonymous routes, where the header is refused.

### The tests
- `TestTheActingOperatorIsDecodedThenChecked` — the decode table: each
  refusal, the 128 boundary, percent-encoded non-ASCII, a raw `%`, U+202E,
  ZWJ, NBSP, edge spaces.
- `TestEveryWriteNamesTheActingOperator` — runs `everyWrite`'s list (extracted
  from `TestEveryWriteCommitsWithItsRecordOrNotAtAll`, which now calls the
  same function, so a write added to one is in both) with an admin-scoped
  supervisor and the header; every record committed by the write, read from
  the hashed `body` column, must carry the name and the `sup-` principal.
  Then the grant create/revoke (`GrantEvent`) and `POST /revocations`. Then
  an unnamed call writes no key. Then the control chain verifies, and fails
  once one stored body's `acting_operator` is edited.
- `TestAccessContextRecordsNameTheActingOperator` — binding put, push, binding
  delete (`ContextEvent`), through `newPushServer`.
- `TestOnlyASupervisorMayNameAnOperatorAndOnlyFaithfully` — `not_supervisor`
  for a token, a session and an anonymous route on a read and a write; `401`
  before the header; each bad value; nothing changed or recorded; 128 accepted.
- `TestTheActingOperatorChangesNoAnswer` — a role binding and a target list,
  named and unnamed, same status and body.
- `TestAVersionOneClientReadsANamedRecord` — undeclared, `1` and `2` read the
  same record and list, the name included, never `500`.
- `TestTheHostCallerCarriesTheActingOperator` — a report provider's `Reports`
  and `Generate` see it; both log lines carry it; an unnamed line does not.
- `internal/audit/acting_test.go` — each emitter (and `AuthEvent`) writes the
  key from its actor, omits it when empty, and the chain detects an edit.
- `server/server_test.go` — a real daemon's host route reads
  `Caller.ActingOperator`.
- Mutations checked by hand: dropping the name in `standingActor` fails exactly
  the four standing-denial writes; disabling `omitempty` in `project` fails the
  projection probe.

### What the prompt left to Control, as decided
- Header, not a token per operator (above).
- Percent-encoding, and `too_long` as a third problem.
- Never on rows, never a filter (`GET …/audit/records` gains no parameter).
- Version `2` because of the header; `attributes` an open map.
- `server.Caller.ActingOperator` for a host's own records.

### Follow-ups (not queued)
- None owed. The console's audit view (0051, Explain and the audit view) carries the obligation to render
  `acting_operator` as the supervisor's word (its prompt says so).
