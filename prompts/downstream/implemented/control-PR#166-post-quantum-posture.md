# control-PR#166 — post-quantum posture

Queued by [control#166](https://github.com/Hoplock/control/pull/166), Control's
phase 0063, released as `v0.50.0`. Each of Control's listeners can now
terminate TLS itself (`south.tls`, `north.tls`), states its wire posture at
start-up, and can require a hybrid post-quantum key exchange (decision M29);
`ext.KeyAlgorithm.String` renders a value outside the enum as `"unknown"`.
`ext`'s interfaces, `server` and the contract are untouched, there is no
migration, and the north-bound API stays at version 6. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/166. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.50.0 and never from main —
(1) ext.KeyAlgorithm.String now returns "unknown" for a value outside the
enum, where it returned "ed25519"; nothing in Enterprise that maps or stores
algorithm codes may rely on the old fallback, and a key store's Generate is
still only ever asked for the three members; (2) ext.KeyAlgorithm has no
post-quantum member on purpose — no OpenSSH post-quantum certificate signature
exists — and when Control adds one (a tripwire in its internal/credential
names the steps), Enterprise's key stores will have to generate and sign with
it, under that release's ### Breaking; nothing is owed for it now beyond
knowing it; (3) Control's configuration gains `south.tls` and `north.tls`
(`certificate_file`, `key_file`, `client_ca_file`, `require_hybrid_kex`):
they are Control's keys, so a host section may not be named either, and a
host's routes on the north-bound listener are served over that listener's TLS
and posture (Control M27, M29) — any Enterprise prompt, plan text or deployment
doc that assumes TLS is always terminated in front of Control should say it
may now be Control's own, and that `client_ca_file` authenticates the
transport only (a caller's credential is unchanged); (4) the pin, and what its
binary now does: no migration; the north-bound API unchanged at 6; `server`
untouched; with no `tls:` certificate a listener is plaintext exactly as
before, and a `tls:` block that cannot be honoured refuses the start.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
