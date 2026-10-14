# control-PR#69 — the north-bound API and policy lifecycle

Queued by [control#69](https://github.com/Hoplock/control/pull/69), Control's
phase 0019: the north-bound surface completed, the policy lifecycle with its
publish-time report, and the proxy's phases 0043–0046 reaching this server. It
cuts `v0.6.0`, with a `### Breaking` section. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`. The route table, the
severities and the finding id are verbatim in Control's
`docs/learnings/0019-northbound-api-and-policy-lifecycle-learnings.md` summary.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/69. Do not implement any queued prompt in this
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
section: for hoplock/enterprise —
(1) ext.FindingWarning now gates activation: it used to block nothing.
Control runs every registered ext.PolicyValidator on validate, upload and
activate. A warning holds activation until the author accepts it by the
finding's id, recorded on operator.policy.activated. FindingBlocking refuses
and no acceptance moves it. FindingAdvice is shown and gates nothing. The id
is <Code>/<provider>/<Path>, so Code and Path must be stable from run to run
for one finding. A warning meant only as information becomes FindingAdvice.
A validator error is an outage (500), never a refusal.
PolicyDocument.Ref.Version is empty at validate and upload, and is the
stored version at activation. No identifier changed. Rewrite every prompt
and plan passage describing its governance validator's severities from this.
(2) Its 0008's skip "no binding route before Control's 0019" is lifted.
Bindings are at PUT/DELETE .../access-context/providers/{provider}/binding
(access-context:write, admin only). They are read at GET on that path and at
.../access-context/bindings (access-context:read). Providers are at
.../access-context/providers. Writes are audited as
access_context.binding_put and access_context.binding_deleted.
(3) Paths and codes a host can no longer take. A host route on a path
Control serves, or a host-declared code that is Control's, is refused at
start-up. Control now serves under /api/v1/tenants/{tenant}/: policy/...,
decisions/..., sessions/..., targets..., subjects..., role-bindings...,
proxies..., fleet/..., audit/..., revocations, extensions,
access-context/providers... and access-context/bindings. Its new codes are
policy_invalid, findings_not_accepted, policy_blocked,
decision_unexplainable, config_key_not_fleet_owned, config_value_invalid,
host_key_unresolved and unavailable. Check every planned host route and
code against both lists.
(4) New permissions a host route may name: target:write, audit:retain,
session:revoke, access-context:read, access-context:write, extension:read.
Every reading role gains access-context:read and extension:read, so a
cross-tenant host route (M24) may name either.
(5) Audit. audit_records.target_auth_method and target_auth_rung are now
credential_method and credential_rung, with the rung counting from 1.
Control's own retention (POST .../audit/retention and
.../audit/captures/retention, audit:retain) deletes a prefix of a stream and
records operator.audit.retained. Its long-term archive (M15) archives a
prefix before Control's retention deletes it, and its verifier starts from
the audit_retention marker as Control's does. Operator acts are
operator.<noun>.<verb> records on the control stream, which its SIEM export
and compliance reporting can name.
(6) events.publish_listener, events.publish_token, audit.read_listener and
audit.read_token are removed and refused as unknown keys. Any configuration,
test or doc naming them changes to the north-bound routes.
(7) Its pin: every obligation above holds from v0.6.0, which control#69's
merge cuts, never from main. Enterprise's pin moves to v0.6.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
