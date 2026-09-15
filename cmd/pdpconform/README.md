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
| `-only` | Run only groups whose name contains this substring. For debugging one endpoint; never for making a run green. |
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
| `read_url` | How the suite reads back what it ingested. **The contract defines no read path**, and the priority ack means *durable*, so the only black-box way to grade that is to ask the server for the record straight after the ack. The assertion is that the record id appears in the response body, so any read path that names the record satisfies it. |
| `batch_size` | How many records go in the batch that is then replayed. |

### `events`

| Key | Meaning |
| --- | --- |
| `proxy_id` | The stream subscribed to. |
| `heartbeat_interval_seconds` | The interval the server advertises, and the ceiling the suite holds it to. **The contract carries no field for this** (see "Ambiguities" below), so it is an input; a proxy's staleness detection defaults to 20s, which is the outer bound of a sane value. |
| `publish_url`, `publish_body` | How the suite makes the server emit an event. There is no contract endpoint for this either: publishing is an operator action and which surface offers it is the implementation's business. |

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

## Ambiguities found in the contract

Recorded here and in `docs/learnings/0002-*` rather than resolved unilaterally
(PROTOCOL §3, M1):

- **The heartbeat interval is not on the wire.** The contract requires
  heartbeats "at a steady interval (comfortably inside the proxy's timeout,
  which defaults to 20s)" but gives the stream no field to state the interval
  it chose. So "within the interval the server advertises" cannot be read off a
  response, and this suite takes it as an input.
- **There is no contract surface for publishing an event or reading a record
  back**, which the durability and gap-recovery obligations both need in order
  to be gradeable at all. Both are suite inputs for that reason. This is
  arguably correct — neither is a proxy-facing operation — but it does mean two
  of PLAN §4's obligations are only checkable against a server that exposes
  something beyond the contract.
