# 0008 — south-bound authorize & route — Learnings

## Summary
- **What shipped:** `/v1/authorize` end to end — `internal/decision` (the
  composition root), the snapshot mapping, routing, the refusals, decision
  records and the cache-hint gate; conformance 28/28 against this server.
- **Key files:** `internal/decision/*.go`, `internal/fleet/cachehint.go`,
  `internal/httpapi/south/{handlers,server,errors}.go`, migration
  `0004_decision_records.sql`, `cmd/pdpconform/testdata/control-policy.yaml`.
- **Order:** validate → compiled program (cached) → assemble inputs → evaluate →
  route → assemble snapshot → serveability + capability checks → cache hint →
  version gate → write record → answer. Subject groups/claims come from THIS
  server's store, never from the request; an unknown subject gets neither.
- **`conn.hop_trail` enters at ROUTING, never the engine.** The path starts at
  `conn.proxy_id` (`fleet.EntryPoint`); a repeat (asking proxy in its own trail,
  or the next hop already in it) and `len(trail)` past `max_hops` are
  `*RouteError` → **5xx, never 401**. The response's `hop.hop_trail` is
  `fleet.Trail(entry, nil)` = trail + this proxy (Details: not what 0006 said).
- **Mapping (`snapshot.go`)** is name-for-name except: `Intent` + the fleet path
  → `route_type`/`target`/`target_port`/`hop`; `Credentials` →
  `target_auth_ladder` with `username` and the method params in `params` and
  device fields under `device_field.`; `SessionDeadline` (instant) → RFC 3339;
  `Enforcement` emitted only when stated; `Concurrency` omitted when both zero;
  `RequireSessionCapture` only when true; `AlgorithmProfile` only when not
  `default`; `GrantContext` only when it says something; `Rule` → record only.
- **Cache hints:** per rule; key = SHA-256 over tenant + the named components;
  TTL clamped down to 5m; **withheld unless the proxy holds a live event
  subscription (M9)** — so none is issued until 0009 wires `SubscriptionState`.
- **Decision record:** `decisions` gains `inputs`, `explanation`, `effect`
  (`allow|deny|unserved`), `proxy_id`, `session_id`, plus a session index. The
  write is **SYNCHRONOUS** and a failed write is a 5xx — Details says why.
- **Latency:** p50 **0.99 ms**, p99 **2.38 ms** over 300 calls, 50 rules, 200
  targets, 20 proxies (target p99 25 ms, hard budget 2 s).
- **Decisions:** none added/amended/withdrawn; the §2 register is unchanged.
  **PLAN §5.4 revised in place** (the hint issue path is 0008's; 0009 turns it
  on by wiring the stream) and §3's `internal/decision` entry revised.
- **NEXT session:** 0009 wiring `SubscriptionState` turns authorize hints ON
  with no other change, and owes the host-key path the same call.

## Details

### The vocabulary gate is a baseline, and the proxy's mock is the reference

The prompt asks for "a valid v1 response with none of the newer fields
invented", which reads as if a field→version table existed. It does not, and
cannot: upstream `Hoplock/proxy#53` deleted the superseded vocabularies and the
whole revision history, so the document states ONE vocabulary in the present
tense with no "since version N" annotation on any field. Inventing a table here
would be inventing history.

What the reference implementation does instead is the whole answer, and
`vocabulary.go` mirrors it field for field: `cmd/mock-control`'s
`vocabularyVersion` returns `control.PolicyVersion` for every response it can
build, with a documented extension point where the next revision tiers its own
fields ABOVE the baseline. So `requiredVersion(resp)` returns
`contract.PolicyVersion`, the gate compares it against what the caller declared,
and a proxy declaring less gets a `5xx` naming both numbers. The conformance
suite grades that as a pass (its version case accepts a 5xx or an unthinned
200), and the gate is answered **per response** rather than per build so that
the next revision makes the distinction bite again without a rewrite.

The device-field namespace is deliberately not a case in that function, and
there is a comment at the function saying so. It is an open namespace: a name
inside it is not a new policy field, and whether a driver accepts one is a
capability question answered from the fleet registry.

### The record write is synchronous, and that is the trade

M5 asks for the record to be written without a synchronous write on the critical
path "if you can avoid it". This phase did not avoid it, deliberately.

An asynchronous writer buys a fraction of a millisecond and costs the guarantee
the record exists for: under load — exactly when a queue is behind — the
decisions an operator most needs to explain are the ones missing. "We allowed it
but cannot say why" is a worse failure than a slightly slower allow, and M4's
promise to the auditor is only worth anything if it is total.

So: one indexed INSERT inside the request, and **a failed write is an outage on
both paths**. A deny that could not be recorded answers `5xx` rather than `401`
— the user's experience would be the same either way, but the operator's would
not, and M11 is not bent by it because nothing here turns an outage into a
decision. The measured cost is visible in the benchmark: the whole call is
~1 ms and the write is most of it.

The one exception is the **unserved** path. An evaluation that allowed and could
not be answered records `effect: unserved` with the reason in the explanation,
and that write is best effort: the caller is already getting an outage, and
failing to record why must not replace the reason they are getting one.

### `fleet.Trail` takes the hops the session has traversed, which is none

0006's `Trail(entry, hops)` appends every hop's `NextProxyID`, and its doc
comment argued the trail "must name every proxy in the chain". The contract says
otherwise for this field: `hop.hop_trail` is "the hop trail to forward to the
next proxy, **this proxy appended**". The two readings differ by the proxies
further along the computed path, which have not been traversed.

The contract wins for wire shapes (PROTOCOL §9), and the reading matters: the
proxy builds its own outgoing trail as `incoming + self` and refuses a chain
where `next_proxy_id` is already in it (`routing.PlanHop`, proxy
`internal/routing/hop.go`). A trail that ran ahead of the session would have the
next proxy find itself in its own incoming trail and refuse the leg as a loop.
So `route.go` calls `fleet.Trail(entry, nil)` and 0006's doc comment was
corrected to say what to pass. The behaviour of `Trail` is unchanged.

### Where the capability checks read from, and why the two sources differ

Two axes, two sources, and they are not interchangeable:

- **Rungs** are checked against `AuthorizeRequest.capabilities` — the
  declaration the proxy makes ON THIS CALL, which is the one that cannot be
  stale. Absent declares nothing, so no rung beyond the two defaults is emitted;
  a route naming one is refused (`*CapabilityError`, an outage). The TARGET's
  half is `fleet.TargetRungs`, where stale, undated and absent are one case that
  leaves the two proxy-side defaults and an attested rung available.
- **Platforms and device fields** are checked against the fleet registry's
  STORED declaration, which `internal/fleet` documents as a readiness signal
  rather than authority. So it constrains only what it actually declares: a
  proxy that has declared nothing constrains nothing, because refusing every
  `ephemeral-account` route on a fleet that has not reported yet is a bootstrap
  that cannot start.

This asymmetry is why the conformance suite now sends `capabilities` on every
authorize request, declaring the whole rung vocabulary. A suite that sent none
could not grade a server that honours the field: the `enforcement` default pair
would pass vacuously against a server that cannot express a rung at all. What
the suite grades is still the envelope, never which rungs an estate can take.

### An unknown target is an outage, and an unknown subject is a deny

Two "we have no record of this" cases that answer differently, on purpose.

A subject with no row contributes no groups and no claims — the request's copy
is a value the caller controls, and a group read off the wire is a group a
compromised proxy can award itself. In practice the default-deny fires, which is
a **decision**: policy was evaluated and nothing matched.

A target with no row has no zone, and a zone this server cannot name is a
destination it cannot route to. That is an **outage** (`RouteError{Reason:
"target-unknown"}`), because picking the asking proxy's own zone would answer
`direct` for every hostname anybody typed. In practice policy denies such a
target first — the conformance suite's deny case depends on exactly that, and
the fixture policy is written so every rule names its targets.

### `intent: direct` against a chained target is a DENY

The one refusal in the routing layer that is not an outage. If a rule authored
`intent: direct` and the estate says the target needs a hop, nothing is broken:
the path exists, the estate is healthy, and the answer is "no" because somebody
wrote the rule that way. Same class as exceeding a concurrency cap, opposite
class from a relay being down. It is recorded as `effect: deny` with the reason.

### Holding the program and the graph

`programCache` and `graphCache` both re-read on a refresh interval
(`decision.refresh`, default 5s) and both **serve what they hold when the read
fails**. That is deliberate: the material in hand was compiled from a bundle
that really was active and built from rows that really existed, so serving it
while the database is unreachable keeps sessions being decided rather than
turning a replica failover into an estate-wide outage. A bundle that is
DEACTIVATED is forgotten rather than served, because that is an operator's
decision and not a failure.

What the staleness costs is bounded and worth stating: a proxy that dies is
routable for up to the refresh interval after this server could have known,
which is seconds on top of a heartbeat TTL already measured in tens of them.

### Two south-bound details that are not the decision layer's

- `south.Options.Decision` is now **required**, like Identity and Fleet: a
  listener that authenticates everybody and can decide nothing holds every
  handshake in the estate open to answer `5xx`.
- A **bound** proxy token may not authorize in another proxy's name (`401`,
  the same refusal `/v1/uids/lease` makes). The route is computed FROM the
  asking proxy, so a credential that could name anybody could ask for a route it
  does not sit on. The conformance suite's token is unbound, which is why its
  cases are unaffected.

### The fixture set is three files, not two

`control-seed.yaml` (identities, fleet, targets), `control-policy.yaml` (the
bundle), `control-expectations.yaml` (what the suite asserts). A route named in
the third with no rule in the second answers a `401` that looks like a graded
deny; a rule in the second whose target is not in the first answers an outage
nobody can see in any of them. Change them together — each file says so at the
top. `hoplock-control seed` now also writes targets, proxy edges, relay
registrations and the policy bundle, and it **compiles the bundle before storing
it**: a bundle that cannot compile is a server with no policy, which is an
outage, and finding that out at seed time costs an error message instead of an
estate.

### Follow-ups this phase did not do

- **0009** wires `SubscriptionState` and turns hints on for both responses, and
  owes the host-key path a call to `fleet.Registry.CacheHint` rather than a
  second copy of the M9 read.
- **0012/0013** widen what a grant carries. `liveGrants` maps only what the
  `grants` columns hold today — scope name, origin, expiry, `external_ref` as a
  bare reference — so `grant_context.system` and the asserted window are empty
  until those phases add the columns. Nothing is guessed.
- **Device posture** has no field on `AuthorizeRequest`, so `model.Input.Device`
  is always nil and a rule requiring posture cannot match. That is the fail-safe
  direction; the axis arrives with the integration that can assert it (M16).
- **No contract gap was found.** Everything the engine can express has a place
  in `AuthorizeResponse`, so there is no `## Upstream request` in this PR.
