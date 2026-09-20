# 0009 — revocation & event fan-out — Learnings

## Summary
- **What shipped:** `GET /v1/proxies/{proxy_id}/events` — the NDJSON stream,
  `internal/revoke` (bus, replay, resync, heartbeats, backpressure), 0006's
  liveness interface, the operator publish surface, and the cache hints this
  stream turns on. **36/36 conformance against this server**, 39/39 vs the mock.
- **Key files:** `internal/revoke/{bus,subscribe,event,operator}.go`,
  `internal/httpapi/south/events.go`, `internal/fleet/hostkey.go`,
  `cmd/hoplock-control/publish.go`, `cmd/pdpconform/checks_events.go`, migration
  `0005_host_key_cache_keys.sql` (`target_host_keys.cache_key`).
- **Contract:** re-vendored at `hoplock/proxy@7c2a356`. `info.version`
  **4.0.0 → 4.1.0**; `policy_version` **did not move** (still `4`) — the field
  is on the event stream, not `/v1/authorize`. Nothing pinned `4.0.0` (0018
  will), so the re-vendor was quiet.
- **Resolver:** `(*contract.RevocationEvent).AdvertisedHeartbeatInterval()
  (time.Duration, bool)`. Absent ⇒ `(0, false)` ⇒ **the reader keeps its own
  timers**; it may tighten detection, never loosen it. Never read the field
  itself. Plus `contract.MaxHeartbeatInterval{,Seconds}` (10s), `MediaTypeNDJSON`.
- **Bus:** `Subscribe(ctx, tenant, proxyID, lastEventID, emit)`;
  `Kill|Invalidate|Resync(ctx, tenant, Audience, …) (Receipt, error)`;
  `LiveSubscriptions` = `fleet.SubscriptionState`, **the signal 0008's hint gate
  reads**; `Close(ctx)` drains. `revoke.Operator` adds `WithdrawHostKey`.
- **Ids/replay:** `evt-<runID>-<12-digit seq>`, one monotonic sequence including
  heartbeats; run id per PROCESS, so an older run's id ⇒ `resync`. Ring 1024
  (`events.replay_buffer`); heartbeats not retained, resyncs are.
- **Slow subscriber:** queue 256 (`events.subscriber_queue`); full ⇒ **dropped**
  into replay-or-resync. Never blocks the bus, never grows.
- **Heartbeat:** `events.heartbeat_interval`, default 5s — ONE value, kept and
  advertised (rounded up). Config **refuses** anything advertising past 10s.
- **Multi-node must provide** (M9's load-bearing assumption): a cluster-total
  sequence, a shared durable replay log with a known floor, cross-node fan-out,
  cluster-wide subscription state, replay-or-resync across failover. Details
  says what each prevents.
- **Publish path** — an input to every later conformance run, and nowhere in the
  contract: `POST /debug/revoke` on `events.publish_listener`, own port, own
  `events.publish_token`, **unbound unless configured**. It is **deleted by
  0014**, not inherited: `docs/PROTOCOL.md` §3 (added here) permits a debug
  endpoint only against a named successor whose prompt carries the removal, and
  0014's now lists every file, key and CI line.
- **Decisions:** M9 implemented. M2 **not amended** — its register row stays
  `live`; the publish listener is recorded inside it as the operator surface's
  temporary front door. §5.4 hints flow; §4 both heartbeat halves.
- **NEXT session:** cache hints are ON wherever the asking proxy holds a live
  subscription. 0010 owns the audit record of an operator action (logged only
  here), and owes `logs.read_url` the same PROTOCOL §3 treatment this phase gave
  `events.publish_url`; 0014 deletes `cmd/hoplock-control/publish.go`.

## Details

### The re-vendor, and what it did not disturb

`make contract-sync REF=7c2a356d892105911213062f99781f8a2f8326bc` rewrote
`contract/control.yaml` and `contract/UPSTREAM` together. The diff is 61 lines:
the document version, the endpoint's heartbeat prose, the "nothing on this API
publishes an event" paragraph, and the new field.

`TestTheTwoNumbersAreTwoNumbers` asserts only that both numbers are present and
legible, and `contract-check` keys off the SHA-256 without reading inside the
file, so nothing failed on the version — as 0009's prompt predicted. **0018 is
the phase that ties a constant to the document**, and after it a re-vendor will
be loud rather than quiet; that is the point of it.

### The field, and the two rules that matter more than the field

`HeartbeatIntervalSeconds` is `int32`, `omitempty`. The doc comment on the field
carries both rules verbatim, because they are what a later reader needs and the
field itself is obvious:

- **it advertises, it does not configure** — same discipline as
  `HostKeyReportResponse.cache`;
- **it may only ever tighten detection, never loosen it** — a reader may notice
  a dead stream sooner than its configured timeout, never later. The inverse
  lets a broken or hostile server silence itself by announcing that it intends
  to.

`AdvertisedHeartbeatInterval` returns `(value, bool)` exactly as `Deadline()`
does. `resolve_test.go` grades it from **raw JSON**, which is the only way to
tell an omitted key from `0`: a decoded struct cannot, and those are precisely
the two readings that differ.

### The bus: what is load-bearing and what is not

**Ordering is per subscriber and it is why heartbeats go through the bus.** The
obvious implementation writes a heartbeat straight to the socket from the
subscription's ticker. That reorders: an event already queued for that
subscriber can be written *after* a heartbeat minted later, and the proxy then
stores an id it has not actually reached. So `Bus.heartbeat` mints under the
bus lock and pushes into the subscriber's own queue; the writer drains one
queue, in one order.

**Registration and the replay decision happen under one lock acquisition.**
`Subscribe` takes `b.mu`, computes the plan, registers, and releases. Anything
published after that point is queued for this subscriber; the replay covers
exactly up to that point. Doing the two separately leaves a window that drops
an event or delivers it twice, and the proxy cannot tell which happened. The
resync line is minted inside the same lock for the same reason — an id minted
after the lock was released could be *lower* than one already queued.

**The resync decision is three cases, and `b.evicted` is the whole of it.**
Retained events are appended to a ring; when one falls out, its sequence number
is recorded. A subscriber resuming at `seq` can be given everything it missed
iff `seq >= b.evicted`. Heartbeats consume sequence numbers but are not
retained, which is why the test is against `evicted` rather than against "is
anything older than this still in the ring" — the latter would resync on every
reconnect once a few heartbeats had passed.

**The run id in the event id is deliberate.** The ring is in memory, so after a
restart this server cannot know what a subscriber missed. Answering `resync` is
the contract's own answer for a server that keeps no history, and it is the safe
direction. The cost is one cache flush per proxy per deploy; the alternative is
resuming live delivery over a gap nobody can see.

**Backpressure: dropping is the policy.** The three options are block the
publisher (one stalled proxy becomes an outage for the fleet), grow the queue
(one stalled proxy becomes an out-of-memory), or drop (that proxy loses its
cache and reconnects into replay or resync). The third is the only one whose
worst case is bounded and local. `evict()` reports whether it was the call that
did it, so the log line appears once rather than once per missed event.

### Multi-node: precisely what an implementation must provide

This is the one component whose single-node assumption is load-bearing
(M9), and `ext.ClusterCoordinator` (0004) is the seam it would be built behind.
A multi-node broker must provide, and none of these is optional:

1. **A sequence that is total across nodes.** Event ids are compared for
   ordering and for "is this replayable"; two nodes minting independently
   produce ids that interleave, and a proxy resuming from one node's id against
   another node's log skips silently.
2. **A shared, durable replay log** with a known retention floor, so
   `evicted` is a fact about the cluster rather than about the node that
   answered. Today a restart empties it and the honest answer is `resync`; a
   cluster that kept the log could replay across a node's restart instead.
3. **Fan-out to a subscriber attached to another node.** A proxy holds its
   subscription to exactly one node; an operator publishes to whichever node
   took the request. Without cross-node delivery the kill switch reaches only
   the proxies that happened to land on the publishing node — and it reports
   success.
4. **Subscription state as a cluster-wide read.** `LiveSubscriptions` gates
   cache hints (M9, §5.4). A node that can only see its own subscribers
   withholds hints from healthy proxies attached elsewhere — a correctness-safe
   failure, but one that silently removes the caching the fleet was sized for.
5. **Delivery-once-or-replay semantics across a failover.** A subscriber whose
   node dies reconnects to another with its last id; that node must either
   replay from the shared log or answer `resync`. Anything else is the silent
   skip this design exists to prevent.

What does **not** need to move: the payload vocabulary, the addressing model,
the heartbeat rule, and the operator surface. Those are `internal/revoke`'s and
would be unchanged.

### The stream over HTTP, and three things that nearly broke it

`internal/httpapi/south/events.go`. Three findings worth the next session's
attention:

1. **`statusRecorder` needed an `Unwrap`.** The access-log wrapper sits between
   the handler and the real `ResponseWriter`, and `http.ResponseController`
   reaches the real one only through `Unwrap()`. Without it `Flush()` answers
   "not supported", the first line's flush fails, and the subscription ends on
   its first event — a stream that looked healthy in every unit test that used
   a recorder.
2. **The request timeout had to be exempted.** `withLimits` puts a 10s deadline
   on every request; on a subscription that is a reconnect storm. The exemption
   is by path shape (`isEventStream`), applies to the DEADLINE only, and
   everything else in the chain — correlation id, access log, panic recovery,
   the proxy credential — still runs.
3. **The write deadline must use the wall clock**, not `Server.now`. A test that
   freezes the logical clock would otherwise set every write deadline in the
   past and cut the stream it is grading. The deadline is a bound on a socket.

`openStream` is the only new function that writes a status code, and
`TestOnlyTheMapperNamesAStatusCode` names it: every refusal on the route is
decided before the first byte and still goes through the mapper.

### Cache hints, and the asymmetry an operator can be misled by

The gate did not change; the subscription source did. `fleet.Registry.CacheHint`
already took `EventStreamHealthy`, and wiring `WithSubscriptionState(bus)` in
`serve.go` is the whole of "hints are on".

The host-key path now calls that same function (`hostKeyCacheHint`), under
three conditions: the stream is healthy for the asking proxy, the key is
**known and accepted** (a first sighting reused would replay trust-on-first-use
into the audit log for every later connection), and the lifetime is
`fleet.host_key_cache_ttl` clamped downward.

**The key is stored, not re-derived.** `HostKeyCacheKey(tenant, host, port, fp)`
is deterministic, so an operator surface could compute one and publish it — and
it would publish a key for a target nobody has reported, report success, and
drop nothing. `Operator.WithdrawHostKey` resolves the key from the record and
returns `fleet.ErrNoHostKeyRecord` or `ErrNoHostKeyCacheKey` instead. Rows
written before migration 0005 carry no key; the next sighting backfills one,
and until then `Resync` is the honest answer.

Every publication's `Receipt` carries `CoversHostKeyDecisions`, and the publish
response carries `covers_host_key_decisions`. A subject-scoped invalidation is
`false`: the proxy keys a host-key decision on target, port and fingerprint, so
`subject` matches none of them. This is the shape 0014 inherits.

### The conformance suite now grades a claim

`cmd/pdpconform` reads `heartbeat_interval_seconds` off the stream and grades
the server against it, in **two** cases so a failure names which half:

- "the heartbeat interval this server advertises is within the contract's
  ceiling" — catches a server advertising 600s and honestly keeping to it;
- "heartbeats arrive within the interval the server advertises" — catches a
  stalled writer.

`events.heartbeat_interval_seconds` became the FALLBACK and its
`validate()` rejection was removed; a server advertising nothing with no
fallback configured now **fails as ungradeable**, which is where that rejection
went rather than being dropped. Both assertions are contract-level and live in
the shared checks, not in an expectation file — the same suite runs against
`cmd/mock-control`, which advertises an interval derived from its own
`heartbeat_ms`.

`checks_events_test.go` points the two cases at deliberately wrong servers and
asserts the suite **fails** each: a pair of assertions that cannot fail is a
pair that grades nothing.

`events.publish_token` is a new expectation key, because this server keeps the
publish path on its own listener with its own credential; left empty the suite
presents the proxy token, which is what the mock needs.

### The publish path, why it is a third port, and when it dies

`docs/PROTOCOL.md` §3 grew a rule in this PR, because this phase is the first
one to want a debug endpoint on a bound surface and the honest answer to "when
does it go away" turned out not to be written anywhere a future session would
look. The rule has four limbs — a phase that genuinely needs it, off-unless-
configured with its own credential, a **named** production successor, and that
successor's **prompt** carrying the removal file by file. The fourth is the one
that is usually skipped, and it is the only one that actually deletes anything:
a learnings note is read by the next session, while the phase that must delete
this path is five phases away.

0014's prompt now carries it: `publish.go`, its test, the two config keys and
their validation, the `config.example.yaml` block, `startPublishListener` and
its shutdown handling, the CI `events:` block, and the repointing of
`events.publish_url`/`publish_token` — plus an acceptance criterion asserting
nothing binds a `/debug/` route. 0010 owes `logs.read_url` the same treatment
and 0014's prompt names that too.



The contract states outright that nothing on `/v1` publishes an event, so gap
recovery is gradeable only through a path the implementation exposes. This one
is `POST /debug/revoke` on `events.publish_listener`:

- **off unless configured** — a deployment that does not set it has no publish
  port at all;
- **refuses to bind without `events.publish_token`** — what it publishes is the
  kill switch;
- **a separate port from both listeners**, because the south-bound listener
  serves the contract and nothing else (M2) and the north-bound listener's
  credential model is 0014's to design. Putting a bearer path on the north port
  now would pre-empt that and leave the port half-real. PLAN M2 records this.

A body-less POST means "invalidate everything", which is what the simplest
harness sends and the least surprising thing to mean by it.

### Deviations from the plan

One, and it is recorded in `docs/PLAN.md` M2: a third listener exists, optional
and off by default. It is **not** an amendment to M2 and the register row is
unchanged — everything M2 forbids still holds, since it never shares the
south-bound port, chain, or credential, and it folds into the north-bound
listener at 0014. What is recorded is why it is a port of its own rather than
the north-bound one: 0014 owns that listener's credential model, and a bearer
path on it now would pre-empt that design.

`fleet.ConfigPublisher` is still **unwired**, and that is not this phase
shirking it: the blocker is that `RevocationEvent.type` enumerates no
configuration event, which is an upstream change (PLAN §4, "Configuration
distribution has no event type yet"). 0009 built the channel such an event would
travel on; it cannot invent the event. The PLAN line that said "0009 implements
it" was wrong and has been corrected in place.

### Test notes

- `internal/revoke` needs no database. `internal/httpapi/south/events_test.go`
  needs Postgres (`HOPLOCK_TEST_DSN`) and a REAL server — `httptest.NewServer`,
  not a recorder — because what is graded is flushing, not being cut by the
  request timeout, and a response that never ends.
- The south harness gives the bus a 100ms interval and the harness clock, so
  "last written to" and "is it stale" are measured against the same instant.
- `golangci-lint` could not be run locally (the installed binary is built with
  Go 1.25 and refuses a module targeting 1.27.0); CI's pinned v2.13.2 is the
  first real run.

### Follow-ups, none of them blocking

- **0010** owns the audit record of an operator action. This phase logs
  `revoke_publish` with the event id and what it covered; that log line is the
  shape an audit row wants.
- **0014** replaces `cmd/hoplock-control/publish.go` with the north-bound API,
  and inherits `revoke.Operator` — including `Receipt.CoversHostKeyDecisions`,
  which is the part that must survive: an operator who is told "invalidated
  everything for Alice" and believes a host key went with it has been misled.
- **0015** wants to reuse this stream's shape for supervisory registration; the
  reusable part is the subscribe/heartbeat/replay loop, not `Bus` itself, which
  is addressed by tenant and proxy id.
