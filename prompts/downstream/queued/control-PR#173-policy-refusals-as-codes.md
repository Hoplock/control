# control-PR#173 — policy's refusals as codes

Queued by [control#173](https://github.com/Hoplock/control/pull/173), Control's
phase 0065, released as `v0.52.0`. Every policy refusal now arrives as a stable
code and typed parameters, never a sentence the server wrote, and the
north-bound API moves to version 8; a client declaring 7 or older is served as
before, so `north_api_min` stays 1. `ext`, `server` and the contract are
untouched, and there is no migration. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/173. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.52.0 and never from main —
(1) the north-bound API is at version 8 (north_api_min still 1): a client that
declares no version — a host's own console and every client shipped with the
server — is answered with the new shapes, and one declaring 7 is answered as
before, so any Enterprise text or code that pins 7 moves to 8 or declares 7 on
purpose; (2) policy rejections: policy.document_malformed now carries
`problem` (tab_indentation, structure, unterminated, character, encoding, tag,
alias, unreadable) and means "not YAML"; new codes policy.key_unknown,
policy.key_duplicate, policy.value_wrong_type, policy.value_unknown,
policy.duration_malformed, match.invalid_time_window,
route.restricted_command_field_unexpected, route.restricted_argument_invalid,
route.restricted_argument_field_unexpected and obligation.approvers_unexpected;
route.filter_rule_invalid, route.restricted_command_invalid and
obligation.contradicts_route carry `field` and no `detail` — every place
Enterprise renders Control's rejections needs a sentence for each (Control's
v0.52.0 CHANGELOG lists every parameter); (3) an unreplayable record carries
`code` and `parameters` (six unreplayable.* causes) beside `reason`, a refused
simulation is 400 invalid_request {field, problem} (problems required,
invalid, ambiguous, not_after_start, nothing_asked, not_in_vocabulary), and a
candidate diff that does not parse is 422 policy_invalid {rejections, digest};
(4) a parameter drawn from a closed set (problem, found/expected, vocabulary)
is an identifier to be worded, never interpolated (Control's ui/DESIGN.md §9);
(5) the component set's ErrorMessage gains an optional `text`; (6) the pin: no
migration, ext and server untouched, the contract unchanged at 4.7.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
