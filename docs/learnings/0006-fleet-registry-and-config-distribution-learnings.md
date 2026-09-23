# 0006 — fleet registry, health & configuration distribution — Learnings

## Summary
- **What shipped:** `internal/fleet` — a pure `Graph` + pathfinding, and a
  `Registry` over the store for enrollment, liveness, the two capability stores
  (M17), configuration rollout and the pre-publish query. Plus migration `0002`
  and five new repositories. No HTTP: 0007/0014 serve the surfaces.
- **Key files:** `internal/fleet/{graph,errors,liveness,enroll,registry,config,
  capability,prepublish}.go`, `internal/store/{fleet,fleet_types}.go`,
  `internal/store/migrations/0002_fleet_registry.sql`.
- **Graph/hop types:** `Node{ProxyID,Zone,Live,Edges,Relays,Capabilities}`,
  `Edge{ToZone,Connection,Address,NextProxyID,Cost}`,
  `Hop{FromProxyID,NextProxyID,Zone,Connection,Address,Cost}`. `Connection` is
  `contract.HopConnection` — no parallel vocabulary.
- **`Path` signature:** `(*Graph).Path(EntryPoint{ProxyID,HopTrail}, Destination
  {Zone,Hostname,Port}) ([]Hop, error)`. **Empty non-nil = `direct`**; non-empty
  = `nexthop`. `fleet.Trail(entry, hops)` builds `hop.hop_trail`.
- **No-path outcome 0008 must turn into an OUTAGE, never a `401`:**
  `*fleet.NoPathError` / `fleet.IsNoPath(err)` / `errors.Is(err, ErrNoPath)`,
  with `Reason` ∈ `no-live-route | entry-unknown | entry-not-live | loop |
  max-hops | search-bound`.
- **Liveness interface 0009 implements:** `fleet.SubscriptionState.LiveSubscriptions(
  ctx, tenant) (map[string]time.Time, error)`. Also `fleet.ConfigPublisher` —
  **unwired until proxy 0042 lands, see below.**
- **Enrollment/approval:** operator `IssueGrant` pre-registers (proxy id +
  granted zones + one-time token, SHA-256 stored) → proxy `Enroll` presents
  `<tenant>.<secret>`. The token resolves the tenant (M18); a zone it was not
  granted is `ErrZoneNotGranted`.
- **Staleness:** `fleet.Liveness{HeartbeatTTL 90s, RelayRegistrationTTL 60s,
  TargetCapabilityTTL 24h, ReportAfter 6h}`, config section `fleet:`. Enrolled +
  heard-from-within-TTL = routable; never-reported is not.
- **CROSS-REPO DEPENDENCY (upstream):** config delivery over the event stream
  needs an event type `hoplock/proxy` does not have. **Already raised and
  queued** as proxy phase **0042** (`Hoplock/proxy#61`) — do not re-raise it;
  check whether it has merged, and if it has, vendor the contract and wire
  `fleet.ConfigPublisher`. Details. **Update (sync for `Hoplock/proxy#65`):
  merged, as contract `4.2.0` and proxy D18.** The shape differs from the sketch
  below: the event has `version` (an opaque string) and `hash`, and there is a
  fetch endpoint and a report endpoint. Phase **0014** owns the wiring. Read
  its prompt and PLAN §4, "Configuration distribution", not the sketch.
- **Gotcha:** a config document is stored as **text, not jsonb** — jsonb
  re-renders bytes and the hash beside it would stop matching.

## Details

### Naming: the prompt's DoD and `docs/PROTOCOL.md` §5 disagreed

The prompt asked for `0006-fleet-registry-and-graph-learnings.md`; §5 requires
the name to match the prompt file, which is
`0006-fleet-registry-and-config-distribution`. §9 says the protocol wins for
process, so this file follows §5. Anything written before this that points at
"the 0006 graph learnings" means this file.

### The cross-repo dependency, stated so the next session is not blocked by it

`docs/CROSS-REPO-PROTOCOL.md` §3.2 applies: **this phase needs something upstream
does not have.**

`contract/control.yaml` enumerates `RevocationEvent.type` as `session_kill`,
`cache_invalidate`, `heartbeat`, `resync`. None can say "your desired
configuration moved". The contract is vendored read-only (M1), so the fix is
normal work in `hoplock/proxy`: **a new event type, or a field on the heartbeat
event, carrying the proxy's desired config version and hash** — the proxy then
fetches the document and reports what it is running. A minimal shape that would
work:

```yaml
# RevocationEvent.type gains `config_changed`, with:
config_changed:
  config_version: integer   # the desired version for THIS proxy
  config_hash: string       # digest of the document's exact bytes
```

**This has been raised, not just written down.** Proxy phase **0042** is queued
for it (`Hoplock/proxy#61`), and the flow that obliged a runnable artifact rather
than a paragraph is `Hoplock/proxy#60`, which rewrote
`docs/CROSS-REPO-PROTOCOL.md` §3.2 from a prohibition into a flow — this phase is
the case it was written from. The next session here does not need to re-derive the
shape or re-raise the need: read 0042, and if it has merged, the work here is to
vendor the contract (`make contract-sync`) and wire the publisher.

Nothing was approximated. `fleet.ConfigPublisher` is the seam (0009 implements
it), its default is a no-op, and everything below the wire is built and tested:
versions are immutable, the desired pointer records what it displaced,
`RollbackConfig` is a call, and a proxy that has not caught up is **drift** in
`Registry.ConfigDrift` and in the fleet view rather than being assumed current.
A publish therefore stages correctly today and starts being pushed the moment
the event exists. PLAN §4 carries this as "Configuration distribution has no
event type yet".

The proxy also has no *inbound* endpoint on `/v1` for enrollment, heartbeat, or
fetching a config document, and it does not need one on the contract: these are
this server's own surfaces, like `logs.read_url` and `events.publish_url` (§4).
0006 defines the domain layer only; whoever builds the south-bound admin surface
owns the HTTP.

### Pathfinding: what the search actually is, and why

Uniform-cost best-first over **paths** (not nodes), loop-free, bounded twice.

- A goal-reaching candidate is **queued, not returned on sight.** Returning the
  first goal found while expanding the cheapest candidate is not the cheapest
  path — a dearer candidate can reach the destination over a cheaper edge. The
  first goal *popped* is the answer.
- **Determinism** is a total order on candidates: `(cost, hop count, proxy-id
  sequence lexicographically)`. Two nodes holding the same rows pop the same
  candidates in the same order, which is what makes them agree; they are
  answering the same question for the same session, and a disagreement builds a
  chain neither predicted. Node edges and zone membership are sorted at
  construction for the same reason.
- **An edge names a zone and branches over every viable member of it.** Resolving
  an edge to one proxy dead-ends when the lowest-id member of a zone has no
  onward reachability and another member does.
- **Two bounds.** `MaxHops` (default 4, matching the proxy's
  `routing.DefaultMaxHops` — they must agree, or this server answers routes the
  proxy refuses) counts *proxies*: the incoming trail, the asking proxy, and
  every hop still to come. `SearchBound` (default 10000) caps candidates
  explored, because a loop-free search over a pathological graph is exponential
  in the hop cap and this runs on the decision path (M5). Hitting it is its own
  reason, `search-bound`, because it names a topology rather than a dead proxy.
- **Direction has no fallback.** `resolveEdge` is the only place that decides it.
  A relay edge whose far proxy holds no live registration with *this* proxy is
  dropped — never downgraded to a dial, even when the edge carries an address
  (there is a test for exactly that, because the downgrade is the "helpful" fix a
  future refactor will reach for, and it opens the inbound path into a protected
  zone that the mode exists to avoid). A relay `Hop` carries no `Address`.

### What 0008 needs from this and should not re-derive

- Hold a `*Graph` and reuse it; do **not** call `Registry.Path` per request. That
  convenience method does three whole-tenant reads, which M5 has no room for.
  `Registry.Graph(ctx, tenant)` is the load; caching and invalidation are 0008's.
- `len(hops) == 0` → `route_type: direct`. Otherwise `nexthop`, and `hops[0]` is
  this proxy's step: `hop.connection`, `hop.next_proxy_id`,
  `hop.max_hops = g.MaxHops()`, `hop.final_target` from the `Destination`, and
  `hop.hop_trail = fleet.Trail(entry, hops)` — the **whole** chain, not just the
  part after this point; a hop that saw a shorter trail than the truth cannot
  detect the loop it is about to close.
- Pass `conn.proxy_id` as `EntryPoint.ProxyID` and `conn.hop_trail` as
  `HopTrail`. The same login and target legitimately answer `nexthop` at the edge
  and `direct` behind it (M6) — there is a test naming that.
- **Every** `*NoPathError` is `5xx`, with the correlation id. There is no reason
  value that means deny.
- A dial `Hop` with an empty `Address` is a topology an operator declared without
  saying how to reach it. This package does not invent one; 0008 refuses it.

### Capabilities: the fail-safe set is derived, not listed

`ResolveTargetRungs(rec *TargetCapabilities, now, ttl) TargetRungs` is the whole
rule, in one function, so the three fail-safe cases converge at one place rather
than at three call sites:

- `rec == nil` (absent), `rec.ObservedAt.IsZero()` (undated) and past-TTL (stale)
  are **one case**. The test compares them against each other rather than against
  a written-out expectation, because the claim *is* that they are one.
- The set that survives all three is exactly the rungs for which
  `contract.*Rung.RequiresProvisioning() == false` — the contract's own
  predicate, not a hand-kept list. That is `{proxy-inspected,
  no-interactive-shell, platform-attested}` and `{proxy-channel-policy,
  platform-attested}` today, and it stays right when the vocabulary grows. The
  prompt's "two proxy-side defaults and an attested rung" are three of those;
  `no-interactive-shell` is in because it is enforced in the proxy's channel
  layer and needs nothing of the target.
- `TargetRungs.Observed` reports whether a *fresh* record contributed. It exists
  so 0008 can explain a refusal, never to change one.
- A **read failure is not "no record"**: `Registry.TargetRungs` returns the error
  rather than the fail-safe set, or a Postgres failover would look like an
  appliance estate (M11, one layer down).
- Proxy-declared capabilities age with the **heartbeat that carried them** — one
  staleness lever, not two. A stale proxy is not a routing option at all, so its
  declared set is moot. A heartbeat with `Capabilities == nil` says nothing and
  leaves the stored set alone; a non-nil empty one withdraws everything.
- `UnmarshalCapabilities` answers the **empty set** on an unparseable document and
  does not error: the fail-safe reading of "I cannot tell what this proxy
  declared" is "nothing", which withholds rungs, and failing would turn one
  malformed row into a fleet-wide outage.

### Enrollment, and why the token carries its tenant

M18 resolves south-bound tenancy from the enrolment credential, but every
repository method must name its tenant (`TestRepositoryMethodsTakeATenant`), so
there can be no "look this token up across tenants" query. The token is therefore
`<tenant>.<secret>`: the tenant is **parsed from a credential this server minted**
and the secret is verified against the single grant under `(tenant, proxy_id)`. A
relabelled token fails the constant-time compare in a tenant where no such grant
exists — there is a test that mints tenant A's secret as tenant B's.

`MintEnrollmentToken` refuses a tenant containing `.`: an ambiguous credential is
one that can be made to resolve to the wrong tenant.

`proxy_enrollments.token_hash` carries **the only globally unique index in this
schema**, and the migration says why: a hash under two tenants would be a
credential resolving to two authorities. It makes a collision impossible rather
than improbable; it is not a lookup path.

`Consume` is one statement with `consumed_at IS NULL` in the predicate, so two
enrollments racing on one token cannot both win — a Go read-then-write would make
the token single-use only when nobody was in a hurry. Absent and already-consumed
are told apart, because they are different operator problems.

Refusals are five sentinels (`ErrEnrollmentUnknown|Spent|Expired|Rejected`,
`ErrZoneNotGranted`) and `fleet.EnrollmentRefused(err)` separates "this server
decided" from "this server could not tell" — M11 at the enrollment layer. Wrong
secret and wrong tenant are deliberately the *same* error, so the endpoint is not
an oracle for which proxy ids exist where.

`Enroll` is one transaction and requires a public key. A refused zone leaves the
grant usable (tested): the alternative is an operator's typo burning a token.
Enrollment does **not** set a heartbeat — it says an operator approved this proxy,
not that it is running — and it hands back the composed configuration, so a proxy
does not need a second round trip for the thing it needs in order to work.

`Heartbeat` on an unknown proxy is `ErrNotEnrolled` and creates nothing. That is
the auto-enrollment path nobody thinks of as one.

### Configuration: two scopes, one materialised document

`proxy_configs` (immutable versions per `zone|proxy` scope) + `proxy_config_desired`
(the published version and **the one it displaced**, which is the rollback target)
+ `proxy_config_state` (the composed document per proxy, with its own monotonic
version and what the proxy says it is running).

- **Composition replaces top-level keys**, not deeply. A deep merge cannot express
  "unset what the zone said": a proxy overriding a nested object would get the
  union, so removing an inherited setting would mean editing the zone document and
  affecting everybody.
- **Materialised on publish, not composed on read.** Write amplification on a zone
  publish is deliberate: publishing is rare, reading "what should this proxy run"
  is not, and a proxy reports one number rather than a pair.
- **A no-op publish does not bump a proxy's version.** Drift that means nothing is
  drift an operator stops reading. Tested, including that a zone publish leaves
  another zone's proxies alone.
- **`document` is `text`.** jsonb is a parsed representation and re-renders
  whitespace and key order, so a document stored that way comes back semantically
  equal and byte-different — and `hash` beside it would no longer be the hash of
  it. Nothing queries inside a config document (the keys are the *proxy's*
  vocabulary, opaque here), so jsonb bought nothing and cost the one property the
  column has to keep. There is a test that hashes the stored bytes.
- Staging a document for a proxy that has not enrolled is legitimate and does not
  fail; enrollment materialises it.

### The pre-publish query spans both sources, and that is the point

`fleet.Requirements(*model.Bundle) []RouteRequirement` reads what a policy needs
out of the policy itself — an operator asks about what they wrote, not about a
second description that can drift. Deny rules produce nothing.

`Registry.CheckPolicy` then ANDs the two sources: `CheckProxy` (pure, exported,
so 0014 can render it for a proxy it holds) against the build's declared set, and
the target's record against `ResolveTargetRungs`. Answering from the proxy alone
passes a policy that fails on every appliance; answering from the target alone
passes one no build implements.

A **ladder needs one servable entry**, not all of them — that is what a ladder is
(proxy D14) — and the per-entry reasons are reported only when none is servable,
because reporting a working policy as a problem is how a check stops being read.
`Satisfaction.Satisfiable()` is the one-line answer; `ProxyVerdict.Live` is
separate from `.OK`, because "could serve it but is stale" and "cannot serve it"
are different afternoons.

The device-field check is the one this query exists for: a policy naming
`device_field.vdom` where the enforcing FortiGate driver does not declare `vdom`
is a ladder that silently loses a rung in production (proxy D14 — a skipped rung,
invisible in the response) and a denial nobody authored on a one-rung ladder.
`matchesTarget` mirrors the compiler's target match rather than being a second
opinion: checking different targets from the ones the rule applies to would be
worse than no answer, because an operator would trust it.

### Store changes worth knowing

- `ProxyRepository.RecordHeartbeat` was **replaced** by
  `RecordHealth(ctx, tenant, ProxyHealthReport)`. Only tests called the old one.
  `ContractVersion` and `DeclaredCapabilities` are pointers/`RawMessage` so
  "the report did not mention it" and "the report said none" stay different
  facts; an empty `LastError` leaves the stored error's *timestamp* alone rather
  than claiming yesterday's error just happened.
- `proxy_edges`' primary key is `(tenant, proxy_id, to_zone, direction,
  next_proxy_id)`. One row per zone would forbid a proxy that legitimately dials
  one member of a zone and reaches another over a relay registration — an
  arbitrary restriction a later phase would pay a migration to remove.
- `ReplaceForProxy` / `ReplaceForUpstream` are replacements in one transaction: a
  declaration is the whole set, and an edge or registration that stops being
  reported must leave routing at once rather than linger. Both work inside a
  caller's transaction (`Enroll` and `Heartbeat` use them).
- `target_capabilities.observed_at` is **NULLABLE on purpose**. A NOT NULL column
  would force this server to invent a date, which is the fail-open M17 forbids.
- `proxies.public_key` is normalised through `nonNilBytes`, so a nil slice is an
  argument error rather than a SQLSTATE.

### Tests, and what runs where

`internal/fleet` splits so the assertions worth proving need no database:
`graph_test.go`, `capability_test.go` and the pure half of `prepublish_test.go`
are values in, values out. `registry_test.go`, `config_test.go`, `tenancy_test.go`
and the rest of `prepublish_test.go` use `storetest` and a **settable clock**
(`fleet.WithClock`), so staleness is tested by moving time rather than sleeping.
`internal/store/fleet_test.go` covers the SQL, including the two things only
Postgres can enforce: the one-statement `Consume` and the global token-hash index.

`internal/fleet/defaults_test.go` is load-bearing: `internal/config` declares the
fleet's defaults a second time (it is the lowest layer and must not depend on the
package that consumes it), and that duplication is affordable only because a test
fails when the two drift.

### Follow-ups, deliberately not done here

- **The upstream config event** (above). Until it lands, delivery is staged.
- **No HTTP.** Enrollment, heartbeat, config fetch and the fleet view need
  surfaces. `/v1/capabilities/report` is 0007's (it is the sibling of
  `/v1/hostkeys/report`) and should call
  `Registry.ReportTargetCapabilities`, whose return value is
  `report_after_seconds`. The operator side is 0014/0016.
- **Revoking a proxy** is a direct `Proxies().Upsert` with
  `store.EnrollmentRevoked` today; there is no `Registry.Revoke`. A revoked proxy
  is already non-routable whatever it reports (tested). The operator action, and
  the audit record it should write, belong with the north-bound API.
- **`Registry.Graph` is reloaded per call.** 0008 needs a cached graph with an
  invalidation story; that is its latency budget to spend, not this phase's.
- **No zone or target inventory management.** Targets are 0014's; this phase reads
  them for the pre-publish query only.
