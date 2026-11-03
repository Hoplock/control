# control-PR#129 — directory-scoped group entries

Queued by [control#129](https://github.com/Hoplock/control/pull/129), Control's
phase 0043, which answers enterprise#51's upstream request, item 2: a claim
mapping's `groups.map` entry may list the `directories` it applies to,
`groups.require_directories` makes scoping mandatory, and every claim mapping
write now reaches every directory subject by itself. It is released as
`v0.30.0`. The obligations are in its `## Cross-repo impact` section, for
`hoplock/enterprise`, the repository that asked.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/129. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.30.0 and never from main
(nothing in ext or server changed) — (1) its 0004's "What Control does not do
yet", rewritten from what merged: a mapping author scopes a directory group
with `directories: [<source>…]` on a `groups.map` entry and makes scoping
mandatory with `groups.require_directories: true`, and 0004's claim rules feed
that one mapping and never replace it; with v0.29.0 (0042) and v0.30.0 (0043)
both in the pin, drop the refusal of a second directory per tenant and the
skip on the two-directory criteria; those criteria cover the request's own
example (one group mapped per directory, one shared group mapped once from
the directories listed, and a directory no entry lists getting nothing from a
scoped entry); (2) what Control decided that it did not ask for: the scoped
match is INSTEAD OF the unscoped entry of the same name, never as well as it;
requiring scoping is the explicit key, and the source-count alternative was
rejected because what a mapping version maps must not depend on state outside
the document; there is no wildcard; the rejections are
mapping.directory_name, mapping.directories_empty (`directories: []`) and
mapping.directory_duplicate, and mapping.too_large counts a scoped entry once
per directory it lists; logins match unscoped entries only; EVERY mapping
write reaches every directory subject by itself, whether or not the sync is
running (at once when written through the API, within a minute by a sweep on
a node that has seen the tenant when written by `identity mapping-put`), each
changed subject getting a cache_invalidate (one for the tenant past 64 in a
batch) and no session ended, so 0004 needs neither a re-push nor its
reconciliation to apply a mapping (a re-push of unchanged data is a replay
that changes nothing anyway); 0004's "When the sync is not running" gains that
a mapping change still reaches directory subjects; its criteria can watch
`stale_directory_subjects` on GET …/identity/claim-mapping fall to zero, beside
`directories` and `require_directories` there, and
`operator.claim_mapping.applied` (recalculated, changed, complete) is
recorded; `directory_source` is on the decision record's subject and on
explain's `mapping`, which bears on 0004's explain criterion; an older Control
build reads a mapping that uses either key as unparseable, an outage for that
tenant and never an unscoped application, so a rolling upgrade finishes
before such a mapping is written; (3) its plan: §4's IdentitySync bullet,
where it says group names reach the mapping with nothing saying which
directory they came from, and points at the request; (4) its pin: v0.30.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
