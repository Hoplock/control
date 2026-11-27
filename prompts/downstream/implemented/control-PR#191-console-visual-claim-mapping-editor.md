# control-PR#191 — the console's visual claim-mapping editor

Queued by [control#191](https://github.com/Hoplock/control/pull/191), Control's
phase 0068. It answers no request: it is the sync a release owes its consumer
when it moves the north-bound API and the component set (`docs/CROSS-REPO-PROTOCOL.md`
§4.1). The obligations are control#191's `## Cross-repo impact` section's, for
`hoplock/enterprise`. Nothing is owed to `hoplock/proxy`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/191. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, from Control's release v0.55.0 — the release
control#191 cuts — and never from Control's main:
(1) The pin, and what the binary now does. North-bound API version 9
(north_api_min stays 1, so a client declaring 8 or less is served exactly as
before): POST …/identity/claim-mapping/validate, a dry run of a claim mapping
document that stores and records nothing — refused exactly as the PUT refuses
it, or answered {digest, attributes, groups, directories,
require_directories, group_map, samples}, each sample {claims, groups,
directory_source?} or {subject} mapped as the server maps a sign-in or a
directory's identity, at most 16 — and GET …/identity/directories, the
directory sources that have written in a tenant, {source, linked,
deactivated}, paged by source (M26). Both are identity:read; a {subject}
sample is answered to identity:write only (403 forbidden otherwise). The
shapes are Control's docs/API-VERSIONING.md row 9. ext/, server/ and the
contract are unchanged.
(2) The component set this repository's screens build from gains TextField
(typed text applied on leaving the field or on Enter, checked before it
applies, refused at the field) and Suggestions (values offered as typed,
binding nothing). Control's ui/DESIGN.md §7 names both and says a list whose
order is its document's (a claim mapping's claims) is the sortable list; §11
names one document core, ui/src/screens/yamlDocument.ts, over the one YAML
library. Where this repository's prompts or plan describe a host screen that
edits a YAML document, or list the components a host screen may use, they say
so: such a screen uses Control's core and components, never a library or a
copy of its own.
(3) Where this repository's plan or prompts describe Control's claim mapping
as written without a way to try it first, or a host screen that edits or
previews a mapping, they say a draft can be dry-run and previewed against the
active version through the route above, and that the directory sources a
tenant holds are a bounded list.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers (none: this is a release's sync), and
says how you searched for stale references — the actual grep, not "I looked
carefully".
```
