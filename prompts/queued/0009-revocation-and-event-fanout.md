# 0009 — Revocation & event fan-out

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M9)**, §4 (heartbeat obligation), §5.4 (a
  server that issues cache hints must serve this stream).
- `docs/learnings/` — read summaries; open `0006` (the liveness interface you
  now implement), `0008` (hint issuance reads stream health), `0002` (the
  conformance suite's event assertions).
- **Read this first, before anything else here.** Upstream `Hoplock/proxy#56`
  (merged, `7c2a356`) put the heartbeat interval **on the wire**:
  `RevocationEvent.heartbeat_interval_seconds` is the interval the server says
  it is keeping now. Until that lands here the contract is vendored at an older
  commit and the conformance suite grades a number a human typed into its
  expectation file rather than a claim this server made — so this phase begins
  by re-vendoring (below) and ends up graded against its own advertisement.
  The same change also states plainly that **nothing on `/v1` publishes an
  event**, which is what the "Operator surface" section below now owes.
- `contract/control.yaml` — `GET /v1/proxies/{proxy_id}/events`. Read it
  **after** the re-vendor below, not before: the field this phase serves is not
  in the copy currently on disk.
- In the **Hoplock Proxy repository**, `api/README.md` §"Revoking" — including
  "The interval the server is keeping", the three rules that come with the
  field — and `docs/PLAN.md` §6.4 — the consumer's fail-closed behaviour is what
  this stream's guarantees are for.

## Objective
Serve the long-lived NDJSON revocation stream: the **only** route this server has
to a running proxy, the kill switch for a session already in flight, and the
thing that bounds the damage of every cache hint issued in 0008 — and in 0007.

## In scope

### First: re-vendor the contract, then land the field (upstream #56)

Three steps, in this order. None of them is optional and the first is not a
formality — the other two cannot be written against a document that lacks the
field, and writing them anyway would be inventing an upstream shape
(`docs/CROSS-REPO-PROTOCOL.md` §6).

1. **Re-vendor.** `make contract-sync REF=7c2a356d892105911213062f99781f8a2f8326bc`
   — the merge commit of `Hoplock/proxy#56`. That rewrites `contract/control.yaml`
   and `contract/UPSTREAM` together; both are generated output and neither is
   ever edited by hand (M1, PROTOCOL §3). Expect the vendored `info.version` to
   move `4.0.0` → `4.1.0` while `policy_version` stands still at `4`: the field
   is on the event stream, not on `/v1/authorize`, and only the latter is
   governed by the number. Nothing in the tree pins `4.0.0` today —
   `TestTheTwoNumbersAreTwoNumbers` asserts only that both numbers are present
   and legible, and `contract-check` keys off the SHA-256 and never reads inside
   the file — so expect the re-vendor to be quiet. If something *does* fail on
   the version, that check is doing its job: update it deliberately rather than
   loosening it, and see 0018.
2. **`RevocationEvent.HeartbeatIntervalSeconds`** in `internal/contract`
   (`types.go`): `int32`, `json:"heartbeat_interval_seconds,omitempty"`. The
   server sets it on `heartbeat` events, **may** set it on any event, and a
   reader takes it wherever it appears — a later event carrying a different
   value is the server re-stating the interval it keeps now, not contradicting
   an earlier claim.
3. **Its absent-value resolver**, in `internal/contract/resolve.go` beside the
   others, because nothing in this repository reads an absent-value field
   directly. Absent means **what every server did before the field existed** —
   the reader falls back to its own timers — so the resolver returns the value
   **and a boolean**, exactly as `Deadline()` does, rather than a zero a caller
   can misread as "no interval". Upstream's own is
   `control.AdvertisedHeartbeatInterval`; match its semantics, not necessarily
   its name.

Two rules travel with the field and are worth more than the field is. Put them
in the doc comment, not only in your learnings:

- **It advertises, it does not configure.** Same absent-value discipline as
  `HostKeyReportResponse.cache`, and for the same reason.
- **It may only ever tighten detection, never loosen it.** A reader may use it
  to notice a dead stream *sooner* than its configured timeout; it must never
  extend that timeout to accommodate a large advertised interval. Sooner is
  always allowed, later is not — the same rule as `cache.ttl_seconds` (clamp
  shorter, never longer) and `report_after_seconds` (re-observe sooner, never
  later). Otherwise a broken or hostile server silences itself indefinitely by
  announcing that it intends to, which is PLAN §6.4's fail-closed rule inverted.

### The conformance suite grades the claim, not the configuration (0002)

`cmd/pdpconform`'s heartbeat case currently reads its bound out of the
expectation file, because when 0002 built it the contract had no field to read.
That was recorded as an ambiguity and upstream #56 answered it, so the case
changes with this phase:

- **Read the advertised interval off the stream.** Take
  `heartbeat_interval_seconds` from the events the subscription actually
  delivers, through the resolver above, and grade the server against that.
- **Fall back to `events.heartbeat_interval_seconds` only when the server
  advertises nothing.** Absent is a legal answer — it means what every server
  did before the field existed — so a server that stays silent is still
  gradeable, from the file, exactly as today. The key therefore stops being
  mandatory and becomes a fallback: `expectations.go` currently rejects a
  non-positive value outright, and that validation has to move with it. Do not
  delete the key; a server advertising nothing with no fallback configured is
  ungradeable, and a vacuous pass is worse than a failure.
- **Assert both halves.** That heartbeats arrive within the advertised interval
  **and** that the advertised interval is within the 10s ceiling. The second is
  the one that catches a server advertising 600s and honestly keeping to it —
  which passes the first assertion while breaking every proxy in the fleet.

**This is contract-level, not a fact about this server**, so it belongs in the
shared assertions rather than in this server's expectation file — CI runs the
same suite against Hoplock Proxy's `cmd/mock-control`, which advertises an
interval derived from its own `heartbeat_ms` (rounded up, so it never claims one
it does not keep) and must keep passing. Getting this layer wrong is how the
mock starts failing for no reason anyone can see; 0018 says more about the two
layers.

### The stream (`internal/revoke`, `internal/httpapi/south`)
- One long-lived response per subscribed proxy, one `RevocationEvent` per
  line, flushed immediately — a buffered proxy or an unflushed writer turns a
  kill switch into a delayed one.
- Event types per the contract: `session_kill`, `cache_invalidate`,
  `heartbeat`, `resync`.
- **A subject-scoped `cache_invalidate` does not reach a host-key decision.**
  0007 may hint the host-key report as cacheable, and the proxy keys those
  entries on target, port and key
  fingerprint — not on a person — so `subject` cannot match one. Withdrawing a
  host-key decision means publishing that decision's own `key`, or `resync`.
  This is an asymmetry in the operator surface below, not a detail of the wire
  format: an operator who publishes "invalidate everything for Alice" and
  believes a target's host key was withdrawn by it has been misled by this
  server, and a revocation that silently misses is worse than one that refuses.
- **Heartbeats within the interval this server advertises, and that interval
  within the ceiling** (PLAN §4). Since upstream #56 those are **two
  obligations, not one**, and meeting either alone is a failure:
  - every `heartbeat` carries `heartbeat_interval_seconds`, and the stream
    actually keeps the interval it names. Derive the advertised number from the
    timer that drives the writer rather than configuring the two separately —
    round **up** to the whole second if you must, so this server never claims an
    interval it does not keep. Two numbers that can drift apart will.
  - that interval is **10 seconds or less** (upstream
    `control.MaxHeartbeatIntervalSeconds`), so two consecutive intervals fit
    inside the proxy's 20s reconnect timeout and one lost heartbeat is not
    mistaken for a dead stream. A server that advertises 600s and then keeps to
    it passes its own claim and breaks every proxy in the fleet.

  A proxy that stops hearing heartbeats reconnects and, past its staleness
  threshold, stops serving cached decisions entirely — so a stalled heartbeat
  writer silently degrades the whole fleet. Where the interval is configured,
  and what stops it being configured above the ceiling, belongs in your
  learnings.
- Clean shutdown: drain and close subscriptions rather than dropping them, so a
  deploy is a reconnect and not a fleet-wide cache flush.

### Fan-out & delivery
- An event bus behind an interface, in-process for the prototype (M9). Say in
  your learnings exactly what a multi-node implementation would have to provide
  — this is the one component whose single-node assumption is load-bearing.
- Address events to one proxy, to a set, or to all; and by subject, so "end
  every session for Alice" does not require the operator to know where she is.
- **Ordered per subscriber**, with monotonic `event_id`s.
- A **replay buffer**: a reconnecting subscriber sends `?last_event_id=` and
  either gets everything after it, or — when the id is too old, unknown, or no
  history is kept — a `resync` as the **first** line and nothing older. Silently
  skipping events is the one behaviour that is worse than both.
- Backpressure: a slow subscriber must not stall the bus or grow memory without
  bound. Decide what happens when its buffer fills — the honest answer is
  usually to drop it and let it reconnect into `resync` — and document it.

### Operator surface (minimal here)
Enough to publish an event: an internal API used by 0014's north-bound surface
and by tests.

**It also needs an HTTP publish path, and that is now the contract's own
answer rather than a gap.** Upstream #56 states outright that **nothing on
`/v1` publishes an event** — an event originates from an operator action on a
surface the contract does not describe, and an operator action is not
proxy-facing, so putting one on `/v1` would make every Hoplock Control implement
an API no proxy calls. The consequence lands here: gap recovery is only
gradeable if the suite can make this server emit an event **while a subscriber
is away**, so this phase must expose a publish path *outside* `/v1` and point
`events.publish_url`/`events.publish_body` at it. Hoplock Proxy's
`cmd/mock-control` `POST /debug/revoke` is the reference shape; the suite
asserts nothing about the shape, only that it works. Do not invent a `/v1`
endpoint for this — that would be a contract change, which is
`docs/CROSS-REPO-PROTOCOL.md` §3.2 and not this phase's to make.

`session_kill` carries a `reason` that is **shown to the user**
before their connection closes, so it must be safe to disclose; validate that it
is present and reject an empty one. A revoked session that looks like a crash is
the failure this field exists to prevent.

Make withdrawing a **host-key** decision expressible here too — by the key 0007
stored on the host-key record, or by `resync` — and do not offer `subject` as
though it covered everything the cache holds. Whatever 0014 puts in front of
this inherits the shape you choose.

### Liveness
Implement 0006's liveness interface from subscription state, and expose the
health signal 0008 reads before issuing a cache hint.

## Out of scope
- The north-bound API surface itself (0014) and the audit record of an operator
  action (0010 stores it).
- Multi-node fan-out.

## Acceptance criteria
- `contract/UPSTREAM` records `7c2a356d892105911213062f99781f8a2f8326bc`,
  `make contract-check` passes, and no file under `contract/` was hand-edited.
- The conformance suite's event assertions pass, including reconnect-with-
  `last_event_id` yielding replay **or** `resync` with nothing skipped.
- **Both halves of the heartbeat obligation, separately tested.** Heartbeats
  arrive within the interval this server advertises on the stream, measured; and
  the advertised interval is within the 10s ceiling. Prove the first fails when
  the writer stalls and the second fails when the advertised number is raised
  past the ceiling — a pair of assertions that cannot fail is a pair that grades
  nothing.
- The absent-value resolver is tested for **absent** as well as present, reading
  the raw JSON rather than the decoded struct: a decoded `RevocationEvent`
  cannot tell an omitted key from `0`, and those are the two readings that
  differ.
- The suite can make this server publish an event through
  `events.publish_url` while a subscriber is detached, and the gap-recovery case
  grades a real gap rather than an idle stream.
- Kill by session id, by subject, and by "all" each reach exactly the intended
  subscribers — assert with three concurrent subscribers.
- `cache_invalidate` by key, by subject, and by all, likewise.
- A host-key decision hinted by 0007 is withdrawn by publishing its stored
  `key`, end to end; the same withdrawal addressed by `subject` is not silently
  reported as having covered it.
- A slow subscriber does not stall other subscribers or grow memory without
  bound; its documented fate is tested.
- Restarting the server drains subscriptions cleanly and they reconnect.
- An empty `session_kill` reason is rejected.
- 0008's rule holds end to end: with a proxy's stream unhealthy, an authorize
  for that proxy returns no cache hint.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0009-revocation-and-event-fanout-learnings.md`. Summary block MUST
give the bus interface, the event id and replay semantics, the buffer sizing and
slow-subscriber policy, the heartbeat interval and where it is configured, the
liveness signal 0008 reads, and precisely what a multi-node implementation must
provide. It MUST also record, because the next contract sync and phase 0018 both
depend on them: the commit `contract/` now sits at and that `info.version` moved
to `4.1.0` while `policy_version` did not move; the resolver's name and its
absent-value contract; and the publish path outside `/v1` that the conformance
suite drives, since that path is an input to every later run and is nowhere in
the contract.
