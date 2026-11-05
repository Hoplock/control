# control-PR#141 — a revoked registration says so

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/141. Do not implement any queued prompt in this
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
section: every one holds from hoplock/control v0.34.0, never main, and the pin
moves to it. (1) 0012's gateway reads `revoked`
({"kind":"revoked","id":"evt-<epoch>-<seq>.<line>","at":"<RFC 3339>"}, the
heartbeat's id form, `at` the instant the deployment recorded, the first if
revoked again) followed by a clean end of the request body, on which it ends
its response as Control's reference supervisor does; it skips any line kind or
member it does not recognise and refuses none; it reads hello's `protocol`,
where an instance at 1 (v0.33.0) never sends `revoked`, so a revoked one still
looks unreachable and the plane can say it cannot report a revocation; what
the gateway sends is unchanged ("That end is three lines and no more").
(2) 0012's "What v0.33.0 does not give this phase": the `revoked` seam is wired
from v0.34.0 — `revoked` with `at` sets the state, a later `hello` from the
same instance_id clears it; the rule against inferring revocation from how a
connection ended stands; a deployment revoked while not connected still shows
unreachable; the skipped "local revocation wins" second half is asserted from
this release. (3) What Control decided unasked: the skip rule is Control's,
written in docs/API-VERSIONING.md ("The registration protocol"); the protocol
is not negotiated and a new line kind or member moves it by one; the delivery
bound is one heartbeat from Control's check, so an instance revoked while the
plane is not reading ends with no line it can see; no `by`, because who revoked
is the supervised deployment's own record (M19's first rule). (4) Its PLAN §5
"Supervising one of Control's deployments" gives the lines at protocol 2 and
closes the gap it names from v0.34.0; its 0013 and 0017 where they assert the
revocation. (5) Its pin: v0.34.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
