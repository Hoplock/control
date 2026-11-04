# 0046 — instance identity, north-bound compatibility & supervisory registration — Learnings

## Summary
- **Shipped (`v0.33.0`, MINOR, migration `0024`; `ext`/`server`/`contract` untouched):** `internal/instance` (identity, health, feed, registration); `GET …/instance`, `…/instance/health`, `…/instance/supervisors`, `POST …/instance/supervisors/{principal}/revoke`; north-bound API version negotiation (`internal/httpapi/north/apiversion.go`, `docs/API-VERSIONING.md`); config `instance:`/`supervisor:`; CLI `supervision status|revoke|restore`, `identity token-issue --supervisor`.
- **Identity:** `instance_id` (`inst-<32 hex>`) generated once in the one-row `deployment` table; display name from config; versions `software`/`contract`/`contract_commit`/`north_api`/`north_api_min`; tenant set from `deployment_tenants` (touched by the paths that resolve a tenant), filtered to the caller's `instance:read` tenants. A node never has an identity.
- **Health:** deployment (database + schema, listeners) / nodes (`deployment_nodes`, written every `instance.report_interval`, 10 s) / per-tenant fleet (`fleet.Registry.Summary`). States `healthy degraded in_progress unknown stale left`; roll-up = worst, `unknown`/`stale` never healthy. Freshness: a node 3 × report interval; checks `instance.health_fresh_for` (30 s). Absent = `unknown` + a reason (`node_not_reported`, `nodes_unreadable`, `fleet_uncomputable`, `not_observed`). Separate pool (`instance.health_connections`, 2), computed ≤ once per `instance.health_interval` (1 s).
- **North-bound version `1`** (`north.APIVersion`/`MinAPIVersion`), header `Hoplock-Api-Version` both ways; undeclared = current; out of range `400 api_version_unsupported`; a `since:"N"` member is omitted for an older client only at its zero value, else `500 api_version_unexpressible`. Any client-observable change moves it; a changed meaning also raises the minimum after the window (≥ 2 MINORs and 180 days).
- **Registration:** one `POST supervisor.endpoint`, NDJSON both ways. Out: `hello`, then `resync` (report) or replay after the supervisor's `resume` id, then events + `heartbeat`, `report` on request. In: `resume`, `heartbeat`, `rereport` — nothing else acts. Feed = `supervision_log` (0027 ids `evt-<epoch>-<seq>`, floor, newest 1024), kinds `node_joined node_left job_moved upgrade_started upgrade_finished health_changed fleet_health_changed policy_published counts_changed`.
- **Singleton:** `Lead(ctx, "hoplock.supervision")`; held → feed observer + registration; lost → both stop, feed baseline reset; the new holder resumes from the supervisor's id (store-held log). Local revocation (`supervision revoke`) is read every heartbeat.
- **Supervisor credential:** an ordinary token, scope grammar `tenant=role[,role];…`, principal id `sup-…` (`identity.IsSupervisor`) — that prefix is the attribution in every audit record.
- **Decisions:** none added; M19 revised in place (shapes), M12 names the three tenantless deployment tables; §3, §10 revised.
- **NEXT (0047 console):** render `…/instance/health` — `in_progress` is not a fault (M20), `unknown`/`stale` are neither healthy nor failed. Downstream sync for `hoplock/enterprise` queued (see the PR).

## Details

### Why the deployment tables have no tenant column
`deployment`, `deployment_nodes` and `supervision_log` describe the deployment
itself. Keying them by tenant gives one deployment an identity per tenant — the
"supervisor counts customers wrong" failure one level over. They join
`schema_migrations` in `bookkeepingTables` with their reason, and
`store.DeploymentRepository` is the only repository whose methods name no
tenant (`TestDeploymentRepositoryReadsNoTenantsRows` holds that only
`TouchTenant` takes one, to record a name). `deployment_tenants` does carry the
tenant column: it is the tenant set.

### Why 0009's stream could not be reused directly
The prompt asked for 0009's shape "pointed the other way", and to say precisely
why if its mechanism could not be reused. What was reused: the id format, the
epoch, the floor, "newest N kept", `resync` when replay is impossible, one
heartbeat interval. What could not: `event_log` is per tenant by M18 (its
stream row is the tenant's publish lock and its audience is a tenant's
proxies), and `revoke.Bus` fans out to subscribers that dial *in*. The
supervisory feed belongs to no tenant and its one reader is dialled *out* to,
so the log is its own table with the same locking discipline (the `deployment`
row is its publish lock) and the session code is `instance.Registration`.
Because the supervisor cannot dial in, the resume point travels in the
supervisor's first response line (`resume`), not in a request header.

### HTTP full duplex — the gotcha
The registration streams its request body from an `io.Pipe` while reading the
response. Go's transport waits for its body-writing goroutine before it gives
up a request, and that goroutine sits in `pr.Read` forever if nothing closes
the pipe: a cancelled dial to a hanging supervisor never returned. The session
closes the pipe on cancellation (`context.AfterFunc` → `pw.CloseWithError`).
`TestASupervisorCannotAffectASession`'s hanging case found it. A supervisor
must call `http.NewResponseController(w).EnableFullDuplex()` (HTTP/1.1) to
read the body after writing headers; the test supervisor in
`internal/instance/register_test.go` is a reference.

### Health is off the decision path by construction
`daemon.buildInstance` opens a second pool (`store.WithMaxConns`), builds its
own `fleet.Registry` over it, and computes at most once per interval under a
single-flight mutex: eight goroutines polling in a tight loop cost one
computation per second. `TestHealthPolledAtItsMaximumRateLeavesTheDecisionPathAlone`
measures authorize p95 before and under that load (bound: 3 × baseline + 25 ms).

### Degraded is legible
A node silent for 3 × report interval is `stale` and makes the nodes level
`degraded` with `node_not_reporting{node}`. A node that stops cleanly writes
`left_at` and leaves the roll-up. An unreachable database is
`database_unreachable` at the deployment level, and the nodes it could not read
are `unknown` (`nodes_unreadable`). The test asserts the two reason sets differ.
A schema *ahead* of the binary is `in_progress` (a newer node migrated during a
rolling upgrade), not a fault. Node rows unreported for 24 h are forgotten.

### Supervisor attribution
No new column: the token's principal id is `sup-<id>` instead of `tok-<id>`, and
every audit constructor already writes `principal_id`. An act through a
supervisor token is therefore attributed in the local store with no change to
any record type. `GET …/instance/supervisors` lists `sup-` tokens in the tenant
with their roles there; reads are not audited (none are, on this surface).

### Version negotiation mechanics
`withAPIVersion` sits inside the access log and wraps the writer in a
`versionedWriter`; `writeJSON` finds it through `Unwrap` and, only when the
declared version is older than the current one, renders the body through
`project` (a reflective walk agreeing with `encoding/json`: tags, `omitempty`,
`omitzero`, `,string`, flattened embedded structs, marshalers). With version 1
current and minimum, that path never runs in production; it is proved by
`TestAnOlderClientNeverReceivesANewerMember` over a server serving 1–3. The
answer-name probe now also refuses a `since` tag naming a version this server
does not serve.

### Deviations
- **Learnings file name.** The prompt names
  `0046-instance-identity-and-supervision-learnings.md`; PROTOCOL §5 requires
  the prompt's own name, and the protocol wins on process (§9).
- **0051's prompt** already carried the north-bound exemption (added by an
  earlier revision); this PR points it at the built constants and document.
- **Tenant "active"** is defined as served within 24 h
  (`DefaultTenantActiveWindow`), since no tenant table existed to hold a state.
- **The feed's first observation after gaining leadership is a baseline** and
  logs nothing; a change during a leadership handover is carried by the next
  `resync`/`report`, not by an event.
- **Registration revocation is a CLI** with database access, because it is the
  deployment operator's, not one tenant's; the supervisor *token* is revocable
  per tenant over the API.

### Follow-ups (not queued)
- `instance:read` is held by every read role; a deployment that wants to hide
  node versions from auditors would need a narrower split.
- The console (0047) should render the reasons by code (M21); the codes are in
  `instance.AllReasons`.
