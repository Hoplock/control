# control-PR#133 — north-bound answer names

Queued by [control#133](https://github.com/Hoplock/control/pull/133), Control's
phase 0045. Every north-bound answer now names every member in snake_case,
before Control's 0046 makes the north-bound API a compatibility promise (M19).
It is released as `v0.32.0`. The obligations are in its `## Cross-repo impact`
section, for `hoplock/enterprise`, whose supervisory plane and console read
the fleet view (control-PR#97's sync, its point 2).

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/133. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.32.0 and never from main
(no ext or server signature changed). Every prompt or plan text that reads
one of these five Control answers names it by its new members:
(1) GET /api/v1/tenants/{tenant}/proxies, each item of `proxies`: proxy_id,
zone, state, live, last_heartbeat_at, subscription_seen_at,
contract_version, capabilities, relay_registrations ([] when none), config,
session_count, last_error, last_error_at — every zero instant ABSENT (means
"never"), never year one; last_error absent when empty. (2) The same
answer's `liveness`: heartbeat_ttl_seconds, relay_registration_ttl_seconds,
target_capability_ttl_seconds, report_after_seconds — integer seconds,
rounded up (90/60/86400/21600 by default), where it was nanosecond counts
under Go names. (3) GET …/audit/blocked-commands, each item of `blocked`:
record (rendered exactly as an item of …/audit/records; its grant,
enforcement, device and gap facts are in its `body` alone), command,
action, target_zone, target_labels ({} when none), decision_found,
matched_rule, route_type, decided_at — the last three absent when the
decision is not found. (4) GET …/audit/sessions/{session}/loss, each row of
`loss`: cause, reports, records, bytes. (5) PUT …/identity/claim-mapping, a
refused mapping's parameters.rejections: code, field (absent at document
scope), message. No status code, order, key or cursor changed. Search every
prompt and the plan for the old Go names (ProxyID, LastHeartbeatAt,
HeartbeatTTL, TargetZone, MatchedRule, DecisionFound, Cause, Reports,
Record.) and for "nanoseconds".

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
