# The north-bound API version

The north-bound API (`/api/v1/…`) is a compatibility promise (PLAN **M19**). A
supervisor consumes many deployments at many releases, so the surface carries a
version of its own and says which one each answer is in. This file is the
promise. The code is `internal/httpapi/north/apiversion.go`.

## Three numbers, none of which is another

| Number | What it versions | Where |
| --- | --- | --- |
| the module release, `vMAJOR.MINOR.PATCH` | the software (M23) | `GET …/instance` → `versions.software` |
| the contract's `info.version` | the proxy contract this build vendors (M1) | `versions.contract` |
| **the north-bound API version**, an integer | this surface | `Hoplock-Api-Version`, `versions.north_api` |

A release can move any of them without the others. `policy_version` is a fourth,
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
