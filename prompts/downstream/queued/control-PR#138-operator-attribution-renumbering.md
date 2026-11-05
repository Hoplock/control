# control-PR#138 — the operator-attribution renumbering

Queued by [control#138](https://github.com/Hoplock/control/pull/138), which
answered enterprise#64's second request, the supervising operator named in
the instance's trail, by queueing Control's phase 0048, the supervising
operator's attribution. It renumbered Control's queued phases after it. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`.
This is not the sync owed for 0048's own change: 0048 queues that when it
merges.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/138. Do not implement any queued prompt in this
session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — never approximate it, and do not stop at telling me: name
the exact shape and queue the "Upstream request" kickoff from docs/KICKOFF.md,
filled in, in this repository's prompts/upstream/queued/ (§4.3).

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: for hoplock/enterprise —
(1) Control's queued phases moved (revision operator-attribution): the
management console is now 0049, E2E topology 0050, CA custody through the key
store 0051, post-quantum posture 0052 and one contract version 0053. On main
when this was queued, this repository still cited them as control#120 numbered
them: the console as 0047 in docs/PLAN.md §3 and its 0007, 0008 and 0016; E2E
as 0048 in its 0017; and CA custody as 0049 in docs/PLAN.md §4 and its 0010 and
0017 — and any others a sync since added. control#137's sync
(control-PR#137-revoked-line-renumbering.md) owes the same citations. Whichever
of the two runs first should cite each phase by name beside the number
Control's docs/PLAN.md §10 mapping table gives when it runs. If that is already
done, this item is done: say so. Hits that only recount a number's history
("control#118 0047", "0048 as control#119 did") are history and stay.
Citations of merged Control phases do not move.
(2) The request enterprise-PR#64-supervising-operator-attribution.md is
answered by a Control phase that is queued, not merged: 0048, the supervising
operator named in the instance's trail. Three texts point at
prompts/upstream/queued/enterprise-PR#64-supervising-operator-attribution.md:
docs/PLAN.md §5's "Supervising one of Control's deployments"; prompts/queued/
0013's actions bullet and its named-operator criterion; and its 0017's
named-operator scenario. Point them at the file where it is when you run (it
moves to prompts/upstream/implemented/ once control#138 has merged), and name
Control's 0048 as queued. Say in 0013 what 0048 will build, including what
Control decided that the request did not ask for:
- the request header Hoplock-Acting-Operator, as asked. It is accepted only
  from a sup- principal, and any other caller, an anonymous route included, is
  400 invalid_request {field: "Hoplock-Acting-Operator", problem:
  "not_supervisor"}.
- The value is the name's UTF-8, percent-encoded wherever an octet is outside
  printable ASCII, and for % itself (RFC 3986 §2.1). Decoded, it is 1 to 128
  unicode.IsPrint characters with no leading or trailing space, so no control
  or format character and no bidirectional override. Over 128 is problem
  "too_long" (limit "128"); anything else wrong, a header sent twice included,
  is problem "invalid". It is checked after authentication, so a bad credential
  is still 401.
- It is recorded, decoded and exactly as sent, as acting_operator beside
  principal_id, on every audit record the call writes, inside the hashed body.
  It is the supervisor's word and never an input: never a principal or a
  subject, never actor_subject, and never read by RBAC, tenancy or the
  decision path. It is never on rows (a grant's created_by, a denial's actor, a
  binding), and no audit filter takes it. It is also on the access log line,
  and on server.Caller.ActingOperator for a host's own routes.
- The north-bound version moves to 2 because of the header, which is honoured
  whatever version a call declares; north_api_min stays 1. So the plane sends
  the header only to an instance whose versions.north_api is at least 2. To an
  instance at 1, the header would be ignored as an unknown header.
- A record's attributes are an open map: their keys are not north-bound
  members, a reader skips a key it does not know, and a new key moves nothing.
  So a reader declaring 1 reads named records unchanged, never a 500.
- A supervisor token per operator, which the request offered, was weighed and
  not taken. The plane chooses which token to present, so a per-person token
  verifies no more than the header, and it makes every provider staff change an
  act at every customer. It still works, and the header names the operator on
  it.

Do not describe any of it as existing (§2). 0013 keeps its naming unwired,
and 0013 and 0017 keep skipping the named half, until the pin carries 0048's
release. Rewriting 0013 from what merges is the sync 0048 owes when it merges.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
