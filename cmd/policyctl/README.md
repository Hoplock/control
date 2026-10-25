# `cmd/policyctl`

The policy lifecycle from a terminal (phase 0019). It is a client of the
north-bound API (PLAN M2) and nothing else — it never opens the database and
never compiles a bundle itself, so a pipeline, the console and this command
cannot disagree about what a bundle means.

```
policyctl [-server URL] [-token TOKEN] [-tenant NAME] [-json] <command> [args]

validate FILE                          compile a bundle and report what publishing it would meet
diff FILE [-against N]                 what separates FILE from version N (default: the active one)
simulate FILE [-from T -to T] [-cases FILE] [-fail-on-flip]
                                       replay recorded decisions, and dry-run synthetic ones
apply FILE [-accept ID]...             upload and activate; accept warnings by id
activate VERSION [-accept ID]...       make a stored version active; rolling back is activating an older one
versions                               every stored version, newest first, every page
explain DECISION_ID                    the whole story of one decision
explain -session SESSION_ID            the story of a session's decisions, newest first; warns when the server cut them
```

`-server`, `-token` and `-tenant` default to `HOPLOCK_SERVER`, `HOPLOCK_TOKEN`
and `HOPLOCK_TENANT` (`default`).

`simulate` asks at least one question: `-from` replays the decisions recorded
from then until `-to` (default now), and `-cases` dry-runs a JSON list of
synthetic questions, each in the shape `POST …/policy/simulate` takes:
`{name, now, subject{id, source, groups, claims, auth_method, mfa},
target{hostname, port, zone, labels}, context{proxy_id, source_addr},
device{posture}, grants[{id, origin, scope, targets, labels, zones, not_before,
expires_at}]}`. `now` is required, and so is each grant's `expires_at`; an
origin or an `auth_method` outside the engine's vocabulary is refused rather
than answered as a different question.

## Output is for scripts

Every line is one record, tab-separated, and its first field names its kind, so
a pipeline greps for the kind it cares about and a kind added later breaks
nothing:

| Kind | Fields |
| --- | --- |
| `valid` | `true`/`false`, the source's digest |
| `rejection` | code, rule, line, the compiler's own message |
| `finding` | severity (`advice`, `warning`, `blocking`), id, message |
| `refused` | HTTP status, error code, message, `correlation_id`, id — on stderr |
| `unaccepted`, `blocking` | finding id, message — on stderr, after `refused` |
| `version` | version, digest, and on `versions` the uploader, time and `active`/`inactive`; on `apply`, `existing` and a bool |
| `activated` | version, `previous`, N, `rollback`, bool, `unchanged`, bool |
| `accepted` | a finding id the activation accepted |
| `from`, `added`, `removed`, `changed`, `moved`, `document`, `text` | a diff: the version diffed against, rule ids, top-level keys, and the unified text one line per record |
| `case` | name, effect, rule — a dry run |
| `replayed` | count, `unchanged`, count, `truncated`, bool |
| `flip` | `allow->deny` or `deny->allow`, decision id, subject, target, rule then, rule now, decided at |
| `unreplayable` | decision id, why |
| `decision`, `mapping`, `obligation`, `grant`, `unserved` | an explanation |

A tab or a line break inside a field — a validator's message, say — is printed
as a space, so a record is always one line. `-json` prints the server's answer
instead, unchanged.

`versions` reads the whole history, following the server's `next` cursor page
by page (M26), and prints nothing until the last page is read, so a failure on
any page is exit `2` and never a partial history. With `-json` it prints one
answer in the server's shape, `{"versions": [...], "truncated": false}`,
holding every page's versions as the server wrote them.

`explain -session` answers the server's own bound of a session's decisions, the
newest. When the server says it cut them (`truncated`), the command prints a
warning on stderr and still exits `0`: the answer is the server's.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | done |
| 1 | the server refused, or the bundle is not publishable as it stands: rejections, unaccepted warnings, a blocking finding, or a flip under `-fail-on-flip` |
| 2 | a usage error (a simulation that asks nothing among them), an unreachable server, or an outage (a `5xx`) |

## GitOps

```
policyctl validate policy.yaml                 # a PR check: exit 1 on a rejection
policyctl simulate policy.yaml -from 2026-10-01T00:00:00Z -fail-on-flip
policyctl apply policy.yaml -accept authoring.interpreter_in_allow_list/logs//usr/bin/awk
```

A warning stops `apply` with exit 1 and prints each `unaccepted` id: accepting
one is a deliberate act, recorded on the activation's audit record, and never a
flag that accepts everything.
