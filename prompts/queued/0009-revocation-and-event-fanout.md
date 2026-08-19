# 0009 — Revocation & event fan-out

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M9)**, §4 (heartbeat obligation), §5.4 (a
  server that issues cache hints must serve this stream).
- `docs/learnings/` — read summaries; open `0006` (the liveness interface you
  now implement), `0008` (hint issuance reads stream health), `0002` (the
  conformance suite's event assertions).
- `contract/management.yaml` — `GET /v1/bastions/{bastion_id}/events`.
- In the **Hoplock Proxy repository**, `api/README.md` §"Revoking" and `docs/PLAN.md`
  §6.4 — the consumer's fail-closed behaviour is what this stream's guarantees
  are for.

## Objective
Serve the long-lived NDJSON revocation stream: the **only** route this server has
to a running proxy, the kill switch for a session already in flight, and the
thing that bounds the damage of every cache hint issued in 0008.

## In scope

### The stream (`internal/revoke`, `internal/httpapi/south`)
- One long-lived response per subscribed proxy, one `RevocationEvent` per
  line, flushed immediately — a buffered proxy or an unflushed writer turns a
  kill switch into a delayed one.
- Event types per the contract: `session_kill`, `cache_invalidate`,
  `heartbeat`, `resync`.
- **Heartbeats within the advertised interval** (PLAN §4). A proxy that stops
  hearing them reconnects and, past its staleness threshold, stops serving
  cached decisions entirely — so a stalled heartbeat writer silently degrades
  the whole fleet.
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
Enough to publish an event: an internal API used by 0013's north-bound surface
and by tests. `session_kill` carries a `reason` that is **shown to the user**
before their connection closes, so it must be safe to disclose; validate that it
is present and reject an empty one. A revoked session that looks like a crash is
the failure this field exists to prevent.

### Liveness
Implement 0006's liveness interface from subscription state, and expose the
health signal 0008 reads before issuing a cache hint.

## Out of scope
- The north-bound API surface itself (0013) and the audit record of an operator
  action (0010 stores it).
- Multi-node fan-out.

## Acceptance criteria
- The conformance suite's event assertions pass, including reconnect-with-
  `last_event_id` yielding replay **or** `resync` with nothing skipped.
- A subscriber receives heartbeats within the advertised interval, measured.
- Kill by session id, by subject, and by "all" each reach exactly the intended
  subscribers — assert with three concurrent subscribers.
- `cache_invalidate` by key, by subject, and by all, likewise.
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
provide.
