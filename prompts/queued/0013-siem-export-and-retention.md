# 0013 — SIEM export & retention

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially §7 (export is a downstream consumer, never the
  source of truth), §2 (M8).
- `docs/learnings/` — read summaries; open `0009` (record schema, chain, query
  layer, retention knobs left here), `0011` (north-bound routes and roles).

## Objective
Get the audit stream into the tools security teams already run, and put a
defensible retention policy underneath it. Both are table stakes in this market:
an audit store nobody can query from their SIEM is an audit store nobody looks
at, and unbounded session recordings are a storage bill that ends the pilot.

## In scope

### Export (`internal/export`)
- A `Sink` interface with implementations for **Splunk** (HEC), **Microsoft
  Sentinel**, and **Elastic**, plus a generic HTTP/JSON sink and a file sink for
  air-gapped environments.
- **Downstream only** (M8): export reads the audit store and never writes to it,
  and nothing reads back from a SIEM as truth.
- **At-least-once with a durable cursor**: a restart resumes where it stopped and
  never silently skips. Duplicates downstream are acceptable and expected;
  gaps are not, and each record's `record_id` lets a SIEM dedupe.
- **Backpressure and failure isolation**: a SIEM being down must never slow
  ingest or the decision path. Buffer, retry with backoff, and surface lag as a
  health metric — silent export lag is how a team discovers at incident time
  that the last three weeks never arrived.
- A stable, documented **export schema**, versioned independently of the internal
  record shape, so an internal refactor does not break a customer's dashboards.
  Include the fields that make this product's events legible: identity, route
  and every hop, credential method, channel and request, forwarding
  destination, command, the **filtering tier that decided** (bastion D12), the
  policy decision id, and any grant.

### Retention
- Per-record-kind retention policies (metadata outlives session recordings —
  they have very different sizes and very different value at month six).
- A retention job that is explicit, scheduled, dry-runnable, and **records what
  it deleted** — a deletion that leaves no trace is indistinguishable from
  tampering, which is the property 0009's chain exists to provide.
- Reconcile deletion with the hash chain: state precisely what verification means
  after a retention pass, and make the verifier report "deleted by retention at
  T" rather than "chain broken". Getting this wrong turns every routine deletion
  into a false tampering alarm, which trains people to ignore real ones.
- Legal-hold: an exemption that pins records against retention, authored and
  audited through the north-bound API.

### Operability
- Health and lag metrics per sink, exposed for scraping and visible in the
  north-bound API.
- Configuration for sinks lives in config, and secrets come from the deployment's
  secret store — never a committed file.

## Out of scope
- Storage tiering of session recordings (note as future work).
- A UI for retention policy authoring; the API and `policyctl` suffice.

## Acceptance criteria
- Each sink is tested against a local fake endpoint: successful delivery, retry
  after failure, and cursor resumption after a restart with no gap.
- A sink that is down does not slow ingest — measured, with ingest continuing
  while the sink is unavailable.
- Export lag is reported and a stalled sink is visibly unhealthy.
- The export schema is snapshot-tested so a change is deliberate and reviewable.
- Retention: dry-run changes nothing; a real pass deletes exactly the intended
  records, writes an audit record of what it removed, and **verification after
  the pass reports the deletion as retention, not as a broken chain**.
- Legal-hold prevents deletion, and setting one is audited.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0013-siem-export-and-retention-learnings.md`. Summary block MUST
give the `Sink` interface and its implementations, the export schema version and
where it is defined, the cursor/at-least-once mechanism, the retention policy
shape, and exactly what chain verification means after a retention pass.
