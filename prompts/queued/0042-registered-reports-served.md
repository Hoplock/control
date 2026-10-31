# 0042 — Registered reports served: Control lists and generates every registered report pack

> Raised upstream by `hoplock/enterprise` in
> https://github.com/Hoplock/enterprise/pull/26, under `## Upstream request`,
> item 5, from its request file
> `prompts/upstream/queued/enterprise-PR#26-report-provider-routes.md`, and
> answered here as one phase (`docs/CROSS-REPO-PROTOCOL.md` §3.2).
>
> **Where it sits.** After the confined inbound action (0041), identity sync
> (0040) and the action executor (0039), and after everything they run after:
> the shared event plane (0027), bounded lists (0028) with every chunk it
> queues, and host namespaces (0038). Before instance identity (0044), the
> console (0045) and the E2E topology (0046). The report-provider renumbering
> note in `docs/PLAN.md` §10 gives one line of reasons for each, and the
> confined-action note gives 0041's. It needs M26's list shape and 0038's
> namespaces. It needs nothing of 0039's, 0040's or 0041's, which run first
> because each closes a gap Enterprise cannot route around and this one does
> not. 0028's chunks (0029–0037) renumbered it, and so did 0041, and the order
> holds, because it runs after all of them.

## Read first
- `docs/PROTOCOL.md` — session workflow. Read §3 for `ext/` and `server/` as a
  compatibility promise, for revising the plan in place, and for the
  downstream hand-over this phase owes. Then §4 and §5.
- `docs/CROSS-REPO-PROTOCOL.md` — **§4.1**, **§4.3**, and **§5** at "The PR
  that answers an upstream request is not a sync". This phase answers a
  request, and it changes what `ext/` does and says. So it owes a downstream
  sync (below).
- `docs/PLAN.md`:
  - **§2**, the register first, then:
    - **M15**, in full. `ReportProvider` is **core** when absent: the
      north-bound audit and decision queries are the reporting available.
      "Where a feature could plausibly sit on either side, it goes here" is
      why the serving is Control's and the packs are a host's. M15 is also
      where a host's routes enter Control's table.
    - **M2** (the north-bound surface, and the console as one of its
      clients), **M18** (the tenant is the one the middleware resolved, never
      one a body names), **M11** (an outage is never an answer, and a
      provider's failure is never a `401`), **M21** (codes, typed parameters,
      and the catalogue that turns a `ReportID` into a name), **M20** (the
      console lists a pack), **M5** (a provider runs in the process that
      serves the decision path), **M24** (why this is a tenant route and
      never a cross-tenant one), **M19** (0044 promises the surface this
      phase adds to), and **M23** (the release).
    - **M26**, the bounded-list decision 0028 recorded and 0029–0037 built,
      and the namespace decision 0038 recorded. Find both in the register.
  - **§3**, "Component responsibilities": `internal/httpapi/north` (the
    route table, the error envelope, `writeJSON` as the one writer, and the
    chain: `withLimits`, `withRecovery`), `server`, `internal/daemon` and
    `ext`.
  - **§6**, at "A host names a permission; Control defines it (M15)", and the
    role table. Reading a report is `audit:read`; `report:write` is a host's.
  - **§7**, "Query" and "Export": what Control already answers under
    `audit:read`, and what a pack summarises.
  - **§10**: this phase's row and the report-provider renumbering note.
- `docs/learnings/` — read every summary. Open these in full:
  - **`0004`**: `ReportProvider` as designed, and the catalogue's three forms.
  - **`0015`**: host routes, `server.CallerFrom`, and the permissions as
    merged ("archive search, reports read `audit:read`; report
    schedules/campaigns change `report:write`").
  - **`0019`** and **`0023`**: the route table, the codes, and how a handler
    refuses a field.
  - **`0028`** with the chunks it queued, and **`0038`**: the list shape and
    its enumerating test, and the four namespaces.
- Code. Read it before designing anything:
  - `ext/audit.go`, from `ReportFormat` to `ReportProvider`, and their doc
    comments. Also `ext/point.go` (`PointReportProvider`'s entry),
    `ext/registry.go` (`Registration`, `Bound`, `Extensions.ReportProviders`)
    and `ext/errors.go` (`KindOf` and the kinds).
  - `internal/httpapi/north/routes.go` (`auditRoutes`, `tenantRoute`),
    `errors.go` (the codes, `AllCodes`, `writeJSON`), `middleware.go`
    (`withLimits`, `withRecovery`), `host.go` (`HostCallerFrom`,
    `hostRequest`), `extensions.go`, and `timeParam` and `invalid` in
    `policy.go`.
  - `server/caller.go` (`CallerFrom`).
  - `internal/daemon/north.go` (the wiring, and the `http.Server`, which has
    no write timeout) and `internal/config/config.go` (`NorthConfig`,
    `DefaultNorthRequestTimeout`).
  - `internal/identity/rbac.go`: `PermAuditRead`, and `PermReportWrite`'s
    comment, which already says reading a report is `audit:read`.
- The request itself, for the requester's own words. The Enterprise decisions
  it turns on are **E3** (what Enterprise pins), **E6** (Enterprise is not the
  system of record for audit, and never records a second copy) and **E11**
  (cross-tenant reporting is the instance operator's). The phase that consumes
  this one is its 0009. Read either in `hoplock/enterprise` only if a choice
  here turns on it. Each is cited by id and never restated.

## Objective
Serve every registered report pack on Control's north-bound API, so that a
registered `ext.ReportProvider` reaches the people who may read the tenant's
audit, and Control's console can list it.

Today `ext` declares the reporting seam, and nothing in Control calls it:

- nothing calls `Extensions.ReportProviders`, `Reports` or `Generate`, so a
  registered pack is listed by `GET …/extensions` and reaches nobody;
- no route lists or produces a provider's report, although
  `ext.ReportFormat`'s doc comment speaks of what "the north-bound API" can
  offer and `ext.ReportDescriptor`'s of "the console" holding its names;
- every north-bound request is cut at `north.request_timeout` (30 s by
  default), so a pack over a year of a large estate could not finish if
  anything did call it;
- every answer on this surface is JSON written by `writeJSON`, and a report
  is a PDF, a CSV or a JSON document somebody else's code wrote.

After this phase Control lists every registered provider's reports for the
tenant it resolved, and generates one on request, under `audit:read`.

## The request, in this repository's vocabulary
- *Asked for*, verbatim from enterprise#26, item 5:

  > Control serves every registered `ext.ReportProvider`, for the tenant it
  > resolved, on the north-bound API. For example:
  > - `GET /api/v1/tenants/{tenant}/reports` returns each provider's
  >   `Reports(ctx, tenant)` descriptors;
  > - `POST /api/v1/tenants/{tenant}/reports/{provider}/{report}` takes
  >   `{from, to, parameters, format}` and streams `Generate`'s `Body` with its
  >   `ContentType`.
  >
  > Both sit under one reading permission (`audit:read`, which the auditor
  > holds, is the obvious one). `ErrNotFound` and `ErrInvalid` answer `404`
  > and `400`, and any other error is an outage (M11). Control's console can
  > then list a pack as it lists Control's own. If Control's answer is instead
  > that a host serves its reports on its own routes, and the seam's doc
  > comment should say so, that is the "not like that, like this" outcome,
  > and 0009 follows it. Either way, the `reports…` family becomes Control's
  > or stays this repository's, and 0009's patterns follow.

  In this repository's words: a new `internal/report` package over
  `Extensions.ReportProviders()`, which lists, checks a request against the
  descriptor the provider listed, and calls `Generate` under its own bound.
  Two tenant routes in `auditRoutes`, under `identity.PermAuditRead`, in
  Control's API namespace (0038). The `reports` family is Control's.
- *Until it exists:* a registered pack is listed nowhere by Control, its
  console included. Enterprise's 0009 builds its packs behind
  `ext.ReportProvider`, tests them against the seam directly, serves them
  through its own `audit:read` routes, and skips the criterion that a pack is
  listed and generated through Control. A customer on Control alone loses
  nothing: the point is core, and the audit query, explain and simulation are
  the reporting available, as M15 says.

## What this phase answers, and where it differs from the ask
The request is **met as asked**, not answered "like this":

- Control serves every registered provider, for the tenant it resolved;
- one route lists each provider's `Reports(ctx, tenant)`, and one generates a
  report from `{from, to, parameters, format}` and streams `Generate`'s body;
- both are under `audit:read`. That is already decided: `PermReportWrite`'s
  comment says "Reading a report is PermAuditRead: a report is the audit
  store, summarised", and 0015 merged "reports read `audit:read`" for a host's
  routes;
- `ErrNotFound` is `404`, `ErrInvalid` is `400`, and every other failure is
  an outage, never a `401` (M11);
- the console lists and generates a pack (0045);
- the `reports` family is Control's. A host's own reporting surfaces —
  schedules, campaigns and the history of what was generated, under
  `report:write` and `audit:read` — stay the host's, in the host's namespace
  (0038).

Answering it the other way would leave a core point no Control code calls,
and the seam's own doc comments describing a surface that does not exist.
M15 puts a feature that could sit on either side here.

The request's shape is an example ("For example"), and reading the plan and
the code adds things the requester could not see. Each item below says why,
then what to build.

1. **The provider and the report travel in the body, not the path.**
   - *Why.* `Registration.Provider` is any non-blank string, by convention a
     module path (`github.com/hoplock/enterprise/reports`). A path segment
     cannot carry a `/`, and `%2F` is decoded by many ingresses and WAFs
     before Control sees it. `ReportID` is unconstrained too.
   - *What to build.* `POST …/reports/generate` takes
     `{provider, report, from, to, parameters, format}` in its body, as
     `…/policy/simulate` and `…/targets/relabel` take theirs. The listing
     names each report by the same two strings. No route has a `{provider}`
     or `{report}` segment.
2. **Control checks the request against the descriptor, before `Generate`.**
   - *Why.* `Reports`' doc comment says the list "may differ per tenant,
     because a reporting pack can be licensed per tenant". So the list is the
     licence answer, and a provider that forgets to re-check it in `Generate`
     would produce a pack for a tenant not entitled to it. Every check that
     follows from the descriptor is the same for every provider, so it is the
     framework's, as M16's replay and skew handling are `accessctx`'s.
   - *What to build.* For each generation, Control calls the provider's
     `Reports(ctx, tenant)` and finds the descriptor. A provider not
     registered, or a report it does not list for this tenant, is
     `404 not_found` naming `provider` and `report`, and `Generate` is never
     called. Then, each a `400 invalid_request` naming its field, with
     `Generate` never called:
     - `format` is not one of the descriptor's `Formats`;
     - a `Required` parameter is missing, a parameter the descriptor does
       not name is present, or a value is outside a non-empty `Values`;
     - `from` or `to` is missing or not RFC 3339, or `from` is not before
       `to`. The period is `from` inclusive and `to` exclusive, as
       `GET …/audit/records` reads it (`internal/store/audit.go`), and
       `ReportRequest`'s doc comment says so.

     A descriptor Control cannot serve is the provider's fault, not the
     caller's: an empty `ReportID` or one repeated within the provider, no
     `Formats`, a format outside the three `ReportFormat` constants, or a
     parameter name that is empty or repeated. That provider's listing entry
     carries `report_provider_failed`, and its reports are not generated.
     Check the format explicitly: `ReportFormat.String` renders an unknown
     value as `"json"`, so an unchecked one would be served as JSON.
3. **The body is served as its format, never as the provider's content
   type.**
   - *Why.* A report body is bytes from somebody else's code, served on the
     listener the console runs on, to a browser holding the operator's
     session cookie. A provider's `Content-Type: text/html` would make the
     response a page on the console's origin. And `writeJSON` is this
     package's only writer today, so nothing yet sets a type it did not
     choose.
   - *What to build.* Control answers with the media type of the requested
     format: `application/json`, `text/csv; charset=utf-8` or
     `application/pdf`. It always sets `X-Content-Type-Options: nosniff`,
     and `Content-Disposition: attachment` with a filename Control builds
     from the `ReportID` and the period in `[A-Za-z0-9._-]` only. A
     `Report` whose `Format` is not the one requested, or whose
     `ContentType` media type is not that format's, is
     `502 report_provider_failed`, and its `Body` is closed before a byte is
     written. The doc comments of `ReportFormat` and `Report` state the three
     media types, so a provider learns them from `ext` and not from a
     refusal.
4. **A generation has its own bound, and a ceiling beside the decision
   path.**
   - *Why.* `withLimits` cuts every north-bound request at
     `north.request_timeout`, before the router sees it, and a compliance
     pack over a year will not finish in 30 s. Raising that bound for every
     route would loosen it where it matters. And a provider runs inside
     Control's process, which also serves the south-bound decision path that
     M5 keeps cheap. An auditor starting ten year-long packs at once must not
     be what slows somebody's login.
   - *What to build.*
     - **`north.report_timeout`** (default `10m`; at least
       `north.request_timeout` and at most `1h`, refused at start-up
       otherwise). It bounds `POST …/reports/generate` alone, from the start
       of the request to its last byte. Every other route keeps
       `north.request_timeout`, and a test proves one still stops there. The
       generation's context stays the request's, so a client that
       disconnects cancels it. Never detach it with `context.WithoutCancel`.
     - **`north.max_concurrent_reports`** (default `2`, at least `1`). It
       caps generations running at once on this node. One past it is
       `503 report_generation_busy` with `Retry-After`, answered before
       `Reports` or `Generate` is called. It is per node because what it
       protects is that node's decision path. The listing is not counted.
     - The listing calls each provider's `Reports` concurrently, under the
       request's own bound. A provider that has not answered when the bound
       runs out is listed with `report_provider_unavailable`, and the rest
       are still listed.

     If reading the decision path's budget shows a better default, change it
     and say why in the learnings.
5. **A body that fails partway is never a short report.**
   - *Why.* M11: an outage must never look like an answer. A compliance
     artefact that ends early and looks complete is worse than none, because
     somebody files it. Once a `200` is written, the status cannot change.
     And `withRecovery` recovers every panic and writes a `500` envelope. Into
     a response already streaming, that envelope would be appended to the
     report as if it were the end of it.
   - *What to build.*
     - Control reads from `Body` until its first byte, or `io.EOF`, before
       writing a status. An error there is answered in the envelope, like
       any other. A body that ends at once is an empty report, `200`.
     - After the status is written, an error from `Body`, or the bound
       running out, aborts the response with `http.ErrAbortHandler`. HTTP/1.1
       then ends without the terminating chunk, and HTTP/2 resets the stream,
       so a client sees a broken response and never a complete one.
       `withRecovery` re-panics `http.ErrAbortHandler` and writes nothing.
     - No `Content-Length` is set. `Body` is always closed. The access log
       records the outcome, the bytes written, the provider, the report, the
       period and the format.
6. **The caller travels with the call, for attribution and never for
   authorisation.**
   - *Why.* Enterprise's 0009 keeps generated reports as evidence of who
     produced what. It cannot, if `Generate` cannot tell who asked.
     `server.CallerFrom` answers only inside a host route today
     (`north.HostCallerFrom` reads the host request's context value), and
     `ReportRequest` carries no caller.
   - *What to build.* Control's two report routes make `server.CallerFrom`
     answer inside `Reports` and `Generate`, with the tenant, subject,
     principal, break-glass flag and correlation id of the request. No `ext`
     signature changes. `server.WriteError` still refuses outside a host
     route. `CallerFrom`'s doc comment, and `ReportProvider`'s, say this.

     A provider never decides who may see a report. `audit:read` and the
     tenant the middleware resolved are the whole of that, because RBAC is
     answered in one place (§6). So a provider's `ErrDenied` is a fault,
     `502 report_provider_failed`, and never a `403`.
7. **Control records no generation, and says so.**
   - *Why.* The control stream records operator acts that change something
     (`operator.<noun>.<verb>`, §7). A report is a read under `audit:read`,
     like `GET …/audit/records`, which records nothing either. Enterprise
     records its own acts and never a second copy of Control's (its E6), and
     item 6 gives it the caller to record one with.
   - *What to build.* Nothing on the control stream. The access log line
     carries what item 5 lists. `ReportProvider`'s doc comment says Control
     writes no record of a generation, so a pack that keeps one writes it
     itself.
8. **The listing degrades per provider, and never as a whole.**
   - *Why.* A provider omitted from the listing reads as "no packs", which is
     the answer an outage must never look like (M11). One provider's failure
     must not hide another's packs.
   - *What to build.* `GET …/reports` answers `200` whenever Control itself
     could answer. Each registered provider is one entry, in registration
     order: its `provider`, its `version`, and its `reports`, each with
     `report`, `formats` (their wire codes) and `parameters` (`name`,
     `required`, `values`). A provider whose `Reports` failed, or whose
     descriptors item 2 refuses, carries `error` with
     `report_provider_unavailable` or `report_provider_failed` and an empty
     `reports`. With nothing registered it is `{"providers": []}`. It answers
     in M26's shape, or is listed exempt in
     `TestEveryListIsBoundedOrSaysWhyNot` (M26) with this reason: its size is set by the binary's registrations and each
     provider's catalogue, never by the estate. Decide which from M26 and
     0028's inventory, and say which in the learnings.

The error answers, together:

| From | Status | Code |
| --- | --- | --- |
| a provider not registered, or a report it does not list for this tenant | `404` | `not_found` |
| Control's own check against the descriptor (item 2) | `400` | `invalid_request`, naming the field |
| `Generate`'s `ErrNotFound` | `404` | `not_found` |
| `Generate`'s `ErrInvalid` | `400` | `invalid_request`, with the provider's message, as `policy_invalid` carries the compiler's |
| `ErrUnavailable` from either call | `503` | `report_provider_unavailable` (new) |
| any other kind, an error that is not an `*ext.Error`, a refused descriptor, or a `Report` item 3 refuses | `502` | `report_provider_failed` (new) |
| the per-node ceiling (item 4) | `503` | `report_generation_busy` (new), with `Retry-After` |

Never a `401`. Never a `403` from a provider: the only `403`s are the
middleware's, for a missing `audit:read` or a tenant out of scope. Declare
the three new codes in `north.AllCodes`.

## In scope

### 1. The report service (`internal/report`, new)
- `report.New(providers []ext.Bound[ext.ReportProvider], opts)`, built in
  `internal/daemon/north.go` from `d.extensions.ReportProviders()` and handed
  to `north.Options`.
- `List(ctx, tenant)`: item 8's entries, with item 2's descriptor checks and
  item 4's concurrent calls under the caller's bound.
- `Generate(ctx, tenant, req)`: item 2's lookup and checks, then
  `Generate`, then item 3's checks on the `Report`. It returns the report or
  a classified error the handler maps through the table above.
- The ceiling of item 4, as a counting semaphore on the service. It is
  acquired before either provider call and released when the body is closed.
- It never imports `internal/httpapi`: the handler maps its errors, and the
  service knows nothing of HTTP.

### 2. The routes (`internal/httpapi/north`)
- In `auditRoutes`, under `identity.PermAuditRead`:
  - `GET …/reports` — "every registered report pack this tenant can
    generate, per provider";
  - `POST …/reports/generate` — "generate one registered report and stream
    it".
- `reports.go`, new: both handlers. The generate handler is the one writer in
  this package besides `writeJSON`. Say so where `writeJSON`'s comment says
  it is the only one, and keep every error answer on `writeJSON`.
- The per-route bound of item 4. `withLimits` runs before the router, so a
  route that carries its own bound needs the timeout moved to where the route
  is known. Choose how, keep the body limit where it is, and prove with a test
  that no other route's bound changed.
- `withRecovery`, per item 5.
- Item 6: the caller made visible to `server.CallerFrom` for the two calls.
- The three codes, in `errors.go` and `AllCodes`.
- 0038's disjointness tests hold with both routes in Control's API
  namespace, and `TestEveryListIsBoundedOrSaysWhyNot` holds as item 8 says.

### 3. Configuration (`internal/config`)
- `north.report_timeout` and `north.max_concurrent_reports` in
  `NorthConfig`, with their defaults and their start-up refusals, in
  `config.example.yaml` with a comment each.

### 4. `ext/` and `server/`: doc comments, and no signature
- `ReportProvider`: what Control does with a registered provider. It lists it
  at `GET …/reports`, calls `Reports` before every `Generate`, generates only
  what the provider listed for that tenant, checks the request against the
  descriptor, bounds and caps generation, answers each error kind as the
  table says, makes the caller visible through `server.CallerFrom`, and
  records nothing of a generation.
- `ReportDescriptor`: what makes a descriptor servable (item 2).
- `ReportRequest`: `From` inclusive and `To` exclusive.
- `ReportFormat` and `Report`: the media type of each format, and that
  `Format` and `ContentType` must agree with the request (item 3).
- `server.CallerFrom` and `north.HostCallerFrom`: where they answer now.
- `ext/README.md`'s `ReportProvider` row, if it no longer says what is true.
- No exported identifier is added, removed or changed. `make release-check`
  reports no break.

### 5. The documents
- `docs/PLAN.md`, revised in place:
  - §3's components: `internal/report`, and `internal/httpapi/north`'s second
    writer and per-route bound;
  - §7: a "Reports" bullet beside "Query" and "Export", saying what Control
    serves and what stays a host's;
  - M15's wording on this point, if it reads as if Control serves no report;
  - §10's row, revised to what was built.

  §6 is unchanged: `audit:read` and `report:write` already say what they
  cover.
- The console prompt (0045) already lists a Reports screen. Correct it if
  what was built differs from what it expects.
- `CHANGELOG.md`: the release this phase cuts, a MINOR with no
  `### Breaking` (M23). Its entry names the two routes, the two configuration
  keys and the three codes.

## Out of scope
- **Enterprise's half** (its 0009): the packs, their frameworks and figures,
  scheduled generation, the history of generated reports, recertification
  campaigns, and everything under `report:write`.
- **Reports of Control's own.** The point is core, and the audit query,
  explain and simulation are Control's reporting (M15). Registering a
  provider of Control's is not asked, and a pack is a regime's question.
- **Scheduled or asynchronous generation**, and storing a generated report.
  A report is generated on request and streamed. A host that keeps
  artefacts keeps them itself.
- **A cross-tenant report** (M24, Enterprise's E11). `ReportRequest.Tenant`
  is one tenant, and Control serves no cross-tenant route of its own. A host
  may serve one as a cross-tenant read.
- **A record of a generation on the control stream** (item 7).
- **Any new `ext` method, field or signature.**
- **The console screens** (0045).
- **`contract/` (M1).** Nothing on the wire to a proxy changes.

## Acceptance criteria
Each criterion is a test over the wire in `internal/httpapi/north`, with fake
providers registered through `server.Options.Registry` where it needs a host,
and in `internal/report`'s own tests otherwise.

**The listing**
- Two providers registered. Each is listed with its reports, in registration
  order, for an `auditor` session and for an `audit:read` API token.
- A provider that lists a report for tenant A only: A's listing shows it, and
  B's does not.
- One provider's `Reports` fails and the other's succeeds: `200`, the failed
  one listed with its code and no reports, the other intact. One that never
  answers is listed `report_provider_unavailable` within the request's bound.
- A descriptor with a format outside the enum, a repeated `ReportID` or no
  formats: that provider is listed `report_provider_failed`.
- Nothing registered: `{"providers": []}`.
- `TestEveryListIsBoundedOrSaysWhyNot` passes, by the shape or by the exemption item 8
  names.

**Access**
- `integration` and `requester` are `403 forbidden` on both routes. A tenant
  outside the credential's scope is `403 tenant_out_of_scope`. A body cannot
  select a tenant.
- Two tenants: a report listed only in A is `404` from B, and `Generate` is
  never called. The fake provider counts its calls.

**Checking the request**
- Each case is a `400 invalid_request` naming its field, with `Generate`
  never called: a format the descriptor does not list, a missing required
  parameter, a parameter the descriptor does not name, a value outside a
  closed set, a missing `from` or `to`, an unparseable one, `from` not before
  `to`.
- An unknown provider and an unknown report are `404 not_found`.

**Generating**
- The body arrives byte for byte, with the format's media type,
  `X-Content-Type-Options: nosniff` and `Content-Disposition: attachment`.
  The filename is safe when the `ReportID` holds `/`, `"`, a space or a
  newline.
- The first bytes reach the client before the provider's body has finished:
  a fake body that blocks after its first chunk until the client has read it
  completes. Control does not buffer a report.
- A provider whose `Report.ContentType` is `text/html`, or whose `Format` is
  not the one requested, is `502 report_provider_failed`. No byte of its body
  is written, and its body is closed.
- `Generate` answering each kind: `ErrNotFound` `404`, `ErrInvalid` `400`
  with its message, `ErrUnavailable` `503 report_provider_unavailable`, and
  `ErrDenied`, `ErrMalformed`, an `*ext.Error` of `KindInternal` and a plain
  error `502 report_provider_failed`. None is a `401`, and none from a provider is
  a `403`.
- A body that fails before its first byte is answered in the envelope. One
  that fails after it ends the response broken (a Go client reads
  `io.ErrUnexpectedEOF`), never as a clean `200`. Nothing of `withRecovery`'s
  envelope is appended, and the access log records the abort.
- An empty body is `200` with no bytes.

**Bounds**
- A generation that runs past `north.request_timeout` and within
  `north.report_timeout` completes. One that runs past `north.report_timeout`
  is aborted. Another route still stops at `north.request_timeout`.
- A client that disconnects cancels the context `Generate` and the body see.
- With `north.max_concurrent_reports: 1`, a second generation during the
  first is `503 report_generation_busy` with `Retry-After`. Neither provider
  method is called for it. After the first ends, the next succeeds. The
  listing is never refused for it.
- A `report_timeout` below `request_timeout` or above `1h`, and a
  `max_concurrent_reports` below `1`, are refused at start-up.

**The caller**
- Inside both `Reports` and `Generate`, `server.CallerFrom` returns the
  request's tenant, subject, principal and correlation id. `server.WriteError`
  outside a host route still renders `500 internal`.

**Throughout**
- 0038's namespace tests pass with both routes in Control's API namespace.
- `go build ./...`, `go vet ./...` and `go test ./...` pass. So do
  golangci-lint at CI's pin, `make contract-check`, `make license-check` and
  `make conform`. `make release-check` reports no break.

## Cross-repo impact
This phase answers an upstream request and changes what `ext/` does and
says, so it owes a downstream sync. Its PR MUST carry `## Cross-repo impact`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1), naming **every** consuming repository.

### `hoplock/enterprise`: the repository that asked
This is the consumer most easily forgotten, because it is already waiting
(`docs/CROSS-REPO-PROTOCOL.md` §5). It knows what it asked for, but not what
was built, and the session there that rewrites its prompts will be a fresh one.
State at least these obligations:

1. **Its 0009, "What Control does not do yet".** Rewrite it from what merged,
   and stop building around the gap. It needs:
   - the two routes, the body, and that the provider and the report are named
     in the body and never in the path;
   - every check Control makes against the descriptor, so its packs make none
     of them again, and what makes a descriptor servable;
   - the three media types, and that `Format` and `ContentType` must agree;
   - the bound, the ceiling and their defaults, and that a body failing
     partway aborts the response;
   - the error kinds and what each answers;
   - that `server.CallerFrom` answers inside both calls, so a pack can record
     who generated it, and that Control records nothing of a generation (its
     E6).

   Turn its skipped end-to-end criterion into a real one.
2. **Its own report routes.** Listing and on-demand generation are Control's.
   What stays its own is the history of generated reports (`audit:read`),
   schedules and campaigns (`report:write`), in the host's namespace 0038
   settled. Its 0009's route list, and its plan's §3 route table row for
   "report reads", say which is which.
3. **Its plan.** §4's `ReportProvider` row and the bullet saying neither
   method is called. Its console phase, if it planned a screen listing packs:
   Control's console lists them now.
4. **Its pin.** Every obligation holds from the release this PR cuts (M23),
   never from `main`. Name that release by version.

Queue the **"Downstream sync" kickoff** from `docs/KICKOFF.md`, verbatim except
for its blanks, as
`prompts/downstream/queued/control-PR#<n>-<short-description>.md`. Commit it
once the PR is open, and end the section by naming that file
(`docs/CROSS-REPO-PROTOCOL.md` §4.3).

### `hoplock/proxy`
Write **"None"**. The contract is untouched (M1), and the proxy consumes
neither `ext/` nor the north-bound surface. "None" is a finding.

### Who runs the sync
Not you. This PR merges first, and the sync runs afterwards in its own session
(`docs/CROSS-REPO-PROTOCOL.md` §2).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`, and add
`docs/learnings/0042-registered-reports-served-learnings.md`.
Enterprise's sync is written from its summary block, which MUST give:

- the two routes, the body, the listing's entry shape, and whether the
  listing answers in M26's shape or is exempt;
- the checks against the descriptor, and what makes a descriptor servable;
- the media types, and the headers Control sets;
- `north.report_timeout` and `north.max_concurrent_reports`, with their
  defaults, and what a body failing partway does;
- the error table: each kind, status and code;
- what `server.CallerFrom` answers inside a provider call, and that Control
  records nothing of a generation.

The context budget: this phase reads `ext/audit.go`, the north-bound chain,
`host.go`, `server/caller.go`, the daemon's wiring and the config. It writes
one new package, one handler file and their tests, and changes about eight
files. That fits under 60% if the listing and generation share one fake
provider across their tests rather than building one per case.
