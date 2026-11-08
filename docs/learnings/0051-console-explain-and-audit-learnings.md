# 0051 — the console's Explain and audit view — Learnings

## Summary
- **Shipped (`v0.38.0`, MINOR; migration `0025`; `ext`/`server`/`contract` untouched):** Explain, the audit view (records, blocked commands, session replay), the supervised badge as a link, and north-bound version `3` to serve them: `GET …/audit/records/{record}/capture` and `?principal=` on `…/audit/records` (Details: why, and who decided).
- **Explain consumes** `GET …/sessions/{session}/decisions` and `GET …/decisions/{decision}` (`decision:read`). **Deep links:** `/console/explain`, `/console/explain/sessions/<id>[?decision=<d_…>]`, `/console/explain/decisions/<d_…>`.
- **Audit consumes** `GET …/audit/records`, `…/audit/records/{record}`, `…/audit/records/{record}/capture`, `…/audit/blocked-commands` (all `audit:read`). **Deep links:** `/console/audit/records?subject=&target=&session=&decision=&principal=&kind=&severity=&range=|from=&to=&order=asc&limit=&record=`, `/console/audit/blocked-commands?label=name=value&…`, `/console/audit/sessions/<id>/replay[?step=<record>]`.
- **Components added to the set:** `Timeline` + `TimelineSkeleton`, `TimeRangePicker` (+ `readRange`/`rangeQuery`/`resolveRange`), text fields on `FilterBar`, `Tree`'s `expandAll`, `STATUS.unserved`; gallery page `time`. The second chart: `screens/AuditCounts.tsx`.
- **Supervisor (M19):** badge → `/console/audit/records?principal=<sup-…>`; `acting_operator` rendered beside its `sup-` principal as "the supervisor's word", never linked or filtered (`auditModel.actorOf`). Fixture: `sup-acme-ops` (`seed.yaml` `api_tokens`).
- **Decisions:** none added or amended; register unchanged. PLAN M19, §7 (Query) and §10 (0051's row) revised in place; `docs/API-VERSIONING.md` row `3`; **`ui/DESIGN.md` §3.6, §6, §7, §8, §10, §11 changed** (Details).
- **Gotchas:** the e2e server embeds `dist` at build — rebuild, then restart it; decision and grant ids are random per run (`data-volatile`); a decision record's `inputs.context` holds `proxy_id`/`hop_trail`, `inputs.connection` holds `session_id` — the reverse of what the Go field names suggest.
- **NEXT (0052):** the diff view joins the set; `useLoad` is now loading from the render whose key changed (Details). 0056 adds gaps to replay's `Terminal`, which already marks a missing capture where it falls.

## Details

### Why this phase moved the north-bound API (and who decided)

The prompt asks for "session replay for recorded sessions", and the console is
an API client with no private path (M2). No north-bound route served a
capture's bytes: `audit.Reader.Capture` existed "for replay" and nothing called
it. The session stopped and asked the user, who chose to add the route here
rather than ship a records-only replay and queue a phase. The same gap held
for the supervised badge: 0049 said it would link to "the audit view filtered
to that supervisor's actions", but the audit query had no principal filter,
and filtering one page in the browser would have read a page as the tenant
(M26). Both are additive and both moved the version to `3` (a new route or
request parameter moves it, `apiversion.go`).

- **`…/audit/records/{record}/capture`** answers `captureView`: `record_id`,
  `session_id`, `capture_bytes`, `capture_sha256` (the record's own, which its
  hashed body covers), `retained`, and `payload` (base64, the contract's
  encoding). No capture on the record, or no record: `404 not_found`. Retained:
  `200` with `retained: true` and no payload — the capture is gone, which is a
  different answer from never taken, and no new code was needed. A record that
  claims a capture the store lacks is a `5xx` (ingest writes captures first).
  Exempt from M26 as one object, bounded by the ingest limit on a capture.
- **`?principal=`** matches `attributes.principal_id`, which every record this
  server writes for an act carries and no proxy record does.
  `AuditQuery.PrincipalID` is bound into the cursor (`auditFilters`), so
  `TestEverySelectionFieldBindsTheAuditCursor` holds it. Migration `0025` adds
  `audit_records_principal_idx (tenant, (attributes->>'principal_id'),
  recorded_at DESC, stream DESC, chain_seq DESC) WHERE … IS NOT NULL`; the
  planner proves the equality implies the predicate (checked with `EXPLAIN`:
  an index scan in order, no sort). Without it, a supervisor that acted twice
  walks the whole tenant. `acting_operator` stays unfiltered, as M19 requires.
- Tests: `internal/httpapi/north/capture_test.go` (bytes and digest, 404s, a
  retained capture, the principal filter paged with its cursor refused
  without it); `instance_test.go` now expects `3`.

### What each screen does with what it reads

- **Explain** looks an id up as a decision when it begins `d_` (the server
  mints `d_` + base32) and as a session otherwise; a decision 404 offers the
  same id as a session. A session's decisions are put in hop order
  (`explainModel.inHopOrder`: hop-trail length, then instant) and the screen
  opens on its first deny, else its first unserved hop, else its innermost hop.
  A session comes to Denied if any hop denied, then Unserved (outage), then
  Allowed. The hop path joins every decision's `inputs.context.hop_trail`,
  each proxy with the decision it took where this server holds one, the next
  proxy a lone edge decision routed to, and the target last. `standing` (M28)
  reads as "a standing denial applied above policy". The directory a subject's
  groups came from is left for 0054, as the prompt says.
- **Audit records** reads one page (`limit` 100 unless the URL names one) and
  resets its page trail and the instant a relative range resolves against
  whenever the selection changes (`useSelectionState`), because a cursor binds
  its selection. Kind and severity are the contract's closed sets, named from
  the catalogue (`auditLabels.ts`); identifiers stay mono. The record drawer
  reads the record by id, so `?record=` works for a record not on the page;
  it names the attribute keys it knows (`KNOWN_ATTRIBUTES`) and skips the rest
  — the body, shown whole, carries every key (docs/API-VERSIONING.md's rule).
- **Blocked commands** needs a label: without one it is a teaching empty
  state, never a `400`. A row whose decision the store no longer holds is the
  partial state's named gap.
- **Replay** reads the session's records oldest first in pages of 500, opens
  on the last step (the whole session), and fetches each `stream` record's
  capture once, in order, up to the step (`useCaptures`). The terminal joins
  the chunks as bytes before decoding (a character can straddle two reads),
  drops CSI/OSC escapes, and applies CR, BS and LF (`terminalText`); it is a
  reading of the bytes, beside their digest. A capture gone or unreadable is
  shown where it falls and ends the run before it, so nothing is joined across
  it — the rule 0056 extends to stream gaps.
- **Counts** are of the page, never the tenant, and say so (caption names the
  page and whether the answer was cut). A table with figures beside one-ink
  bars: greyscale needs nothing more.

### Changes to `ui/DESIGN.md` in this PR, and why

1. **§3.6** adds `--rail-width` (300px): the column a timeline stands in
   beside what it explains, on Explain and replay. Layout dimensions are
   tokens.
2. **§6** adds **`Unserved`** to the outage row, and says a decision reads as
   its effect and a session as its worst hop. An unserved decision is an allow
   the server could not answer (§5.6) — M11 forbids drawing it as a deny, and
   no existing label said it.
3. **§7** specifies the timeline and the time range picker (their behaviour,
   keyboard, URL form and states), that a filter-bar field may be typed text
   applied on Enter or blur, and `Tree`'s `expandAll` for a hop path.
4. **§8 and §11** name the two charts: Fleet's bar and the audit counts.
5. **§10** says a substituted answer may be the server's own answer changed
   (the harness's new `patch` substitute), which makes a cut session or a
   retained capture from the seeded data without a route the server binds.

### The fixture (`ui/e2e/fixture/`)

`policy.yaml` (new, seeded by `seed.yaml`'s `policy:`): `ops-db-shell` (allow,
capture required, `record-session`), `vault-through-dmz` (allow, requires a
JIT grant of scope `vault-maintenance`), `no-shell-on-build-hosts` (deny with
a reason). `seed.yaml` adds subjects `grace` (sre) and `mallory`, three
targets, px-core-01's edge into `dmz` via px-dmz-03, and `sup-acme-ops`.
`audit.ts` (`seedAudit`, after `seedFleet`): an administrator's grant; five
real `/v1/authorize` calls as the proxies (sessions `sess-e2e-allow`,
`-deny`, `-rule-deny`, `-hops`); records by `/v1/logs/batch` (captures with
ANSI colour, a blocked `systemctl stop postgresql`); and the supervisor's
`session_kill` naming `Dana Ruiz` plus a `cache_invalidate` naming nobody.
Authorizing does not move a proxy's `contract_version` (the fixture's health
still counts every proxy at `0`), so Fleet's numbers are unchanged. The live
supervisor puts its badge, and the new navigation entries, on every screen,
so every baseline changed.

### Changes outside the new screens

- `shell/load.ts`: an answer is held with the key it answers; a read whose key
  changed returns loading from that render. Before, for one frame the pager
  named the next page over the last page's rows (the paging spec read rows
  twice and found it). Fleet's checks pass unchanged.
- `Shell` exposes the live supervisors on `useDeployment()`; `TopBar`'s badge
  is a `Link`. `Nav` takes a screen's `section` so Audit stays current across
  its three paths.
- `e2e/harness.ts`: `data-force-child` now names its state (it forced only
  hover), and the `patch` substitute.

### Verified in a browser

Every screen was driven against the seeded server at 1280 in both themes
before the checks were written; that found the context/connection mix-up, a
deny's `{}` snapshot shown as an answer, the native disclosure triangle (a
font glyph, now an SVG), and that the actor column was off-screen (moved
after severity). `explain-audit.spec.ts` asserts each acceptance criterion,
the keyboard paths, and no sideways scroll at 768px on Explain, records and
replay.

### Noticed, not changed

- A deny's 401 carries no decision id, so a proxy's record of a denied
  session names none; Explain finds it by session, which is the point (M4).
- The prompt's "0054 adds the directory" is still true: `mapping.directory_source`
  is in the answer and not rendered.
- 0050's note stands: preferences are keyed by the session's principal id.

### Cross-repo

No shared surface changed (`ext`, `server`, the contract and the cross-repo
protocol are untouched), but the release changes what Enterprise's binary
serves (north-bound `3`, two console screens) and the component set its
screens are built from, so a downstream sync for `hoplock/enterprise` is
queued (the PR's `## Cross-repo impact`). Proxy: none.
