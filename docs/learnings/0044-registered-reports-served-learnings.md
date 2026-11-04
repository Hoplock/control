# 0044 — registered reports served — Learnings

## Summary
- **Release `v0.31.0`, MINOR, no `### Breaking`; no migration; no exported identifier in `ext`/`server` changed (doc comments only).** No decision added; M15, M26 (an exemption) and §3/§7/§10 revised in place; register row M15 now renders in §7. Answers `Hoplock/enterprise#26`, item 5, **as asked**: the `reports` family is Control's.
- **Routes** (Control's API namespace, both `audit:read`): `GET /api/v1/tenants/{tenant}/reports` → `{"providers":[{provider, version, reports:[{report, formats:["json"|"csv"|"pdf"], parameters:[{name, required, values}]}], error?}]}`, registration order, `200` whenever Control can answer; `{"providers":[]}` with none. **Exempt from M26** (sized by registrations, not the estate). `POST …/reports/generate` body `{provider, report, from, to, parameters, format}` — provider and report never in the path; a `tenant` field is `400` (unknown field).
- **Checks, before `Generate`, in order:** `provider`/`report` present (`400 … required`) → ceiling → provider registered and `Reports(ctx, tenant)` lists the report (`404 not_found` naming both) → `format` present and listed → names not in the descriptor (`parameters.<name> invalid`, sorted) → `Required` missing (`required`) → value outside non-empty `Values` (`invalid`) → `from`/`to` present, RFC 3339 (`required`/`invalid`), `from < to` (`to not_after_start`). **Servable descriptor:** non-empty `ReportID` unique in the provider, ≥1 format each one of the three constants, parameter names non-empty and unique — else the whole provider is `report_provider_failed`.
- **Served as:** `application/json` · `text/csv; charset=utf-8` · `application/pdf`, never the provider's type; `X-Content-Type-Options: nosniff`, `Content-Disposition: attachment; filename="<id>_<from>_<to>.<fmt>"` (`[A-Za-z0-9._-]`, UTC `20060102T150405Z`), `Cache-Control: no-store`, no `Content-Length`, flushed per 32 KiB chunk. A `Report` whose `Format` ≠ requested, whose `ContentType` media type ≠ the format's, a CSV charset ≠ UTF-8, or a nil `Body` → `502`, body closed unread.
- **Bounds:** `north.report_timeout` (default `10m`, or `request_timeout` if longer; refused below `request_timeout` or above `1h`) bounds the generate route alone, request start to last byte; `north.max_concurrent_reports` (default `2`; negative refused, `0` = default) caps generations per node, released when the body closes. A body failing after its first byte, or the bound running out, aborts with `http.ErrAbortHandler` (client reads `io.ErrUnexpectedEOF`); before its first byte it is answered in the envelope.
- **Errors:** Control's lookup / `Generate`'s `ErrNotFound` → `404 not_found`; Control's checks → `400 invalid_request {field, problem}`; `Generate`'s `ErrInvalid` → `400 invalid_request`, message = the provider's words; `ErrUnavailable` (either call), or no answer/first byte by the bound → `503 report_provider_unavailable`; anything else (`ErrDenied` too, a non-`*ext.Error` even wrapping a sentinel, a panic, an unservable descriptor, a refused `Report`) → `502 report_provider_failed`; over the ceiling → `503 report_generation_busy` + `Retry-After: 10`. Never `401`, never a provider's `403`.
- **Caller:** `server.CallerFrom` (`north.HostCallerFrom`) answers inside `Reports` and `Generate` with tenant, subject (zero for a token), principal, break-glass and correlation id; `server.WriteError` still refuses there (`500 internal`). **Control records nothing of a generation**; the access log line carries `report_provider`, `report_id`, `report_format`, `report_from`, `report_to`, `report_outcome` (`complete` | `aborted` | the code), `report_bytes`, and `aborted: true` on an abort.
- **Key files:** `internal/report/{doc,service}.go`; `internal/httpapi/north/{reports,router,middleware,host,errors,server,routes}.go`; `internal/config/config.go`; `internal/daemon/north.go`; `ext/audit.go`; `server/caller.go`.
- **NEXT:** Enterprise's sync is queued as `prompts/downstream/queued/control-PR#131-registered-reports-served.md`. A route needing its own bound sets `Route.Timeout`; `withLimits` asks the router (`boundFor`), and `TestEveryRouteButTheGenerationKeepsTheListenersBound` holds every other route to the listener's.

## Details

### Where the per-route bound lives

`withLimits` runs before the router and used to apply `north.request_timeout`
to everything. A child context cannot extend its parent's deadline, so the
bound had to be chosen where the route is known. Two shapes were possible:
move the deadline into the router's wrap (`enforce`), or keep it in
`withLimits` and ask the router which route the request will reach. The second
was built: `Router.boundFor` calls `mux.Handler(req)` — which matches exactly
as dispatch will and does not touch the request — and looks the pattern and
method up in `bounds`, filled only for a route with a non-zero
`Route.Timeout`. So 404s, 405s, consoles and every other route keep exactly the
deadline they had, set exactly where it was, and the body limit did not move. A
`GET` of the generate path (a 405) keeps the listener's bound; the test says
so. `HostRoute` has no `Timeout`: a host cannot lengthen its own bound.

### Never a short report

- The status waits for the first byte. The body is read on its own goroutine
  (two 32 KiB buffers, ping-ponged: the handler receives chunk k only after it
  wrote chunk k-1, so the reader may refill that buffer), and the handler
  selects on chunks and on the request's context. So a provider that blocks in
  `Read` ignoring its context cannot hold the handler past the bound.
- Abandoned mid-stream, the body is closed on a goroutine beside the blocked
  `Read` (the `ext.Report` doc now says `Close` may be concurrent with `Read`
  and should unblock it); drained, it is closed synchronously.
- After the status, the write deadline is set to the request's deadline, so a
  client that stops reading cannot hold the response (or the ceiling slot)
  past the bound. **Gotcha:** with no `WriteTimeout` on the `http.Server`,
  net/http never resets a connection's write deadline between keep-alive
  requests, so a completed report clears it (`SetWriteDeadline(time.Time{})`).
- `withRecovery` re-panics `http.ErrAbortHandler`; `withLogging` now logs from a
  `defer` (and re-panics) so an aborted response still has its access log line.
  Both were mutation-tested: removing either fails the abort test.

### The ceiling and abandoned calls

Each `Generate` runs the provider-facing half (lookup, `Reports`, checks,
`Generate`, the `Report` checks) on one goroutine; the handler waits for it or
for the context. Abandoned at the bound it answers `503
report_provider_unavailable`, and the goroutine, when the provider finally
returns, closes any body it got — which releases the slot. **A provider that
ignores its context keeps its slot until it returns**, deliberately: it is
still running in the process beside the decision path, which the ceiling
protects (M5). A provider that never returns wedges that node's generation at
the ceiling until restart, while listing and every other route keep working.
The default of 2 was kept: nothing in the decision path's budget (§5, M5)
argued for another number, and two lets one long pack run without blocking a
short one.

### Listing

All providers are asked concurrently with the request's own context; the
handler waits until every one answered or the context ends, then lists the
silent ones `report_provider_unavailable`. So a listing with a silent provider
answers at the request's deadline (30 s by default), not earlier. Answers
arriving late are dropped into a buffered channel, so nothing leaks blocked.
Descriptors are cloned before they are kept.

### Choices beyond the prompt's letter

- `Cache-Control: no-store` on a report, and a CSV whose `ContentType` names a
  charset other than UTF-8 is refused: Control serves CSV as UTF-8, so serving
  Latin-1 under that label would be wrong. Both are in the `ext` doc.
- A nil `Report.Body` is `502`, not an empty report.
- `max_concurrent_reports: 0` takes the default, as every "at least 1" key in
  this file does (`audit.sinks.max_in_flight`); YAML cannot tell `0` from
  absent. Negative is refused.
- `report_timeout` absent defaults to `max(10m, request_timeout)` and is then
  exempt from the 1 h ceiling (an unexported `reportTimeoutDefaulted`), so a
  file with a request timeout past an hour, legal before, still loads.
- The provider's `ErrInvalid` message is stripped of the `ext.Errorf` sentinel
  prefix, control characters become spaces, and it is cut at 512 bytes on a rune
  boundary.
- Only an `*ext.Error` carries a kind. A plain error wrapping `ext.ErrNotFound`
  is `502`, per the prompt's table; `ext.KindOf` would have read it as not
  found.
- Running out of time before the first byte is `503
  report_provider_unavailable` (as the listing's silent provider is), not
  `502`: a timeout is "not now", the 0013 convention for a provider's timeout.

### Tests

`internal/httpapi/north/reports_test.go` (one shared fake, `packs`): listing
order, shape and licensing per tenant; per-provider degradation including a
silent provider and four unservable descriptors; access (integration,
requester, out of scope, a body naming a tenant, a report licensed only in A);
every `400` field and both `404`s with `Generate` never called; byte-for-byte
body, headers, a filename from `a/b "c\nd`, the request `Generate` saw, the
caller in both calls, the access log line and no audit record; streaming (the
first bytes arrive while the provider blocks); a refused `Report` closed
unread; each error kind and a panic; a body failing before/after its first
byte and an empty body; the route's own bound vs the host route's; every route
but generate bound by the listener; a disconnect cancelling the provider's
context and closing the body; the ceiling refusing a second generation without
calling the provider, never the listing, and admitting the next once the first
closed. `internal/report/service_test.go`: unservable descriptors, panics in
each method and in the body, classification, the provider message, an
abandoned call keeping its slot, a double `Close` releasing once, refusals
releasing. `server/report_test.go`: a pack registered through
`server.Options.Registry` listed and generated through `server.Main`, with
`server.CallerFrom` inside both calls and `server.WriteError` refusing.
`internal/config`: defaults and refusals. Conformance (`conform-self`, all
groups): 58/58.

### Cross-repo

Downstream sync for `hoplock/enterprise` owed and queued as
`prompts/downstream/queued/control-PR#131-registered-reports-served.md`
(control#131's `## Cross-repo impact`): its 0009
rewrites "What Control does not do yet" against `v0.31.0`, drops its own
listing/generation routes, keeps schedules, campaigns and history in its own
namespace, and turns its skipped end-to-end criterion into a real one.
`hoplock/proxy`: none — the contract is untouched.
