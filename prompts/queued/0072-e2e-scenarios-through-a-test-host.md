# 0072 — E2E scenarios through a test host's extensions

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — **§9** (the test topology, as 0061 built it), **§6** at "The
  directory door" (what a deactivation refuses), **§4** at "Configuration
  distribution" (proxy D18), and decisions **M9** (revocation and its hints),
  **M11** (`401` is a deny, everything else an outage), **M15** (a host extends
  Control through `ext` and `server`), **M18** and **M28** (what a lock and a
  deactivation are, and what each owes the stream).
- `docs/learnings/` — every summary; open in full `0061` (the topology, the
  harness, and why this phase exists), `0039` (the inbound action executor and
  the lock), `0040` (the identity sync's sink and de-provisioning), `0042` (the
  directory source every sink call and tied connector names), `0043` (directory-
  scoped group entries and the recalculation a mapping write starts) and `0019`
  (the fleet configuration surface).
- `prompts/implemented/0061-e2e-topology-and-ci.md` — the scenario text this
  phase finishes. Its wording for each scenario below is the specification;
  this prompt does not restate it.
- In the **Hoplock Proxy repository**: its `test/e2e/fleetconfig_test.go`, which
  proves the proxy's half of the configuration round trip.

## Objective
0061 built the cross-repository topology (`deploy/`) and its suite
(`test/e2e/`) and ran nineteen scenarios green against a real proxy. Its
session did not reach six more, and they are this phase. Each is a product
claim neither repository can prove alone, and three of them need what no
stock binary has: a host registering fake extensions through
a host's registry (`server.Options.Registry` to Hoplock Enterprise; `daemon.Host` to
a binary inside this module), which is how a host extends Control (M15).

## In scope

### The test host's fakes (`test/e2e/host/`)
0061's host (`test/e2e/host`, built only under the `e2e` tag by
`deploy/build.sh`) starts Control through `internal/daemon` with an empty
`daemon.Host` — as `ui/e2e/fixture/host` does, since only `cmd/hoplock-control`
may import `server` (`architecture_test.go`). Give it, in its registry:

- an `ext.ActionHandler` whose only job is to call the executor `Start` hands
  it, and
- an `ext.IdentitySync` whose only job is to call the sink it is handed,

each driven by the suite. **How the suite drives them is the design decision
of this phase**, and it is bound by `docs/PROTOCOL.md` §3's debug-endpoint
rule: whatever channel the suite uses must not be a route on either listener
this server binds, must not match `/debug/` (`TestNoGoFileBindsADebugRoute`),
and exists only in a binary built under the `e2e` tag. A file or socket the
fake watches inside the control container, written by
`docker compose exec`, satisfies all of that; say in the learnings what you
chose and why.

### Scenarios (`test/e2e/`)
Each as 0061's prompt words it, added to `TestTopology`'s ordered run:

1. **An inbound action ends a session and locks its subject** (0039, M28) —
   `kill_session` with the handler's `ReasonText` (and Control's words when it
   gives none) on the user's terminal; `lock_subject` refusing a new
   connection with the vague message and `explain` naming `subject_locked`,
   `action-handler:<provider>`, the action id and the external reference, and
   ending a session the subject still held; `unlock_subject` letting them
   back in; each act's record with the handler as actor; `audit-verify`
   passing. Use `carol`, whom 0061 seeded for exactly this, so a lock gone
   wrong never strands `alice`.
2. **The directory de-provisions a person with a live session** (0040,
   M28), including the console sign-in and its refusal through a connector
   tied to the directory (`federation.subject_deactivated`). The console
   needs an OIDC connector the topology does not have yet: add a test IdP to
   `deploy/` (a container, never a route on this server) or say in the
   learnings why the console half is proven another way.
3. **Two directories feed one tenant** (0042, 0043), the shared-group
   mappings and the recalculation with no directory call after it, waiting
   for `stale_directory_subjects` to reach zero.
4. **A Control without the endpoint refuses `ephemeral-user`, and says it is
   an outage** — with a `brokered-key` session over the same topology still
   succeeding. 0061 set `uids.block_size: 8` in `deploy/control/control.yaml`
   so a proxy comes back for blocks inside a run; exhausting the range (a
   second, narrower configuration the scenario restarts this server with) is
   one way. Keep 0061's closing assertion — every uid strictly increasing per
   target — true across it.
5. **The fleet configuration round trip** (proxy D18; 0019), asserted on what
   the real proxy **reports**: `applied` for a live setting, `pending_restart`
   naming a startup-only one and not shown caught up until the proxy
   restarts, a stopped proxy fetching on its next stream connect with no
   second publish, and a session opened mid-rollout succeeding. Values equal
   to the proxies' bootstrap values (`deploy/proxy/*.yaml`).
6. **Cache + revocation**: with the event stream unhealthy, this server
   issues no cache hint and the proxy re-authorizes every connection (M9).
   Assert it on the authorize records this server stores per connection, as
   0061's `countDecisions` does.

## Out of scope
- Anything 0061 already proves; its scenarios stay as they are.
- The relay-registration gap 0061 raised upstream (`deploy/README.md`, "What
  this topology cannot prove"): a contract change in `hoplock/proxy`, then the
  phase that re-vendors it.

## Acceptance criteria
- The six scenarios pass locally and in CI's `e2e` job, inside the suite's
  one ordered run, and the whole suite still passes.
- The fakes exist only in the e2e-tagged host binary; no listener this server
  binds gains a route, and `TestNoGoFileBindsADebugRoute` passes unchanged.
- 0061's closing assertions still hold after them: no uid issued twice, the
  standing accounts byte-identical, no `logging.gap` refused, `audit-verify`
  passing.

## Cross-repo
None owed unless a scenario finds a disagreement with the real proxy. If it
does, fix it here when this side is wrong (0061 fixed the showcase join that
way), or raise it under `## Upstream request` when the contract is
(`docs/CROSS-REPO-PROTOCOL.md` §3.2, §4.2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0072-e2e-scenarios-through-a-test-host-learnings.md`, whose
summary says how the suite drives the fakes and what each new scenario proves.
