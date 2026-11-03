# control-PR#127 — the directory source on the sync sink

Queued by [control#127](https://github.com/Hoplock/control/pull/127), Control's
phase 0042, which answers enterprise#51's upstream request, item 1:
`ext.SyncedSubject.Source`, `ext.SyncedGroup.Source`, `ext.DirectoryRef`, and
`SyncSink.DeactivateSubject` and `DeleteGroup` taking a `DirectoryRef`,
released as `v0.29.0`. The obligations are its `## Cross-repo impact`
section's, for `hoplock/enterprise`, the repository that asked.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/127. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.29.0 and never from main,
and v0.29.0 breaks two signatures its 0004 calls — (1) its 0004's "What
Control does not do yet", rewritten from what merged: its sync names each
directory as Source (^[a-z0-9][a-z0-9-]{0,62}$, required on SyncedSubject,
SyncedGroup and DirectoryRef, else KindInvalid naming source), sends each
IdP's ids as the IdP holds them with no namespacing, and calls
DeactivateSubject(ctx, ext.DirectoryRef{Tenant, Source, ExternalID},
reasonCode) and DeleteGroup(ctx, ext.DirectoryRef{…}); Control keys
everything by (tenant, source, ExternalID), a subject's Groups resolve within
its own source, and a login name stays unique per tenant across sources
(KindConflict); the sink accepts several directories per tenant, but
Control's 0043 (directory-scoped group entries) had not merged when this was
queued — check whether it has, and by which release — so 0004 keeps refusing
a second directory per tenant until its pin carries 0043's release, and drops
the skip only on the two-directory criteria that do not depend on group
mapping (two subjects for one id, deactivation per source, a tied connector
per source); a tied connector names its source with `hoplock-control identity
connector-put --directory-claim <claim> --directory-source <source>`, made
after the directory has provisioned; (2) what Control decided that it did not
ask for: a tie to a source nothing in the tenant has written (no link, no
group) is refused, naming up to 20 sources the tenant has, and claim and
source are both or neither (writing neither unties); rows written before
v0.29.0 are keyed under the source `default`, so a sync rebuilt against it
sends Source "default" to carry on, and any other name provisions new
subjects whose usernames collide (KindConflict); the deactivation's standing
entry on a decision record carries `source` beside `external_ref`; a subject
read has two different `source`s: the top-level one is what asserted the
subject (for a directory subject the sync's provider), `directory.source` is
the directory; a person who moves between directories is two subjects, and
the second's login name is KindConflict while the first still presents it;
`source` is on every identity_sync.* record and `directory_source` on GET
/api/v1/session/methods; (3) its plan: §4's IdentitySync bullet, where it
says nothing on the sink names a directory and points at the request;
(4) its pin: v0.29.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
