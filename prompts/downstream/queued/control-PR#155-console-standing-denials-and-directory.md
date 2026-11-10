# control-PR#155 — the console's locks, quarantines and the directory's subjects

Queued by [control#155](https://github.com/Hoplock/control/pull/155), Control's
phase 0055, released as `v0.42.0`. It shows standing denials and the
directory's facts in Control's console, offers the four levers over a lock
and a quarantine, adds a claim-mapping screen, and, to serve them, moves the
north-bound API to version `5`. It grows the status vocabulary Enterprise's
screens are built from. `ext`, `server`, the contract and the cross-repo
protocol are untouched. The obligations are its `## Cross-repo impact`
section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/155. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.42.0 and never from main —
(1) its pin, and what its binary now does: no migration; north-bound API
version 5 with north_api_min still 1; Control's console shows a subject lock,
a target quarantine and the directory's deactivation as deny-class states
(Locked, Quarantined, Deactivated) wherever the subject or target is shown,
offers lock, unlock, quarantine and release to a holder of session:revoke
(admin, grant-admin) and nobody else, and has the claim mapping at
/console/inventory/claim-mapping[?edit=1]; an Enterprise screen that links to
a lock, a quarantine or the claim mapping names these paths under /console/
and never builds its own, and the inbound action channel's "release a
lockout" is the subject page's Unlock (/console/inventory/subjects/<id>);
(2) north-bound version 5 adds group_map to GET …/identity/claim-mapping (the
document's group entries in order, each {external, group, directories},
directories absent on an unscoped entry, group_map absent when the mapping
maps no group) — the first since member: a client DECLARING version 4 or
older that reads a mapping with group entries is answered 500
api_version_unexpressible, so a host client that reads the claim mapping
declares 5 or declares nothing — and ?record=missing on GET …/subjects (the
ids a lock or the directory's deactivation holds with no subject row, each as
GET …/subjects/{subject} answers it, by id, paged, the cursor binding the
filter; another value 400 invalid_request {field: record, problem: invalid},
and group beside it {field: record, problem: at_most_one_of_group_or_record});
a screen that must find a lockout to release whose subject was deleted reads
this list and never pages the tenant's subjects for it;
(3) the component set Enterprise's screens build from gains, at this pin: the
statuses Locked, Quarantined and Deactivated on DESIGN.md §6's deny row
(STATUS.locked/quarantined/deactivated) — an Enterprise screen showing a lock
an inbound action placed, or a person its sync deactivated, renders it through
these and never through Denied or an outage tone — and ConfirmDialog's ready
prop (a field refused where it stands holds the confirm); an undelivered
standing denial (503 standing_denial_undelivered) reads as Pending delivery,
in force, with a retry, never a failure; DataTable's <th> carries
data-column;
(4) Control's own browser checks now run against a fixture host — Control
plus one ext.IdentitySync (ui/e2e/fixture/host) — so an Enterprise prompt
that registers its own sync in its e2e fixture can follow that shape; the
identity sync's row on Control's extensions screen is Control's 0056, not
Enterprise's. No citation of Control's numbers moves; nothing was renumbered.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
