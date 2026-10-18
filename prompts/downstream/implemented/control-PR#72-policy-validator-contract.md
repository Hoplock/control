# control-PR#72 — the policy validator contract, corrected

Queued by [control#72](https://github.com/Hoplock/control/pull/72), Control's
phase 0022: the audit of 0019's policy lifecycle. It cuts `v0.9.0`, with a
`### Breaking` section. It corrects and completes what
[control#69](https://github.com/Hoplock/control/pull/69) told
`hoplock/enterprise` about `ext.PolicyValidator`, whose sync is queued beside
this one as `control-PR#69-northbound-api.md`; run that one first. The
obligations are #72's `## Cross-repo impact` section's, for
`hoplock/enterprise`. The corrected doc comments are verbatim in Control's
`ext/policy.go` at `v0.9.0`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/72. Do not implement any queued prompt in this
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
(1) A finding severity outside FindingAdvice, FindingWarning and
FindingBlocking is an outage: validate, upload and activate answer it as a
validator error (5xx). It used to read as advice. Enterprise's validator
returns one of the three.
(2) A validator runs only on a source the compiler accepted: at validate, at
upload before it is stored, and at activation against the fleet as it is
then. It never sees an invalid source.
(3) PolicyDocument: Ref.Version is "" until a version is stored (validate,
upload) and the stored version at activation. AuthorID is the subject asking
for that judgement — the uploader, and at activation the activating subject
— and is EMPTY for an API token, so a CI apply carries no author. ProposedAt
is each call's own time. ActiveVersion is the version serving when asked (at
activation, the one being replaced), "" while the tenant has none.
(4) With no validator registered, Control's own publish-time checks run
beside the compiler's. A report lists every finding by severity, then id,
whoever produced it: a validator's findings do not follow Control's in
position.
(5) Two new Control finding codes, authoring.cache_hint_spans_sources
(warning) and algorithm.policy_unmet_truncated (advice); a ban no build has
declared an offer for is algorithm.declaration_unknown (advice), no longer
algorithm.ban_unmatched.
(6) Its pin moves to v0.9.0. Rewrite every prompt and plan passage
describing its governance validator from the corrected ext/policy.go
comments.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
