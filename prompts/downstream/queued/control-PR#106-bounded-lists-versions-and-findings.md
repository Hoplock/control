# control-PR#106 — bounded lists: policy versions, a session's decisions and a finding's targets

Queued by [control#106](https://github.com/Hoplock/control/pull/106), Control's
phase 0035. It pages `GET …/policy/bundles`, adds `truncated` to a session's
decisions, caps every list a publish-time finding names at 100, and folds the
findings that were one per proxy into one per rule. Those findings now carry
new ids. It cuts `v0.22.0`, a MINOR with a behaviour-only `### Breaking`. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/106. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, all true from Control v0.22.0 —
(1) A Control publish-time finding's `targets` or `proxies` holds at most 100
items, in the check's own order, beside `targets_count`/`proxies_count` (the
whole number) and `targets_truncated`/`proxies_truncated` (Control's M26.3).
Both are present on every such finding, cut or not. The report answers
validate, upload, apply and activate, and is the body of
`409 findings_not_accepted`. Wherever this repository's prompts or plan read or
render Control's report (the console, the policy-change approval flow,
anything else), they show the count and say when the list was cut.
(2) The findings that named one proxy are now one per rule:
`algorithm.floor_undeclared/<rule>` (was `…/<rule>/<proxy>`),
`algorithm.axis_emptied/<rule>/<axis>` (was `…/<rule>/<proxy>/<axis>`) and
`algorithm.declaration_unknown/<rule>/<profile>` (was `…/<rule>/<proxy>`).
Each names its builds in `proxies`; for `axis_emptied` that is
`[{proxy_id, offered}]`. `proxy_id`, and `axis_emptied`'s top-level `offered`,
are gone. `algorithm.declaration_unknown/<rule>`, which names no proxy, is
unchanged. Anything that stores, forwards or carries a warning's acceptance by
finding id must use the new ids, because an old one accepts nothing and the
activation is refused 409 until the new id is accepted.
(3) `GET …/policy/bundles` answers {versions, truncated, next} (control#97's
obligation 2, now built). `GET …/sessions/{session}/decisions` answers a
session's newest 100 and says `truncated` past them. It takes no limit and no
cursor, so an explain view of a session says when the server cut it.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
