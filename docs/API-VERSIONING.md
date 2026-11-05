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
- **A new error code**, or **a new value of a closed set** (a health state, a
  reason code, an event kind), moves the version: a client written against a
  closed set may refuse a value it does not know.
- **A changed meaning is never additive**, whatever it does to the JSON: the
  same member saying something different, a list in another order, a cursor
  that does not continue. It moves the version **and** raises
  `north_api_min` once the deprecation window has passed.

Every change that moves the version moves it by one, and the release that does it
says so in `CHANGELOG.md`.

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
