# control-PR#143 — the supervising operator named in the instance's trail

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/143. Do not implement any queued prompt in this
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
section: every one holds from hoplock/control v0.35.0, never main, and the pin
moves to it; its only public API change is server.Caller.ActingOperator.
(1) 0013, the actions: the plane sends `Hoplock-Acting-Operator` on every call
it makes for a named operator — the name's UTF-8 with every octet outside
printable ASCII and `%` itself percent-encoded, decoding to 1–128 printable
characters with no control or format character and no leading or trailing
space — and only to an instance whose `versions.north_api` is at least 2; to an
instance at 1 it sends none and shows that the instance cannot record who
acted. The seam is wired from this release; the rule against writing a name
into a `reason` or other free text stands; the plane keeps its own record of
which operator made each call (the provider's answer; the instance's record is
the customer's). (2) 0013 and 0017, the criteria: the skipped named half is
asserted from this release — the instance's records carry `acting_operator`
beside the `sup-` `principal_id` — and still skipped against an instance below
v0.35.0. (3) What Control decided unasked: the header, not a token per
operator (a per-person token verifies nothing more, since the plane chooses
which to present, and would make every provider staff change an act at every
customer's deployment; a token per person still works and the header names the
operator on each); percent-encoding, with refusals `400 invalid_request`
{field: "Hoplock-Acting-Operator", problem: not_supervisor | invalid (a header
sent twice included) | too_long, limit "128"}, and a bad credential `401`
first; the name never on a row (created_by, updated_by, a standing denial's
actor) and never a filter, only on the records, `GET …/audit/records`
included; the version at 2 because of the header, with a record's `attributes`
an open map whose keys are their writer's — a reader skips a key it does not
know and a new key moves nothing (docs/API-VERSIONING.md);
server.Caller.ActingOperator for a host's own records and a report provider's
calls, recorded as the supervisor's word and never authorised on. (4) Its PLAN
§5 "Supervising one of Control's deployments": the credential's paragraph gains
the header, the north-bound version is 2, and the gap it names is closed from
v0.35.0. (5) Its pin: v0.35.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
