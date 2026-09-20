# `pdpconform` — the black-box conformance suite

`pdpconform` drives the south-bound contract (`contract/control.yaml`, PLAN
**M1**) over real HTTP and reports pass/fail per assertion, exiting non-zero if
anything failed. It is what "implements the contract" means in this repository:
every phase from 0007 onward is graded by it.

## What "black-box" means here

The suite talks to a server over HTTP and reaches into nothing. It has no access
to a database, no in-process handler, and no knowledge of how any answer was
reached; every assertion is made about bytes that crossed a socket. That is what
makes it able to grade *any* implementation, which is the point: CI runs it
against **Hoplock Proxy's `cmd/mock-control`** as well as, later, against this
server, because a suite only ever run against the implementation it was written
beside tests agreement with itself.

It does import `internal/contract` — the repository's one home for wire shapes
(PLAN §3) — and deliberately so: the alternative is a second copy of every
payload type, which diverges and then grades the divergence. Two things stop
that import turning into agreement-with-itself:

- **`internal/contract`'s enum constants are tested against
  `contract/control.yaml` itself**, in both directions, so the types are checked
  against the document rather than against this server's opinion of it.
- **Every assertion about an ABSENT field reads the raw JSON, not the decoded
  struct.** A decoded `AuthorizeResponse` cannot tell `require_session_capture:
  false` from an omitted key, and those are exactly the two readings the
  contract insists are different — so the absent-value cases would be
  ungradeable if the suite trusted its own types there.

Nothing else from this server is imported, and nothing should be.

```
make conform BASE_URL=http://127.0.0.1:8080 TOKEN=<proxy token> \
             EXPECT=cmd/pdpconform/testdata/mock-expectations.yaml

# or directly, with the extra flags
go run ./cmd/pdpconform -base-url URL -token TOKEN -expectations FILE [-v] [-only SUBSTRING]
```

| Flag | Meaning |
| --- | --- |
| `-base-url` | The server under test. Required. |
| `-token` | The bearer token it accepts from a proxy. |
| `-expectations` | The file below. Required. |
| `-only` | Run only groups whose name contains one of these comma-separated substrings. For debugging one endpoint, and for grading a server that implements part of the contract — phase 0007's CI leg names the four groups it serves. Never for making a run green: leaving a group out is a statement the PR has to make in words too. |
| `-v` | Print the details of passing assertions too, which is how you check a case is not passing vacuously. |
| `-timeout` | Per-request timeout. The event stream is exempt: it never ends on its own. |

## Running it locally against the mock

```
git clone https://github.com/hoplock/proxy /tmp/proxy
go -C /tmp/proxy run ./cmd/mock-control -listen 127.0.0.1:8080 \
    -fixtures $PWD/cmd/pdpconform/testdata/mock-fixtures.yaml &
make conform BASE_URL=http://127.0.0.1:8080 TOKEN=pdpconform-dev-token
```

`testdata/mock-fixtures.yaml` configures the mock to serve what
`testdata/mock-expectations.yaml` names. Both are test data.

## Running it locally against THIS server

The suite is pointed at the real implementation by a second pair of files and
no Go at all. There is no north-bound API yet (0014), so the server is
configured with `hoplock-control seed`:

```
go run ./cmd/hoplock-control migrate --config config.yaml
go run ./cmd/hoplock-control seed    --config config.yaml \
    --file cmd/pdpconform/testdata/control-seed.yaml
go run ./cmd/hoplock-control --config config.yaml &

make conform BASE_URL=http://127.0.0.1:8080 \
             TOKEN=default.pdpconform-dev-secret \
             EXPECT=cmd/pdpconform/testdata/control-expectations.yaml \
             CONFORM_FLAGS='-v -only=authentication,authorize,host,capabilities,uid'
```

`testdata/control-seed.yaml`, `testdata/control-policy.yaml` and
`testdata/control-expectations.yaml` are **one document in three parts**: the
fixtures, the policy they are decided under, and what the suite asserts. A login
or a target in one and not the others grades nothing, or fails for a reason
visible in none of them. Change them together.

`-only` names the groups this build serves; `authorize` covers four of them at
once (the envelope, the absent-value defaults, vocabulary negotiation and the
device-field namespace). Log ingest is 0010's, and that phase adds its group to
the `conform-self` CI job and replaces its placeholder section in the
expectation file. **Beware the substring collision:** `POST /v1/auth` also
matches `authorize (POST /v1/authorize)`, and `make` passes `CONFORM_FLAGS`
unquoted, so a value containing spaces is split by the shell before the flag
package sees it. Use space-free substrings.

## The expectation file

**Everything the suite knows about a server is in this file.** No login, target,
fingerprint, token, or URL appears in any Go source file here. That is the
property phase 0017 needs: pointing the suite at the real server is a new
expectation file and no code change at all.

It is decoded **strictly** — an unknown key is an error — and validated for the
inputs whose absence would make a case vacuous. A vacuous pass is worse than a
failure, because it reads as coverage.

### Top level

| Key | Meaning |
| --- | --- |
| `proxy_id` | The proxy the suite presents itself as on every `conn`. |
| `second_proxy_id` | A **different** proxy id, used only by the uid cases: exclusivity is per target, not per proxy, and a server that keyed its cursor by proxy would look perfect to a single-proxy suite. |

### `auth`

| Key | Meaning |
| --- | --- |
| `cert_accept` | `login`, optional `target`, `key` (`type`, `fingerprint`, optional `blob`), optional `expect_subject`. |
| `cert_deny` | A `login` and a `key` the server rejects. |
| `password_no_mfa` | Optional. A `login`/`password` whose password alone completes the flow. |
| `password_mfa_approve` | A `login`/`password` whose challenge resolves to authenticated, and `max_polls`. |
| `password_mfa_deny` | A `login`/`password` whose challenge resolves to a denial. |
| `password_mfa_expiry` | Optional. A `login`/`password` whose challenge is never answered; the suite waits until the challenge's own `expires_at` plus `wait_seconds`, then polls and expects `401`. |
| `unknown_challenge_token` | A token the server has never issued. |

### `authorize`

Each route is `{login, subject, target, port, proxy_id}`; `subject` defaults to
`login` and `port`/`proxy_id` may be omitted.

| Key | Meaning |
| --- | --- |
| `direct`, `nexthop` | The two route types. |
| `deny` | An identity/target pair the server refuses — a `401` **decision**. |
| `full` | The richest route the server serves, for the envelope-shape assertion. |
| `ladder` | A route whose `target_auth_ladder` carries a **`brokered-key`** entry. Every entry must name `params.username`; a fixture asserting a brokered-key shape without one encodes a contract nobody serves. |
| `negotiation` | The route the version cases run against. Point it at the richest policy the server serves: the more vocabulary it carries, the more a thinned low-version answer has to drop to be caught. |
| `device_fields.with_fields` | An `ephemeral-account` route carrying `device_field.<name>` parameters. |
| `device_fields.without_fields` | A comparable route carrying none. The suite finds the lowest `policy_version` at which **this** one is served and asserts the one above is served there too. |
| `defaults.*` | Four **pairs** — see below. |

#### `defaults` is a pair, on purpose

`enforcement`, `session_deadline`, `require_session_capture`, and `concurrency`
each take a `default:` route and a `set:` route:

```yaml
defaults:
  enforcement:
    default: { login: alice, target: host.company.com }      # answers WITHOUT it
    set:     { login: alice, target: build-01.company.com }  # answers WITH it
```

The suite asserts the field is absent on the first **and** present, and
resolving to something other than the default, on the second. Without the second
half the assertion passes against a server that cannot express the field at all,
which is the exact failure mode the acceptance criteria name.

### `hostkeys`

| Key | Meaning |
| --- | --- |
| `first_sighting.target` | A target to report an unseen key for. The suite generates a fingerprint unique to the run — "first sighting" is only first once. |
| `known.target`, `known.fingerprint` | A key the server already trusts. |

### `capabilities`

| Key | Meaning |
| --- | --- |
| `target` | The host capabilities are reported for. |
| `platform` | Optional; the device driver, absent for a POSIX host. |
| `expect_report_after_seconds` | `-1` (the default) does not assert a value; `>= 0` asserts it exactly. Absent and `0` are the same answer — the interval is the proxy's. |

### `uids`

The suite **suffixes every uid target with a token unique to the run**
(`uid-exhaust.company.com` → `uid-exhaust-1a2b3c4d.company.com`). The per-target
cursor only ever advances and is never reset — that is the invariant — so a
fixed name would grade the invariant on the first run against a server and
nothing at all on the second. Give these names a form the server accepts with a
label appended.

| Key | Meaning |
| --- | --- |
| `target`, `range_min`, `range_max`, `uid_count` | The non-overlap target. Give the range room for the leases below. |
| `abandoned_leases` | How many blocks are taken and never allocated from. |
| `expiry_wait_seconds` | Slack added to the term the server itself stated, before leasing again past an expired block. |
| `floor.*` | A separate target for the `observed_floor` cases, so raising a cursor deliberately does not disturb the non-overlap target. |
| `exhaustion.*` | A separate target with a **narrow** range, leased until the cursor reaches the top and the server answers `409`. Three block-widths is enough. |

### `logs`

| Key | Meaning |
| --- | --- |
| `read_url` | How the suite reads back what it ingested. **The contract deliberately defines no read path** — a proxy writes logs and never queries them, so an operator read API on `/v1` would be one every Hoplock Control implements and no proxy calls — and the priority ack means *durable*, so the only black-box way to grade that is to ask the server for the record straight after the ack. The implementation supplies the path; the assertion is that the record id appears in the response body, so any read path that names the record satisfies it. |
| `batch_size` | How many records go in the batch that is then replayed. |

### `events`

| Key | Meaning |
| --- | --- |
| `proxy_id` | The stream subscribed to. |
| `heartbeat_interval_seconds` | **The fallback bound, not the bound.** The suite reads `RevocationEvent.heartbeat_interval_seconds` off the stream (upstream `Hoplock/proxy#56`) and grades the server against that claim. Absent is a legal answer — it means the reader stays on its own timers — and this key is what keeps such a server gradeable. A server that advertises nothing against a file that configures nothing is **ungradeable and therefore FAILS**, because a vacuous pass is worse than a failure. |
| `publish_url`, `publish_body`, `publish_token` | How the suite makes the server emit an event. The contract deliberately defines no endpoint for this — publishing is an operator action, not a proxy-facing one — so the implementation supplies the path and the suite takes it as an input, asserting nothing about its shape. `publish_token` is for a server that keeps the operator surface on its own listener with its own credential; left empty, the suite presents the proxy token. |

## What the suite asserts, and what it deliberately does not

It grades the **envelope**, never the policy content. Which channels a route
permits, how large a uid block is, what `term_seconds` a server chooses, and
whether a given host key is worth a cache hint are all the implementation's
business (phases 0007 onward). Whether a uid can ever be handed out twice is the
contract's, and that is graded hard.

Three assertions are worth knowing about because they are written against a
**rule** rather than a literal, which is what keeps them from going stale at the
next contract revision:

- **Vocabulary negotiation** sends the same authorize request at the current
  version and at `1`. A `5xx` is a pass — it is the correct answer when the
  policy needs vocabulary the caller cannot read. A `200` is a pass only if it
  dropped no policy field the current-version answer carried; a thinned snapshot
  that drops a restriction and returns `200` is the failure, and a `401` is
  never right (a version mismatch is a rollout problem, not a decision about a
  user). The comparison is against the current-version answer rather than
  against a table of which field arrived in which revision — the contract states
  **one live vocabulary in the present tense** and carries no such table.
- **An absent `policy_version` is its own case**, distinct from the `1` case.
  They look similar and are not: `1` is a proxy that told the truth about being
  old, and absent is a proxy that said nothing. Only the first can be answered
  safely. The answer must be `400 invalid_request`.
- **A device field demands no version bump.** The suite finds the lowest version
  at which the comparable route *without* device fields is served, and asserts
  the route *with* them is served at the same one. Pinning that to a number
  would make the case stale at the next revision, and a suite that expected a
  bump would be asserting a rule the contract does not have.

## What the contract leaves to the implementation

0002 raised two ambiguities here rather than resolving them unilaterally
(PROTOCOL §3, M1). Upstream `Hoplock/proxy#56` (merged) answered both, in
opposite directions — one became a field, the other became a stated boundary —
and what is left is recorded here and in `docs/learnings/0002-*`.

**The heartbeat interval is on the wire, and the suite now reads it there.**
It was not when this suite was written, which is why the expectation key
survives as a fallback rather than as the bound. The contract carries
`RevocationEvent.heartbeat_interval_seconds`: the interval the server is
**currently keeping**, normally on `heartbeat` events but legal on any, and a
later event carrying a different value re-states the interval rather than
contradicting an earlier one. Three rules come with it —

- **absent means what every server did before the field existed**, so a reader
  falls back to its own timers and a silent server is still conformant;
- **it may only ever tighten detection, never loosen it** — sooner is always
  allowed, later never, the same rule as `cache.ttl_seconds` and
  `report_after_seconds`;
- **there is still a ceiling and the field does not replace it**: heartbeats at
  **10 seconds or less**, so two consecutive intervals fit inside the proxy's
  20s reconnect timeout.

So "within the interval the server advertises" is graded from the stream, and it
is **two** assertions rather than one: the server keeps the interval it
advertises, *and* that interval is inside the ceiling. A server advertising 600s
and honestly keeping to it passes the first and breaks every proxy in the fleet,
which is why they are separate cases — a failure names which half.

Both are contract-level rather than facts about any one server, so they live in
the shared assertions and not in an expectation file. The same suite runs
against Hoplock Proxy's `cmd/mock-control`, which advertises an interval derived
from its own `heartbeat_ms` (rounded up, so it never claims one it does not
keep); getting this layer wrong is how the mock starts failing for no reason
anyone can see.

`checks_events_test.go` points these two cases at deliberately wrong servers —
one stalling its writer, one advertising 600s, one advertising nothing with no
fallback configured — and asserts that the suite **fails** each. A pair of
assertions that cannot fail is a pair that grades nothing.

**Nothing publishes an event or reads a record back, and that is the answer, not
a gap.** Both operations are operator-facing rather than proxy-facing: a proxy
writes audit records and never queries them, and an event originates from a
revocation or a policy change on a surface `/v1` does not describe. Putting
either on `/v1` would oblige every Hoplock Control to implement an API no proxy
calls, so the contract says plainly that it does not, and an implementation that
wants the durability and gap-recovery guarantees **graded** exposes paths of its
own outside `/v1`. That is why `logs.read_url` and `events.publish_url` are
inputs, and why the suite asserts nothing about their shape.

This still means two of PLAN §4's obligations cannot be graded against a server
that serves only the contract — the difference is that this is now a documented
boundary with a reason, rather than something the contract forgot. Hoplock
Proxy's `cmd/mock-control` `GET /debug/logs` and `POST /debug/revoke` are the
reference shapes. This server answers the second of them with a publish
listener of its own, off unless `events.publish_listener` is configured and
credentialled (phase 0009, `cmd/hoplock-control/publish.go`); the CI leg
configures one, which is what makes gap recovery gradeable here.
