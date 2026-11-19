# 0061 — E2E topology, CI gate & hardening — Learnings

## Summary
- **Shipped:** `deploy/` (real proxy at `deploy/PROXY_REVISION`, a protected-zone proxy reached only by its outbound relay registration, this server as `test/e2e/host` = `daemon.Main` + an empty `daemon.Host`, as the console's fixture host does (only `cmd/hoplock-control` may import `server`), Postgres migrated+seeded by `control-init`, sshd targets); `test/e2e/`, 19 scenarios in one ordered `TestTopology`, green; CI jobs `e2e` and `govulncheck` (both gate, both in `release.needs`) and the scheduled, non-gating `contract upstream` workflow (`scripts/contract-behind.sh`). Release `v0.48.0`, MINOR, no migration, no `ext`/`server` change.
- **Run it:** `make e2e` (or `e2e-up`, then `go test -tags e2e -count=1 -v -run 'TestTopology/<name>' ./test/e2e/`, then `e2e-down`). Needs Docker, git, ssh-keygen. North API `127.0.0.1:18081`, token `ht.default.e2e-operator-secret`. Scenario table and node/network table: `deploy/README.md`.
- **Fixtures:** `gen-material.sh` → `deploy/keys/`, `deploy/.env` (KEK), `control/seed.yaml` from `seed.template.yaml`; one rule per scenario in `control/policy.yaml`; one sshd under six DNS aliases = six target rows. Nothing generated is committed.
- **Pin:** `deploy/PROXY_REVISION` (`hoplock/proxy@97e97de`), separate from `contract/UPSTREAM`; `deploy/build.sh` refuses a proxy whose `api/control.yaml` differs from the vendored one. Bump: edit the pin, `make e2e`, one PR.
- **govulncheck:** `make vulncheck`, `GOVULNCHECK_VERSION` v1.9.0 (v1.1.4 cannot load a go1.27 module). Red with no code change = a new advisory: upgrade, or a dated justification; never delete the job. Seen failing on a known-vulnerable pin in this PR's CI (Details).
- **Found and fixed here:** the showcase join returned NOTHING against the real proxy — it records a blocked command as `policy_decision`/`command.policy`, and stamps `decision_id` only on the session's `authorize` record. Now both kinds, decision via that record (cache hits too), and `decision_id`+`grant_id` on each item (`internal/store/audit.go`).
- **Upstream request raised:** the contract has no way for a proxy to report its relay registrations, so the seed writes one and `fleet.relay_registration_ttl: 8760h` keeps it believed. Queued in `prompts/upstream/queued/` (the PR names the file).
- **Deviation:** six scenarios moved to **0071** (queued last; no renumbering): the three needing a test host's fakes, uid-lease refusal, fleet config round trip, stream-unhealthy hints.
- **NEXT:** 0071 adds fakes to `test/e2e/host` (never a route on this server's listeners); 0063 extends this topology with TLS. Known gaps: `deploy/README.md`, "What this topology cannot prove".

## Details

### What the suite proves, and where to look
`deploy/README.md` has the table, one row per scenario. The order in
`TestTopology` is load-bearing: `outage_is_not_a_deny`, `uid_floor_survives_restarts`
and `failed_issuance_is_an_outage` stop or restart components, and
`no_uid_issued_twice` reads the `uidLedger` every earlier scenario fed. Every
SSH client runs in the `client` container (`edge` only); the suite reads this
server through the north-bound API. `TestMain` waits until a brokered session
and a relayed one both succeed — before that, a scenario fails as an outage
that has nothing to do with what it tests.

### Findings the topology made (the reason it exists)
1. **The showcase join was empty against a real proxy** (fixed). Two
   assumptions, each green in unit tests: that a blocked command is a
   `command` record (the proxy writes `policy_decision`, event
   `command.policy`, `action: block_command`, `tier: filtered_exec|restricted_exec`;
   it also writes a `policy_decision` with `action: deny` for the channel
   request a restricted-exec block refuses, which is correctly NOT a second
   blocked command), and that the command record carries `decision_id` (only
   `authorize` does — 0010's test comment "0008 stores decision_id on both
   sides" was the wrong belief). The join is now a `LEFT JOIN LATERAL` keyed on
   the record's decision or its session's authorize record on the same proxy;
   a plain join on that expression made the planner hash-join and sort, which
   `TestTheShowcaseJoinReadsItsOrderFromAnIndex` caught.
2. **Relay registrations have no report path** (raised upstream). The proxy
   holds the registration; nothing in the contract tells Control. The only
   writer of `relay_registrations` is `seed`, and `fleet.Registry.Heartbeat`
   has no production caller. In a real deployment a relay route works for
   `relay_registration_ttl` (60s) after a seed and then never again.
3. **Chain legs have different session ids.** `explain` on the user's
   session id shows the edge leg only. The inner leg's record is found by
   subject, target and time; its `session_start` carries `hop_peer: true` and
   `hop_trail`. Not a contract violation; a usability gap, listed.
4. **Field names agree.** `credential_method`, `credential_rung` (from 1),
   `credential_certificate_serial`, `target_kex_algorithm`, `algorithm_profile`
   and the declaration's `algorithm_profiles` (exactly the contract's three:
   `default`, `legacy-rsa-sha1`, `legacy-device`) all arrive as this store
   indexes them, and no `logging.gap` is refused.
5. **The hop trail is recorded at `inputs.context.hop_trail`**, not under
   `connection` — a test written from the prompt's wording looks in the
   wrong place.

### Environment notes (this session's sandbox, not CI)
The sandbox had no `ssh-keygen` and its egress proxy allowed only
`archive.ubuntu.com` among apt mirrors, so local runs used an Ubuntu 26.04
base (OpenSSH 10.2) through `E2E_BASE_IMAGE`, and an ssh-keygen run inside the
client image. That is why the Dockerfiles take `BASE_IMAGE` and why the target
drops `pam_motd` (Ubuntu's writes `.cache/motd.legal-displayed` into the home
of whoever logs in, which broke "byte-identical"). CI runs the default
`debian:stable-slim`. `vuln.go.dev` was blocked too, so govulncheck was proven
in CI, not locally.

### The govulncheck gate, seen failing
This PR's commit history carries the proof: one commit pins a module version
with a published advisory reachable from the module, CI's `govulncheck` job
fails on it, and the next commit reverts it. The PR description links both
runs.

### The contract-behind notice
`scripts/contract-behind.sh <full proxy checkout>` compares the bytes of
upstream `api/control.yaml` at HEAD with the vendored checksum. Current: one
line, exit 0. Behind: the pinned commit, the newest upstream commit that
changed the file, the commits in between, a `::warning::` annotation and the
job summary — amber, exit 0, because a pin behind is often legitimate.

### Deviation: what went to 0071, and why
The prompt's scenario list was too large for one session's context budget
(PROTOCOL §1). The six left are the ones needing new machinery — fake `ext`
registrations driven by the suite (inbound action, directory de-provisioning
with a console sign-in that needs an IdP container, two directories), a second
server configuration (uid-lease exhaustion), and the D18 rollout and stream
health, which need the most real waiting. 0071 is queued last because nothing
in 0062–0070 depends on it; move it up if the user wants it next (that is a
renumber, PROTOCOL §6). The prompt's known-gaps list named "a UI"; the console
(0049–0060) has since been built, so it is not listed.

### Known gaps (the next prompts' seed)
Multi-node event fan-out and HA, external anchoring for the audit chain,
device posture, real scale and geo testing (PLAN §11), plus the two the
topology found: relay registration reporting (upstream) and chain-leg session
correlation.
