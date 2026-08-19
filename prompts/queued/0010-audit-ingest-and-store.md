# 0010 — Log ingest & tamper-evident audit store

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M8)**, §4 (the priority ack means durable),
  §7 (audit, query, redaction).
- `docs/learnings/` — read summaries; open `0003` (audit table + the
  database-enforced `record_id` uniqueness), `0002` (the suite's idempotency and
  durability assertions), `0008` (decision records, which audit joins to).
- `contract/control.yaml` — `/v1/logs/batch`, `/v1/logs/priority`.
- In the **Hoplock Proxy repository**, `docs/PLAN.md` §7 — what the proxy sends and
  why the priority path exists.

## Objective
Make this server the **system of record**. Ingest both log paths, store records
append-only and tamper-evident, and make them queryable well enough to answer a
security team's actual questions.

## In scope

### Ingest (`internal/audit`)
- `/v1/logs/batch` → `202`, reporting how many records were actually stored.
- `/v1/logs/priority` → `200`, and **the ack means durable**: the record is
  committed before the response is written. The proxy acts on a critical
  security event knowing this server recorded it; acking early turns that
  guarantee into a lie that only surfaces after an incident.
- **Idempotent on `record_id`**, enforced by the database (0003). A proxy
  draining its disk buffer after an outage will resend, and it may resend
  concurrently with a live batch, so dedupe in Go is not sufficient.
- Validate and bound: record size, batch size, unknown record kinds. A malformed
  record is a `400` for that request, and a partially-valid batch must have
  defined semantics — say which you chose (reject the batch, or store the valid
  records and report the rest) and make it match the contract.

### Tamper evidence (M8)
- Records are immutable and **hash-chained per stream**, each record covering its
  predecessor, so a removed or altered record breaks verification.
- A **verifier** — a command and a test — that walks a chain and reports the
  first break with enough context to investigate.
- Say plainly in your learnings what this does and does not defend against: it
  detects tampering by anyone who cannot rewrite the whole chain, and it does not
  defend against an attacker who can. External anchoring is future work; claim
  only what the mechanism delivers.
- Session capture (pty streams, proxy §7) is stored so a session can be
  replayed. Address size and retention explicitly rather than discovering them.

### Query (`internal/audit`, read side)
By session, subject, target, decision id, time range, kind, and severity. The
query that matters — and that should have a test — is the one that sells the
product:

> every blocked command on `env=prod` last week, who ran it, over which route,
> and which decision (and, later, which approved grant) permitted the access.

That is a join from audit to decisions, and it is why 0008 stores `decision_id`
on both sides.

### Redaction (PLAN §7)
The initial-auth password never reaches this server, and must never be written
even if a malformed record contains one. Assert it here: "the proxy promises
not to send it" is not a control on this side.

## Out of scope
- SIEM export (0013) — this store is the source, the export is a consumer.
- The north-bound query API (0013) — build the query layer, not the HTTP surface.
- Retention jobs (0013).

## Acceptance criteria
- The conformance suite's log assertions pass, including replaying a batch not
  double-counting.
- Concurrent duplicate submissions of the same `record_id` result in exactly one
  stored row — test with parallel writers, not sequentially.
- A priority record is retrievable immediately after its ack (the suite's
  durability assertion, against a real database).
- Chain verification passes on a healthy chain, and **fails at the right record**
  when a row is altered or deleted directly in the database — test both.
- The showcase query returns correct results over seeded data, joined to
  decisions.
- A record containing a password-shaped field is stored redacted, or rejected —
  whichever you chose — and never written in the clear. Test against what is
  actually on disk.
- Ingest throughput is measured and reported, with the batch size used.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0010-audit-ingest-and-store-learnings.md`. Summary block MUST
give the record schema and kinds, the chain construction and how to verify it
(and its honest limits), the partial-batch semantics, the query API, the
retention knobs left for 0013, and the measured ingest throughput.
