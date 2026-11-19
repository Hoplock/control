# control-PR#164 — CA custody through the key store

Queued by [control#164](https://github.com/Hoplock/control/pull/164), Control's
phase 0062, released as `v0.49.0`. The certificate authority's keys now live in
the deployment's one key store — a registered `ext.KeyStore` if any, else
Control's software custodian — the same one the retention keys already used,
and a CA key that store does not hold is refused rather than signed by another
custodian. `ext`'s interfaces, the contract and the north-bound API version are
untouched; `server` changes only in its doc comments. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/164. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.49.0 and never from main —
(1) its ext.KeyStore (HSM/KMS custody) now holds the CA's keys too: it must
generate, sign with and describe Ed25519 keys named `ssh-ca:<generation>`,
per tenant, beside the `audit-retention:*` keys it already holds, and keep a
retired one through the rotation overlap (Control never destroys one); it
should answer ext.KindNotFound for an absent key and ext.KindUnavailable when
it cannot be asked — Control tells the two apart (`503 ca_key_not_held` vs
`503 key_store_unavailable` on POST /v1/credentials/certificate; `500
internal` vs `503 unavailable` on GET …/ca) — and state a truthful
KeyInfo.Custodian, which is now what `ca show` and GET …/ca report; (2) a CA
key made in Control's software custodian before Enterprise's key store was
registered is refused, not used — only a test fixture or development database,
since nothing is deployed — and `hoplock-control ca rotate` (or POST
…/ca/rotate) is the way out; (3) the `ca` subcommands (show, rotate, issue,
certificates) now call HostConfig and seal the host's registry, as the daemon,
audit-verify and audit-retention do, and the doc comments of server.Main and
server.Options.HostConfig name every subcommand that does; no `server`
identifier changed; (4) the pin, and what its binary now does: no migration;
north-bound API still version 5.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
