# 0015 — Cross-repo E2E topology, CI gate & hardening

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§9 (test topology)** and the learnings summaries
  from **all** prior phases.
- `docs/learnings/` — read every summary; open the ones whose setup you must
  wire (esp. `0002` conformance, `0006` fleet enrollment, `0008` authorize,
  `0009` events, `0010` audit assertions, `0012` grants).
- In the **Hoplock Proxy repository**: its `deploy/` topology and its own e2e phase.
  You are extending that idea across the repository boundary, not rebuilding it.

## Objective
Prove the thing neither repository can prove alone: a **real proxy** enforcing
decisions from a **real PDP**. The proxy's own e2e proves it enforces what a
mock tells it; this repo's unit and conformance tests prove this server decides
correctly in isolation. Only here do "decides" and "enforces" meet — and that
seam is where a product like this actually breaks.

Then close the prototype out: supply-chain gate, hardening, and an honest list of
what is left.

## In scope

### The topology (`deploy/`)
`docker compose`, on a shared network, in one CI runner:

1. **postgres** — stock image, migrated at start-up by an explicit step.
2. **management** — `cmd/hoplock-control`, both listeners, seeded with a policy
   bundle, targets and labels, identities, and fleet enrollment.
3. **proxy** — built from the **Hoplock Proxy repository**, configured to point at
   this server. Pin the proxy revision explicitly and record it, so a failure
   is attributable to a change on one side or the other.
4. **proxy-relay** — a second proxy in a "protected zone", reachable **only**
   by its own outbound relay registration (proxy D11). Enforce the isolation in
   the compose network so the scenario fails if anything else carried the
   session.
5. **target** — `sshd` with the provisioning account for `ephemeral-user` and a
   plain pre-existing account standing in for an appliance (`brokered-key`).
6. **client** — runs the scenario SSH clients.

### Scenario suite
Each scenario is a product claim, proven across both components:

- **Direct route**: exec and interactive shell succeed; the audit store holds a
  replayable record and a decision record that explains the allow.
- **Multi-hop through the relay zone**: succeeds, with **no inbound rule** into
  the protected zone, and each hop's leg and direction visible in the audit
  trail.
- **Denied**: policy denies; the user sees the deliberately vague message; the
  operator resolves the session id through `explain` to the exact rule (M4).
  This pair — vague to the user, total to the auditor — is the disclosure story
  end to end and it is the scenario worth demoing.
- **Outage is not a deny**: stop this server; a connecting user is told it is an
  outage and given a session id, not "access denied" (M11, proxy §4.3).
- **The three policy axes**: `sftp` denied while `shell` succeeds; a tunnel to
  the permitted host:port succeeds while another is refused; `tcpip-forward`
  refused with no listener created.
- **Both filtering tiers**: a filtered-exec policy lets `sh -c '<denied>'`
  through (the guardrail's honest limit) while restricted exec denies it. The
  audit record names which tier decided.
- **Both credential methods**: `ephemeral-user` creates and removes the target
  user; `brokered-key` leaves the appliance-like target unmodified.
- **Revocation**: an operator kills a live session; the user is told why, and it
  ends. A cached decision is invalidated and the next connection is re-decided.
- **Cache + revocation interaction**: with the event stream unhealthy, this
  server issues no hint and the proxy re-authorizes every connection.
- **JIT**: a denied user requests access, an approver approves, the next
  connection succeeds, and after expiry it is denied again.
- **Audit**: the showcase query returns the blocked commands with the identity,
  route, decision, and grant behind them.

### CI
- A job that builds both images, brings the topology up, runs the suite, tears
  it down, and gates PRs. Keep it fast enough that people don't route around it;
  say what it costs.
- **`govulncheck ./...`** failing the build on any finding, with the same
  reasoning the Hoplock Proxy repository records: this server holds credentials, terminates
  TLS, and talks to a database, so "are our dependencies currently vulnerable?"
  is a CI answer, not a periodic chore. Note in the job that it can go red with
  no code change when a new advisory lands, and that the fix is an upgrade or a
  dated justification — never deleting the job.
- Verify the gate once by pinning a known-vulnerable dependency and confirming
  it fails, then revert. A gate nobody has seen fail is not known to be a gate.
- Keep `make conform` in CI against both this server and the proxy's mock.

### Hardening & close-out
- Address TODOs from earlier phases that block a coherent prototype.
- Tidy config, document every knob, and make `README.md` and `docs/PLAN.md`
  reflect the built system, including how to run the topology locally.
- Write the honest **known-gaps** list to seed the next set of prompts:
  multi-node event fan-out, HA, external anchoring for the audit chain, device
  posture, a UI, real scale and geo testing.

## Out of scope
- Real geo/anycast and scale testing (needs real infrastructure).
- Performance tuning beyond the budgets already stated (M5, 0008).

## Acceptance criteria
- The topology comes up cleanly and the full suite passes locally and in CI.
- The protected zone accepts **no** inbound connections and multi-hop still
  works — assert the network restriction, not just the successful session.
- The deny scenario resolves end to end: the user's vague message, the session
  id, and `explain` naming the exact rule.
- The outage scenario proves a stopped server is never reported as a denial.
- No ephemeral users or keys leak after the suite; the appliance-like target is
  byte-identical afterwards.
- `govulncheck` gates every PR, passes on the tree, and has been seen to fail on
  a known-vulnerable input.
- The pinned proxy revision is recorded, and the docs say how to move it.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0015-e2e-topology-and-ci-learnings.md`. Summary block MUST
document how to run the topology locally, the seed/fixture layout, each scenario
and what it proves, how the proxy revision is pinned and bumped, how the
`govulncheck` gate is wired and what to do when it goes red without a code
change, and the known-gaps list.
