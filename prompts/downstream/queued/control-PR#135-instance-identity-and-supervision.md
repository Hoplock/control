Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/135. Do not implement any queued prompt in this
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
section: the pin moves to hoplock/control v0.33.0, and the supervisory plane
(E14) is written against that release, not main; the north-bound API version
`1` and its negotiation (`Hoplock-Api-Version`, `api_version_unsupported`,
`api_version_unexpressible`, a supervisor always declares, docs/API-VERSIONING.md);
the registration protocol version 1 (one NDJSON POST, hello / resume / resync /
replay / heartbeat / rereport, the nine event kinds, the `hoplock.supervision`
singleton); and the supervisor credential (`identity token-issue --supervisor`,
ordinary scope grammar `tenant=role[,role];…`, principal id `sup-…`,
`instance:read`, listed and revoked under `…/instance/supervisors`) — verbatim
in the PR.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
