# 0008 — South-bound authorize & route

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§5 (the whole section)**, §2 (M4, M5, M11), §4.
- `docs/learnings/` — read summaries; open `0005` (the engine's signature and
  output vocabulary), `0006` (`Path` and the no-path outcome), `0007` (the
  listener and the `Deny`-vs-`error` mechanism), `0003` (decision table).
- `contract/control.yaml` — `/v1/authorize` and every schema it references,
  including `ConnMeta.hop_trail`.
- In the **Hoplock Proxy repository**, `docs/PLAN.md` §6.1 ("Hop trail, loops,
  and the cap") — what the proxy does with the trail on its side, and why every
  entry in it can only cause a refusal.

## Objective
Serve the endpoint the whole system turns on. One call assembles the inputs,
evaluates policy, computes a route, writes an explanation, and returns a
**whole-connection snapshot** — while a user's SSH handshake is held open,
on every connection, per hop.

## In scope

### The composition root (`internal/decision`)
Assemble inputs in one place: identity and claims (0007), target and its labels
(0003), live grants (0012 owns them; the approval *workflow* is a Hoplock
Enterprise extension), connection metadata from
the request — **including `conn.hop_trail`, see below** — the current time, and
the fleet path (0006). Evaluate (0005). Build the snapshot. Persist the decision
record. Return.

### `conn.hop_trail` — the server's only view of a chain

`ConnMeta.hop_trail` has been in the contract since 0002; the proxy started
sending it with its own phase 0008 (`Hoplock/proxy#6`, merged). **Read it.** It
is the proxy ids the session has already travelled through, oldest first, and it
is empty on the user's first hop.

It is the *only* thing that tells this server it is being asked about the second
leg of a chain rather than a fresh connection. Every hop of a chain calls this
endpoint for itself, with the same identity and the same final target (proxy
D2), so without the trail two calls that mean entirely different things are
byte-identical apart from `conn.proxy_id`. Three consequences, and each is a
thing to build rather than a thing to note:

- **Route from where the caller actually is.** The path (0006) starts at
  `conn.proxy_id` — the proxy *asking* — and not at the user's entry proxy,
  which on a chained call is the first id in the trail and is somewhere else
  entirely. Take it from the request; never assume the two are the same. The
  same login and target legitimately answer `nexthop` at the edge and `direct`
  at the proxy behind it; the proxy's mock models exactly this by matching its
  routes on `proxy_id` too.
- **Refuse a loop, and refuse it as an outage.** If `conn.proxy_id` or the
  `next_proxy_id` you are about to answer with already appears in the trail, the
  fleet graph has produced a cycle. That is a fault in the estate's routing, not
  a decision about the user: `5xx` (M11), never `401`. The proxy enforces this
  too, as a safety net — M6's "this server should not give a bad answer" applies
  here, and 0006 already asks you to enforce the maximum path length on this
  side for the same reason.
- **Length is the hop count so far.** `len(hop_trail)` against the cap in force
  is how this server keeps its own answers inside `max_hops` instead of relying
  on the proxy to notice.

**The trail carries no authority, and must never be given any.** Every entry in
it can only cause a refusal, which is what makes it safe to accept from a
caller: a forged trail restricts the forger. So it may narrow a decision and it
may never widen one — never grant on the strength of a hop id in the trail, and
never let a shorter trail unlock something a longer one would not. The authority
on a chain leg is the previous hop's key, which 0007 authenticates.

Put the trail in the decision record (M4) as an input. "Which hop asked, and
what had it already been through" is the first question anyone debugging a
chained session asks, and it is unrecoverable afterwards if it was not stored.

### Snapshot assembly
Translate the engine's output into the contract's response, complete: route type
and target, permitted channels, in-channel requests, forwarding destinations,
global requests, filter policy (rule list **or** restricted exec, never both),
target credential method and parameters, hop metadata **including connection
direction**, `decision_id`, and — only when policy says so — a cache hint.

Anything the engine can express and the contract cannot carry is a **contract
gap**: stop and report it (PROTOCOL §3). Do not approximate it, and do not edit
`contract/`.

### Vocabulary negotiation (`policy_version`, PLAN §4)
The request carries `policy_version`: the highest policy vocabulary the calling
proxy implements (absent means `1`, the pre-v2 vocabulary — `permitted_channels`
and a filter rule list only). **Answer within it.** The proxy decodes this
response strictly and fails the session closed on a field it does not
understand, so a field sent outside the declared version is not ignored — it
takes the session down, as an outage rather than a deny.

That cuts both ways and both halves need building:
- **Never emit a field the declared version does not include.** Assembly is
  version-aware, in one place, not sprinkled through the mapping.
- **When policy needs a field the proxy cannot read, that is an outage, not a
  quiet downgrade.** Return `5xx` (M11) naming the version mismatch. Silently
  dropping the restriction and allowing the session is the one behaviour this
  rule exists to prevent: it turns a mid-upgrade fleet into a fleet enforcing
  less than its policy says, invisibly. Dropping a *permission* is merely wrong;
  dropping a *restriction* is a breach.

Hoplock Proxy's `cmd/mock-control` implements this and is the reference: read
its authorize handler if the intended behaviour is unclear.

### Cache hints (PLAN §5.4)
- Authored per rule, never global, never invented.
- The key selects the sharing scope and **must never be shared across
  identities**.
- **Never issue a hint to a proxy whose event stream is unhealthy** (M9): a
  cached allow that cannot be withdrawn is a grant with no revocation. This
  needs a liveness read from 0006/0009 on the issue path.

### Decision records (M4)
Every evaluation — allow and deny alike — writes a record: inputs, matched rule,
obligations, snapshot, timestamp, keyed by `decision_id`. This is the other half
of the proxy's deliberately vague "access denied": the user gets a session id,
and an operator resolves it here into the whole story. A deny with no stored
explanation makes that promise false, so **the deny path writes a record too** —
it is the path that matters most and the easiest one to forget.

### The latency budget (M5)
- A hard server-side deadline: answer, never hang. A timeout the proxy
  classifies as an outage beats a slow answer that looks like one.
- No unbounded work: compiled policy from memory, indexed reads only, one
  transaction.
- Record the decision **without** putting a synchronous write on the critical
  path if you can avoid it — but if you make it asynchronous, say what happens
  when the writer is behind or fails, because "we allowed it but cannot say why"
  is a worse failure than a slightly slower allow. State the trade-off you chose
  in your learnings.
- A benchmark, and a documented p99 target under a realistic bundle and fleet.

### Snapshot fields added by the privileged-access revision

`docs/PLAN.md` §5.2 lists the full snapshot; these are the ones that did not
exist when this prompt was first written, and each has a rule attached:

- **An ordered `target_auth` ladder** (proxy D14), not a single method. Authoring
  a ladder is a policy decision with real consequences — it states both a
  preference and what the deployment will accept instead — so a one-entry ladder
  must be as easy to express as a multi-entry one, and the engine must never
  synthesise a fallback the policy did not write.
- **Device platform and expiry posture** on `ephemeral-account` routes (proxy
  D13), constrained by the target's own attributes and by the proxy's declared
  capabilities (M17, 0006). Naming a platform the enforcing proxy has no driver
  for is a decision that cannot be served.
- **A per-route algorithm profile** where the target speaks something the
  proxy's SSH stack does not enable by default. This deliberately weakens a leg,
  so it is a policy choice with an audit consequence, never a default.
- **A session deadline**, as an absolute instant. Prefer the instant over a
  duration: a duration re-anchors at each hop of a chained route and silently
  multiplies the window.
- **Concurrency caps** per subject and/or target. This server cannot count live
  sessions — only the proxy can — so it states the ceiling and the proxy
  enforces it.
- **Grant context**: the system, the reference, and the window that justified
  this access, copied from the grant that supplied it (M10, M16). The proxy
  carries it opaquely into its records; this is what makes an audit trail able
  to answer "why was this allowed" without a human joining two systems by hand.
- **Required session recording** as an obligation the proxy refuses to serve
  without, on unbounded-privilege routes (proxy D16).

### The latency budget is not a formality

M5 now carries a magnitude: an estate where machine-to-machine health checking
runs through the proxy can put this endpoint into five figures of requests per
second. Two things follow for this phase. Cache hints stop being an optimisation
and become load-shedding, so 5.4's "issued deliberately" needs a deliberate
answer for the machine-identity pattern — one subject against very many targets,
where a hint keyed per (subject, target) has a hit rate near zero. And the
measured latency numbers this phase reports should be measured under fan-out,
not only under a single hot subject.

## Out of scope
- The revocation stream (0009) — read its liveness, do not implement it.
- The grant *approval workflow* (a Hoplock Enterprise extension via `ext`).
  Control ships manual, time-boxed grants (0012); read live grants as an input
  and do not care which of the two created them.
- Simulation and explain-a-decision APIs (0014) — write the records they read.

## Acceptance criteria
- The conformance suite's authorize assertions pass; `make conform` is green.
- End-to-end tests through the real listener and a real database: `direct`,
  `nexthop` (both connection directions), and `401`.
- **`conn.hop_trail` is read, not ignored:** one login and one target, asked
  twice — an empty trail from the edge proxy answers `nexthop`, and a trail
  naming that edge proxy, asked by the next proxy, answers `direct` for the same
  user and target. A test that only exercises the empty-trail call cannot tell a
  server that reads the trail from one that discards it.
- A trail that already contains the asking proxy, or the `next_proxy_id` about
  to be answered, returns a `5xx` outage — not a `401`, and not a route that
  closes the loop.
- A decision record for a chained call names the trail it was decided under.
- A deny writes a decision record naming the deciding rule — test it.
- **No path available** (an enclave relay down) returns a `5xx` outage, not a
  `401`. This is the M11 test that matters most in this phase, because "deny"
  is the tempting shortcut here.
- A route whose policy omits a cache hint returns none; one that sets it returns
  the server's TTL; and no key is ever shared across two subjects (test with two
  identities against one target).
- A proxy with an unhealthy event stream receives **no** cache hint even when
  policy grants one.
- **Vocabulary negotiation, both directions:** a request declaring the current
  version gets the full snapshot; a request declaring `1` (or omitting the
  field) against a policy that only needs the v1 vocabulary gets a valid v1
  response with none of the newer fields invented; and a request declaring `1`
  against a policy that **requires** a newer field gets a `5xx` naming the
  mismatch — never a thinned snapshot, and never a `401`.
- A benchmark reports p50/p99 against the stated budget, with the bundle and
  fleet size documented.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0008-southbound-authorize-and-route-learnings.md`. Summary block
MUST give the input assembly order (including where `conn.hop_trail` enters it
and how loops and the hop cap are refused), the snapshot mapping (engine field →
contract field), the cache-hint issuance rules, the decision-record shape and
whether its write is synchronous, and the measured latency numbers. Phase 0014's simulation
and explain features read those records; phase 0016 asserts this end to end
against a real proxy.
