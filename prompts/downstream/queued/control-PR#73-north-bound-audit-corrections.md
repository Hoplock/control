# control-PR#73 — corrections to control#69 from the north-bound audit

Queued by [control#73](https://github.com/Hoplock/control/pull/73), Control's
phase 0023: the fourth audit of 0019, the north-bound surface and the record.
It cuts `v0.10.0`, with a `### Breaking` section. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`, and three of them
correct obligations (3), (5) and (8) of control#69, whose own sync
(`control-PR#69-northbound-api.md`) had not run when this was queued. Run that
one first, or both in one session, and build on these corrections where the two
differ.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/73. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, all holding from v0.10.0 and correcting
control#69's (whose own sync, control-PR#69-northbound-api.md, runs first or
in the same session) —
(1) Correction to control#69's (3). From v0.10.0 a host route is refused at
start-up when Control already serves its path in any form: its own pattern, a
method beside it, or a literal under one of Control's wildcards —
targets/<x>, decisions/<x>, audit/records/<x>, policy/bundles/<x>,
access-context/<x>/push and the like. Under v0.6.0–v0.9.0 such a literal was
accepted and took the path from Control. Check every planned host route
against Control's patterns with their wildcards filled in. A host may still
declare no code that is Control's.
(2) Correction to control#69's (5). operator.fleet_config.published and
operator.fleet_config.rolled_back drop the attribute operator.unannounced and
gain before (the version the act displaced). operator.claim_mapping.activated,
operator.proxy.approved and both fleet_config records now commit in the act's
transaction. Any SIEM mapping or compliance report naming unannounced changes.
(3) Correction to control#69's (8). Control's queued phases moved twice since:
control#71 (revision audit-findings) queued 0024 signed retention markers and
0025 the shared event plane, and control#73 (revision bounded-lists) queued
0026, an audit of every north-bound list that settles one bounded-list shape
(a limit, a cursor, an answer that says where it stopped) and queues its
implementation in chunks right after it. So today: instance identity and
supervisory registration is 0027 (its E14 builds on it), management console
0028, E2E topology 0029, post-quantum posture 0030, one contract version 0031
— and 0026's chunks will move them again, each move with its own note. Every
citation of a Control phase number resolves through Control's docs/PLAN.md §10
mapping table; cite a phase by name beside its number. Where an Enterprise
prompt reads a Control list, note that its shape changes in 0026's chunks.
(4) Observable from v0.10.0: a north-bound body past the listener's limit is
413 payload_too_large with a limit parameter, host routes included;
GET .../audit/negotiated answers truncated; PUT .../subjects/{subject} refuses
a reserved or invalid claim name (field claims.<name>); revocation and relabel
refusals name their field. Any Enterprise prompt that calls these routes or
describes these answers says so.
(5) Its pin moves to v0.10.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
