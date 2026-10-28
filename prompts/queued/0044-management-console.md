# 0044 — Management console (web UI)

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially §2 (M2 — the console is a north-bound client and
  authenticates like one; **M20** — the design is specified, not improvised;
  **M21** — the console is localisable from its first screen and English is the
  only locale that ships; **M27** — the four namespaces on the north-bound
  listener, and where this console and a host's screens are served), §3 (`ui/`,
  and `internal/httpapi/north` at "A host binary's routes (M15) enter **the
  same table**").
- **`ui/DESIGN.md` — in full, before writing any markup.** It is the design
  system this phase is built to: tokens, type scale, layout, status vocabulary,
  component inventory, state matrix, internationalisation, and the checks that
  enforce all of it. It
  is binding under M20, and it is not a style suggestion you may improve on
  mid-implementation — if it is wrong, change it in this PR and say so.
- `docs/learnings/0025-archive-before-retention-learnings.md` and
  `docs/learnings/0026-audit-sink-delivery-learnings.md` — the summary blocks,
  and `docs/PLAN.md` **M8** with §7 at "Retention has to remove a chain's
  TAIL" and at "Export", for the archive's and each sink's lag on
  `GET …/extensions` and the retention answers the audit view shows (below).
- `docs/learnings/` — read summaries; open `0019` (**the API this consumes** —
  every route and its required role), `0011` (RBAC), `0010` (audit query),
  `0006` (proxy health), and `0028` with the chunks it queued: every list this
  console renders pages through the bounded-list shape 0028 decided (M26), and
  never assumes one answer holds the whole tenant. Open `0038` too: it
  reserved where this console is served, where a host's screens and assets go
  and how a host's catalogue keys are namespaced (M27), and it rewrote this
  prompt to match. Build into those namespaces, and never serve a console path
  inside the one reserved for a host. The code is
  `internal/httpapi/north/console.go` (`Console`, the mount) and
  `namespace.go` (the prefixes); the tests are
  `namespace_internal_test.go`. Open `0041` for the report routes the Reports
  screen consumes (below).
- **`docs/PLAN.md` M28 and `docs/learnings/0039-action-executor-lock-and-quarantine-learnings.md`
  (open it in full), for the subject lock and the target quarantine, which
  this console shows and lets an operator place and lift (below).** The routes
  are `POST …/subjects/{subject}/lock` and `…/unlock` and
  `POST …/targets/{target}/quarantine` and `…/release`, all under
  `session:revoke`. `GET …/subjects` and `…/targets` carry `locked`, `lock`,
  `quarantined` and `quarantine`. The error codes are `target_quarantined` and
  `standing_denial_undelivered`. Enterprise's inbound action channel relies on
  an operator being able to release a lockout here.
- **`docs/PLAN.md` §6 at "The directory door", M28's paragraph on the
  directory's deactivation, and `docs/learnings/0040-identity-sync-and-deprovisioning-learnings.md`
  (its summary), for a subject a registered identity sync provisions, which
  this console shows and does not let an operator edit (below).** `GET
  …/subjects` and `GET …/subjects/{subject}` carry `directory_managed`,
  `directory` (`provider`, `external_id`), `deactivated` and `deactivation`
  (`deactivated_at`, `reason_code`); `PUT` and `DELETE` of such a subject are
  `409 subject_directory_managed`. `GET /api/v1/session/methods` carries a
  connector's `directory_claim`, and the identity sync's row of `GET
  …/extensions` carries `started` and `start_error`.
- `docs/PLAN.md` §4 at "One endpoint carries two observations", §5.2's
  algorithm bullet (the floor and the bans), and **M17**, for what
  `Hoplock/proxy#69` added and 0019 serves: the impact preview, the fleet
  coverage view and the emergency runbook (below). The same bullet, from
  "Refusing exactly what the proxy refuses", covers what `Hoplock/proxy#72`
  added: what each build's profiles offer, and a ban's per-build finding. In
  the **Hoplock Proxy repository**, `api/README.md` "Banned algorithms" states
  the runbook. It is cited here and never restated.
- `docs/PLAN.md` §7 at "A gap in a proxy's stream is a record, and it is not a
  break in the chain", and **M11**, for what `Hoplock/proxy#71` added and 0019
  serves: the `logging.gap` records that say where a proxy's stream has a
  hole, and why (the audit view, below). In the **Hoplock Proxy repository**,
  `api/README.md` "When records do not arrive" specifies the record. It is
  cited here and never restated.

## Objective
Give Hoplock Control an operator console. A self-hosted deployment should be
usable without `curl`: see the proxy fleet's health, manage targets and
identities, author and simulate policy, read the audit trail, and — the one
screen that sells the product — **paste a session id and get the full
explanation of why access was allowed or denied**.

**It must also be good to look at and to use.** This is the surface a self-hoster
judges the whole product by, and the one an operator reads at 02:00 during an
incident. "Works" is half the bar here; `ui/DESIGN.md` is the other half, and it
is written so that half can fail a review rather than merely disappoint one.

## In scope
- `ui/`: a single-page app served by `cmd/hoplock-control` from embedded assets
  (`embed.FS`), so a deployment is still one binary. No separate web server, no
  Node runtime in production.
- **Served inside its own namespace, and nowhere else (M27).** 0038 reserved
  four prefixes on the north-bound listener, each a constant in
  `internal/httpapi/north`, and built the tests that hold them apart:

  | Namespace | Constant | Prefix |
  | --- | --- | --- |
  | Control's API | `ControlAPIPrefix` (less `HostAPIPrefix`) | `/api/v1/` |
  | a host's API | `HostAPIRoot(provider)` | `/api/v1/hosts/<provider>/` |
  | **this console** | `ConsolePrefix` | `/console/` |
  | a host's console | `HostConsoleRoot(provider)` | `/host-console/<provider>/` |

  - Mount the console through `north.Options.Consoles`, as one `north.Console`
    with no `Provider`: its `Patterns` (the shell, `assets/`, the catalogue,
    and the history fallback as a `{…...}` subtree), its `Catalogue` keys and
    one `Handler`. The mount refuses at start-up any pattern outside
    `/console/`, and `/` redirects to `/console/` only once it is mounted.
    Wire it in `internal/daemon` where the north server is built.
  - Every path this console serves is under `/console/`: the shell, every
    asset, and every deep link and URL-serialisable filter it writes
    (`/console/audit/records?subject=…`, never `/audit?…`). The app's router
    base is `/console/`, and the build's asset base is `/console/assets/`.
  - Nothing is served at `/` but that redirect. The history fallback answers
    only inside `/console/`. An unknown path outside every prefix is
    Control's `404` envelope, and a path under `/api/v1/` is never answered
    with the console's index.
  - A host's screens and assets load from `/host-console/<provider>/`, never
    from inside `/console/`, and none of this console's paths sits under the
    host's. This console's shell may load a host's screen modules from there;
    a link from one console to the other names the other's prefix.
  - Catalogue keys (M21): this console's carry no `:`; a host's are
    `<provider>:<key>` (`hoplock/enterprise:approvals.title`), and one equal to
    a key of this console's is refused when the host's catalogue is mounted.
    Pass the console's real `en.json` keys as `Catalogue`, so the refusal is
    judged against them.
  - If a screen needs a path outside these rules, the namespace is wrong.
    Change M27 in this PR and say why, rather than serving around it.
- **It is an API client and nothing more.** The console calls the north-bound
  API (0019) with the operator's own session; it never reaches into the database
  and never has a privileged path of its own. Any capability the console has,
  the API has, and RBAC (0011) applies identically — a console that can do what
  the API forbids is a second, unaudited authorisation system.
- Screens, in priority order:
  1. **Fleet** — proxies, zones, health, last heartbeat, contract version, which
     have live relay registrations (0006), and each proxy's configuration state
     as the proxy **reported** it (0019, proxy D18): desired and running
     version, and `applied`, `pending_restart` (with the settings that are
     waiting on a restart), `rejected` or `fetch_failed` (with `last_error`). A
     proxy that has never reported is shown as its own state, never as
     `applied`. **Algorithm-floor coverage** too (`Hoplock/proxy#69`): which
     `algorithm_floor` levels each proxy's build declares, with the key
     exchanges each level accepts **in that build**, as 0019 serves them. During
     a rolling upgrade two builds may accept different exchanges for one level.
     Show that as *in progress* (M19), never as a fault, and name the proxies
     that cannot enforce a level yet. A proxy that has declared nothing is
     `Not reported`, never "no floor". Beside the levels, show what each
     profile the build accepts offers per axis, before any floor or ban
     (`Hoplock/proxy#72`, as 0019 serves it), so an operator reading a ban's
     per-build finding can see what that build offers. A proxy whose build
     declared no profiles is `Not reported`, never "offers nothing".
  2. **Explain** — a session or decision id in, the whole story out: identity,
     matched rule, mapping version, grant, obligations, route and hops. Deep
     links from the audit view.
  3. **Audit** — query by subject, target, time, kind, severity; the blocked-
     command view; session replay for recorded sessions. **Where a proxy's
     stream has a hole, and why** (`Hoplock/proxy#71`, served by 0019). A
     `logging.gap` is shown at its **span**, from `gap_first_at` to
     `gap_last_at`, and never at its own timestamp, which is often after the
     session ended. Show how many records are missing, their kinds, and how
     many were critical. A gap that hid something critical is marked as
     critical. Show the two causes as different facts:
     - **`evicted`**: this server never received the records. The proxy was
       cut off from it for longer than its disk window could hold. That is
       outage-class, and it reads as one (M11).
     - **`refused`**: this server received the records and refused them
       itself. Show its own answer (`refusal_code`, `refusal_message`) and the
       refused record ids. It is neither a deny nor an outage, and it must not
       read as either. DESIGN.md §6 has no state for it yet, so decide one in
       DESIGN.md in this PR (M20).

     A session with a hole is the **partial** state (DESIGN.md §7): some data
     and a named gap, never an error page, and never shown as complete. Where
     a session has two reports of one cause, show their sum. Replay over a
     span that a gap covers shows the hole, and never splices the capture on
     either side together. A gap is never rendered as tampering, and a stream
     whose chain verifies is never rendered as "nothing missing" (PLAN §7).
     Gaps that belong to no session appear in a per-proxy view of the stream,
     placed by span.

     **What the archive holds, and what retention waited for** (0025). With
     an archive registered, the audit view shows its lag for the tenant from
     the archive row of `GET …/extensions` (`lag`: `streams_behind`,
     `oldest_unconfirmed_at`, `failing`, `captures_waiting`), and says
     plainly when the archive is caught up. It is a delivery fact, never an outage and never
     tampering (M11, PLAN §7). A retention pass the archive held back says so:
     the answer's `held_by_archive` and `archived_through_seq` after a pass the
     operator ran here, and the `operator.audit.retained` record's
     `operator.held_by_archive` in the trail. A pass by sequence refused with
     `409 retention_not_archived` is shown as a refusal that deleted nothing,
     naming the stream and `archived_through_seq`, and offers the pass by
     instant, which deletes as far as the archive holds. With no archive
     registered, none of this appears.
  4. **Policy** — bundle versions, diff, validation errors inline against the
     source, **simulate** a candidate against recorded traffic, activate,
     roll back. **Before a route's `algorithm_floor` is raised, preview the
     impact** (`Hoplock/proxy#69`, 0019's satisfiability report). Show the
     targets the raise would break, with the level each was seen to meet, what
     it negotiated or offered, and when. Show targets with no observation, or a
     stale one, as `Not reported`, never as meeting the floor. Show the proxies
     that cannot enforce the level yet. It warns and never blocks, because
     the observation is a past fact about a target that may have been upgraded
     since. The same view explains why `pq-hybrid-kex` needs OpenSSH 9.9 or
     later on the target, and it never promises sntrup761. A ban that names
     something no proxy declared is a warning the author reads and may
     override (0019). So is a ban that leaves an axis empty on some proxies'
     builds (`Hoplock/proxy#72`). Show it per build: the proxies that will
     refuse the route, the axis, and what each of those builds offers there.
     Show a rolling upgrade's disagreement as *in progress* (M19), never as a
     fault. It warns and never blocks, even when every proxy would refuse the
     route. A proxy whose build declared no offer for the route's profile is
     shown as not judged, never as fine.
  5. **Inventory** — targets and labels, identities, groups, roles, grants.
     **A subject that is locked and a target that is quarantined say so
     wherever they are shown** (M28, 0039): in the list and in the detail, with
     the lock's or the quarantine's facts — when it was placed, by whom, the
     reason code, the external reference, and the inbound action behind it if
     one placed it — and as a different state from a disabled or an unknown
     subject. A lock is a deny made on purpose, and the console renders it with
     a deny's colour and words (M11), never an outage's. A subject whose row
     was deleted while locked is listed by the lock (`record_missing`), and its
     lock can still be lifted. The console **offers lock, unlock, quarantine and
     release** to a holder of `session:revoke`, and to nobody else. Each is a
     destructive action and confirms as one. Locking asks for the text the
     holder is shown (one line, at most 512 bytes) and shows it before sending,
     because it is displayed verbatim as their sessions end; a quarantine says
     plainly that it ends no session and drops every cached decision in the
     tenant, and points to the revocation workflow for ending the ones running.
     `503 standing_denial_undelivered` is rendered as "in force, not yet
     delivered" with a retry that asks again, never as a failure to lock.
     Deleting a quarantined target is refused (`target_quarantined`), and the
     console says release it first.
     **A subject the directory manages says so wherever it is shown** (0040):
     whose it is (the sync's provider and the directory's id for the person),
     and a **deactivation** as a state of its own — when the directory applied
     it and its reason code — distinct from a lock, with a deny's colour and
     words. A subject both locked and deactivated shows both, and the console
     never suggests that unlocking re-admits a deactivated person: only the
     directory lifts a deactivation, and the console offers no control that
     claims to. It **offers no edit and no delete of a directory-managed
     subject** (the API refuses both with `409 subject_directory_managed`,
     rendered as "managed by your directory; change it there"); the lock,
     role bindings and local group membership stay offered. A subject the
     directory deactivated before it created it is listed with
     `record_missing`, as a deleted locked subject is. The sign-in page and the
     identity screens show a connector's `directory_claim`, so an operator can
     tell a connector that reaches only provisioned people from one that
     creates subjects at login.
  6. **Extensions** — which `ext` points have an implementation registered
     (0004). An operator debugging behaviour must be able to see that Enterprise
     is in play. **Each registered sink shows its delivery lag** for the tenant
     (0026), from its registration's `lag` on `GET …/extensions`:
     `streams_behind`, `oldest_unconfirmed_at`, `failing` and
     `skipped_by_retention` — the same names as the archive's lag, so one
     component renders both. A sink that is failing says so, and a sink that
     a retention pass overtook shows how many records it missed and that the
     pass's `operator.audit.retained` record explains the gap. Neither is an
     outage or tampering: a sink is a downstream consumer, and the local store
     is still the record (M8, M11). A sink caught up says so plainly. With no
     sink registered, the sink point shows what Control does instead and no
     lag.
  7. **Emergency runbook** — the workflow for the day an advisory lands
     (`Hoplock/proxy#69`; upstream's `api/README.md` states the steps). It walks
     an operator through three steps, each one a call 0019 serves and audits:
     add the ban to the affected routes and activate it; send
     `cache_invalidate` with `all`, because a cached decision keeps the old
     policy until its hint runs out; then find the running sessions by what
     they negotiated (for example every open session whose
     `target_cipher_out` is the banned cipher) and end them with `session_kill`.
     `GET …/audit/negotiated` is paged (M26, 0037): the workflow reads it to
     its last page, following `next` until `truncated` is false, and ends
     every `session_id` listed, never only the first page's.
     The `reason` is shown to the user, so the workflow asks for it and shows
     it before sending. Each step is a destructive action and confirms as one.
     Each shows what the call reported covering, and the workflow never skips
     the second step, because a ban that stops at activation reaches only new
     decisions.
  8. **Reports** — every registered report pack this tenant can generate, from
     `GET …/reports` (0041), and a form that generates one through
     `POST …/reports/generate` and saves the file. The form is built from the
     descriptor: the period, each parameter (a closed set as a choice,
     required ones marked), and the formats it lists. A report is named
     through the catalogue of the host that registered it, under its
     `<provider>:` keys (M27), and by its code where no catalogue names it
     (M21). A
     provider listed with an error is shown as unavailable, never as having
     no packs. With no provider registered, the screen says that the audit
     view is the reporting available, which is the point's own answer (M15),
     and is not shown as broken. A generation that ends broken is shown as
     failed, never offered as a saved report.
- **Auth**: OIDC for humans, the same session the API issues. No separate login.
- Build: assets built and checked in, or built in CI with a reproducible step;
  `make build` must work on a machine with no Node installed. Say which you
  chose and why in learnings. `ui/DESIGN.md` §11 constrains the toolchain
  (TypeScript + Vite, headless primitives, no visual kit, no CSS-in-JS runtime,
  an ICU MessageFormat runtime, a bundle budget) — the framework choice inside
  those constraints is yours.
- **Design, accessibility and localisation per `ui/DESIGN.md`** — the two
  sections below say what that obliges and how it is checked.

### Deployment identity and tenant context (M18, M19)
- The console always says **which deployment** this is: name, instance id,
  version set, and health (0043). An operator with a staging and a production
  instance open in two tabs must never have to guess which is which, and the
  distinction has to survive a screenshot.
- Where more than one tenant is in scope, the current tenant is visible in the
  chrome and switching is explicit. With a single tenant, none of this appears —
  tenancy is invisible to a deployment that does not use it (M18).
- If the deployment is supervised, say so, name the supervisor, and link to the
  audit view filtered to that supervisor's actions. An operator must be able to
  see what was done to their deployment from above, in their own console.

### Design and visual quality (M20)
`ui/DESIGN.md` is the specification; this is what implementing it obliges, and
every line of it is checkable.

- **One token file.** Colour, type, space, radius, elevation and motion live in
  `tokens.css` (or the framework equivalent) and nowhere else. No component
  carries a raw hex, a raw font size, or a raw radius.
- **Both themes ship together.** Light and dark are both designed, follow the
  system by default, and the operator's override persists. Dark is not an
  inversion filter and not a follow-up PR.
- **Every screen defines four states** — loading (a skeleton matching the final
  layout, so nothing shifts), empty (what would be here, why it is not, the one
  action), error (what failed, the correlation id, retry) and partial (some
  data, a named gap). A fleet with one unreachable node is *partial*, not an
  error page.
- **The status vocabulary is the one in DESIGN.md §6**, rendered by a single
  component, never colour alone, and it encodes decisions this plan already
  made: a **deny** and an **outage** are different colours and different words
  (M11 — a console that paints both red re-creates the confusion M11 exists to
  prevent); a rolling upgrade reads as *in progress*, never as a fault (M19);
  *not reported* is its own state and never renders as healthy (M17, PLAN §4).
- **Accessibility is part of done, not a later pass** — WCAG 2.2 AA in both
  themes, visible focus on everything, full keyboard operation with focus
  trapping in dialogs, accessible names on icon-only controls, real table
  markup, and `prefers-reduced-motion` honoured.
- **Nothing is fetched from the internet at runtime.** Fonts and icons are
  bundled into the binary. An air-gapped deployment must render identically, and
  a security product must not report its operators' activity to a CDN.
- **Destructive actions look destructive** — activating a bundle, killing a
  session, revoking a grant confirm by typing the object's name. A failed
  mutation reports inline, never by a toast alone.

**These are enforced, not reviewed by eye** (DESIGN.md §10), and each is a CI job
this phase adds:

1. a token lint that fails on a colour, font size or radius outside the token file;
2. a contrast test over the token pairs themselves, both themes, asserting each
   against the threshold that actually applies to it — the palette in DESIGN.md
   is already verified this way, so the test should pass on arrival and exists to
   keep it true;
3. axe assertions on every screen in the E2E run, both themes, zero violations at
   `serious` or above;
4. a committed screenshot baseline — every screen, both themes, both densities,
   plus loading, empty and error states — diffed in CI, updated deliberately in
   the PR that changes the design;
5. a test that loads the console with all external origins blocked and asserts it
   renders identically;
6. a test asserting no transition exceeds 0ms under reduced motion.

### Multilingual support, English only (M21)
Build the machinery; write one catalogue. `ui/DESIGN.md` §9 is the
specification — this is the shape of what lands.

- **Every user-visible string comes from `ui/src/locales/en.json`**, ICU
  MessageFormat, keyed by a stable id. `en.json` is the **only** catalogue
  committed in this phase. Do not add a second locale, do not machine-translate
  one to "prove" it works, and do not ship a language picker with one entry —
  the picker appears when there is something to pick.
- **No sentence built by concatenation**, and no user-visible text baked into an
  image or sprite. Plurals and interpolation belong to the message.
- **Logical CSS properties throughout** (`margin-inline-start`, never
  `margin-left`) and `dir` taken from the locale. No RTL locale ships; this is
  what stops adding one from being a restyle of every component.
- **Layout absorbs +40% expansion** — no control sized to its English label, no
  single-line assumption for catalogue text.
- **`Intl` for every date, time, number and list.** Timestamps render in the
  operator's zone with UTC on hover; the copyable form is ISO-8601 UTC.
- **Identifiers stay LTR, mono and unsubstituted** in every locale: session and
  decision ids, fingerprints, hostnames, labels, rule names, policy source.
- **Operator-authored content is never translated** — a rule name is data, and
  "explain why" must cite the rule that is actually in the bundle.
- **Errors come from the API as a code plus parameters** (0019) and from the
  compiler the same way (0005); the console builds the sentence. If either
  phase shipped prose only, say so and raise it rather than parsing English.
- **The server is not asked to localise.** The locale is resolved in the browser
  from the operator's stored preference and `navigator.languages`; the server
  negotiates no language and returns no translated content.

**Proved by pseudolocale, not asserted** (DESIGN.md §10, checks 7–9): an `en-XA`
build accents and pads every string, and a mirrored `en-XB` checks direction.
A string that renders unaccented is hardcoded; a layout that clips is one that
will not survive German. That is how a claim about locales nobody has written
becomes a test that runs today.


## Out of scope
- Enterprise screens (approval inboxes, report schedules and history,
  recertification campaigns). Listing and generating a registered pack is the
  Reports screen above, and Control's. Enterprise adds its
  own, served through the same shell from `/host-console/<provider>/` (M27):
  this phase does not add the `server.Options` field a host hands its screens
  through, and says in its learnings what that field must carry (its
  patterns, its catalogue keys and one handler, as `north.Console` does) so
  the phase that adds it can. They
  inherit these tokens and this component set; a second visual identity inside
  one console is the thing M20 exists to prevent.
- Dashboards and charts beyond fleet health and simple audit counts. The two
  that exist are hand-written SVG; no chart library until there is a third.
- Re-opening the design. DESIGN.md's palette, scale and constraints are settled
  input to this phase. Correct it where it is wrong — in this PR, with the
  reason — but do not spend the session redesigning it.
- **Any locale other than English.** Translations are a later phase and a
  different skill; this phase ends when a second catalogue would be the only
  thing left to add. Also out of scope: server-side localisation, a translation
  management integration, and locale-aware content in the audit store (M21 keeps
  records in codes and data, permanently).

## Acceptance criteria

**Function**
- `make build` produces one binary serving the console with no Node present.
- 0038's namespace tests stay green, and pass with the real console mounted in
  place of its stub (`internal/httpapi/north/namespace_internal_test.go`):
  - `TestEveryPathIsInsideItsOwnNamespaceAndNoOther`: every path the console
    serves is under `/console/`, and none is under `/api/v1/` or a host's
    prefix;
  - `TestEachNamespaceAnswersOnlyInsideItsOwnPrefix`: an unknown API path
    answers the API's `404` envelope, never `index.html`; a deep link outside
    `/console/` does not load the console; `/` redirects to `/console/` and
    nothing else does;
  - `TestAConsoleOutsideItsPrefixIsRefused`: a host screen's assets cannot
    shadow one of the console's, and a host catalogue key equal to one of the
    console's is refused;
  - `TestEveryWayAHostCanOverlapControlIsRefusedOrAnswersNothingOfControls`
    (`namespace_test.go`) and the route-listing tests, unchanged.
- Every screen works against a seeded database; end-to-end tests drive the
  critical paths (explain, policy activate, grant create) headlessly.
- An RBAC test: an auditor session sees read-only views and the API refuses the
  mutating calls the console hides — assert the API refusal, not just the hidden
  button.
- No API route is reachable from the console that RBAC would deny.
- The console degrades honestly when an extension point is absent: an Enterprise
  feature that is not installed is not shown as broken.
- With a fake report provider registered, the Reports screen lists its pack
  and generates it with a parameter and a format chosen. A second provider
  listed with an error is shown as unavailable, and its absence of packs is
  never shown as "none".
- The floor's impact preview names a target whose observed level is below a
  candidate floor, shows a target with no observation or a stale one as `Not
  reported`, and names a proxy that cannot enforce the level. The fleet view
  shows each proxy's declared levels and profile offers per build. A ban that
  empties an axis on one of two seeded builds is shown as a per-build warning
  naming that proxy, and it does not block the publish. Assert each against a
  seeded fixture.
- The emergency runbook drives 0019's three calls in order, shows the kill
  `reason` before sending it, and cannot finish without the `cache_invalidate`
  step. An end-to-end test walks it.
- Against a seeded fixture, the inventory shows a locked subject and a
  quarantined target in both the list and the detail with their facts, a locked
  subject whose row was deleted by the lock alone, and offers lock, unlock,
  quarantine and release to a `grant-admin` or `admin` session and to no
  other role. Locking and quarantining each confirm; the lock shows the holder
  text before sending. `standing_denial_undelivered` renders as in force and
  retryable. Assert the API refusal for a role without `session:revoke`, not
  only the hidden button.
- With a fake action handler registered, the extensions screen shows it as
  running; with one whose `Start` failed it shows it as registered and not
  started, with the error's kind (`start_error`), and neither as an outage. With
  none registered, the point shows what Control does instead (M15). Assert
  each.
- With two fake sinks registered, one behind and failing and one overtaken by a
  retention pass, the extensions screen shows each one's lag on its own row:
  the failing one as failing, the overtaken one with its skipped count. With
  none registered, no sink lag is shown. Assert each.
- With a fake archive registered and behind, the audit view shows its lag;
  a retention pass by sequence beyond it renders `retention_not_archived` as
  a refusal that deleted nothing; and a pass by instant renders as held back
  by the archive. With none registered, no lag is shown. Assert each.
- Against a seeded fixture, the audit view shows an `evicted` gap and a
  `refused` gap in one session's timeline at their spans, as different states,
  and neither of them as denied. It shows two reports of one cause summed. It
  shows the session as partial rather than complete. And replay across the gap
  shows the hole. A session-less gap appears in the per-proxy view. Assert
  each.

**Design** — each of these fails the build, not the reviewer's patience:
- The token lint passes: no colour, font size or radius outside the token file.
- The contrast test passes for every semantic pair in both themes.
- Axe reports zero `serious`+ violations on every screen, in both themes.
- The screenshot baseline is committed and CI diffs clean: every screen × both
  themes × both densities, plus the loading, empty and error state of each.
- Every screen has all four states implemented and captured (§ Design above) —
  a screen with no empty state is an incomplete screen, not a minor gap.
- The offline test renders the console identically with all external origins
  blocked; no request leaves the page at runtime.
- Reduced motion collapses every transition to 0ms.
- Bundle budget holds: ≤ 350 KB gzipped JS on first load, fonts ≤ 120 KB.
- The deployment identity (name, id, health, accent stripe) is legible in a
  screenshot of any screen, and two deployments are distinguishable from that
  screenshot alone — this is asserted in the baseline, not eyeballed.
- A deny and an outage are rendered with different colours **and** different
  words, asserted in a test (M11). A rolling upgrade renders as in-progress, not
  as a fault (M19). A capability that is stale, undated or absent renders as
  `Not reported`, not as healthy (M17).

**Localisation** — English is the only catalogue, and these still hold:
- The pseudolocale run is clean: no screen renders an unaccented string, and no
  screen clips or overflows at +40% expansion, in both themes.
- The mirrored pseudolocale run reports no physical-property leak.
- The string lint passes: no user-visible literal in a component, and no date or
  number formatted outside the locale helpers.
- `ui/src/locales/en.json` is the only catalogue in the tree, and a test asserts
  every key it holds is referenced and every referenced key exists.
- Every API and compiler error the console can surface is rendered from its code
  and parameters — asserted with a fixture carrying a code the catalogue does
  not know, which must render a useful fallback naming the code and the
  correlation id rather than an empty string or a crash.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0044-management-console-learnings.md`. Summary block MUST give
the asset build/embed mechanism, the routes each screen consumes, the auth flow,
and **how Hoplock Enterprise adds screens to this shell** without forking it —
which, per M20, includes how it inherits the tokens and the component set rather
than bringing its own look — and, per M21, **how it adds its own catalogue keys
without colliding with this one**, since Enterprise ships screens this
repository never sees.

Details MUST also record: the framework chosen and why, within DESIGN.md §11's
constraints; where the token file and the catalogue live, and how the nine checks are wired into
CI; and **any change made to `ui/DESIGN.md` in this PR**, with the reason — a
design document that drifts silently from the console is worse than none, and
the next session reads it expecting it to be true.
