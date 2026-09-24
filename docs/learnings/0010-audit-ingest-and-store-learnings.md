# 0010 — log ingest & tamper-evident audit store — Learnings

## Summary
- **What shipped:** `internal/audit` — both contract log paths, the per-tenant
  per-stream hash chain, redaction, the verifier (`hoplock-control
  audit-verify`) and the query layer; migration `0006`; the record read-back
  listener 0014 deletes. **Conformance 36/36 against this server** (every group)
  and **40/40 against the proxy mock**.
- **Key files:** `internal/audit/{record,redact,chain,ingest,verify,query}.go`,
  `internal/store/{audit,types}.go`, `internal/store/migrations/0006_audit_ingest.sql`,
  `internal/httpapi/south/logs.go`, `cmd/hoplock-control/{auditread,auditverify}.go`.
- **Record schema:** `body` (canonical JSON, **text not jsonb** — the chain
  hashes these bytes) plus derived index columns: `subject login target message
  event decision_id proxy_id attributes device_fields enforcement_{execution,
  reach,verified,attested_by} target_auth_{method,rung} algorithm_profile
  grant_{system,reference,window_start,window_end,additional_kind,additional}
  capture_{bytes,sha256}`. Captures live in `audit_captures`.
- **Kinds are CLOSED (12, the contract's); an unknown one is `400`.** Severity
  stays unconstrained — 0001 says why. Asymmetry explained in PLAN §7.
- **Chain:** `sha256(len-prefixed: version|tenant|stream|seq|prev|body)`;
  `Stream = "proxy:<id>"` (or `unattributed` for an unbound token).
  **It detects anyone who cannot rewrite the whole stream, and nothing more** —
  no external anchor, so an attacker with full write access rewrites it
  cleanly. Verify with `hoplock-control audit-verify --tenant X [--stream S]
  [--captures]`; exit 1 = broken.
- **Partial batches: ALL OR NOTHING.** `accepted` is contractually "fewer than
  sent means duplicates", so partial acceptance would tell the proxy to drop
  records nobody stored.
- **Query API:** `audit.Reader` — `Get Find Session Capture AccountMappings
  DeviceConfigChanges UnderGrant ByDeviceField BlockedCommands
  DegradedCredentials`. `BlockedCommands` is the showcase join (audit→targets
  for the label, LEFT JOIN decisions).
- **Migration `0006`** drops `payload`, adds the columns above + `audit_captures`
  + 7 indexes. **Decisions:** none added/amended/withdrawn; the §2 register is
  unchanged. PLAN §2 (M2), §3 and §7 revised in place; §10's 0010 row too.
- **Throughput** (local Postgres 16, 1 vCPU container, no race detector):
  **~9 400 records/s at batch 50**, ~5 900/s at batch 500, ~660/s at batch 1.
- **Retention is 0014's and has ONE rule:** delete a contiguous PREFIX of a
  stream, never a record out of its middle — see PLAN §7 and 0014's prompt.
- **Upstream request raised** (see the PR): `algorithm_profile` on records, the
  `device.config.change` event, and the `target_auth_*`/`credential_*` naming
  split. Both names are read here; nothing is blocked. **Update (sync for
  `Hoplock/proxy#66`): answered and merged.** `credential_*` (counting from 1)
  is the only spelling, so `DegradedCredentials`' `> 0` is wrong, and a sweep's
  change record has no session id. Phase **0014** owns all of it. Read its
  prompt, not the Details below.
- **NEXT session:** the read listener (`audit.read_listener`) is a debug path
  0014 must delete — its prompt now names every file, key and CI line.

## Details

### Why `body` is text and everything else is an index

The chain hash covers the record's bytes, and a verifier re-reads them and
re-hashes with nothing in between. That forces the column type: `jsonb` is a
decoded document rather than bytes — it drops key order, drops duplicate keys,
and rewrites number literals (`1e2` comes back `100`) — so a hash taken over
what arrived would stop matching what comes back. Phase 0006 hit the same wall
storing a configuration document beside its hash.

The consequence is that one record is on disk twice: once as the authoritative
`body`, once spread across the projection columns. That is deliberate and
one-directional. Every projection is recomputable from the body; nothing
recomputes the body from the projections; and the verifier reads only the body,
so an attacker who edits a projection column changes a query's answer without
changing the hash — which is worth saying out loud, because it is the one thing
this design does not catch and the mitigation is that the body is what an export
and a legal hold carry.

`canonical` (in `record.go`) is a struct rather than a map so field order comes
from the source, and `encoding/json` sorts map keys, so the encoding is
deterministic without a bespoke canonicaliser. `TestTheCanonicalBodyIsDeterministic`
encodes the same record fifty times.

### Why a stream is the submitting proxy

A chain has one head, so every append to it serialises. The choice is therefore
about what serialises with what:

- **one chain per deployment** — strongest evidence (any deletion anywhere
  breaks it), and the whole fleet queues behind one lock;
- **one chain per session** — no contention, and deleting a whole session is
  undetectable because there is nothing left to break;
- **one chain per proxy** — what shipped. A proxy is already a single writer
  shipping records in order, so serialising it costs nothing it was not already
  paying, and it is a unit an operator can name when asked what to verify.

The tenant half is not a choice: M18 requires a departing customer to be able to
take a chain that still verifies, and a chain spanning tenants makes departure
either a broken chain or a disclosure of everybody else's record count and
timing.

The lock is a Postgres advisory lock (`pg_advisory_xact_lock(hashtext(tenant),
hashtext(stream))`) held for the transaction, not a mutex in this process:
there is more than one process, and a Go-side lock would pass every test and
lose the race in the deployment this product is sold into. A hash collision
between two stream names costs them a shared lock and nothing else.

### Idempotency, and why it is a pre-check under the lock

`AppendChain` takes the lock, selects the submitted ids that are already
stored, hands the survivors to the caller to position and hash, and inserts them
as one multi-row statement. The pre-check is what keeps a replay from taking a
chain position; the unique index is still what makes it *correct*, because a
record id is unique per tenant rather than per stream and a writer on another
stream could in principle take one between the check and the insert. That path
aborts the transaction rather than inserting a gap.

`TestConcurrentDuplicateSubmissionsStoreExactlyOneRow` runs eight writers at the
same batch and asserts both the row count and that the sequence has no gap. The
sequential version of that test passes against a Go-side dedupe, which is why
the acceptance criterion insisted on parallel writers.

One multi-row `INSERT` rather than N: the rows are already in one transaction so
correctness does not need it, but a round trip per record turns a 500-record
batch into 500 of them. It took batch-50 throughput from ~4 200/s to ~9 400/s.

### Redaction: recorded, not silent, and not a content scan

A password-shaped **attribute key** has its value replaced with `[redacted]`
before anything is hashed, and the key is listed in the record's own `redacted`
field. Two decisions inside that:

- **Redact rather than reject.** A record carrying a credential is a record of
  something that happened — a proxy bug, most likely — and refusing it would
  delete the evidence of the bug in the one case an operator most needs it.
- **Match on the key, never scan the values.** A scan that decided what looked
  like a password would eventually redact a command an operator needs to read.
  The list is `redactedKeys` + `redactedPrefixes` in `redact.go`, matched
  against the **last dotted segment**, so `device_field.password` and
  `grant_additional_context.api_key` are caught by the same entries that catch a
  bare `password`.

`TestAPasswordIsNeverOnDisk` reads the raw `body` and `attributes` columns
through the pool rather than through the repository: a redaction applied on the
way out would pass every other assertion in the file.

### Grant context, and how `additional_context` actually arrives

The contract's `GrantContext` is on the authorize response and the proxy copies
it onto every record of the session. It arrives as **flat string attributes**,
because `LogRecord.attributes` is `additionalProperties: {type: string}`. Read
`hoplock/proxy`'s `internal/logging/grant.go` for the exact keys; the shape is:

- `grant_system`, `grant_reference`, `grant_window_start`, `grant_window_end`;
- the **string** form of `additional_context` as one attribute,
  `grant_additional_context`;
- the **object** form as one attribute per field under
  `grant_additional_context.` — the device-field pattern, and there for the same
  reason: an auditor asks which sessions a change ticket authorised, and a whole
  object flattened into one string turns that into a substring search.

So the two forms are already distinguishable on the wire and this store keeps
them distinguishable: `grant_additional_kind` is `string` or `object`, and
`grant_additional` is the JSON of whichever arrived. A record carrying both
keeps the object and files the string under the empty key rather than dropping
either.

### The rung in force, and the two attribute names

`enforcement_{execution,reach,verified,attested_by}` match `hoplock/proxy`'s own
attribute keys exactly. The credential method does not: PLAN §7 names the fields
`target_auth_method` and `target_auth_rung` (after the contract's
`target_auth_ladder` the index points into) and the proxy emits
`credential_method` and `credential_rung`. **Both are read and both land in the
same column** — indexing only the plan's names would leave the degradation query
empty against every real deployment — and the divergence is raised upstream
rather than absorbed silently.

`enforcement_verified` is a `*bool` end to end, and the column is nullable. NULL
means the record stated no rung; `false` means an attested rung nothing here
verified. Collapsing them would invent a claim in the direction the contract's
attribution rule exists to prevent. An attribute value this server cannot parse
as a bool also leaves it NULL rather than defaulting either way.

`target_auth_rung` is nullable for the same reason: rung `0` is "the method
policy preferred" and is a different fact from "no rung stated".

**Superseded by `Hoplock/proxy#66` (merged).** The plan's names came from the
proxy's own contract text, which published `target_auth_*` (0-based) while its
code emitted `credential_*` counting from **1**. `#66` corrected the text and
kept the code. So the stored rung was always 1-based, the preferred method is
rung `1`, and the `> 0` in `DegradedCredentials` returns every session. 0014
drops the second name and fixes the threshold (its prompt, "The records the
proxy emits since proxy phase 0043").

### The two device events

Neither has a `kind` of its own — the contract's kind enum is closed and both
arrive as `provisioning` — so the `event` attribute is the only thing that
distinguishes them, which is why it is an indexed column:

- `device.account.mapping` — what the proxy emits today. On a device whose
  account-name length forced the readable login segment out of the name, this
  record is the only place attribution exists.
- `device.config.change` — **the proxy does not emit this yet.** Its plan §5.3
  says it should ("emit the device configuration-change event as a distinct,
  queryable audit kind"); the schema, the column, the index and
  `Reader.DeviceConfigChanges` are here, and the name is in the upstream
  request. Nothing else is blocked by it. **Update: `Hoplock/proxy#66`
  (merged) emits it**, `info` on the batch path, and a sweep's change carries
  `session_id: ""`, which `Parse` refuses today. 0014 owns both the ingest rule
  and the query filters.

### Why the read-back path exists at all, and what deletes it

Nothing on the contract reads a record back and upstream `Hoplock/proxy#56` says
that is deliberate: a proxy writes logs and never queries them. So the priority
ack's durability guarantee is gradeable only through a path this server exposes
outside `/v1` — `audit.read_listener`, `GET /debug/logs/{record_id}`, off unless
configured, its own credential, its own port (a third one: publishing the kill
switch and reading everybody's audit records are different privileges).

`docs/PROTOCOL.md` §3's four limbs are met, and limb 4 is the one that is
usually skipped: `prompts/queued/0014-...md` now names every file, config key,
fixture and CI line that goes with it, plus the requirement that the north-bound
replacement serve a record **by id**, because the suite substitutes
`{record_id}` into `logs.read_url`.

The conformance suite gained two optional expectation keys for this —
`logs.read_token` and the `{record_id}` placeholder — both of which the proxy
mock's expectation file leaves out, so the mock leg is unchanged (40/40).

### Throughput, and why nothing asserts a number

Measured by `TestIngestThroughputIsMeasured` against a real database, printed
with the batch size, asserted at nothing. `/v1/authorize` has a latency budget
because a proxy holds a user's handshake open while it waits (M5); log ingest
has no such caller — the proxy batches to disk and ships asynchronously, and the
priority path carries one record. What throughput decides is how far behind a
fleet's buffers may fall, so the useful thing to publish is the figure and its
conditions, not a threshold that fails on a laptop and passes on a build runner.
`BenchmarkIngest` is the same measurement in a form `-bench` can track.

### Session capture: size, and the retention rule it forces

Captures go in `audit_captures`, keyed `(tenant, record_id)`, with **no foreign
key** (matching the rest of the schema, and deliberately: a capture whose record
was removed is itself evidence). The record keeps `capture_bytes` and
`capture_sha256`, and the digest is **inside the hashed body**, so the chain
covers the bytes without carrying them — `audit-verify --captures` re-digests
them, and it is off by default because it reads every captured byte in the
store.

The bound is `audit.max_capture_bytes`, default 1 MiB. A pty capture is one read
off the wire per record (the proxy's `raw-chunk` format), so a chunk is bounded
by the proxy's read buffer and 1 MiB is two orders of magnitude above it.

**Retention is 0014's, and it has one rule that is easy to get wrong:** deleting
a record from the middle of a chain is indistinguishable from tampering, because
the mechanism cannot tell a policy from an attacker. A retention job deletes a
contiguous **prefix** of a stream and records where it stopped; captures are the
easy case, because they are a separate table and the record keeps the digest, so
"delete captures older than N days" leaves a chain that still verifies and a
store that can say the bytes are gone rather than changed. Both are written into
PLAN §7 and into 0014's prompt.

### Things a later session will trip over

- **`storetest.AuditRecord` is not chained.** Its hash fields are empty on
  purpose; a fixture that made up a chain would let a test pass verification
  against hashes nothing computed. Build a real chain through `internal/audit`.
- **The south listener now serves every contract endpoint**, and
  `TestTheListenerServesExactlyTheContractPaths` asserts the whole table. A
  route added upstream shows up here as a missing route.
- **`build` is on `discipline_test.go`'s allowed list** for naming an HTTP
  status: the router is where a route's own success code is declared, and
  `/v1/logs/batch` answers `202`. It still cannot choose what a caller is told
  on a failure, which is what that test is about.
- **A tenant mismatch is its own error type**, not a `MalformedError`. The
  acceptance criterion required it to be distinguishable, and the handler logs
  it at WARN separately because it means two customers' configurations have been
  crossed.
