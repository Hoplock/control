# control-PR#116 — identity sync: Control's sync sink and de-provisioning

Queued by [control#116](https://github.com/Hoplock/control/pull/116), Control's
phase 0040 (decision M28, extended; release `v0.27.0`). The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`. It answers
`Hoplock/enterprise#26`, item 3.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/116. Do not implement any queued prompt in this
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
section: for hoplock/enterprise — every obligation holds from Control
v0.27.0 (M23), never from main.
(1) Its 0004, "What Control does not do yet": rewrite it from what merged and
stop building around the gap. Control implements ext.SyncSink per tenant,
joined on ExternalID by a link row and never on the username; Control
allocates the subject id (dir-...). It is safe for concurrent use: calls on one
ExternalID are serialised, calls on different ids run concurrently. Ordering:
an upsert applies only when its UpdatedAt (required, compared to the
microsecond) is later than the last applied one, else it changes nothing and
writes no record; Active: false is a deactivation with exactly
DeactivateSubject's effect; Active: true lifts a standing deactivation only
when its UpdatedAt is later than both the last applied one and the instant
Control applied the deactivation (Control's clock); DeactivateSubject for an
unknown ExternalID is kept and the later create provisions the subject already
deactivated; DeleteGroup keeps a tombstone (also for an unknown id) and an
UpsertGroup older than it changes nothing. A deactivation revokes, with the
sync as actor (identity-sync:<provider>, no person): north-bound sessions, API
tokens and outstanding SSH certificates in its own transaction; then every
live or scheduled grant (access revocation, ending the sessions each backed);
every pending grant request (the cancel path); then cache_invalidate and
session_kill for the subject. The holder is shown "Your account was
deactivated by your organisation's directory.", never the reasonCode. A
repeat writes no second record and finishes the rest; a re-activation
restores nothing revoked. Error kinds: KindInvalid (names the field, nothing
applied, do not retry unchanged); KindConflict (the Username is presented by
another subject in the tenant; nothing applied; an existing subject is never
adopted or merged); KindUnavailable (the change stands and its revocations or
publications are unfinished, or Control is stopping the sync: safe to retry
with the same call); KindInternal (a store failure, safe to retry).
ExternalID is unique per tenant across every directory the one sync feeds in,
so its multi-IdP collision rule must namespace them. The tenant's claim
mapping decides what of its claim rules' output reaches policy: attributes
and Email (as the claim email) are offered as claims, groups (ExternalIDs)
resolved to the Name UpsertGroup gave and offered under the mapping's
groups.claim, so a group is matched by its directory Name; a renamed group
maps nothing until the mapping names the new name; attributes named
chain_hop_proxy_id or under hoplock. are dropped. A connector may name a
directory_claim (hoplock-control identity connector-put --directory-claim,
refused while no sync is registered): a login through it reaches the
provisioned subject and rewrites nothing, or is refused with
federation.not_provisioned and creates nothing; a deactivated subject is
refused with federation.subject_deactivated through every door and its
north-bound credentials stop authenticating. PUT and DELETE of a
directory-managed subject answer 409 subject_directory_managed; subject reads
carry directory_managed, directory {provider, external_id}, deactivated and
deactivation {deactivated_at, reason_code}. Every applied call is recorded as
identity_sync.subject_provisioned, subject_updated, subject_deactivated,
subject_reactivated, group_upserted, group_deleted or refused, with provider
and external_id. A Start error (or a panic, or no return in 30 s) is logged at
ERROR, Control serves, and GET .../extensions shows the sync registered and
not started (started, start_error); an unreachable directory is not a Start
error: return nil and reconnect on your own goroutines. Turn its skipped
end-to-end criteria into real ones.
(2) Its criterion that explain names a claim rule: Control's decision record
names Control's mapping version only; if Enterprise still needs its rule
there, that is a new upstream request.
(3) Its plan: §4's IdentitySync row and its misreadings, and wherever E4 and
E11 say what provisioning does.
(4) Its pin: every obligation holds from Control v0.27.0, never from main.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
