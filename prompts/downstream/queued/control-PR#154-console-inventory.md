# control-PR#154 — the console's Inventory

Queued by [control#154](https://github.com/Hoplock/control/pull/154), Control's
phase 0054, released as `v0.41.0`. It adds Inventory to Control's console —
targets, identities, groups, roles and grants, each a list and a detail, with
every write the API offers — and, to serve it, moves the north-bound API to
version `4` with the group reads and holder filters. It grows the component
set Enterprise's screens are built from. `ext`, `server`, the contract and the
cross-repo protocol are untouched. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/154. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.41.0 and never from main —
(1) its pin, and what its binary now does: migration 0026 (one GIN index on
subjects, subjects_groups_idx; it blocks writes to subjects while it builds,
so a sign-in waits, and authorize does not), north-bound API version 4 with
north_api_min still 1; Control's console has Inventory at
/console/inventory/targets[?label=k=v], /console/inventory/targets/<id>,
/console/inventory/subjects[?group=], /console/inventory/subjects/<id>,
/console/inventory/groups, /console/inventory/groups/<name>,
/console/inventory/roles, /console/inventory/roles/<role>,
/console/inventory/grants[?subject=&state=], /console/inventory/grants/<id>,
and the creates at /console/inventory/new/{targets,subjects,grants}; an
Enterprise screen that links to an identity, a group, a role, a target or a
grant names these paths under /console/ and never builds its own; (2) north-bound version 4 adds GET …/groups (every group name in
the tenant once — on a subject, bound to a role, or recorded — with
member_count, roles and, where one exists, record; paged by name, M26) and
GET …/groups/{group} (404 not_found for a name the tenant does not hold), both
identity:read; ?group= on GET …/subjects (a group's members); and ?role=,
?subject=, ?group= on GET …/role-bindings (one role's holders, or what one
holder holds directly; an undefined role is 400 invalid_request on role, both
holders {field: group, problem: at_most_one_of_subject_or_group}); each
cursor binds its filters. A host list or screen that needs a group's members
or a holder's roles reads these and never pages the tenant's subjects or
bindings itself; a host client that depends on them declares version 4;
(3) the component set Enterprise's screens build from (Control's 0050) gains,
at this pin: ViewLinks (ui/src/components/ViewLinks.tsx, a screen's views as
links, the current one aria-current="page"), Ident's title size (a page about
one object named by its id), the status Scheduled (info; a grant whose window
has not opened, DESIGN.md §6), the tokens --form-max and --form-column-min,
and DataTable's cut-page note marked data-partial-note; DESIGN.md §7 now says
a list's partial state is the rest of a cut answer, said beside its rows.
Every prompt that builds an Enterprise screen with views, an object's page or
a form uses these and adds no look of its own, and an approval screen that
shows a grant's window reads it through Scheduled/Active/Expired/Revoked;
(4) Control's console offers a grant create to grant:write and, with a
grant workflow registered (E8), shows the 202 as a pending request naming
the workflow and the request id, never a grant; nothing a workflow returns
changes. No citation of Control's numbers moves; nothing
was renumbered.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
