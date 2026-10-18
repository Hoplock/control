# control-PR#74 — signed retention markers

Queued by [control#74](https://github.com/Hoplock/control/pull/74), Control's
phase 0024: every retention pass is signed by a per-tenant key behind
`ext.KeyStore`, and the verifier refuses a marker or a removed capture whose
seal does not hold. It cuts `v0.11.0`, with a `### Breaking` section. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`,
and they extend control#69's (5) on retention, whose own sync
(`control-PR#69-northbound-api.md`) runs first or in the same session.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/74. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, all holding from v0.11.0 and extending
control#69's (5) (whose own sync, control-PR#69-northbound-api.md, runs first
or in the same session) —
(1) Its long-term archive's verifier, which starts from the audit_retention
marker as Control's does, must check the marker's seal as audit.Verifier does,
or call it: Ed25519 over the length-prefixed bytes "hoplock.retention/1",
tenant, stream, through_seq, through_hash (audit.RetentionMessage), checked
against ext.KeyStore.Signer(ref).Public() with ref = the marker's own tenant
and audit_retention.key_name — never a stored public key. An empty signature,
one that does not check, or a key the store does not hold is a break
(audit.BreakRetentionMarker, "retention-marker"). Captures: a per-tenant
signed watermark in audit_capture_retention ("hoplock.capture-retention/1",
tenant, instant to the microsecond), and a retained capture counts only when
its record's body timestamp is before it.
(2) A host's registered ext.KeyStore now holds one Ed25519 key per tenant,
named audit-retention:<generation>, generated on the first pass; the newest
name signs. It must keep every generation: destroying one makes every marker
it signed a break. Control's certificate authority still uses only its
software custodian, not a registered ext.KeyStore — a known gap Control may
queue; do not build on either behaviour of the CA's custody yet.
(3) Retention on a deployment with no key store (no
credential.key_encryption_key_env and no ext.KeyStore) is refused, 503
retention_key_not_configured; a pass over a start that does not verify is 409
retention_unverified. A host may declare neither code.
(4) audit-verify and the new audit-retention subcommand hand the host's
configuration sections to HostConfig and seal its registry, as the daemon
does; no other subcommand does. A HostConfig with side effects must tolerate
being called by them.
(5) Records and answers: operator.audit.retained gains operator.key and
operator.signature (a capture pass's also operator.watermark); new
operator.audit.retention_adopted and operator.audit.retention_key_rotated;
both retention answers gain key. Any SIEM mapping or compliance report naming
retention records changes.
(6) Its pin: these hold from v0.11.0, which control#74's merge cuts. Retention
written by v0.6.0–v0.10.0 is unsigned and is a break until an operator adopts
it with hoplock-control audit-retention adopt (Control's CHANGELOG v0.11.0).
(7) Control's queued phases moved again (revision ca-custody): a new 0030, CA
custody through the key store, gives the certificate authority a registered
ext.KeyStore when there is one, else Control's software custodian — so an
Enterprise key store will hold CA keys (ssh-ca:<generation>) once it lands.
Post-quantum posture is now 0031 and one contract version 0032; instance
identity (0027), the console (0028) and E2E (0029) did not move. Every
citation of a Control phase number resolves through Control's docs/PLAN.md §10
mapping table; cite a phase by name beside its number. Where an Enterprise
prompt plans HSM or KMS custody for the CA, note that it depends on Control's
0030, not on any release so far.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
