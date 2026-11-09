# The north-bound API and registration protocol versions

The north-bound API (`/api/v1/…`) is a compatibility promise (PLAN **M19**). A
supervisor consumes many deployments at many releases, so the surface carries a
version of its own and says which one each answer is in. The supervisory
registration, the one connection a supervised deployment holds open to its
supervisor, carries a number of its own for its lines. This file is the
promise for both: the north-bound API first, then the registration protocol.
The code is `internal/httpapi/north/apiversion.go` and
`internal/instance/register.go`.

## Four numbers, none of which is another

| Number | What it versions | Where |
| --- | --- | --- |
| the module release, `vMAJOR.MINOR.PATCH` | the software (M23) | `GET …/instance` → `versions.software` |
| the contract's `info.version` | the proxy contract this build vendors (M1) | `versions.contract` |
| **the north-bound API version**, an integer | this surface | `Hoplock-Api-Version`, `versions.north_api` |
| **the registration protocol version**, an integer | the supervisory registration's lines, both ways | `hello`'s `protocol`, never `GET …/instance` |

A release can move any of them without the others. `policy_version` is a fifth,
and it is the proxy's (§4).

## Negotiation

- A client declares the version it reads: `Hoplock-Api-Version: N`.
- A client that declares nothing is served the **current** version. That is
  every client shipped with the server — the console, `policyctl`, a host's
  own — and only those should rely on it. **A client shipped apart from the
  server declares**, always.
- Every answer carries `Hoplock-Api-Version` naming the version it was served
  in.
- A declared version the server does not answer in — older than
  `versions.north_api_min`, newer than `versions.north_api`, or not a number —
  is `400 api_version_unsupported`, with `parameters.min` and `parameters.max`.
  The server never guesses.
- An answer the server cannot express in the declared version is `500
  api_version_unexpressible` with a correlation id: never a thinned body, and
  never a `401` (M11).

## What "additive" means

A change is **additive** when a client of the previous version is served exactly
what it was served before. Concretely:

- **A new member** of an answer, with a **documented absent value**, is
  additive. Its view field carries `since:"N"`. A client declaring an older
  version is not sent it while it holds its absent value — Go's zero value is
  the absent value on this surface — and is answered `500
  api_version_unexpressible` when it holds anything else, because leaving it
  out would tell that client something false. (This is the south-bound rule:
  a restriction an old reader cannot see is not silently dropped.)
- **A new route** is additive. An older client does not call it.
- **A new request header** is additive, like a new route: an older client does
  not send it, and is served exactly as before. It still moves the version, so
  that a client can tell from `versions.north_api` whether a deployment honours
  it. Without that, a header sent to a deployment that predates it is ignored
  as any unknown header is, and nothing says so.
- **A record's `attributes` is an open map, and its keys are not members.** An
  audit record's `attributes` (the contract's `LogRecord.attributes`,
  `additionalProperties: string`) holds its writer's keys: the proxy's under
  the contract, which moves on its own version (M1), or this server's for its
  own records. So:
  - a reader skips a key it does not know, and refuses none;
  - a new key moves nothing, here or anywhere else, and is never a `since`
    member: an older client is served a record carrying it as it stands, never
    `500 api_version_unexpressible`;
  - a key whose meaning changes is a changed meaning (below).

  This is the registration protocol's skip rule, applied to one map.
- **A new error code**, or **a new value of a closed set** (a health state, a
  reason code, an event kind), moves the version: a client written against a
  closed set may refuse a value it does not know.
- **A changed meaning is never additive**, whatever it does to the JSON: the
  same member saying something different, a list in another order, a cursor
  that does not continue. It moves the version **and** raises
  `north_api_min` once the deprecation window has passed.

Every change that moves the version moves it by one, and the release that does it
says so in `CHANGELOG.md`.

## The acting operator: `Hoplock-Acting-Operator`

A supervisor holds one credential per deployment it supervises (M19), and its
plane acts through it for many people. So a supervisor's call may name the
supervising operator it acts for, and the deployment records that name, as the
supervisor's word, on every audit record the call writes. Since version `2`.

- **Who may send it.** A caller whose principal is a supervisor's (`sup-…`,
  minted by `identity token-issue --supervisor`). From any other caller — an
  ordinary token, a session, or nobody, on an anonymous route — it is `400
  invalid_request` with `{"field": "Hoplock-Acting-Operator", "problem":
  "not_supervisor"}`. An ordinary credential cannot put a name in a record.
- **The encoding.** The value is the name's UTF-8, with every octet outside
  printable ASCII, and `%` itself, percent-encoded (RFC 3986 §2.1): `José` is
  `Jos%C3%A9`, and `100%` is `100%25`. Printable ASCII other than `%` may be
  sent as it is. A raw octet outside printable ASCII is not a value this
  header can carry.
- **The checks**, on the decoded name: valid UTF-8; 1 to 128 characters
  (runes); every character printable (Go's `unicode.IsPrint`: letters, marks,
  numbers, punctuation, symbols and the ASCII space — no control or format
  character, so a bidirectional override is refused); no leading or trailing
  space. Over 128 is `problem: "too_long"` with `limit: "128"`. Anything else
  wrong — a value that does not decode, an empty one, one sent twice — is
  `problem: "invalid"`.
- **When.** After the credential is authenticated and before anything the
  request could change: a bad credential is `401` whatever the header says, and
  a refused header changes nothing and records nothing.
- **What is recorded.** The name, decoded and exactly as sent (no case
  folding, no normalisation, no trimming), as `acting_operator` in the
  `attributes` of every audit record the call writes, beside `principal_id`.
  It is inside the hashed body, so it is as tamper-evident as the principal,
  and every reader of the record sees it: `GET …/audit/records`, a sink, the
  archive. Records written later by work the call started (a claim mapping's
  `operator.claim_mapping.applied`, a sink's delivery) are not the call's, and
  carry no name. The access log line (`northbound_request`) and
  `northbound_refused` carry it beside `principal_id`.
- **What it is.** The supervisor's assertion, not this server's verification.
  What was verified is the `sup-` principal, and nothing about it changes. The
  name is never a principal, a subject (`actor_subject`) or the record's
  `subject`; it is never on a row (a grant's `created_by`, a standing
  denial's actor, a binding's `updated_by`); it is never a filter; and nothing
  — RBAC, tenant resolution, the decision path — reads it. **It changes no
  answer**: the same call returns the same status and body with or without it.
- **At any declared version.** It is honoured whatever version a call
  declares, since it changes no answer. A client that relies on it reads
  `versions.north_api` first: at `2` or later the name is recorded, and at `1`
  (`v0.33.0`, `v0.34.0`) the header is ignored and the act is recorded with no
  name — so a client sends none to such a deployment, and says that it cannot
  record who acted.

## The bounded-list shape is promised list by list

Every list answers `{<list>, truncated, next}` and takes `?limit=` and `?after=`
(M26). That shape, each list's order and the continuity of its cursor are part
of the version: a client paging a list at version N pages it the same way at
N+1.

## Deprecation window

When a version stops being served (`north_api_min` rises past it):

1. The release that introduces its successor names, in `CHANGELOG.md`, every
   change a client of the old version must make.
2. The old version is served for **at least two further MINOR releases and at
   least 180 days** after the release that superseded it, whichever is later.
3. The release that stops serving it lists that under `### Breaking`.

A client learns the range from `GET …/instance` (`versions.north_api`,
`versions.north_api_min`) and from any `api_version_unsupported` answer, never
by breaking.

## History

| Version | First served in | What it is |
| --- | --- | --- |
| 1 | `v0.33.0` | the surface as it stood at 0046: every route and member until then, plus `…/instance`, `…/instance/health` and `…/instance/supervisors` |
| 2 | `v0.35.0` | the `Hoplock-Acting-Operator` request header, from a supervisor alone, with the problems `not_supervisor`, `invalid` and `too_long` on `invalid_request`; and the `acting_operator` attribute it writes on every record the call writes. No member, route or code is added, and `north_api_min` stays `1`: a client of `1` is served exactly as before, records carrying `acting_operator` included |
| 3 | `v0.38.0` | `GET …/audit/records/{record}/capture`, one record's session capture for replay (`record_id`, `session_id`, `capture_bytes`, `capture_sha256`, `retained`, and `payload` in base64 unless `retained`), audit:read; and the `principal` query parameter on `GET …/audit/records`, the records naming that principal as their actor (`attributes.principal_id`), which the list's cursor binds. No member of an existing answer and no code is added, and `north_api_min` stays `1`: a client of `1` or `2` is served exactly as before |

## The registration protocol

The supervisory registration is one `POST` to the supervisor whose request and
response bodies are both newline-delimited JSON (M19). The comment at the head
of `internal/instance/register.go` lists every line, and the version it
arrived in.

- **What it versions:** the lines of one registration, in both directions:
  which kinds can arrive, and the members each carries. The `data` of
  `hello`, `resync` and `report` is the north-bound views, at the north-bound
  version `hello`'s identity names (`versions.north_api`), so it moves with
  that number and not this one.
- **Carried, not negotiated.** `hello`'s `protocol` is the version this
  deployment speaks. It is informational: nothing negotiates it, no line says
  which version the supervisor reads, and the deployment sends every line its
  version has. A supervisor reads it to know which lines can arrive.
- **The skip rule.** A supervisor skips a line of a kind it does not
  recognise, and a member it does not recognise on a line it does, and never
  refuses either. The deployment keeps the same rule inbound: a supervisor line
  of a kind it does not act on is logged and ignored. This is what lets a
  supervisor written against version N read a deployment at N+1.
- **What moves it:** a new line kind, or a new member on a line, in either
  direction. Each moves it by one, so `hello` keeps saying what can arrive.
  The north-bound version, the contract version and the module release do not
  move with it, and it does not move with them.
- **What it does not cover:** a changed meaning of an existing line or member.
  Skipping cannot make that safe: it would need the supervisor to say which
  version it reads, and nothing does. The phase that first needs such a change
  designs that negotiation; none is planned.

### Registration protocol history

| Version | First sent in | What it adds |
| --- | --- | --- |
| 1 | `v0.33.0` | the registration as 0046 built it. Out: `hello`, `resync`, the feed's events, `report`, `heartbeat`. In: `resume`, `heartbeat`, `rereport` |
| 2 | `v0.34.0` | out, `revoked`: `{"kind":"revoked","id":…,"at":…}`, the last line of a registration whose deployment's operator revoked it locally (`supervision revoke`). `id` is a heartbeat's (`evt-<epoch>-<seq>.<line>`): it names a position without taking one, and is never logged in the feed or replayed. `at` is the instant the deployment records, the first one if it was revoked again; there is no `by`. The request body then ends cleanly, within one heartbeat of the check that saw the revocation, and a supervisor that does not take the line within it loses the connection at that bound. It is sent for nothing else, so a connection that ends without it says nothing about why, and a deployment revoked while not connected sends nothing. A deployment whose `hello` says `1` never sends it |
