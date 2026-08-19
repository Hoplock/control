# 0007 — South-bound authorize & route

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§5 (the whole section)**, §2 (M4, M5, M11), §4.
- `docs/learnings/` — read summaries; open `0004` (the engine's signature and
  output vocabulary), `0005` (`Path` and the no-path outcome), `0006` (the
  listener and the `Deny`-vs-`error` mechanism), `0003` (decision table).
- `contract/management.yaml` — `/v1/authorize` and every schema it references.

## Objective
Serve the endpoint the whole system turns on. One call assembles the inputs,
evaluates policy, computes a route, writes an explanation, and returns a
**whole-connection snapshot** — while a user's SSH handshake is held open,
on every connection, per hop.

## In scope

### The composition root (`internal/decision`)
Assemble inputs in one place: identity and claims (0006), target and its labels
(0003), live grants (table exists; 0012 owns the flow), connection metadata from
the request, the current time, and the fleet path (0005). Evaluate (0004). Build
the snapshot. Persist the decision record. Return.

### Snapshot assembly
Translate the engine's output into the contract's response, complete: route type
and target, permitted channels, in-channel requests, forwarding destinations,
global requests, filter policy (rule list **or** restricted exec, never both),
target credential method and parameters, hop metadata **including connection
direction**, `decision_id`, and — only when policy says so — a cache hint.

Anything the engine can express and the contract cannot carry is a **contract
gap**: stop and report it (PROTOCOL §3). Do not approximate it, and do not edit
`contract/`.

### Cache hints (PLAN §5.4)
- Authored per rule, never global, never invented.
- The key selects the sharing scope and **must never be shared across
  identities**.
- **Never issue a hint to a bastion whose event stream is unhealthy** (M9): a
  cached allow that cannot be withdrawn is a grant with no revocation. This
  needs a liveness read from 0005/0008 on the issue path.

### Decision records (M4)
Every evaluation — allow and deny alike — writes a record: inputs, matched rule,
obligations, snapshot, timestamp, keyed by `decision_id`. This is the other half
of the bastion's deliberately vague "access denied": the user gets a session id,
and an operator resolves it here into the whole story. A deny with no stored
explanation makes that promise false, so **the deny path writes a record too** —
it is the path that matters most and the easiest one to forget.

### The latency budget (M5)
- A hard server-side deadline: answer, never hang. A timeout the bastion
  classifies as an outage beats a slow answer that looks like one.
- No unbounded work: compiled policy from memory, indexed reads only, one
  transaction.
- Record the decision **without** putting a synchronous write on the critical
  path if you can avoid it — but if you make it asynchronous, say what happens
  when the writer is behind or fails, because "we allowed it but cannot say why"
  is a worse failure than a slightly slower allow. State the trade-off you chose
  in your learnings.
- A benchmark, and a documented p99 target under a realistic bundle and fleet.

## Out of scope
- The revocation stream (0008) — read its liveness, do not implement it.
- Grants' request/approval flow (0012) — read live grants as an input.
- Simulation and explain-a-decision APIs (0011) — write the records they read.

## Acceptance criteria
- The conformance suite's authorize assertions pass; `make conform` is green.
- End-to-end tests through the real listener and a real database: `direct`,
  `nexthop` (both connection directions), and `401`.
- A deny writes a decision record naming the deciding rule — test it.
- **No path available** (an enclave relay down) returns a `5xx` outage, not a
  `401`. This is the M11 test that matters most in this phase, because "deny"
  is the tempting shortcut here.
- A route whose policy omits a cache hint returns none; one that sets it returns
  the server's TTL; and no key is ever shared across two subjects (test with two
  identities against one target).
- A bastion with an unhealthy event stream receives **no** cache hint even when
  policy grants one.
- A benchmark reports p50/p99 against the stated budget, with the bundle and
  fleet size documented.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0007-southbound-authorize-and-route-learnings.md`. Summary block
MUST give the input assembly order, the snapshot mapping (engine field → contract
field), the cache-hint issuance rules, the decision-record shape and whether its
write is synchronous, and the measured latency numbers. Phase 0011's simulation
and explain features read those records; phase 0014 asserts this end to end
against a real bastion.
