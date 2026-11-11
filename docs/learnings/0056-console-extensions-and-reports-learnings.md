# 0056 — the console's Extensions and Reports — Learnings

## Summary
- **Shipped (`v0.43.0`, MINOR; no migration; north-bound API unchanged at `5`; `ext`/`server`/`contract` untouched):** Extensions at `/console/extensions` and Reports at `/console/reports[?provider=&report=]`, the `FeedLag` component, eight statuses, and a fixture host that registers an action handler, two sinks and two report packs.
- **Routes consumed** (`ui/src/api/routes.ts`): Extensions — `GET …/extensions` (`extension:read`), reading each row's `registered`, `registrations[]` (`provider`, `version`, `default`, a sink's `lag`), `when_absent`, the archive's row `lag`, and `started`/`start_error` on `ActionHandler` and `IdentitySync`; never `absent` (English: the console's catalogue says it, `extensionsModel.ts` `POINTS`). Reports — `GET …/reports` and `POST …/reports/generate` (both `audit:read`), the latter through the new `ApiClient.download` (whole body or `broken`, never a short file; filename from `Content-Disposition`).
- **The lag component is `ui/src/components/FeedLag.tsx`** (`FeedLagView`, `FeedLagSkeleton`, `lagStatus`, `RETAINED_KIND`), in the gallery at `?section=lag`. 0057 renders the archive's lag with it: `<FeedLagView lag={row.lag ?? null} label=… />` — `captures_waiting` shows when present; `null` is Not reported.
- **How a host's report names reach the catalogue:** looked up in `HostCatalogueContext` (`ui/src/i18n/host.ts`) at `<host>:reports.<pack provider>.<report>` and `….parameters.<name>`, else the code (`Ident`, `translate="no"`). **Nothing provides that context yet** — `server.Options` has no host-console field — so every report is named by its code today. **0068** (queued) adds the field and loads the catalogue.
- **Statuses added** (DESIGN.md §6): `running`, `caughtUp` (allow); `notStarted`, `failing` (warn); `behind` (info); `unavailable`, `failed` (outage); `notRegistered` (unknown). Error sentences: `report_provider_failed`, `report_provider_unavailable`, `report_generation_busy`.
- **Decisions:** none added or amended; register unchanged. PLAN §3 and §10 (0056's row, new 0068 row) revised in place; **`ui/DESIGN.md` §6, §7, §9 changed** (Details).
- **Gotchas:** every console baseline image changed (two navigation entries); `extensions` waits for `e2e-lake` to read caught up (fixture `audit.sinks.idle_interval: 1s`); an overtaken sink cannot be held still on one server (catching up zeroes `skipped_behind`), so it is the server's answer patched in the browser; `prepare` + `[data-refresh]` re-reads a screen with a substitution installed after load.
- **NEXT (0057):** reuse `FeedLag` for the archive's lag in the audit view; the Extensions screen already renders the archive's row with it when one is registered.

## Details

### What each screen does, and why it reads that way

**Extensions.** One panel per point, in the listing's order, after an "In
play" panel that lists every registration whose `default` is false — the
quickest answer to "is Enterprise (or any host) in play here?". A point's
status (`pointStatus`): nothing registered → `Not registered` (neutral), with
the console's sentence for what Control does instead plus a line saying core
or disabled; `started: true` → `Running`; `started: false` → `Not started`
(warn) with `start_error` as a sentence (`START_ERRORS`, the eight
`ext.Kind` strings) and its code; anything else registered → `Active`. Sink
registrations each carry a `FeedLagView`; the archive's row carries one when
an archive is registered. Partial gaps: a sink or archive with no `lag`, and a
point code this console does not know (rendered by its code). Empty
(`points: []`) never happens on a real server; the empty state says so and
offers a refresh.

**Reports.** One panel per provider. A provider with `error` is `Unavailable`
(outage tone) with the error code's sentence and "this is not a pack with no
reports"; it is also a partial gap. A provider with zero reports and no error
says it offers none. Each report row: its name (host catalogue) or code, its
formats as badges, its parameter count, and "Generate…", which writes
`?provider=&report=` (a deep link; both are data, never path segments). The
form: From/To (`datetime-local`, operator's zone, default the last seven days
to the minute, `data-volatile` so screenshots mask them), each parameter (a
`<select>` for a closed set with an empty first option, an LTR mono input for
free-form; "required" marked beside the code), the formats as radios. Client
checks (`reportsModel.check`) mirror the server's; a server `invalid_request`
naming a field is placed at that field (`faultOf`). Outcomes: saved (the
browser's download, `shell/save.ts`, under the server's filename) with a
toast and an inline "Saved as"; refused (form-level when not a field); 5xx →
`Failed` + the outage sentence; a body that breaks → `Failed` + "nothing was
saved". No pack registered → the empty state, whose one action opens the
audit view (M15's answer for the point).

### The fixture host's extensions (`ui/e2e/fixture/host/extensions.go`)

| Registration | What it does | What the screen shows |
| --- | --- | --- |
| `e2e-directory` (IdentitySync, 0055) | provisions the directory at Start | `Running` |
| `e2e-actions` (ActionHandler) | `Start` returns `KindUnavailable` | `Not started`, `unavailable` |
| `e2e-siem` (AuditSink) | every `Export` fails | `Failing` |
| `e2e-lake` (AuditSink) | confirms every batch | `Caught up` |
| `example.com/e2e/compliance` (ReportProvider) | `access-review` (csv, json; `scope` closed+required, `owner` free) and `privileged-sessions` (csv; body breaks after 23 bytes) | listed; generates; `Failed` |
| `example.com/e2e/flaky` (ReportProvider) | `Reports` returns `KindUnavailable` | `Unavailable`; the screen is partial |

Every other screen's baseline is unaffected by these except through the
navigation: delivery writes no audit record (M8), and no seed reads a sink.

### What is substituted in the browser, and why

Three states one server cannot hold still: a sink a retention pass overtook
(`OVERTAKEN` — a real one needs a signed retention pass that would change
the audit view's baseline, and a sink that then catches up within a second
zeroes `skipped_behind`, `store/feed.go`); Control alone (`BARE`, every point
empty but the coordinator's default); and the handler running / the sync not
started (the inverse of the fixture's). Each is the server's own answer
patched (`kind: "patch"`), installed in `prepare` and read again through the
screen's Refresh. The PR captions those images as substituted.

### Tests

- `ui/test/extensions.test.tsx` (17): each lifecycle point running / not
  started (kind named, no outage tone) / none; two sinks each on its own row,
  failing and overtaken, neither outage nor deny; caught up; none registered
  shows no lag; a missing lag is Not reported and a gap; `POINTS` equals
  `ext`'s catalogue; `extendedBy` skips Control's defaults.
- `ui/test/reports.test.tsx` (12): the form's defaults, client faults, the
  body posted, server faults at fields, an unavailable pack never "none", a
  name from a host catalogue vs the code, foreign keys ignored,
  `attachmentName`, a broken stream is `broken`.
- `ui/e2e/extensions-reports.spec.ts` (10, CI job "extensions and reports"):
  the acceptance criteria against the fixture host, including a real JSON
  download whose content is read back.
- The RBAC harness and the route test cover both screens by registration
  (every role the fixture signs in holds `extension:read` and `audit:read`;
  generating changes nothing, so it is not a registry `mutation`).

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§6**: five rows gain the new statuses, and a paragraph says how an
   extension point, a downstream consumer and a report pack read. Needed
   because "registered and not started", "failing" and "unavailable" each had
   to be placed on one side of M11 on purpose: a failed `Start` and a failing
   sink are degraded (Control serves; the local store is the record), while a
   pack that cannot answer and a broken generation are outages of the pack.
2. **§7**: the feed lag joins the composites, with its states, because 0057
   renders the archive's lag through it and the prompt requires it in the set.
3. **§9**: the host catalogue keys a report and its parameters are named
   under, because a host must know which keys to write (and 0068 loads them).

### Noticed, not changed

- **No host console can be mounted through `server.Options`** (0049 recorded
  the shape). Queued as **0068**; until it lands, `HostCatalogueContext` is
  never provided and every report is named by its code — correct per M21,
  and tested both ways.
- Loading a host's **screen modules** into the shell, and a navigation entry
  for them, is not queued: 0068's prompt makes its learnings name that phase.
- `GET …/extensions` answers `absent` and `control_ships` in English; the
  console ignores both (M21). A point added to `ext` without a console
  sentence renders by its code and is a named gap, so the console never lies
  about it — but `test/extensions.test.tsx` pins `POINTS` to today's eleven
  so the gap is noticed.

### A capture race CI found, fixed here

`inventory-subjects` (0055) failed CI's baseline once (light, 2% of pixels,
same size). Its screen holds two tables — the identities list and the ids a
denial holds with no record, a second `GET …/subjects?record=missing` — and
its `ready` waited for "a data row" in any table. When the second read
answered first, `ready` passed while the list was still loading, `byName`
found no name column and silently did not sort, and the list was captured in
id order, where the directory's random `dir-…` ids move rows. Reproduced by
holding the list's answer 1.5 s in the browser (the old steps captured it
unsorted; the new ones sorted). `ready` is now `subjectRows` (a row of the
table that has the name column), and `byName` waits for `aria-sort` on that
column after its click. This phase's extra load (two sinks polling every
second) made the race likelier; it was 0055's.

### Cross-repo

No shared surface changes (`ext`, `server`, the contract and the decision ids
are untouched). The release changes what Enterprise's binary serves, and
Enterprise's report packs now have a screen that lists and generates them, so
a downstream sync for `hoplock/enterprise` is queued (the PR's
`## Cross-repo impact`). Proxy: none.
