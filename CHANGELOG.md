# Changelog

What each release of `github.com/hoplock/control` contains, newest first, for
anyone who depends on the module or runs the binary. Hoplock Enterprise reads it
before every bump of its pin (its E3).

What a release is, when one is cut, and what its number promises is decision
**M23** in `docs/PLAN.md` §2. It is not restated here.

**Adding an entry.**

- List what someone consuming the module or running the binary can observe. Do
  not list prompts, plans, learnings or CI.
- A phase releases in its own PR. Add a `## vX.Y.Z` section above the newest
  one, and move anything under `## Unreleased` into it. In the section:
  - `### Breaking`, when the release changes a public package incompatibly.
    `make release-check` finds those changes and prints them;
  - the phase or phases it contains, by number;
  - the changes to public packages, by identifier;
  - a behaviour change that no API diff can see, which nothing else catches.
- A change that does not release goes under `## Unreleased` until one does.
- A released section is what its tag says. Never edit one; correct it in the
  next release's section.
- `make release-check` says what merging the tree releases, and refuses a
  version number that does not match the change.

## Unreleased

## v0.43.0

Phase 0056, the console's Extensions and Reports (M15, M8, M11, M21, M27,
M20). No migration. The north-bound API is unchanged at version `5`: both
screens read routes that were already served (`GET …/extensions`,
`GET …/reports`, `POST …/reports/generate`). Nothing in `ext`, `server` or
the contract changes.

- **Extensions, at `/console/extensions`** (`extension:read`). Every
  extension point, what is registered at it — a host's registrations listed
  first, under "In play" — and Control's own default where it has one. An
  action handler or identity sync whose `Start` succeeded reads `Running`;
  one whose `Start` failed reads `Not started`, with the error's kind, and
  never as an outage. A point with nothing registered reads `Not registered`
  and says, in the console's own words, what Control does instead: an
  extension that is not installed is not shown as broken.
- **Each audit sink's delivery lag**, on its own registration's row:
  `Caught up`, `Behind` (in progress) or `Failing` (degraded, retried by
  itself, never an outage), streams behind, the oldest unconfirmed record,
  and the records a retention pass removed before the sink confirmed them,
  with a link to the `operator.audit.retained` records that explain the gap.
  The archive's lag renders through the same component.
- **Reports, at `/console/reports[?provider=&report=]`** (`audit:read`). Every
  registered report pack for the tenant and its reports; a pack that could
  not list them reads `Unavailable` and never as having none. A report's form
  is built from its descriptor — the period, each parameter (a closed set as
  a choice, the required ones marked) and its formats — and saves the file
  under the name the server gave it. A generation whose body breaks after its
  first bytes reads `Failed` and saves nothing. With no pack registered, the
  screen points to the audit view, which is the reporting available. A report
  is named by its code: nothing hands a host's catalogue to the console yet.
- **Status vocabulary**: `Running`, `Caught up`, `Not started`, `Failing`,
  `Behind`, `Unavailable`, `Failed` and `Not registered` join the console's
  statuses; `report_provider_failed`, `report_provider_unavailable` and
  `report_generation_busy` have sentences.

## v0.42.0

Phase 0055, the console's locks, quarantines and the directory's subjects
(M28, M7, M2, M11, M26, M20, M21). North-bound API version `5`: a client of
`1` to `4` is served as before, except that reading a claim mapping that has
group entries answers it `500 api_version_unexpressible` (below). No
migration. Nothing in `ext`, `server` or the contract changes.

- **Standing denials, wherever their subject or target is shown.** A subject
  lock reads `Locked`, a target quarantine `Quarantined` and the directory's
  deactivation `Deactivated` — each a deny, never an outage — in the
  inventory's lists and on each page, with their facts: when, by whom (a
  person, an inbound action's handler or the directory's sync), the reason
  code, the text the holder was shown, the external reference and the
  inbound action. A subject both locked and deactivated shows both. The
  identities removed while locked, and the ones the directory deactivated
  before creating them, are listed beneath the identities.
- **Lock, unlock, quarantine and release** are offered to a holder of
  `session:revoke` (`admin`, `grant-admin`) and nobody else, each confirmed
  by typing the id. Locking asks for the one line the holder is shown and
  shows it before it is sent; a quarantine says it ends no session and names
  `POST …/revocations` for the ones running. A lock or quarantine answered
  `503 standing_denial_undelivered` reads as in force, not yet delivered,
  with a button that asks again. Removing a quarantined target reads
  "release it first".
- **A subject a directory provisions** shows its sync's provider, the
  directory it comes from (`directory.source`) and that directory's id for
  the person, in the list and on its page, and is offered no edit and no
  removal: `409 subject_directory_managed` reads "managed by your
  directory". Its lock and its roles stay an operator's. Nothing offers to
  lift a deactivation.
- **The claim mapping, at `/console/inventory/claim-mapping`.** The active
  version's facts, each group entry with the directories it is scoped to,
  `require_directories`, how many directory identities are still being
  brought onto it, the document, the sign-in connectors with each tie's
  `directory_claim` and `directory_source`, and, for `identity:write`, an
  editor whose refusals are rendered code by code at the field each names.
  The sign-in page shows a tied connector's claim and directory, and Explain
  the directory a provisioned subject's groups came from.
- **`group_map` on `GET …/identity/claim-mapping`**: the document's group
  entries in order, each `{external, group, directories}`, `directories`
  absent on an unscoped entry, `group_map` absent when the mapping maps no
  group. It is the first member added after version `1`: a client declaring
  an older version reading a mapping with group entries is answered `500
  api_version_unexpressible`, never a body without them.
- **`?record=missing` on `GET …/subjects`** lists, by id and a page at a
  time, the ids a lock or the directory's deactivation holds with no subject
  row, each as `GET …/subjects/{subject}` answers it; another value is `400
  invalid_request {field: record, problem: invalid}`, and `group` beside it
  `{field: record, problem: at_most_one_of_group_or_record}`.

## v0.41.0

Phase 0054, the console's Inventory (M2, M7, M10, M26, M20, M21). Migration
`0026` adds one index to `subjects`; it blocks writes to that table while it
builds, so a sign-in waits for it, and authorize does not. North-bound API
version `4`, additive: a client of `1` to `3` is served exactly as before.
Nothing in `ext`, `server` or the contract changes.

- **Inventory, at `/console/inventory/`.** Targets and their labels,
  identities, groups, the fixed roles and grants, each a list a page at a
  time and a detail, every filter in the URL: `targets?label=key=value`,
  `subjects?group=`, `grants?subject=&state=`. A target's page shows its
  newest audit records; an identity's, the roles it holds directly and its
  grants; a group's, its members and roles; a role's, its holders; a
  grant's, the decisions it supplied. Each links to the others and to the
  audit view filtered by the identity or the host.
- **Writing from the console is what the API allows the operator.** Adding,
  editing and removing a target is offered to `target:write`; adding, editing
  and removing an identity, and binding and withdrawing a role, to
  `identity:write`; granting and revoking access to `grant:write`. Any other
  role sees the read-only view, and the API refuses the call to it.
  Removing, withdrawing a role and revoking a grant ask the operator to type
  the object's id; revoking also asks for the reason the holder is shown. A
  refused write is reported at the field the API names. With an approval
  workflow registered, granting access says the request is pending rather
  than showing a grant.
- **`GET /api/v1/tenants/{tenant}/groups`** lists every group name in the
  tenant once — on an identity, bound to a role, or recorded — with
  `member_count`, `roles` and, where one exists, `record`, a page at a time;
  **`GET …/groups/{group}`** answers one, `404 not_found` for a name the
  tenant does not hold. Both need `identity:read`.
- **`?group=` on `GET …/subjects`** answers a group's members.
  **`?role=`, `?subject=` and `?group=` on `GET …/role-bindings`** answer one
  role's holders, or what one holder holds directly; an undefined role is
  `400 invalid_request` on `role`, and both holders at once
  `{field: group, problem: at_most_one_of_subject_or_group}`. Each list's
  cursor binds its filters, so a cursor carried across them is refused.
- **A grant whose window has not opened reads `Scheduled`**, a new status
  in the console's vocabulary, never a fault.

## v0.40.0

Phase 0053, the console's Policy screens (M3, M4, M11, M20, M21, M26). No
migration, the north-bound API version is unchanged (`3`), and nothing in
`ext`, `server` or the contract changes.

- **Policy, at `/console/policy`.** The tenant's stored policy versions, a
  page at a time and newest first, with the active one named above them. A
  version, at `/console/policy/versions/<n>`, shows its source with each
  publish-time finding beneath the line it is about; its changes against the
  active version, or `?against=` another, by rule and then line by line; its
  publish-time report, every finding by severity and then id; and a replay of
  the decisions recorded in a window, which changes nothing.
- **Authoring a candidate, at `/console/policy/candidate`.** A source not
  stored yet — the active version's, or `?from=<n>` another's, to start from —
  is validated, compared with the active version, replayed against recorded
  traffic and uploaded. A bundle the compiler refuses shows each rejection
  beneath the line it names, in the console's own words, built from the
  rejection's code and parameters; a code the console does not know shows
  the code itself and the correlation id. An upload refused is reported at
  the form and in the source, never by a notification alone, and stores
  nothing. Every role that reads policy may validate, compare and replay; only
  `policy:write` is offered Upload.
- **Activating and rolling back are one confirmation.** Activating a stored
  version, newer or older than the active one, asks the operator to type its
  name (`v4`), lists each warning in its publish-time report with a box to
  accept it by its id — which the activation's audit record names — and
  reports a refusal inside the dialog. Only a holder of `policy:publish` is
  offered it, and the API refuses it to anyone else.
- **What a validator registered by a host reports** shows in the report by
  its code, its provider and its parameters, never its English message: a
  validator's parameters are what a reader of its finding has.

## v0.39.0

Phase 0052, the console's look, chosen with an operator (M20, M21). No
migration, and nothing in `ext`, `server` or the contract changes.

- **The console has a new look, "Carbon", and opens dark.** Grounds are near
  black with a blue cast, panels sit one step lighter, and hairline borders
  carry the structure. The accent is now an electric cyan, replacing the
  indigo. Text is a step denser, so more of a table fits on a screen: body
  13px and table cells 12px. An operator who has never chosen a theme now
  sees dark. Light, or following the system, is one choice in the account
  menu, and a theme an operator already chose is kept.
- **Status colours keep their meanings and glyphs**, retuned for the darker
  ground. A deny and an outage remain different colours, and the info blue
  moved bluer so it is not mistaken for the cyan accent.
- **The default deployment stripe follows the accent**: a deployment with no
  `console.stripe` shows a cyan bar. The `teal` stripe is the nearest of the
  configurable stripes to it, so a deployment that must never be mistaken
  for one with no stripe set should choose `magenta`, `orange` or `slate`.
- **A host's screens change look with no change of their own**, because they
  take every colour from the console's tokens.
- **The console's theme and density are in the account menu**: the
  operator's name at the end of the top bar opens it, with each preference a
  group of choices and signing out beneath. They are no longer at the foot of
  the navigation.
- **Display preferences and saved views are remembered for the operator**,
  not for one sign-in: signing in again, in the same browser, finds them,
  even after someone else signed in there. Before, each sign-in started from
  whatever the browser's last operator had chosen.

## v0.38.0

Phase 0051, the console's Explain and audit view (M4, M8, M19, M26, M11, M20,
M21). Run `hoplock-control migrate`: migration `0025` adds one partial index
to `audit_records`, and while it builds, writes to that table wait — no
proxy's log batch and no operator's record is stored until it commits, and a
proxy buffers and resends meanwhile; reads continue. Nothing in `ext`,
`server` or the contract changes.

- **North-bound API version `3`** (`versions.north_api`), with
  `versions.north_api_min` still `1`: a client declaring `1` or `2` is served
  exactly as before. Version `3` is the route and the parameter below.
- **`GET …/audit/records/{record}/capture`** (`audit:read`): one `stream`
  record's session capture, for replay — `record_id`, `session_id`,
  `capture_bytes` and `capture_sha256` (the size and digest the record's
  hashed body carries, so the bytes can be checked against the chain),
  `retained`, and `payload`, the bytes in base64 as the proxy sent them. A
  capture a retention pass emptied is `retained: true` with no `payload`; a
  record that never carried one, and a record that does not exist, are `404
  not_found`.
- **`?principal=` on `GET …/audit/records`**: the records naming that
  principal as the actor (`attributes.principal_id`) — an operator's
  session, an API token, or a supervisor's `sup-` credential, so an operator
  can read what a supervisor did to their deployment. The cursor binds it. A
  supervisor's `acting_operator` is never a filter.
- **Explain at `/console/explain`.** Paste the session id a user was given,
  or a decision id (`d_…`), and read the whole story: what the session came
  to, its decisions hop by hop, and for each the outcome and why (a rule, the
  default deny when no rule matched, a standing denial, or an allow the
  server could not serve — an outage, never shown as a deny), the subject's
  identity, the matched rule and terms, the claim mapping version beside the
  active one, the grant, the obligations, the route, the hop path, and the
  record as stored. Deep links: `/console/explain/sessions/<id>`,
  `/console/explain/decisions/<id>`, and `?decision=` for the hop shown.
- **The audit view at `/console/audit/records`**: records by subject, target,
  session, decision, acting principal, kind, severity and time (all time, the
  last 15 minutes to 30 days, or between two instants), newest or oldest
  first, every filter in the URL; one page at a time, saying when the answer
  was cut, with that page counted by kind and severity. A record opens at
  `?record=<id>` with its chain position, hash and body, and links to its
  explanation and its session's replay. Where a supervisor's record carries
  `acting_operator`, the name is shown beside the `sup-` principal as the
  supervisor's word, linked to nothing.
- **Blocked commands at `/console/audit/blocked-commands?label=name=value`**,
  each with the rule and route that let the session in, and a link to that
  decision's explanation; one whose decision this server no longer holds says
  so.
- **Session replay at `/console/audit/sessions/<id>/replay`**: a session's
  records in order, stepped through by keyboard, buttons or played, with the
  terminal as its captures show it at that step. A capture removed by
  retention or unreadable is marked where it falls, and nothing either side
  of it is joined.
- **The supervised badge in the top bar is a link** to the audit view of that
  supervisor's acts.
- A screen's read whose selection changed is shown loading from the moment it
  changes, so a pager never names the next page over the last page's rows.

## v0.37.0

Phase 0050, the console's component set, its browser checks, and Fleet (M20,
M21, M26, M11, M17, M19). No migration, and nothing in `ext`, `server` or the
contract changes.

- **Fleet at `/console/fleet`.** One page at a time of
  `GET …/proxies` (by proxy id, `?limit=` 100 unless the URL names another,
  following the answer's own `next`): each proxy's zone, health, last
  heartbeat, contract version, live relay registrations, session count, and
  its configuration as the proxy **reported** it — `Applied`, `In progress`
  (an older document applied while a newer is desired), `Pending restart`
  (with the settings waiting), `Rejected` and `Fetch failed` (with the
  proxy's `last_error`), and `Not reported` for a proxy that has never
  reported, which is never shown as applied. A proxy enrolled but never
  heard from is `Unreachable` and makes the page *partial*, naming it,
  rather than an error. A chart of the whole tenant's fleet by health, from
  `…/instance/health`'s fleet summary. Filters, the zone/table view and the
  open proxy are in the URL.
- **Withdrawing a proxy from the console** (`POST …/proxies/{proxy}/withdraw`)
  is offered only to a role holding `fleet:write`, and confirms by typing the
  proxy's id; a refusal or an outage is reported inside the dialog.
- **The console never offers a call the operator's role cannot make**: every
  control that calls the API is shown only when the session holds that
  route's permission in the tenant. The API's RBAC is unchanged and is what
  refuses.
- **Every screen has a loading, empty, error and partial state.** An outage is
  labelled an outage and a refusal a refusal, in different colours and words;
  an error code the console has no sentence for names the code and the
  correlation id. The Deployment view now reads this way too: a health that
  cannot be read beside a readable identity is partial.
- **A command palette** (`Ctrl-K` / `⌘K`) goes to each screen and sets the
  theme and density.
- The deployment chip's accessible name is now its visible text (name, short
  instance id, health); the versions stay its description.

## v0.36.0

Phase 0049, the operator console's shell (M20, M21, M27). The north-bound
listener now serves a console at `/console/`, built into the binary: `make
build` and `go build` need no Node. No migration, and nothing in `ext` or
`server` changes.

- **`/console/`** serves the console: its shell, its assets under
  `/console/assets/` (cached as immutable, gzip-compressed for a browser that
  accepts it), its English catalogue at `/console/locales/en.json`, the font
  licences under `/console/licenses/`, and the shell for every other path
  under `/console/`, which the console routes. Only `GET` and `HEAD` are
  served there. A missing asset is a `404`, never the shell.
- **`/` redirects to `/console/`** (`302`). Any other path outside
  `/console/`, `/host-console/` and `/api/v1/` is still Control's `404`
  envelope, and no API path is ever answered with the console.
- **What the console shows so far:** sign-in, through the north-bound API's
  own session — the identity providers `GET /api/v1/session/methods` lists for
  OIDC, and the break-glass local login, marked as such; then, in the chrome
  of every page, which deployment this is (display name, short instance id,
  health), the tenant where more than one is in scope, a badge for each live
  supervisor credential and a break-glass session; and a Deployment view of
  `…/instance` and `…/instance/health`. Light and dark themes, two densities,
  English only. It calls nothing the API does not serve and holds no
  privilege of its own.
- **Signing in to the console through OIDC** needs the connector's
  `redirect_uri` to be `https://<north listener>/console/sign-in/callback/<connector>`;
  the console hands the provider's answer to
  `GET /api/v1/session/federated/{connector}/callback`, which sets the session
  cookie as before. A connector whose `redirect_uri` names that API route
  still works for a client that is not the console. SAML connectors are
  listed but cannot complete a sign-in from the console yet.
- **New configuration:** `console.stripe`, the deployment's colour in the
  console — `accent` (the default), `magenta`, `teal`, `orange` or `slate`.
  Any other value refuses to start.
- **The console's responses carry a `Content-Security-Policy`** that allows
  only this origin, so a browser fetches nothing for it from anywhere else.

## v0.35.0

Phase 0048, the supervising operator named in the instance's trail (M19). A
supervisor's call may say which of its operators made it, and the supervised
deployment records that name on every audit record the call writes. No
migration. One exported field is added to `server` (`Caller.ActingOperator`),
and nothing in `ext` changes.

- **North-bound API version `2`** (`versions.north_api`), with
  `versions.north_api_min` still `1`: a client declaring `1` is served exactly
  as before, and no deprecation window starts. Version `2` is the header
  below. A new request header is additive, like a new route, and moves the
  version so that a client can tell whether a deployment honours it.
- **`Hoplock-Acting-Operator`**, a request header a supervisor's call may
  carry: the name of the operator it acts for, as UTF-8 with every octet
  outside printable ASCII and `%` itself percent-encoded (`José` is
  `Jos%C3%A9`). Decoded, it must be 1–128 printable characters, with no
  control or format character and no leading or trailing space. It is checked
  after the credential, so a bad credential is still `401`. Refusals are `400
  invalid_request` with `field: "Hoplock-Acting-Operator"` and `problem`:
  `not_supervisor` from any caller that is not a supervisor's `sup-` token (an
  ordinary token, a session, an anonymous route), `too_long` (`limit: "128"`),
  or `invalid` (does not decode, empty, sent twice, a character refused). A
  refused call changes and records nothing. It is honoured at any declared
  version, and it changes no answer.
- **`acting_operator`** on every audit record such a call writes, beside
  `principal_id`, in the record's hashed body: an operator's act, a grant's,
  an access-context push or binding, and a supervisor's `logout`. It is the
  supervisor's word, not Control's verification, and it is never a principal,
  a subject, a row's actor or a filter. Records written later by work the call
  started (`operator.claim_mapping.applied`, a sink's delivery) carry none. The
  access log line (`northbound_request`) and `northbound_refused` carry it
  too.
- **`server.Caller.ActingOperator`**: the name, decoded, on a host route's
  caller and on a report provider's call that named one. Record it as the
  supervisor's word beside `Principal`; never authorise on it.
- **The rule a reader of a record is held to**, written in
  `docs/API-VERSIONING.md`: a record's `attributes` is an open map whose keys
  are their writer's, never members of this surface. Skip a key you do not
  know, and refuse none; a new key moves no version.

## v0.34.0

Phase 0047, a revoked registration says so (M19). The supervisory
registration's protocol moves to `2`, and nothing else changes. No migration.
No exported identifier in `ext` or `server` is added, removed or changed, and
the north-bound API version stays `1`.

- **Registration protocol `2`**: `hello` says `"protocol":2`. When the
  deployment's operator revokes the registration locally (`hoplock-control
  supervision revoke`), the node holding it writes
  `{"kind":"revoked","id":…,"at":…}` as the connection's last line. `id` is a
  heartbeat's, `at` is the instant the deployment records (the first one, if
  it is revoked again), and there is no `by`. The node then ends the request
  body cleanly and waits for the supervisor to end its response, all within
  one heartbeat of the check that saw the revocation. A supervisor that does
  not take them within it loses the connection at that bound. Nothing else
  sends the line: a shutdown, a dropped connection, a failed write, a lost
  singleton and a silent supervisor end the connection as before, and a
  deployment revoked while not connected still sends nothing.
- **The rule a supervisor is held to**, written in `docs/API-VERSIONING.md`:
  skip a line kind or member it does not recognise, and never refuse one; read
  `hello`'s `protocol` to know which lines can arrive (a deployment at `1`,
  `v0.33.0`, never sends `revoked`). Nothing negotiates the number. A
  supervisor that ends its response when it reads the end of the request
  body, as the reference one in `internal/instance/register_test.go` does,
  receives the line.
- **Log**: `supervision_revoked` gains `revoked_at` and `told_supervisor`,
  which is true only when the supervisor ended its response after the line.

## v0.33.0

Phase 0046, instance identity and supervisory registration (M19). A
deployment has one identity, reports its health at every level it has, serves
a versioned north-bound API, and can register outbound to a supervisor. Run
`hoplock-control migrate`: migration `0024` adds four tables. No exported
identifier in `ext` or `server` is added, removed or changed.

- **Identity**: `GET /api/v1/tenants/{tenant}/instance` — the instance id
  (generated once, kept in the database, never a node's), `instance.display_name`,
  the software, contract and north-bound API versions, and the tenant set the
  caller may read.
- **Health**: `GET /api/v1/tenants/{tenant}/instance/health` — the deployment
  (database and schema, listeners), every node (version, report time, jobs,
  listeners, event-plane lag and failures), and the tenant's fleet summary.
  States `healthy`, `degraded`, `in_progress`, `unknown`, `stale`, `left`;
  every value carries its observation time; a rolling upgrade is
  `in_progress`. Health is read through a pool of its own
  (`instance.health_connections`) at most once per `instance.health_interval`.
- **North-bound API version `1`**, in `Hoplock-Api-Version` on every answer. A
  declared version outside the range is `400 api_version_unsupported`; an
  answer it cannot express is `500 api_version_unexpressible`. A client that
  declares nothing is served the current version, so existing clients are
  unchanged. The promise is `docs/API-VERSIONING.md`.
- **Supervisor tokens**: `identity token-issue --supervisor` mints an ordinary
  scoped token whose principal id begins `sup-`, so every act through it is
  attributed to it in the local audit trail. Listed by
  `GET …/instance/supervisors`, revoked by
  `POST …/instance/supervisors/{principal}/revoke` (`identity:write`, audited
  as `operator.supervisor.revoked`).
- **Supervisory registration**, off unless `supervisor.endpoint` is set: one
  outbound NDJSON connection held by the cluster singleton
  `hoplock.supervision`, carrying identity, health and a coarse event feed out
  with replay and `resync`, and acting on nothing inbound.
  `hoplock-control supervision revoke|restore|status` revokes it locally,
  without a restart.
- New permission `instance:read`, held by every role that reads a tenant.
- New configuration sections `instance:` and `supervisor:`.
- The listeners are bound before they are served, so a node reports whether
  each is serving.

## v0.32.0

Phase 0045, north-bound answer names: every member of every north-bound
answer is named in snake_case, before the north-bound API becomes a
compatibility promise (M19). Five answers rendered internal structs under Go
field names; each is now rendered through a view (`docs/PLAN.md` §8). No
migration, no new route, and no exported identifier in `ext` or `server` is
added, removed or changed. No status code, list order, key or cursor
changes: a cursor minted by an earlier release continues.

### Breaking

No identifier is added, removed or changed, so `make release-check` sees
none of this. Five wire changes:

- **`GET /api/v1/tenants/{tenant}/audit/blocked-commands`**, each item of
  `blocked`: `Record` becomes `record`, rendered exactly as an item of
  `GET …/audit/records` (`record_id`, `stream`, `chain_seq`, …, `body`). The
  record's `Grant`, `Enforcement`, `Device`, `DeviceFields` and `Gap` facts
  are no longer members: they are in its `body` alone, as on every other
  audit list. `Command`, `Action`, `TargetZone`, `TargetLabels`,
  `DecisionFound`, `MatchedRule`, `RouteType` and `DecisionAt` become
  `command`, `action`, `target_zone`, `target_labels` (`{}` when the target
  has none), `decision_found`, `matched_rule`, `route_type` and `decided_at`.
  When the decision is not found, `matched_rule`, `route_type` and
  `decided_at` are absent, where they were `""` and `0001-01-01T00:00:00Z`.
- **`GET /api/v1/tenants/{tenant}/audit/sessions/{session}/loss`**, each row
  of `loss`: `Cause`, `Reports`, `Records`, `Bytes` become `cause`,
  `reports`, `records`, `bytes`.
- **`GET /api/v1/tenants/{tenant}/proxies`**, each item of `proxies`:
  `ProxyID`, `Zone`, `State`, `Live`, `LastHeartbeatAt`,
  `SubscriptionSeenAt`, `ContractVersion`, `Capabilities`,
  `RelayRegistrations`, `Config`, `SessionCount`, `LastError`, `LastErrorAt`
  become `proxy_id`, `zone`, `state`, `live`, `last_heartbeat_at`,
  `subscription_seen_at`, `contract_version`, `capabilities`,
  `relay_registrations` (`[]` when none), `config`, `session_count`,
  `last_error`, `last_error_at`. Every zero instant is absent, where it was
  `0001-01-01T00:00:00Z`, and `last_error` is absent when empty.
- **The same answer's `liveness`**: `HeartbeatTTL`, `RelayRegistrationTTL`,
  `TargetCapabilityTTL`, `ReportAfter`, each a count of nanoseconds, become
  `heartbeat_ttl_seconds`, `relay_registration_ttl_seconds`,
  `target_capability_ttl_seconds` and `report_after_seconds`, each integer
  seconds rounded up (`90`, `60`, `86400`, `21600` with nothing configured).
- **`PUT /api/v1/tenants/{tenant}/identity/claim-mapping`**, a refused
  mapping's `parameters.rejections`: `Code`, `Field`, `Message` become
  `code`, `field` (absent at document scope) and `message`.

## v0.31.0

Phase 0044, registered reports served: Control lists and generates every
registered `ext.ReportProvider` on its north-bound API, for the tenant the
middleware resolved, under `audit:read` (`docs/PLAN.md` §7 "Reports", M15).
Raised by `Hoplock/enterprise#26`, item 5. No migration, and no exported
identifier in `ext` or `server` is added, removed or changed.

### Added and changed

- **`GET /api/v1/tenants/{tenant}/reports`** lists every registered provider,
  in registration order: `provider`, `version` and `reports`, each `report`,
  `formats` (`json`, `csv`, `pdf`) and `parameters` (`name`, `required`,
  `values`). It answers `200` whenever Control can: a provider whose `Reports`
  failed, whose descriptors Control cannot serve, or that had not answered
  within the request's bound is listed with `error`
  (`report_provider_unavailable` or `report_provider_failed`) and no reports.
  With nothing registered it is `{"providers": []}`. It is not paged.
- **`POST /api/v1/tenants/{tenant}/reports/generate`** takes `{provider,
  report, from, to, parameters, format}` and streams the report. Control calls
  `Reports` for the tenant first and generates only what is listed there
  (`404 not_found` otherwise), and answers `400 invalid_request` naming the
  field for a format the descriptor does not list, a required parameter
  missing, one it does not name, a value outside a closed set, or a `from` or
  `to` that is missing, not RFC 3339, or not in order (`from` inclusive, `to`
  exclusive) — all before `Generate` is called. The body is served as the
  format's media type (`application/json`, `text/csv; charset=utf-8`,
  `application/pdf`), never the provider's, with `X-Content-Type-Options:
  nosniff`, `Content-Disposition: attachment` (a filename in `[A-Za-z0-9._-]`),
  `Cache-Control: no-store` and no `Content-Length`. A `Report` in another
  format or media type is `502 report_provider_failed`, its body closed unread.
  A body that fails after its first byte, or the bound running out, aborts the
  response: never a short report.
- **Errors.** `Generate`'s `ErrNotFound` is `404 not_found`; its `ErrInvalid`
  `400 invalid_request` with the provider's message; `ErrUnavailable` from
  either call `503 report_provider_unavailable`; every other kind (`ErrDenied`
  included), an error that is not an `*ext.Error`, a panic, or a descriptor
  Control cannot serve `502 report_provider_failed`. A generation past the
  node's ceiling is `503 report_generation_busy` with `Retry-After`. Three new
  codes: `report_provider_unavailable`, `report_provider_failed`,
  `report_generation_busy`.
- **`north.report_timeout`** (default `10m`, or `north.request_timeout` when
  that is longer; at least `north.request_timeout` and at most `1h`) bounds a
  generation alone, from the start of the request to its last byte. Every
  other route keeps `north.request_timeout`.
- **`north.max_concurrent_reports`** (default `2`; a negative is refused)
  caps the generations running at once on one node. The listing is not
  counted.
- **`server.CallerFrom`** now also answers inside Control's calls into a
  registered `ext.ReportProvider` (`Reports` and `Generate`), with the
  request's tenant, subject, principal, break-glass flag and correlation id.
  `server.WriteError` still answers only inside a host route.
- **`ext` doc comments** on `ReportFormat`, `ReportParameter`,
  `ReportDescriptor`, `ReportRequest`, `Report` and `ReportProvider` state what
  Control now does: the media type of each format, what makes a descriptor
  servable, the checks made before `Generate`, the period's bounds, the bound
  and the ceiling, the answer to each error kind, that `Body.Close` may be
  called while a `Read` is in progress, and that Control records nothing of a
  generation.

## v0.30.0

Phase 0043, directory-scoped group entries: a claim mapping's group entry may
list the directories it applies to, a tenant may require that every directory
group match only such an entry, and every claim mapping write now reaches
every directory subject by itself (decision M7, `docs/PLAN.md` §2 and §6).
Raised by `Hoplock/enterprise#51`, item 2. No migration, and nothing in `ext`
or `server` changes.

### Added and changed

- **`directories` on a `groups.map` entry** lists directory sources, as an
  identity sync names them. Such an entry matches a directory group of its
  `external` name from a directory it lists, and never a login's group claim.
  For a group named N from directory S, the entry listing S for N wins
  *instead of* the entry for N with no `directories`, never as well as it.
  An entry with no `directories` matches as before: logins, and every
  directory not listed for its name. There is no wildcard: a directory
  connected later reaches no scoped entry until somebody lists it.
- **`groups.require_directories: true`** makes every directory group match
  only an entry that lists its own directory; the unscoped entries then serve
  logins alone. A mapping with neither key maps exactly as before, and
  `schema_version` stays `1`.
- **Three mapping rejection codes**: `mapping.directory_name` (a listed name
  that is not a valid source), `mapping.directories_empty` (`directories: []`,
  which is refused rather than given a meaning), and
  `mapping.directory_duplicate` (a source listed twice for one `external`, in
  one entry or across two; the message names the entry that listed it
  first). `mapping.too_large` counts a scoped entry once for each directory
  it lists, so the bound holds over what a document matches; every document
  valid before stays valid.
- **`GET …/identity/claim-mapping`** gains `directories` (the sorted sources
  the mapping lists, empty when none), `require_directories`, and
  `stale_directory_subjects` (how many directory subjects are not yet on the
  active version). `groups` includes what scoped entries produce. The `PUT`'s
  answer is unchanged.
- **The decision record's subject gains `directory_source`**, the directory a
  provisioned subject's groups came from, beside `mapping_version`. It is
  omitted for a subject no directory provisioned and absent on older records.
  `explain` shows it in `mapping.directory_source`, and `policyctl explain`
  prints it on the `mapping` line.
- **`operator.claim_mapping.applied`**: a record of a mapping version reaching
  the tenant's directory subjects, with `recalculated`, `changed` and
  `complete`. Its actor is the version's author.
- `identity mapping-put` (dry run included) and `identity mapping-show` print
  the directories the mapping lists and `require_directories`; `mapping-show`
  prints how many directory subjects are not yet on the active version, and
  `mapping-put` names any listed source nothing in the tenant has written yet.

### Operator notes

- **A mapping that uses `directories` or `require_directories` is unreadable
  to an older build**, which decodes the mapping strictly. An older node that
  reads one as a tenant's active mapping refuses that tenant's logins and
  directory writes as an outage; it never applies it unscoped. Finish a
  rolling upgrade before writing a mapping that scopes.
- **Every claim mapping write, scoped or not, now reaches every directory
  subject by itself**: at once when written through the API, and within a
  minute when written by `identity mapping-put` straight to the database, on
  any node that has seen the tenant (its configured tenant, a tenant its
  proxies subscribe in, or one whose mapping it served). It runs whether or
  not an identity sync is registered or started. Each subject whose groups,
  claims or login names change has its cached decisions dropped
  (`cache_invalidate`, or one for the tenant past 64 in a batch); live
  sessions are not ended. Until this release a new mapping reached a directory
  subject only when the directory changed that person.
  `stale_directory_subjects` shows how many remain.

## v0.29.0

Phase 0042, the directory source on the sync sink: every directory identity
and group keyed by the directory it comes from, so two directories can feed
one tenant, each keeping its own ids (decisions M7 and M28, `docs/PLAN.md`
§2). Raised by `Hoplock/enterprise#51`, item 1. **Run `hoplock-control
migrate`:** migration `0023` keys `directory_links` and `directory_groups` by
`(tenant, source, external_id)` and adds `idp_connectors.directory_source`.

### Breaking

- **`ext.SyncSink.DeactivateSubject(ctx, ref ext.DirectoryRef, reasonCode
  string)`**, where it took `(ctx, tenant, externalID, reasonCode)`, and
  **`ext.SyncSink.DeleteGroup(ctx, ref ext.DirectoryRef)`**, where it took
  `(ctx, tenant, externalID)`. Every implementation and every caller changes.
- **`Source` is required on `ext.SyncedSubject`, `ext.SyncedGroup` and
  `ext.DirectoryRef`**, and matches `^[a-z0-9][a-z0-9-]{0,62}$`. A caller
  compiled against `v0.28.0`'s structs still builds, and every one of its
  calls is then `KindInvalid` naming `source`, applies nothing, and is
  recorded as `identity_sync.refused`. No API diff sees this.
- **Rows written before this release are keyed under the source `default`**:
  every link, every group and tombstone, and every connector with a
  `directory_claim`. A sync rebuilt against this release that sends
  `Source: "default"` carries on exactly as it was. One that sends another
  name provisions new subjects, and each whose username an old subject still
  presents is refused `KindConflict` rather than doubled.
- **`identity connector-put --directory-claim` needs `--directory-source`**,
  and the reverse; a tie to a source nothing in the tenant has written is
  refused. A script that ties a connector with the claim alone fails.

### Added and changed

- **`ext.DirectoryRef`** (`Tenant`, `Source`, `ExternalID`) names one
  identity or group in one directory. **`ext.SyncedSubject.Source`** and
  **`ext.SyncedGroup.Source`** name the directory each comes from.
- **Everything the sink keyed on an `ExternalID` is keyed on `(tenant,
  source, ExternalID)`:** the link and the group, the ordering by
  `UpdatedAt`, a deactivation kept for an id not yet seen, the group
  tombstone, the delivery a retry owes, and the per-identity lock. Two
  directories that both call somebody `00u1` provision two subjects, and a
  deactivation, a stale write or a tombstone in one touches nothing in the
  other. A subject's `Groups` resolve among the groups of its own source, and
  a group change re-derives only that source's members. A login name is still
  unique per tenant across sources (`KindConflict`), and a subject is never
  adopted or merged, so a person who moves between directories is two
  subjects.
- **A tied connector names its source.** `identity connector-put
  --directory-source <source>` beside `--directory-claim`: both or neither,
  the source held to the pattern above, and refused while nothing in the
  tenant comes from it (the error names the sources the tenant has, up to
  twenty), so provision first, then tie. Writing a connector with neither
  unties it. A login through it looks the link up by `(tenant,
  directory_source, claim value)`. `GET /api/v1/session/methods` shows
  `directory_source` beside `directory_claim`, both omitted when empty.
- **The subject read's `directory` is `{provider, source, external_id}`.**
  The top-level `source` is unchanged: what asserted the subject, for a
  directory subject the sync's provider, not the directory's source.
- **The decision record names the directory's source.** A deactivation's
  standing entry carries `source` beside `external_ref`, and `explain` shows
  it. A lock's and a quarantine's entries carry none, and records written
  before this release have none.
- **Every `identity_sync.*` record carries `source`** beside `provider` and
  `external_id`, `identity_sync.refused` included whenever the refused call
  named one.
- The authorize path sends the statements it sent before: the link's `source`
  is read in the join that already reads the deactivation.

## v0.28.0

Phase 0041, confined inbound actions: an action held to its requester's scope,
and an unlock that lifts only its own lock (decision M28, extended,
`docs/PLAN.md` §2). Raised by `Hoplock/enterprise#50`. An action that sets
neither new field behaves exactly as it did in `v0.27.0`. **Run
`hoplock-control migrate`:** migration `0022` adds
`action_executions.if_placed_by`.

- **`ext.Action.Within *ext.ActionScope`** confines an action to what its
  requester may touch. `ext.ActionScope` has three fields, each of which must
  hold for everything the action reaches when it is set: `SubjectIDs` and
  `TargetIDs` (at most 1 000 each, each held to an action id's bounds) and
  `TargetLabels` (at most 64, each key and value one non-blank line of at most
  256 bytes). Control resolves what the action reaches from its own data
  inside the transaction that performs it — a session's subjects and targets
  from its decision records, every hop included; a grant's subject and the
  targets its selector can reach — and refuses an action that reaches outside:
  `KindDenied`, `ReasonCode` `out_of_scope`, nothing executed, recorded as
  `action.refused` with `scope_field` and `outside`. The error names the
  scope field and never the subject or target outside it.
- **A dimension a kind reaches whole fails a scope that confines it.** A scope
  that sets `TargetIDs` or `TargetLabels` refuses `lock_subject`,
  `unlock_subject` and a kill by subject; one that sets `SubjectIDs` refuses
  `quarantine_target`.
- **A grant is inside a target scope only when its selector cannot reach
  outside it.** It must list hostnames, each naming a target the tenant holds
  (matched case-insensitively) that the scope admits, unless its own labels
  require a label the scope requires. A wildcard hostname, a hostname with no
  target row, a selector of zones alone and an empty selector are outside
  every target scope.
- **A confined requester learns nothing outside its scope.** A selector Control
  holds nothing for is `out_of_scope` rather than `KindNotFound`, unless the
  scope names it by id alone. A repeat of an `ActionID` is checked against the
  scope it carries before the first result is returned or anything is sent
  again. A non-nil scope with every field empty is `KindInvalid` on `within`;
  a bound exceeded is `KindInvalid` on `within.subject_ids`,
  `within.target_ids` or `within.target_labels`.
- **`ext.Action.IfPlacedBy`** makes `unlock_subject` conditional: the lock is
  lifted only if the action with that `ActionID` placed it **and** this
  handler's principal (`action-handler:<provider>`) did. Any other lock — an
  operator's, another action's, one placed under a provider name since
  changed — is left standing: `Affected` 0, `ReasonCode` `not_this_lock`, nil
  error, recorded as `action.unchanged`. With no lock it is `not_locked`, as
  before. It is `KindInvalid` on `if_placed_by` with any other kind. It is part
  of the action's identity: an unlock under a used `ActionID` with a different
  `IfPlacedBy`, or with one where the first had none, is `KindConflict`.
  `Within` is not.
- The doc comments of `ext.ActionLockSubject`, `ext.ActionUnlockSubject`,
  `ext.ActionResult` and `ext.ActionExecutor` say what is now true. No method
  is added to any interface.

## v0.27.0

Phase 0040, identity sync: Control's sync sink and de-provisioning that takes
effect. Control implements `ext.SyncSink` and starts the registered
`ext.IdentitySync`, a directory's deactivation is a standing denial only the
directory lifts (decision M28, extended, `docs/PLAN.md` §2 and §6), and a login
through a connector tied to the directory reaches the subject the directory
provisioned. Raised by `Hoplock/enterprise#26`. With no sync registered nothing
runs that did not before. **Run `hoplock-control migrate`:** migration `0021`
adds `directory_links`, `directory_groups`, `idp_connectors.directory_claim` and
an index on `north_principals`.

No `ext` identifier is added, removed or changed, so `make release-check` sees
none of this. The doc comments of `ext.SyncedSubject`, `ext.SyncedGroup`,
`ext.SyncSink` and `ext.IdentitySync` say what is now true, and `SyncSink`
carries a **concurrency contract it did not have**: Control's sink is safe for
concurrent use, two calls on one `ExternalID` are serialised, and calls on
different ids run concurrently. Read them before you build a sync on them.

- **A registered `ext.IdentitySync` is started.** `Start(ctx, sink)` is called
  once, after the registry is sealed and before either listener opens, with
  the server's lifetime as the context; `Stop` on shutdown, before the
  listeners drain, bounded by the shutdown deadline. A `Start` that returns an
  error, panics or does not return in 30 seconds is logged at `ERROR` with the
  provider and the error's kind, and Control serves. A directory that is
  merely unreachable is not a `Start` error. A sink call once `Stop` began is
  `KindUnavailable`.
- **`UpsertSubject` provisions a subject joined on `ExternalID`, never on the
  username.** Control allocates the subject id (`dir-…`). The subject's one
  login name is the directory's `Username`, replaced on each write; a name
  another subject in the tenant presents is `KindConflict`, and an existing
  subject is never adopted. Its groups and attributes reach policy only
  through the tenant's claim mapping: attributes and `Email` (as `email`) are
  offered as claims, `Groups` (group `ExternalID`s) resolved to the names
  `UpsertGroup` gave and offered under the mapping's `groups.claim`. An
  attribute named `chain_hop_proxy_id` or under `hoplock.` is dropped.
  `UpdatedAt` is required; an upsert not later than the last applied one
  changes nothing and writes nothing.
- **`DeactivateSubject`, or an upsert with `Active: false`, is a standing
  denial.** Every authorize for the subject is a deny with
  `subject_deactivated` whatever policy, grants or windows say, and only the
  directory lifts it: an upsert with `Active: true` whose `UpdatedAt` is later
  than both the last applied one and the instant Control applied the
  deactivation. It revokes the subject's north-bound sessions, API tokens and
  outstanding certificates, every live or scheduled grant, every pending grant
  request, and its live sessions, which are told "Your account was deactivated
  by your organisation's directory." A deactivation for an unknown id is kept,
  and the create that follows provisions the subject deactivated. A repeat
  writes no second record and finishes what the first could not.
- **`UpsertGroup` and `DeleteGroup`.** A group's `Name` is what the claim
  mapping matches. A group named anew, renamed, deleted or re-created
  re-derives every member in one transaction and drops their cached
  decisions. A delete keeps a tombstone, and an older upsert after it changes
  nothing.
- **Errors**: `KindInvalid` names the field and changes nothing; `KindConflict`
  is the username; `KindUnavailable` is a change that stands whose revocations
  or publications could not be finished, safe to retry with the same call;
  `KindInternal` is a store failure, safe to retry.
- **A connector may name a `directory_claim`**: `hoplock-control identity
  connector-put --directory-claim <claim>`, refused while no identity sync is
  registered. A login through it reaches the provisioned subject and rewrites
  nothing on it, or is refused with `federation.not_provisioned` and creates
  nothing. `GET /api/v1/session/methods` shows the claim.
- **A deactivated subject's login is refused through every door**
  (`federation.subject_deactivated`), and a north-bound credential of the
  subject stops authenticating. A federated login that would add a login name
  a directory-managed subject presents is refused (`federation.login_refused`).
- **`GET …/subjects` and `…/subjects/{subject}` gain `directory_managed`,
  `directory` (`provider`, `external_id`), `deactivated` and `deactivation`
  (`deactivated_at`, `reason_code`).** `PUT` and `DELETE` of a
  directory-managed subject are `409 subject_directory_managed` (a new code).
- **`GET …/extensions` shows whether the identity sync started**, on its row,
  as `started` and `start_error`, the action handler's shape.
- **New records on the control stream**, with the sync as actor
  (`identity-sync:<provider>`): `identity_sync.subject_provisioned`,
  `identity_sync.subject_updated`, `identity_sync.subject_deactivated`,
  `identity_sync.subject_reactivated`, `identity_sync.group_upserted`,
  `identity_sync.group_deleted` and `identity_sync.refused`; a deactivation's
  revocations write `grant.revoked`, `grant.request_closed` and
  `operator.revocation.published`.

## v0.26.0

Phase 0039, inbound actions: the executor, a subject lock and a target
quarantine. Control implements `ext.ActionExecutor` and starts the registered
`ext.ActionHandler`, and an operator can now pull every lever the handler can
(decision M28, `docs/PLAN.md` §2). Raised by `Hoplock/enterprise#26`. With no
handler registered nothing runs that did not before, except the lock and the
quarantine, which an operator places and lifts. **Run `hoplock-control
migrate`:** migration `0020` adds `subject_locks`, `target_quarantines` and
`action_executions`.

No `ext` identifier is added, removed or changed, so `make release-check` sees
none of this. The doc comments of `ext.ActionKillSession`, `ext.Action`,
`ext.ActionResult`, `ext.ActionExecutor`, `ext.ActionHandler` and
`ext.KindDenied` say what is now true. Read them before you build an inbound
channel on them.

- **A registered `ext.ActionHandler` is started.** `Start(ctx, exec)` is called
  once, after the registry is sealed and before either listener opens, with
  the server's lifetime as the context. `Stop` is called on shutdown before the
  listeners drain, bounded by the shutdown deadline, whether or not `Start`
  succeeded. A `Start` that returns an error, panics or does not return in 30
  seconds is a broken registration: Control logs it at `ERROR` with the
  provider and the error's kind, serves, and executes nothing. A handler whose
  platform is merely unreachable returns nil and reconnects on its own
  goroutines; Control never retries a `Start`.
- **`exec.Execute` performs all five kinds.** Each goes through the service an
  operator's north-bound route calls. The handler is the actor on every record
  it writes and every grant row it touches: `action-handler:<provider>`, no
  subject. Each record carries `action_id`, `action_kind`, `external_ref`,
  `reason_code` and `provider`.
  - `kill_session` takes exactly one of `SessionID` or `SubjectID`; both, or
    neither, is `KindInvalid`. `Affected` is one: the kill Control published.
    It is never a count of sessions seen to close.
  - `lock_subject` and `unlock_subject` take `SubjectID`; `revoke_grant`
    takes `GrantID`; `quarantine_target` takes `TargetID`. A selector a kind
    does not take is `KindInvalid`, naming the field.
  - It is idempotent per tenant on `ActionID`: the same action returns the
    first result and sends again what the first owed the event stream; a
    different action under the id is `KindConflict`.
  - `KindNotFound` for a subject, session, grant or target not known in
    `Action.Tenant`, which includes one that exists only in another tenant.
    `KindDenied` with `ReasonCode` `break_glass_subject` for a lock on a
    break-glass subject. `KindUnavailable` when the event stream refused a
    publication, or Control is stopping: the act was made and stands, and the
    same `ActionID` delivers it. `KindInternal` is a store failure.
  - `ReasonText` is shown to the holder verbatim, so it must be one line, with
    no control characters, at most 512 bytes: anything else is `KindInvalid` on
    `reason_text` and is never truncated. When it is empty Control shows
    "Your session was ended as part of a security response.",
    "Your access was suspended as part of a security response." or "Your
    temporary access grant was revoked as part of a security response."
  - Zero `Affected` with a nil error is nothing to act on, with `ReasonCode`
    `already_locked`, `not_locked`, `already_quarantined`, `already_revoked` or
    `expired`.
- **A subject lock and a target quarantine.** While one stands, every authorize
  call it covers is a deny (`subject_locked`, `target_quarantined`) whatever
  policy, grants or external windows say; it is a deny made on purpose and
  never an outage. A lock holds for a subject id with no row, and survives a
  login, a `PUT`, and a delete followed by a re-create. The decision record,
  `GET …/decisions/{id}` and replay say so. Routes, all under `session:revoke`,
  which `grant-admin` and `admin` hold:
  - `POST /api/v1/tenants/{tenant}/subjects/{subject}/lock` and `…/unlock`,
    with an optional `reason_code` and `reason` (the text the holder is shown);
  - `POST /api/v1/tenants/{tenant}/targets/{target}/quarantine` and
    `…/release`;
  - each answers `200` and says whether this call changed anything, and a lock
    or quarantine already standing, or none to lift, changes nothing and
    writes no record;
  - `503 standing_denial_undelivered` is a denial that stands whose
    publication failed: ask again to re-send.
- **`GET …/subjects`, `…/subjects/{subject}`, `…/targets` and
  `…/targets/{target}` gain `locked` and `lock`, `quarantined` and
  `quarantine`.** A subject whose row was deleted while locked reads with
  `record_missing: true`. `DELETE …/targets/{target}` on a quarantined target
  is `409 target_quarantined` until it is released.
- **`GET …/extensions` shows whether the handler started.** The action handler's
  row, while one is registered, gains `started` and, when `Start` failed,
  `start_error` (the error's kind). The listing stays one object per row, which
  M26 exempts, and no other row changes.
- **New records on the control stream:** `operator.subject.locked`,
  `operator.subject.unlocked`, `operator.target.quarantined`,
  `operator.target.released`, and, for an inbound action only, `action.refused`
  and `action.unchanged`.
- **Hoplock Enterprise:** every obligation above holds from this release, not
  from `main`.

## v0.25.0

Phase 0038, host routes and the console in namespaces of their own. The
north-bound listener is four namespaces, each under a prefix of its own, and
no path is in two of them (decision M27, `docs/PLAN.md` §2): Control's API
under `/api/v1/` outside `/api/v1/hosts/`; a host's API under
`/api/v1/hosts/<provider>/`; Control's console under `/console/`; and a host's
screens under `/host-console/<provider>/`. A host's route can no longer take
a path Control serves, today or in a later release, and Control can no longer
take one of the host's.

### Breaking

No identifier is added, removed or changed, so `make release-check` sees
none of this. Six behaviour changes:

- **Every host route moved.** A tenant route is served at
  `/api/v1/hosts/<Provider>/tenants/{tenant}/<Pattern>`, where it was
  `/api/v1/tenants/{tenant}/<Pattern>`; a cross-tenant read at
  `/api/v1/hosts/<Provider>/cross-tenant/<Pattern>`, where it was
  `/api/v1/cross-tenant/<Pattern>`. `Pattern` is unchanged and still
  relative. The old paths answer Control's `404 not_found`. The route
  listing and the start-up log name each route at its full path. Tenancy is
  resolved exactly as before: `{tenant}` is a selector checked against the
  caller's scope, and `server.CallerFrom` hands a handler what it did.
- **`server.Options.Provider` is `<owner>/<name>`.** Each of the two is
  lower-case letters, digits and hyphens (`hoplock/enterprise`), because it
  is two segments of every path the host serves. Any other provider is
  refused at start-up, before the configuration is read.
- **A host pattern with a `.` segment, or an empty segment before its end, is
  refused at start-up**, as one with `..` was. The mux redirects a request
  for such a path to its clean form, so the route could never be served, and
  `ca//` turned Control's `404` under `…/ca/` into a redirect.
- **A host pattern equal to one of Control's is no longer refused.**
  `targets/{target}`, `grants`, or a literal such as `targets/special` is a
  path in the host's own namespace now, and is mounted there. Start-up still
  refuses a host route on a path Control serves, as defence in depth, and no
  pattern a host can write reaches one.
- **`/console/` and `/host-console/` are reserved.** Nothing is served there
  yet; Control's console (0044) and a host's screens will be, each under its
  own prefix with no catch-all outside it. Once Control's console is
  mounted, `/` redirects to `/console/`; until then it is `404 not_found`, as
  before.
- **What `v0.10.0` left out.** Its entry, and the doc comment of
  `server.Route` since, said a host could not take a path Control serves.
  That held for every path Control served and every `405` it answered — this
  phase checked each route of Control's against every overlap shape, at the
  request level. But a host catch-all (`{x...}`), a subtree (`targets/`), the
  tenant root (`{$}`) or a wildcard over Control's literals answered paths
  under `/api/v1/tenants/{tenant}/` that Control did not serve yet, in place
  of Control's `404` envelope. A later release serving one of those paths
  would have refused that host at start-up, or taken the path from it with
  no error. Neither can happen now.

- **Phase:** 0038, host routes and the console in namespaces of their own.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `server.Route`, `server.AccessTenant`,
  `server.AccessCrossTenant` and `server.Options.Provider` say where a host's
  routes are served.
- **Migration:** none.

## v0.24.0

Phase 0037, bounded lists 9: the audit joins, negotiated sessions and
per-session reads. The last two lists answer in decision M26's shape
(`docs/PLAN.md` §2): `GET /api/v1/tenants/{tenant}/audit/blocked-commands`,
by its record's recorded-at, stream and chain sequence, and
`…/audit/negotiated`, by each session's latest matching record. A session's
certificates and its algorithm summary say when their bound cut them. Every
`GET` route Control serves is now a paged list, a list bounded by one object
that says when it was cut, or one object or a list bounded by configuration.

### Breaking

No signature changes; `make release-check` finds none. Five behaviour
changes:

- **`…/audit/blocked-commands` gains `truncated` and `next`.** It is
  `{"blocked": [...], "truncated": bool, "next": "<cursor>"}`, newest first,
  and now takes `?order=asc` for oldest first. Pass `next` back as `?after=`
  with the same `label`, window and order to read the following page; a
  cursor is `400 invalid_request` `{field: after, problem: invalid}` under
  any other label, label value, window or order.
- **`…/audit/negotiated` gains `next`, and each session is an object.**
  `sessions` was a list of session ids; each item is now
  `{"session_id": "…", "latest_recorded_at": "<RFC 3339>"}`, the session's
  latest matching record being its place in the answer, which is what a
  cursor holds. The order is unchanged: that instant, newest first, then the
  session id. `truncated` keeps its meaning, and `next` is present exactly
  when it is true; a cursor binds the question (`axis`/`value`, `floor` or
  `ban_axis`/`ban_name`) and the window. The emergency runbook ends exactly
  the sessions listed, so a caller follows `next` until `truncated` is false
  and hands every `session_id` to `session_kill`.
- **`…/audit/sessions/{session}/certificates` and `…/algorithms` gain
  `truncated`.** Each reads at most 500 of the session's records, as before,
  and now says when the session had more. The algorithm summary of the
  first 500 is still served. Neither takes `limit` or `after`.
- **A limit above 10 000 is refused on both lists, not clamped.** It is
  `400 invalid_request` `{field: limit, problem: exceeds_maximum, limit:
  "10000"}`. Absent, a page holds at most 500, as before.
- **A walk of `…/audit/negotiated` lists each session once.** A session
  that gains a newer matching record while a caller walks the pages moves
  toward the head of the list, behind the walk's position. That walk does
  not list it again, nor at all if it had not reached it yet; the next walk
  lists it.

- **Phase:** 0037, bounded lists 9: the audit joins, negotiated sessions and
  per-session reads.
- **Public packages:** none changed.
- **Migration:** none.

## v0.23.0

Phase 0036, bounded lists 8: the audit query. Five audit lists now answer in
decision M26's shape (`docs/PLAN.md` §2), one page at a time, by recorded-at,
stream and chain sequence: `GET /api/v1/tenants/{tenant}/audit/records`,
`…/audit/drift`, `…/audit/algorithm-policy-unmet`, `…/audit/gaps` and
`…/audit/certificates/{serial}/records`.

### Breaking

No signature changes; `make release-check` finds none. Four behaviour
changes:

- **A limit above 10 000 is refused, not clamped.** The five lists took
  `?limit=` above 10 000 and answered 10 000 without saying so. It is now
  `400 invalid_request` `{field: limit, problem: exceeds_maximum, limit:
  "10000"}`; zero, a negative or a non-integer is still `{field: limit,
  problem: invalid}`. Absent, a page holds at most 500, as before, and a
  page holds exactly `limit` unless it is the last.
- **The five answers gain `truncated` and, when cut, `next`.** Each is
  `{"<member>": [...], "truncated": bool, "next": "<cursor>"}` — `records`,
  `changes`, `unmet`, `gaps`, and `records` beside `serial` for the
  certificate join, which every page carries. Pass `next` back as `?after=`
  with the same filters to read the following page. A cursor binds the
  list, every filter the list read (repeated `kind` and `severity` values in
  any order bind once), the window and the order, and is
  `400 invalid_request` `{field: after, problem: invalid}` under anything
  else. A client that read one of these lists from one answer, and stopped
  at what it was given, must now follow `next` until `truncated` is false.
- **Every one of the five takes `?order=asc`.** Only `…/audit/records` did;
  the others now read oldest first with it, and newest first otherwise, as
  before. Within one instant, records are ordered by stream and then chain
  sequence, in the list's direction, where the order among them was the
  planner's.
- **Migration `0019` locks `audit_records` while it runs.** It builds one
  index in one transaction, which blocks writes to the table — every
  proxy's log batch, which a proxy buffers to disk and resends, and every
  operator act's record — until it commits, and drops the index it replaces
  at the end, which blocks reads from the drop to the commit. Run
  `hoplock-control migrate` when that pause is acceptable; its length grows
  with the table.

Also observable: an item of `…/audit/drift`, `…/audit/algorithm-policy-unmet`
and `…/audit/gaps` gains `stream` and `chain_seq`, its record's place in the
order, which is what a cursor holds.

- **Phase:** 0036, bounded lists 8: the audit query.
- **Public packages:** none changed.
- **Migration:** `0019_audit_query_order` replaces `audit_records_recorded_idx`
  `(tenant, recorded_at DESC)` with `audit_records_order_idx`
  `(tenant, recorded_at DESC, stream DESC, chain_seq DESC)`, read backwards
  for `?order=asc`. No row is rewritten.

## v0.22.0

Phase 0035, bounded lists 7: policy versions, a session's decisions and a
finding's targets. `GET /api/v1/tenants/{tenant}/policy/bundles` now answers
in decision M26's shape (`docs/PLAN.md` §2), one page at a time, and the two
lists bounded by one object say when their bound cut them: a session's
decisions, and the targets or proxies a publish-time finding names.

### Breaking

No signature changes; `make release-check` finds none. Five behaviour
changes:

- **The version list answers at most 500 versions by default, and refuses
  above 10 000.** It answered every version the tenant ever uploaded.
  `?limit=` asks for 1 to 10 000; a page holds exactly that many unless it
  is the last. Above 10 000 is `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`; zero, a
  negative or a non-integer is `{field: limit, problem: invalid}`.
- **The version list gains `truncated` and, when cut, `next`.** It is
  `{"versions": [...], "truncated": bool, "next": "<cursor>"}`, newest first
  by version; a version in it is unchanged. Pass `next` back as `?after=` to
  read the following page; a cursor from another list is
  `400 invalid_request` `{field: after, problem: invalid}`. A client that
  read the history from one answer must now follow `next` until `truncated`
  is false. `policyctl versions` does, and prints the same lines as before;
  its `-json` prints one answer holding every page's versions.
- **A session's decisions gain `truncated`.**
  `GET /api/v1/tenants/{tenant}/sessions/{session}/decisions` answers its
  newest 100, as before, and now says `"truncated": true` when the session
  has more. It takes no `limit` and no `after`. `policyctl explain -session`
  prints a warning on stderr when the answer is cut, and still exits `0`.
- **A publish-time finding names at most 100 targets or proxies.** On
  `satisfiability.no_proxy_provides`, `satisfiability.target_cannot_take`,
  `satisfiability.target_unproven`, `algorithm.target_below_floor`,
  `algorithm.target_not_reported` and the three findings below, `targets` or
  `proxies` keeps its first 100, in its existing order, beside
  `targets_count` (or `proxies_count`), the whole number, and
  `targets_truncated` (or `proxies_truncated`). Both are present on every
  such finding, cut or not. `satisfiability.no_proxy_provides`'s message now
  gives how many proxies fall short instead of naming each. The report is
  the answer to validate, upload, apply and activate, and the body of
  `409 findings_not_accepted`.
- **What a build lacks is one finding per rule, naming each build.** These
  were one finding per proxy, so a report grew with the fleet:
  - `algorithm.floor_undeclared` is `algorithm.floor_undeclared/<rule>`
    (was `…/<rule>/<proxy>`), with `proxies`, a list of proxy ids, in place
    of `proxy_id`;
  - `algorithm.axis_emptied` is `algorithm.axis_emptied/<rule>/<axis>` (was
    `…/<rule>/<proxy>/<axis>`), with
    `proxies: [{"proxy_id": "...", "offered": [...]}]` in place of
    `proxy_id` and `offered`;
  - `algorithm.declaration_unknown` naming proxies is
    `algorithm.declaration_unknown/<rule>/<profile>` (was
    `…/<rule>/<proxy>`), with `proxies` in place of `proxy_id`. The one that
    names no proxy, `algorithm.declaration_unknown/<rule>`, is unchanged.

  **An acceptance of an old id accepts nothing.** An `apply` or `activate`
  that accepted `algorithm.floor_undeclared/<rule>/<proxy>` or
  `algorithm.axis_emptied/<rule>/<proxy>/<axis>` under `v0.21.0` is refused
  `409 findings_not_accepted` until it accepts the new id, which covers
  every build the rule falls short on. A pipeline carrying `-accept` flags
  forward must replace them. Activation records already written keep the ids they
  accepted.

- **Phase:** 0035, bounded lists 7: policy versions, a session's decisions
  and a finding's targets.
- **Public packages:** none changed.
- **Migration:** none. The version list's order is the primary key
  `(tenant, version)`, read backwards, and the list no longer reads a
  version's source; `GET …/policy/bundles/{version}` still answers it.

## v0.21.0

Phase 0034, bounded lists 6: role bindings and outstanding certificates.
`GET /api/v1/tenants/{tenant}/role-bindings` and
`GET /api/v1/tenants/{tenant}/ca/certificates` now answer in decision M26's
shape (`docs/PLAN.md` §2), one page at a time, each over an index migration
`0018` adds.

### Breaking

No signature changes; `make release-check` finds none. Four behaviour
changes:

- **Both lists answer at most 500 items by default, and refuse above
  10 000.** They answered every binding and every outstanding certificate in
  the tenant. `?limit=` asks for 1 to 10 000; a page holds exactly that many
  unless it is the last. Above 10 000 is `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`; zero, a
  negative or a non-integer is `{field: limit, problem: invalid}`.
- **Both answers gain `truncated` and, when cut, `next`.**
  `role-bindings` is `{"bindings": [...], "roles": [...], "truncated": bool, "next": "<cursor>"}`,
  ordered by role, then a subject's bindings before a group's, then the
  holder's id; `roles` is the same on every page.
  `ca/certificates` is `{"tenant": "...", "certificates": [...], "truncated": bool, "next": "<cursor>"}`,
  ordered by issuance and then serial, newest first, and still without the
  certificate itself. Pass `next` back as `?after=` to read the following
  page; a cursor from another list is `400 invalid_request`
  `{field: after, problem: invalid}`. A client that read a whole list from
  one answer must now follow `next` until `truncated` is false.
- **Outstanding is judged on every page.** A certificate that expires or is
  revoked during a walk is not on the pages after it; the others keep their
  places, because issuance never moves. `hoplock-control ca certificates`
  reads every page and prints the same lines as before.
- **Migration `0018` locks `identity_role_bindings` and `ssh_certificates`
  against writes while it runs.** It builds one index on each in one
  transaction, which blocks binding and unbinding roles, and issuing and
  revoking certificates (the brokered-certificate path included), until it
  commits. Reads continue, and nothing is dropped. Run
  `hoplock-control migrate` when that pause is acceptable; its length grows
  with the two tables.

- **Phase:** 0034, bounded lists 6: role bindings and outstanding
  certificates.
- **Public packages:** none changed.
- **Migration:** `0018_role_binding_and_certificate_order` adds
  `identity_role_bindings_order_key`, unique on
  `(tenant, role, (subject_id IS NULL), coalesce(subject_id, group_id))`, and
  `ssh_certificates_issued_order_idx` on
  `(tenant, issued_at DESC, serial DESC) WHERE revoked_at IS NULL`. The
  indexes 0007 built stay: the two partial unique keys are what a bind's
  `ON CONFLICT` names, and `ssh_certificates_outstanding_idx` serves a
  compromise rotation's revocation. No row is rewritten.

## v0.20.0

Phase 0033, bounded lists 5: grants and grant requests.
`GET /api/v1/tenants/{tenant}/grants` and
`GET /api/v1/tenants/{tenant}/me/grant-requests` now answer in decision M26's
shape (`docs/PLAN.md` §2), one page at a time, newest first, and a grant's view
says when its sample of decisions was cut.

### Breaking

No signature changes; `make release-check` finds none. Five behaviour
changes:

- **Both lists answer at most 500 items by default, and refuse above
  10 000.** They answered 100 by default and clamped silently at 1 000.
  `?limit=` asks for 1 to 10 000; a page holds exactly that many unless it
  is the last. Above 10 000 is `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`; zero, a
  negative or a non-integer is `{field: limit, problem: invalid}`, as
  before.
- **Both answers gain `truncated` and, when cut, `next`.**
  `grants` is `{"tenant": "...", "grants": [...], "truncated": bool, "next": "<cursor>"}`,
  ordered by creation and then grant id, newest first;
  `me/grant-requests` is `{"tenant": "...", "requests": [...], "truncated": bool, "next": "<cursor>"}`,
  ordered by when each request was raised and then request id, newest
  first. Pass `next` back as `?after=` with the same `subject` and `state`
  to read the following page; a cursor under other filters is
  `400 invalid_request` `{field: after, problem: invalid}`. A client that
  read a whole list from one answer must now follow `next` until
  `truncated` is false. A requester's cursor names neither them nor the
  tenant: whoever presents it reads their own requests from that place.
- **A state filter is judged on every page.** `?state=` on `grants` is
  judged at the server's instant when each page is read, so a grant that
  expires or starts during a walk is listed if it matches when the walk
  reaches it, and not otherwise. It is never listed twice.
- **`GET …/grants/{grant}` gains `decisions_truncated`.** The view still
  samples the newest 50 decisions the grant supplied; the member is `true`
  when the grant supplied more. It takes no `limit` and no `after`: every
  record a grant backed is in the audit query.
- **Migration `0017` locks `grants` and `grant_requests` while it runs.** It
  builds three indexes in one transaction, which blocks writes to both
  tables (creating, revoking and deciding grants and requests) until it
  commits, and drops the three it replaces at the end, which blocks reads,
  the decision path's included, from the drops to the commit. Run
  `hoplock-control migrate` when that pause is acceptable; its length grows
  with the two tables.

- **Phase:** 0033, bounded lists 5: grants and grant requests.
- **Public packages:** none changed.
- **Migration:** `0017_grant_list_order` replaces `grants_by_created_idx`,
  `grants_by_subject_idx` and `grant_requests_by_subject_idx` with
  `grants_created_order_idx`, `grants_subject_order_idx` and
  `grant_requests_subject_order_idx`, each ending in the id, descending with
  the time before it. No row is rewritten.

## v0.19.0

Phase 0032, bounded lists 4: algorithm declarations and target observations.
`GET /api/v1/tenants/{tenant}/fleet/algorithms` now answers in decision M26's
shape (`docs/PLAN.md` §2), one page at a time, and the target observations,
which answered two lists side by side, are now two lists of their own.

### Breaking

No signature changes; `make release-check` finds none. Four behaviour
changes:

- **`GET …/fleet/target-observations` is withdrawn.** It answers
  `404 not_found`. Its two lists are now
  `GET /api/v1/tenants/{tenant}/fleet/target-observations/rungs`
  (`{"rungs": [...], "ttl_seconds": N, "truncated": bool, "next": "<cursor>"}`,
  by hostname, port and platform) and
  `GET /api/v1/tenants/{tenant}/fleet/target-observations/key-exchanges`
  (`{"key_exchanges": [...], "ttl_seconds": N, "floor_levels": [...], "truncated": bool, "next": "<cursor>"}`,
  by hostname and port), each taking `fleet:read` as the old route did. Each
  item is the one the old answer held, with its own `fresh`. `ttl_seconds`
  and `floor_levels` describe the list and are the same on every page.
- **A host route on either new path is refused at start-up**, as on every
  path Control serves: a host that served `GET` or `POST` on
  `fleet/target-observations/rungs` or `…/key-exchanges` no longer starts.
- **The three lists answer at most 500 items by default.** They answered the
  whole tenant. `?limit=` asks for 1 to 10 000; a page holds exactly that
  many unless it is the last. Above 10 000 is `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`; zero, a
  negative or a non-integer is `{field: limit, problem: invalid}`.
- **Each answer gains `truncated` and, when cut, `next`.** `fleet/algorithms`
  is `{"declarations": [...], "truncated": bool, "next": "<cursor>"}`. Pass
  `next` back as `?after=` to read the following page. A client that read a
  whole list from one answer must now follow `next` until `truncated` is
  false. A cursor whose port is outside a 32-bit integer is
  `400 invalid_request` `{field: after, problem: invalid}`.

- **Phase:** 0032, bounded lists 4: algorithm declarations and target
  observations.
- **Public packages:** none changed.
- **Behaviour no API diff shows:** a stored declaration on a page that does
  not decode fails that page with a `500` and its correlation id, as it
  failed the whole list before; a page never skips it.

## v0.18.0

Phase 0031, bounded lists 3: proxies and configuration rollouts.
`GET /api/v1/tenants/{tenant}/proxies` and
`GET /api/v1/tenants/{tenant}/fleet/config/rollouts` now answer in decision
M26's shape (`docs/PLAN.md` §2), one page at a time, as the target and subject
lists have since `v0.16.0`.

### Breaking

No signature changes; `make release-check` finds none. Three behaviour
changes on the two lists:

- **They answer at most 500 items by default.** They answered the whole
  tenant. `?limit=` asks for 1 to 10 000; a page holds exactly that many
  unless it is the last. Above 10 000 is `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`; zero, a
  negative or a non-integer is `{field: limit, problem: invalid}`.
- **Both answers gain `truncated` and, when cut, `next`.** They are
  `{"proxies": [...], "liveness": {...}, "truncated": bool, "next": "<cursor>"}`
  and
  `{"rollouts": [...], "fleet_owned_keys": [...], "truncated": bool, "next": "<cursor>"}`.
  `liveness` and `fleet_owned_keys` describe the list and are the same on
  every page. Pass `next` back as `?after=` to read the following page. A
  client that read the whole fleet from one answer must now follow `next`
  until `truncated` is false.
- **`?drifted=true` is now a cursor filter.** A cursor from the drifted view
  continues only the drifted view, and one from the whole view only the whole
  view; either presented to the other is `400 invalid_request`
  `{field: after, problem: invalid}`. Any other value of `drifted` is the
  whole view, as before.

- **Phase:** 0031, bounded lists 3: proxies and configuration rollouts.
- **Public packages:** none changed.
- **Behaviour no API diff shows:**
  - The drifted view is filtered as it is read, so a page of it holds its
    limit unless it is the last. It holds exactly the proxies with a
    published document (desired version not 0) that have not reported
    `applied` on that document. For every state Control writes, that is the
    set it answered before.
  - A page of the fleet view reads relay registrations and configuration
    states for its own proxies only, and a proxy's event subscription, on
    any node, for each proxy of the page. A failure in any of those reads is
    a `500` with its correlation id, never a shorter page.

## v0.17.0

Phase 0030, bounded lists 2: the shape for a host's lists. A host's list
can now answer in decision M26's shape (`docs/PLAN.md` §2) through the same
code Control's lists use, so one client reads both. Additive only; nothing
Control serves changes.

- **Phase:** 0030, bounded lists 2: the shape for a host's lists.
- **Public packages:** `server` gains, compatibly:
  - `server.DefaultListLimit` (500) and `server.MaxListLimit` (10 000), the
    bounds of every north-bound list;
  - `server.List`, one page a caller asked of a host's list (`Limit`,
    `After`);
  - `server.ReadList(w, r, name, keyLen, filters...)`, which reads `?limit=`
    and `?after=` and answers Control's `400 invalid_request` refusals;
  - `(server.List).Write(w, r, member, items, more, last, extra)`, which
    answers `{<member>: [...], "truncated": bool, "next": "<cursor>"}`.
  - `server.Route`'s doc comment now says a list is expected to answer
    through them. Nothing enforces it: Control cannot see which route is a
    list.
- **Behaviour no API diff shows:**
  - A host list's cursor names `<provider>:<name>`, and binds the filters the
    host passes. A cursor from one of Control's lists, from another of the
    host's, or under other filters is refused on a host list as
    `400 invalid_request` `{field: after, problem: invalid}`, and a host
    list's cursor is refused the same way on Control's lists. It never carries
    the tenant, nor on a cross-tenant read the set.
  - `ReadList` and `List.Write` fail closed as `server.WriteError` does: a
    call outside a host route, an empty list name, an order key of no
    components, a `List` that `ReadList` did not return, a member that is
    empty, `truncated` or `next`, an extra member colliding with any of the
    three, items that are not a JSON array, or `more` with no `last` or a
    `last` of the wrong length answers `500 internal` with its correlation id
    and logs `northbound_host_list_refused` at error, naming the problem.

## v0.16.0

Phase 0029, bounded lists 1: the mechanism, proven on targets and subjects.
`GET /api/v1/tenants/{tenant}/targets` and
`GET /api/v1/tenants/{tenant}/subjects` are the first lists to answer in
decision M26's shape (`docs/PLAN.md` §2), one page at a time, and every later
list takes the same path.

### Breaking

No signature changes; `make release-check` finds none. Three behaviour
changes on the two lists:

- **They answer at most 500 items by default.** They answered the whole
  tenant. `?limit=` asks for 1 to 10 000; a page holds exactly that many
  unless it is the last.
- **Both answers gain `truncated` and, when cut, `next`.** The answer is
  `{"targets": [...], "truncated": bool, "next": "<cursor>"}` (and the same
  with `subjects`). `truncated` is always present and `next` exactly when it
  is true. Pass `next` back as `?after=` to read the following page, under the
  same filters (`?label=` for targets). An empty page is
  `{"targets": [], "truncated": false}`. A client that read the whole list from
  one answer must now follow `next` until `truncated` is false.
- **`?limit=` above 10 000 is refused**, as `400 invalid_request`
  `{field: limit, problem: exceeds_maximum, limit: "10000"}`. Zero, a negative
  or a non-integer is `{field: limit, problem: invalid}`.

- **Phase:** 0029, bounded lists 1: the mechanism, targets and subjects.
- **Public packages:** none changed. `server` offers the shape to a host in
  phase 0030.
- **Behaviour no API diff shows:**
  - A cursor is opaque, never expires, and holds nothing the caller was not
    handed. It continues only the list, filters and order it was minted for;
    any other is `400 invalid_request` `{field: after, problem: invalid}`,
    and the client starts the list again. It never carries the tenant: under
    another tenant's path it continues that tenant's list, which the
    caller's credential must already reach.
  - A page is stable under writes: an item added behind the position is not
    seen, one added ahead of it is, and the item the position names may be
    gone.
  - A failure while reading a page is a `500` with its correlation id, never
    a shorter page.

## v0.15.0

Phase 0028, bounded lists: audit and plan. **This release changes no
behaviour.** No handler, store method, response shape or public identifier
changed, and `make release-check` finds no API difference.

What it plans, for anyone reading Control's north-bound lists or building a
host's:

- **Every north-bound list is to be bounded by its caller and say where it
  stopped** (decision M26 in `docs/PLAN.md` §2).
  - `?limit=` defaults to 500 and is refused above 10 000.
  - `?after=` continues from an opaque cursor.
  - The answer is `{<list>, truncated, next}`.
- Phases 0029–0037 implement it, each in a release of its own. Each lists its
  own `### Breaking`, and between them those breaks will include:
  - lists cut at 500 by default;
  - limits above 10 000 refused where they were clamped;
  - `GET …/fleet/target-observations` split into `…/rungs` and
    `…/key-exchanges`.
- Phase 0030 adds the same shape to `server` for a host's lists.

## v0.14.0

The shared event plane. The event stream (`GET /v1/proxies/{proxy_id}/events`)
is the same whichever node a proxy reaches: a revocation or a `config_changed`
published on one node reaches a proxy whose stream another node holds, a proxy
reconnecting to any node — or after a restart — is replayed what it missed, and
the cache-hint gate sees a stream held on any node. Control on one node takes
the same path, through its single-node coordinator.

### Breaking

No signature changes; `make release-check` finds none. These change what an
implementation of `ext.ClusterCoordinator` is relied on for:

- **`ext.ClusterCoordinator`'s bus is now called.** Every node publishes a
  wake-up on topic `hoplock.events.logged` (kind `logged`, the tenant set, a
  small JSON payload naming the position) each time it logs an event, and
  every node subscribes to that topic for its lifetime. Nothing called
  `Publish` or `Subscribe` before.
- **Its doc comment now states what the bus must deliver**, under "What the
  bus must deliver": at least once to every subscription open when `Publish`
  was called, on every node including the publisher's; in any order, with
  duplicates allowed; a subscription the bus cannot keep up with ended by
  closing its channel after what it already holds, never left open with
  events skipped; closed when its context ends or it is cancelled; payloads
  delivered as published, `Origin` included; `Publish` failing promptly
  rather than hanging. `Node` must return an id unique among live members and
  stable for the process, because Control keys the streams a node holds by
  it. An implementation that silently drops events to a slow subscriber
  delays revocations reaching that node by up to `events.sync_interval`.
- **Control's single-node coordinator closes a subscriber it drops.** It used
  to discard the event and keep the subscriber; a subscriber now sees its
  channel close once it falls 64 events behind.
- **Event ids changed shape**, from `evt-<run>-<sequence>` to
  `evt-<epoch>-<position>` (and `evt-<epoch>-<position>.<line>` on a heartbeat
  and on the `resync` answering a reconnect). Ids are opaque to a proxy, which
  only echoes one back, so nothing breaks; every proxy reconnecting for the
  first time after the upgrade presents an id the new log did not mint and is
  answered `resync` once, as it was after every restart before this release.

- **Phase:** 0027, the shared event plane.
- **Public packages:** `ext.ClusterCoordinator` and `ext.ClusterEvent` doc
  comments, above. `server` is unchanged.
- **Behaviour no API diff shows:**
  - Migration `0016`: `event_log` (every event a proxy may be replayed, with
    its audience), `event_streams` (each tenant's epoch, head and floor; its
    row is the tenant's publish lock), `event_subscriptions` (which proxy
    holds a stream on which node), and the sequence `event_log_seq`. Run
    `hoplock-control migrate` before starting this release.
  - A publication answers once the event is committed to the log. A node that
    dies after answering loses nothing; one whose wake-up is lost delays the
    other nodes by at most `events.sync_interval`.
  - A restart replays rather than resyncs: a proxy reconnecting with an id
    still in the log is replayed what it missed, on any node.
  - `events.replay_buffer` (default 1024) is now per tenant and kept in the
    store rather than one ring per process. A `config_changed` to every proxy
    of a large zone is one event per proxy, so size it to cover that.
  - New configuration: `events.sync_interval` (default 1s, or
    `events.heartbeat_interval` when that is shorter; refused above
    `events.heartbeat_interval`): how often a node reads the log for events no
    wake-up told it of, and writes and reads the deployment's subscriptions.
  - The cache-hint gate and the fleet's liveness read whether a proxy holds a
    stream on any node: this node's own and a copy of every other node's, at
    most two sync intervals late, from memory and never the store. The gate's
    read is now one lookup per hinted answer; it used to copy the tenant's
    whole subscription map per call.
  - `POST …/revocations` answers `delivered` as the subscriptions on the node
    that took the request; proxies whose streams other nodes hold receive the
    event from the log and are not counted.
  - A `config_changed` is settled (`announced_hash`) once the stream logs it,
    wherever the proxy's stream is held, and any node's announcer retries one
    the stream never took. Before, only a delivery to a stream on the
    announcing node settled it.
  - A clean shutdown removes the node's rows from `event_subscriptions`. New
    `WARN` lines: `revoke_wake_failed`, `revoke_wake_dropped`,
    `revoke_wake_subscribe_failed`, `revoke_fanout_deferred`,
    `revoke_catch_up_failed`, `revoke_presence_read_failed`,
    `revoke_presence_write_failed`, `revoke_presence_remove_failed`.

## v0.13.0

Audit sink delivery. With one or more `ext.AuditSink`s registered, Control
hands each of them every record it commits, keeps a durable position per sink,
and offers again what a sink did not confirm. A sink that is down or slow
slows nothing but its own delivery. Retention does not wait on a sink. With no
sink registered, nothing changes. The archive's feed now bounds the calls in
flight to the archive the same way.

- **Phase:** 0026, audit sink delivery.
- **Public packages:** no signature changes. `ext.AuditSink`'s doc comment now
  says what Control does with one, and `ext.AuditRecord.Sequence`'s says when a
  gap is not a drop. An implementation written to the old comment should check
  these:
  - **`Export` is now called, concurrently.** Control may have batches of
    different streams or tenants in flight to one sink at once, up to
    `audit.sinks.max_in_flight` (default 4), so `Export` must be safe for
    concurrent use. It never has two batches of one stream in flight, and
    offers a stream's next batch only after the previous one returned. The
    old comment implied one call at a time.
  - Each batch is one tenant's and one stream's records, in ascending
    `Sequence`, at least once; every tenant's records go to every sink. A nil
    return confirms the batch; an error confirms nothing, and the same
    records are offered again after a backoff. `Export` must be idempotent
    on `RecordID`.
  - The position is Control's, in its store per sink, tenant and stream; a
    sink keeps no cursor. A sink is named by its `Registration.Provider`: one
    that registers again under the same name resumes from its positions, and
    a new name is a new sink, starting each stream at its chain start.
  - A call past its deadline confirms nothing and keeps one of the sink's
    places in flight until it returns, so a sink that ignores its deadline
    stalls only itself.
  - **Retention does not wait on a sink.** When a retention pass removes
    records a sink has not confirmed, the sink resumes after the pass's
    marker. The gap is explained by the pass's `operator.audit.retained`
    record on the control stream, which the sink is handed too.
  - Delivery writes no audit record, and captures' bytes never cross to a
    sink.
  - **`ext.ArchiveStore`'s doc comment now states its concurrency**, which it
    left out: `Archive` and `ArchiveCaptures` may be called concurrently for
    different streams, never two calls for one stream at once, so both must
    be safe for concurrent use. Control has called them from up to four
    workers at once since `v0.12.0`; this says so, and adds no requirement.
- **Behaviour no API diff shows:**
  - Delivery runs off ingest and the decision path, on the archive's feed
    with positions of its own (migration `0015`: `audit_feed_positions` gains
    `skipped_records`, `skipped_behind` and `failing_since`; a sink's rows are
    keyed `sink:<provider>`). Each stream is delivered by one node at a time,
    by the lease 0025 added, so it is correct on several nodes. One sink,
    tenant or stream that fails, hangs or lags slows no other.
  - A sink that starts failing for a stream is logged once at `WARN`
    (`audit_sink_failing`, with `provider`, `tenant`, `stream` and `kind`)
    and once when it recovers (`audit_sink_recovered`). A sink a retention
    pass overtakes is logged once at `WARN`
    (`audit_sink_skipped_by_retention`, with `from_seq`, `through_seq` and
    `records`). The start-up line `audit_sink_delivery_configured` names
    every sink.
  - New configuration, applied to every sink alike, each with a default and
    bounded at start-up: `audit.sinks.batch_records` (500, at most
    `audit.max_batch_records`), `attempt_timeout` (30s), `idle_interval`
    (5s), `backoff_cap` (5m, at least 1s), `max_in_flight` (4, at least 1).
  - `GET …/extensions`: each registration at the audit sink point gains `lag`
    for the caller's tenant — `streams_behind`, `oldest_unconfirmed_at`,
    `failing`, and `skipped_by_retention` (records a pass removed before the
    sink confirmed them, since it last caught up). The archive's `lag` gains
    `failing`. With no sink registered, the sink point's row is unchanged.
  - **The archive's feed is bounded the way a sink's is.** Its four workers
    are now a bound on calls in flight to the archive,
    `audit.archive.max_in_flight` (4, at least 1), and a call past
    `audit.archive.attempt_timeout` keeps its place until it returns. Before,
    an abandoned call freed its worker, so an archive that hung could gather
    one stuck call per stream. Now an archive that hangs on every place
    stalls its own feed, and retention waits on it as it always did.
  - Recording a failure of the archive's feed now also marks its position
    row failing, which is what the archive's `failing` reads.

## v0.12.0

An archive fed before retention deletes. With an `ext.ArchiveStore`
registered, Control hands it every record it commits and every session
capture, keeps how far the archive has confirmed each stream, and never
deletes a record, or empties a capture, that the archive has not confirmed.
With no archive registered, nothing changes.

### Breaking

- **`ext.ArchiveStore` gains `ArchiveCaptures(ctx context.Context, batch
  []ext.Capture) error`.** An implementation must add it, or it does not
  compile against this release. It must store each capture's bytes durably
  before it returns nil, and be idempotent on `Capture.RecordID`: Control
  offers a capture only after `Archive` confirmed its record, offers it again
  after an error, and empties the local bytes only once it has been
  confirmed. Each `Body` is valid only until the call returns.
- **`ext.ArchiveStore` is now called.** A registered archive was handed
  nothing before; now `Archive` receives every committed record, one stream's
  records per batch in ascending `Sequence`, at least once, and a nil return
  confirms the batch. An implementation that answered nil without storing
  lets Control delete what it never kept.
- **With an archive registered, retention waits on it.**
  `POST …/audit/retention` by `through_seq` beyond what the archive holds —
  every record through it, and every capture of those records — is
  `409 retention_not_archived` with `stream` and `archived_through_seq`, and
  deletes, seals and records nothing. By `before`, the pass deletes no
  further than the archive holds. `POST …/audit/captures/retention` runs no
  further than the earliest capture the archive has not confirmed, signs its
  watermark over the instant it ran to, and empties only confirmed captures.

- **Phase:** 0025, an archive fed before retention deletes.
- **Public packages:**
  - `ext.AuditRecord` gains `Stream` (the chain the record belongs to:
    `control`, `proxy:<id>`, `unattributed`), `PrevHash` and `Body` (the
    canonical JSON the chain hashed). `Sequence` is a position in that
    stream, not in the tenant's chain as its doc comment said, and
    `ChainHash` is now verifiable from the record alone.
  - `ext.ChainLink(tenant, stream, sequence, prevHash, body) string` computes
    a record's link exactly as Control's chain does, and is the code Control
    computes it with; `ext.ChainVersion` (`hoplock.chain/1`) is inside every
    link. The encoding is in `ChainLink`'s doc comment, and a later one is a
    new version, never a change to this one.
  - `ext.Capture`: `Tenant`, `RecordID`, `SessionID`, `Digest` (the record's
    `capture_sha256`, `sha256:<hex>`), `Size` (its `capture_bytes`) and
    `Body`.
  - `ext.ArchiveStore`'s doc comment says what Control does with one.
    `Search` is still called nowhere.
- **Behaviour no API diff shows:**
  - Delivery runs off ingest and the decision path. Each stream is fed by
    one node at a time, by a lease in its position row (migration `0014`:
    `audit_feed_positions`), so every node may run the feed, and positions
    and leases are correct on several nodes without the shared event plane.
    A call to the archive that outlives `audit.archive.attempt_timeout` is
    abandoned and retried, a failing stream backs off from a second to
    `audit.archive.backoff_cap` and holds back no other, and a stream that
    starts failing is logged once at `WARN` (`archive_feed_failing`) and once
    when it recovers (`archive_feed_recovered`).
  - A stream retained before an archive was registered is fed from after its
    marker; the `operator.audit.retained` record on the control stream says
    where the gap is.
  - Captures carry a confirmation of their own (`audit_captures.archived_at`).
  - New configuration, each with a default and bounded at start-up:
    `audit.archive.batch_records` (500, at most `audit.max_batch_records`),
    `capture_batch_bytes` (16 MiB, at least `audit.max_capture_bytes`),
    `attempt_timeout` (30s), `idle_interval` (5s), `backoff_cap` (5m, at
    least 1s).
  - Both retention answers gain `held_by_archive` (`false` with no archive
    registered); the prefix pass's gains `archived_through_seq` while one is.
    `operator.audit.retained` carries `operator.held_by_archive` and, for a
    prefix pass, `operator.archived_through_seq`, while one is.
  - `GET …/extensions`: the archive's row gains `lag` for the caller's
    tenant while one is registered — `streams_behind`,
    `oldest_unconfirmed_at` and `captures_waiting`.
  - New code: `retention_not_archived` (409).

## v0.11.0

Signed retention. A retention pass signs what it leaves behind with a
per-tenant key the database does not hold in usable form, and `audit-verify`
refuses a marker, or a removed session capture, whose signature does not
check. A writer of the database alone can no longer delete a stream's oldest
records — or empty a capture — and make it verify as lawful retention.

### Breaking

- **Retention is refused without a key store.** With no
  `credential.key_encryption_key_env` and no registered `ext.KeyStore`,
  `POST …/audit/retention` and `POST …/audit/captures/retention` answer
  `503 retention_key_not_configured` with `config_key:
  credential.key_encryption_key_env`, and delete nothing. Both passes ran
  unsigned before.
- **Retention written by `v0.6.0`–`v0.10.0` no longer verifies, and nothing
  builds on it.** Nothing can tell an unsigned marker from a forged one, so it
  is never silently trusted:
  - `hoplock-control audit-verify` reports a stream whose marker is unsigned
    as `BROKEN`, a `retention-marker` break naming the marker's sequence and
    hash, and exits 1. With `--captures` it does the same for each capture
    emptied by a pass that signed nothing.
  - A retention pass over such a marker, or over such captures, is
    `409 retention_unverified` (`stream`, or `captures: true`) and deletes
    nothing. So is a pass over a stream that no longer starts where its marker
    says.
  - No path signs them after the fact. Nothing is deployed yet, so a
    database carrying such retention is a development or test one, and is
    rebuilt rather than migrated.
- **`audit-verify` and `audit-retention` hand a host's configuration sections
  to `HostConfig` and seal its registry,** as the daemon does, so they check
  and sign with a host's `ext.KeyStore`. Every other subcommand still never
  calls `HostConfig`.

- **Phase:** 0024, signed retention markers.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `server.Main` and `server.Options.HostConfig` name the
  subcommands that call `HostConfig`.
- **Behaviour no API diff shows:**
  - A registered `ext.KeyStore` now holds keys: one Ed25519 key per tenant,
    `audit-retention:<generation>`, generated on the first pass. Without one,
    Control's software custodian holds them, encrypted under the
    key-encryption key. The certificate authority still uses the software
    custodian only.
  - Each marker stores the key that signed it and the signature
    (migration `0013`: `audit_retention.key_name`, `signature`), so a marker
    signed before a rotation still verifies after it. A destroyed key makes
    every marker it signed a `retention-marker` break naming the key.
  - Capture passes keep a signed per-tenant watermark
    (`audit_capture_retention`). A capture counts as retained only when its
    record's own timestamp, read from the hashed body, is before it.
  - A capture pass whose `before` is ahead of the clock runs to now, and its
    answer's `before` says so. Both retention answers gain `key`, the key that
    signed. `operator.audit.retained` carries `operator.key` and
    `operator.signature`, and a capture pass's also `operator.watermark`.
  - `hoplock-control audit-retention key | rotate-key`: show the tenant's
    retention key and its public half, and make a new generation (recorded as
    `operator.audit.retention_key_rotated`).
  - New codes: `retention_key_not_configured` (503) and
    `retention_unverified` (409).

## v0.10.0

The fourth and last audit of `v0.6.0`: the north-bound surface — its routes,
roles, error codes, the record every write leaves, each handler — and what
`v0.6.0` and its pull request said, checked against the code, with every
defect fixed under a test that failed first.

### Breaking

- **A host route on a path Control serves through a wildcard is refused at
  start-up.** `server.Main` and `server.Run` refused a host pattern equal to
  one of Control's, a method beside one, or one the mux found ambiguous, but
  accepted a literal under one of Control's wildcards — `targets/special`
  beside `targets/{target}`, `access-context/bindings/push` beside
  `access-context/{provider}/push` — and the mux then routed that path to the
  host. No identifier changed.
- **`PUT …/subjects/{subject}` refuses a claim the claim mapping would
  refuse** (`400`, `field: claims.<name>`, `problem: reserved` or `invalid`):
  `chain_hop_proxy_id`, any `hoplock.*` name, and a name outside the attribute
  pattern. A stored claim is a policy attribute on every decision for the
  subject, so a reserved name stored here stood beside every login as if a
  proxy's key had authenticated a chain leg.
- **Refusals that changed status or field:**
  - a body past the listener's limit is `413 payload_too_large` with
    `limit`; it was `400 invalid_request`, `field: body`;
  - `POST …/revocations`: a `host_key_withdraw` with a blank `target` or
    `fingerprint` is `400` naming `host_key.target` or
    `host_key.fingerprint`, where it was `409 host_key_unresolved`; a
    selector, reason or audience refusal names `session_ids`, `keys`,
    `reason` or `proxy_ids` rather than `body`;
  - `POST …/targets/relabel`: a change naming no target, or a target a
    second time, is `400` naming `changes[<i>].target`; a target that is not
    there is `404` with `target`;
  - `POST …/audit/retention` with a negative `through_seq` is `400`; it was
    read as absent.
- **The fleet configuration records drop `operator.unannounced`.** The
  record is now written in the publish's transaction, before anything is
  announced; which announcements the stream took is in the answer, as it
  was. The record gains `before`: the version the publish displaced.

- **Phase:** 0023, the audit of 0019's north-bound surface and of the record.
- **Public packages:** no identifier is added, removed or changed. The doc
  comment of `server.Route` names the paths a host may not take.
- **Behaviour no API diff shows:**
  - Three writes now commit with their record: `PUT …/identity/claim-mapping`
    (which also stored and activated the version in two statements),
    `POST …/proxies/enrollments`, and `PUT …/fleet/config/{scope}/{id}` with
    its `rollback`. Each was recorded after it committed, so a failed record
    left a change the chain did not state.
  - A target, subject or claim-mapping write reads what it replaces under the
    tenant's lock for that collection, so concurrent writes record a chain of
    befores. A relabel's write re-reads each target under that lock and
    changes only its labels: a hostname, zone or credential method written
    while it simulated is kept, where it was overwritten with the values the
    preview read.
  - `GET …/audit/negotiated` answers `truncated`, true when its limit cut the
    list short: the runbook's next step ends exactly the sessions listed. An
    `axis` without its `value`, or a `ban_axis` without its `ban_name`, is
    `400` naming the missing one; it was a `500`.
  - No refusal quotes a Go operation, a package's error text, or the
    database's: `this query cannot be answered: store.Audit.RetainPrefix:
    invalid: …` is `this query cannot be answered: the head of stream … is
    never retained`.
- **What `v0.6.0` stated wrongly or left out:**
  - its route list omitted `PUT …/identity/claim-mapping` (`identity:write`);
  - its `### Breaking` did not say that a host route on a path it began
    serving, or a host code equal to one of its eight new codes, is refused at
    start-up — a host binary that started under `v0.5.0` could fail to start;
  - "every operator act is recorded" held, but three of those records were
    written outside the act's transaction, against PLAN §3's rule (above).

## v0.9.0

The third audit of `v0.6.0`: the policy lifecycle — the publish-time report,
upload, activation, simulation, explain and `policyctl` — checked against the
code, with every defect fixed under a test that failed first.

### Breaking

- **A validator finding whose `ext.FindingSeverity` is none of the three is an
  outage.** Validate, upload and activate answer it as a validator error
  does (`5xx`), naming the provider, the code and the value. It was rendered
  `advice` (`FindingSeverity.String`'s default), which gates nothing, so a
  governance rule could pass silently.
- **Two activation-gating warnings arise where they did not.** A cache hint on
  a rule matching `context.source_cidrs` is the warning
  `authoring.cache_hint_spans_sources`, whatever bounds the session: a proxy
  reuses a cached decision by identity, target, port, method and hop trail,
  never by client address. And `algorithm.policy_unmet` now warns every allow
  route reaching a target with a stored `target.algorithm_policy_unmet` under
  its profile, including a route naming no floor and no ban (it warned only
  those that named one). A bundle that activated under `v0.8.0` may now need
  these accepted by id to activate again.
- **`POST …/policy/simulate` refuses a dry-run case that would ask a
  different question** (`400`): a grant with no `expires_at`, which was never
  live, and a grant `origin` or a `subject.auth_method` outside the engine's
  vocabulary, which matched no rule naming one.

- **Phase:** 0022, the audit of 0019's policy lifecycle.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `ext.PolicyBundleRef.Version`, `ext.PolicyDocument` (and its
  `AuthorID`, `ProposedAt`, `ActiveVersion`), `ext.FindingSeverity` and
  `ext.PolicyValidator` now say what Control does: a validator runs only on a
  source that compiles, at validate, upload and activation; `Version` is
  empty until a version is stored; `AuthorID` is the subject asking for that
  judgement, empty for an API token; `ProposedAt` is that call's time;
  `ActiveVersion` is the version serving when asked; Control's own
  publish-time checks run beside the compiler's when no validator is
  registered.
- **Behaviour no API diff shows:**
  - Uploads and activations are serialised per tenant. Identical sources
    uploaded at once are one version, found by digest rather than by reading
    every stored source. Concurrent activations each record the version they
    replaced as `before`, and one that finds its version already active
    records nothing; the loser of a race was a `5xx` from a unique index.
  - A ban is a typo warning (`algorithm.ban_unmatched`) only when at least one
    build has declared what it offers. With none, the ban is advice
    (`algorithm.declaration_unknown`) and the emergency runbook's ban
    publishes without an acceptance.
  - A read of the stored unmet records that stops at its limit (1000, newest
    first) says so: advice, `algorithm.policy_unmet_truncated`.
  - Replay lists a record whose recorded instant is missing, or whose source
    address or grant instant does not parse, as unreplayable with its reason;
    it was replayed with zero values in their place.
  - Explain answers `500 decision_unexplainable` for five more partial
    stories: inputs with no instant, an effect this server never writes, an
    allow the engine did not make, an allow with no snapshot, and an unserved
    record that does not say why.
  - The all-added text diff no longer counts a line after the final line
    break.
  - `policyctl` prints a tab or a line break inside a field as a space, so a
    record is one line. `simulate` with neither `-from` nor `-cases`, or with
    `-to` alone, is a usage error (exit `2`); it exited `1`.

## v0.8.0

The second audit of `v0.6.0`: the audit store, retention and fleet
configuration delivery, checked against the code, with every defect fixed
under a test that failed first.

- **Phase:** 0021, the audit of 0019's audit store, retention, fleet
  configuration, store, configuration and daemon wiring.
- **Public packages:** no identifier is added, removed or changed. `ext` and
  `server` are unchanged.
- **Migration:** none. The audit columns and `audit_retention` are as `v0.6.0`
  left them.
- **Behaviour no API diff shows:**
  - A retention pass (`POST …/audit/retention`, `POST
    …/audit/captures/retention`) and its `operator.audit.retained` record
    commit in one transaction. A pass whose record cannot be written deletes
    nothing and moves no marker. Before, the deletion committed first, so a
    failure between the two left a deletion nothing recorded.
  - `POST …/audit/retention` answers `through_hash` on every answer. A pass
    by `before` that took nothing now carries it too: the marker's hash, or
    empty when the stream was never retained.
  - `GET …/audit/negotiated`'s `limit` counts sessions. It counted records,
    so a session with several matching records could crowd others out of
    the answer the runbook hands to `session_kill`.
  - `GET …/audit/sessions/{session}/certificates` and `…/algorithms` read
    every record of the session that carries a serial or an algorithm. They
    read the session's first 500 records and missed anything after them.
  - The negotiated-value, ban, refused-record-id and certificate-serial
    filters of the audit query use the indexes `v0.6.0` built for them.
    Before, each one read every record in the tenant.
  - A proxy whose zone and own scope have nothing published desires no
    version. Its fetch answers `204`, it is sent no `config_changed`, and the
    rollout view does not show it as drift, even after it reports its
    bootstrap file applied. Its first document is `v1`. `v0.6.0` stored the
    empty composition as `v1`: it announced that version, which the fetch
    then denied, and showed the proxy behind it for good. A proxy enrolled
    under `v0.6.0` or `v0.7.0` with nothing published keeps that `v1` until
    its scope is published. Its fetch now answers `200` with no settings,
    which the contract treats as the bootstrap file.
  - A tenant in which a fleet configuration is published or rolled back is
    one the announcer retries in. A lost announcement there was retried only
    once a proxy of that tenant subscribed on the same node.
  - A `config_changed` counts as announced only when a proxy's live event
    stream on the node took it, or the proxy's report names the document.
    Each node's announcer delivers to the proxies whose streams it holds, so
    a publish on one node reaches a proxy subscribed on another within one
    announce interval (15 s). A publish answers `announced: false` for a proxy
    not subscribed on the publishing node, saying why, where it answered
    `true` because the event was retained. Revocations still reach only the
    publishing node's subscribers, so Control still runs as one node.

## v0.7.0

The first audit of `v0.6.0`: the south-bound surface and the decision path,
checked against the code, with every defect fixed under a test that failed
first.

### Breaking

- **`cmd/pdpconform` expectation files must name `algorithms.bans`:** the ban
  route's bans as authored, one list per axis. A file without it is refused
  at start-up, as any missing key is, because the case that compares the
  answer with it would otherwise grade nothing.
- **`south.max_log_body_bytes` below one storable record is refused at
  start-up:** below `audit.max_capture_bytes` as base64, plus
  `audit.max_record_bytes`, plus 4 KiB (`config.MinLogBodyBytes`). A body over
  the limit is a `400` the proxy cures by halving the batch, which a batch of
  one record cannot be, so such a limit lost that record. The shipped bounds
  need about 1.4 MiB under the 4 MiB default, so a configuration that leaves
  all three keys at their defaults is unchanged.

- **Phase:** 0020, the audit of 0019's south-bound surface and decision path.
- **Public packages:** no identifier is added, removed or changed. `ext` and
  `server` are unchanged.
- **Behaviour no API diff shows:**
  - A policy bundle may ban an algorithm name made of blanks. It was refused
    as `route.algorithm_ban_empty`, which the proxy reserves for the empty
    name; it is now a name no build offers, published with the
    `algorithm.ban_unmatched` warning like any other.
  - `POST /v1/credentials/certificate` mints only on a decision inside the
    window a session may be served under it: its cache hint's TTL, or none,
    plus two minutes (`credential.DecisionSetupAllowance`). An older
    `decision_id` is `401 unknown_decision`. A conforming proxy cites a
    decision only while it may reuse it, so it never sees this.
  - A proxy's algorithm declaration, as `GET …/fleet/algorithms` and the
    publish-time report read it, follows the build the proxy runs within a
    minute on every node. A node used to remember its own last write for as
    long as it ran, so after a rollback the stored copy could keep another
    node's newer build indefinitely.
- **Conformance suite:** the error-discipline group grades the certificate
  endpoint and both fleet configuration routes for a rejected token. New
  cases: a ban answered exactly as authored, and unchanged when the request
  declares `capabilities.algorithm_profiles`; a key-exchange report and a
  session-less priority record answering `accepted: true`; a certificate's
  `valid_before` inside the route's `lifetime_seconds`; and a `logging.gap`
  accepted on the path its severity picks.

## v0.6.0

The north-bound surface is complete: an operator, a pipeline or `policyctl`
authors, simulates, publishes and explains policy, manages the inventory,
queries and retains audit, and acts — and a proxy can be sent brokered
certificates, an algorithm floor and bans, and fleet configuration.

### Breaking

- **Configuration keys removed:** `events.publish_listener`,
  `events.publish_token`, `audit.read_listener` and `audit.read_token`. The
  two debug listeners they bound are deleted, and a configuration naming any
  of the four is refused at start-up as an unknown key. What they served is
  on the north-bound listener under a scoped API token:
  `POST /api/v1/tenants/{tenant}/revocations` (`session:revoke`) and
  `GET /api/v1/tenants/{tenant}/audit/records/{record_id}` (`audit:read`).
- **`ext.FindingWarning` stops activation until it is accepted.** It used to
  block nothing. A warning — from Control's own checks or a registered
  `ext.PolicyValidator` — now holds activation until the author accepts it by
  the finding's id, and the acceptance is on the activation's audit record. A
  validator that meant a finding as information returns `FindingAdvice`. No
  identifier changed, so no API diff shows this.
- **Audit columns renamed:** `audit_records.target_auth_method` and
  `target_auth_rung` are `credential_method` and `credential_rung`, the only
  names the proxy emits (migration `0012`). SQL naming the old columns fails.

- **Phase:** 0019, the north-bound API, inventory and policy lifecycle.
- **Public packages:** no identifier is added, removed or changed. The doc
  comments of `ext.FindingSeverity`, `ext.FindingWarning`,
  `ext.FindingBlocking` and `ext.Finding.Code` say what each severity does at
  activation and that `Code`, with the provider and `Path`, forms the id a
  warning is accepted by.
- **North-bound routes**, each under `/api/v1/tenants/{tenant}/`: the policy
  lifecycle (`policy/validate`, `policy/bundles`, `policy/bundles/{version}`,
  `…/diff`, `…/activate`, `policy/diff`, `policy/active`, `policy/apply`,
  `policy/simulate`); explain (`decisions/{decision}`,
  `sessions/{session}/decisions`); inventory (`targets`, `targets/relabel`,
  `subjects`, `role-bindings`, `proxies`, `proxies/enrollments`,
  `proxies/{proxy}/withdraw`, `fleet/config/{scope}/{id}`, its `rollback`,
  `fleet/config/rollouts`, `fleet/algorithms`, `fleet/target-observations`);
  the audit query (`audit/records`, `audit/blocked-commands`, `audit/drift`,
  `audit/algorithm-policy-unmet`, `audit/negotiated`, `audit/gaps`,
  `audit/sessions/{session}/loss`, `…/algorithms`, `…/certificates`,
  `audit/certificates/{serial}/records`) and retention (`audit/retention`,
  `audit/captures/retention`); `revocations`; `extensions`; and
  `access-context/providers`, `access-context/bindings` and
  `access-context/providers/{provider}/binding`.
- **Permissions:** `target:write`, `audit:retain`, `session:revoke`,
  `access-context:read`, `access-context:write` and `extension:read` are
  added, and a host route may name any of them. Every reading role gains
  `access-context:read` and `extension:read`; `policy-author` and
  `fleet-admin` gain `target:write`; `grant-admin` gains `session:revoke`;
  `admin` holds all. `audit:retain` and `access-context:write` are the
  administrator's alone.
- **Error codes:** `policy_invalid` (`422`), `findings_not_accepted` (`409`),
  `policy_blocked` (`422`), `decision_unexplainable` (`500`),
  `config_key_not_fleet_owned` and `config_value_invalid` (`422`),
  `host_key_unresolved` (`409`) and `unavailable` (`503`).
- **South-bound:** the contract is vendored at `4.7.0` (`hoplock/proxy@976faaa`)
  and this server speaks `policy_version` `6`. `POST /v1/credentials/certificate`
  issues a brokered certificate per session; `GET /v1/proxies/{proxy_id}/config`
  and `POST …/config/report` are served, and a publish announces
  `config_changed`. A route naming `brokered-certificate` is refused to a proxy
  declaring below `5`, and one with a floor or a ban below `6` — a `5xx`
  naming both numbers, never a thinned answer.
- **Binary:** `bin/policyctl` is built beside the server: `validate`, `diff`,
  `apply`, `activate`, `versions`, `simulate`, `explain`, exiting `0`, `1` for
  a refusal and `2` for a usage or transport error.
- **Configuration added:** `policy.interpreters` (the executables a restricted
  allow-list warns on; empty takes the shipped list, and a list replaces it) and
  `south.max_log_body_bytes` (the two log paths' body limit, 4 MiB).
- **Migration:** `0012` adds the algorithm declarations, the key-exchange
  observations, the configuration report and announcement columns, the audit
  record projections, `audit_retention`, and `audit_captures.retained_at`.
- **Behaviour no API diff shows:** `session_id: ""` is accepted on every
  record kind and never looked up as a session. Every operator act is recorded
  as `operator.<noun>.<verb>` on the control stream. A retention pass deletes
  a prefix of a stream and `audit-verify` resumes after it. A proxy's running
  configuration comes only from its config report; the heartbeat no longer
  carries one.

## v0.5.0

A host serves a read that spans tenants, and Control resolves which tenants it
covers.

- **Phase:** 0018, the cross-tenant access class.
- **Public packages:** `server.Access` is added, with `server.AccessTenant`
  (the zero value: what every host route was before) and
  `server.AccessCrossTenant`. `server.Route.Access` is added; a `Route` that
  does not set it is unchanged. `server.Caller.Tenants` is added: the set
  Control resolved on a cross-tenant route, sorted and never empty, and nil on
  a tenant route, where `Caller.Tenant` is the one; `Caller.Tenant` is empty
  on a cross-tenant route. `ext` is unchanged.
- **North-bound routes:** Control adds none of its own. A host's
  `AccessCrossTenant` route is mounted at `/api/v1/cross-tenant/<Pattern>`.
  With no selector it covers every tenant in the caller's scope where the
  caller holds the route's permission, and `403 forbidden` naming the
  permission when there is none. A `tenant` query value or an
  `X-Hoplock-Tenant` header narrows that set, never widens it: a named tenant
  out of scope is `403 tenant_out_of_scope`, and one in scope without the
  permission `403 forbidden` naming both — refused, never dropped. No error
  code is added, and no role gains a permission.
- **Start-up refusals:** a host route whose `Access` is neither class; a
  cross-tenant route whose permission the auditor role does not hold
  (`grant:write`, `report:write`, `access-context:push`, ...); and a
  cross-tenant pattern naming `{tenant}`. Each names the route, before a file
  is read or a port bound.
- **Behaviour no API diff shows:** a session covers the one tenant it signed
  in through, so on a cross-tenant route it covers that tenant; a principal
  spans tenants only as a token minted with `identity token-issue --scope`
  naming several. The north-bound access log line of a cross-tenant request
  carries `tenants`, the sorted set, and no `tenant`.

## v0.4.0

An external system ends a window it opened, and the sessions it backed end
with it.

- **Phase:** 0017, ending an external window.
- **Public packages:** `ext.WindowAssertion.Ended` is added. With it, a push
  ends the window its `ID` opened: Control reads the `ID`, records `Reference`,
  `IssuedAt` and `AdditionalContext` when set, and ignores every other field.
  The zero value is an open, as every push was before. The documentation of
  `WindowAssertion`, its `ID`, `Subject`, `Targets` and `Window`, and
  `AccessContextProvider`'s push direction says what an end does. `server` is
  unchanged.
- **North-bound routes:** none added. The push receiver answers an end `200`
  with `{assertion_id, outcome, grant, state}`, where `outcome` is `revoked`,
  `already_ended` or `recorded`, after the settle pass when it revokes. An end
  whose revocation could not be published is `503
  grant_revocation_undelivered` with `grant_id` and `revoked: true`, as the
  revoke route answers one; pushing the end again re-sends. An open under an
  id its system already ended is `400 window_closed`, `field: id`. No error
  code is added, and no role gains a permission.
- **Behaviour no API diff shows:** an end revokes the grant its window became,
  audited as `grant.revoked` with the integration's credential and
  `grant_external_ended: "true"`, and the holder is shown "The access window
  this session depended on has ended." — never the vendor's text. An end is
  never refused for its age, scope or window. An end of a window already
  revoked, ended or expired changes nothing; an expired one is not rewritten
  as revoked. An end for an id with no window is kept and recorded as
  `access_context.window_ended`, and a later open under that id is refused. A
  probe that stops confirming a window still revokes nothing.
- **Configuration:** a declarative provider's `push.ended` — assertions over
  the push body in `probe.confirm`'s vocabulary, all of which must hold, with
  no placeholders — makes a push an end. Absent, every push is an open.
- **Migration:** `0011_external_window_ends.sql` adds one table,
  `access_context_window_ends`. Run `hoplock-control migrate` before starting
  this release.

## v0.3.0

A person asks for access for themselves, through a registered approval
workflow.

- **Phase:** 0016, self-service grant requests.
- **Public packages:** `ext.GrantRequest.BreakGlass` is added: true when the
  credential that raised the request was minted on the break-glass path,
  copied from the stored request, so a resubmission after an outage keeps it.
  `ext.GrantWorkflow`'s documentation names the two doors a request reaches
  `Submit` through: an administrator's create, and a requester's own, where
  `Subject` and `RequestedBy` are one person. In `ext.Points()`, the
  `GrantWorkflow` point's `Absent` text adds that a requester's own request is
  refused while nothing is registered. `server` is unchanged.
- **Permissions and roles:** `grant:request` is added to the closed set, held
  by a new role, `requester`, and by `admin`. `requester` holds nothing else:
  no read set, and so not `license:read`, which is now held by every role
  except `integration` and `requester`.
- **North-bound routes:** four, in a new self-scoped class whose subject is
  the caller's and never a field the caller sends, all under `grant:request`:
  `POST` and `GET /api/v1/tenants/{tenant}/me/grant-requests`, and `GET …/{request}`
  and `POST …/{request}/cancel` beneath it. The create takes the
  administrator's body without `subject` and answers as the administrator's
  create does with a workflow registered (`202`, `201`, `403
  grant_request_denied`, `502 grant_workflow_failed`). With no workflow
  registered it answers `403 grant_workflow_not_registered` and stores
  nothing. Somebody else's request is `404`. A credential with no person
  behind it is refused on every one of them, `403 subject_required`. Two error
  codes are added: `subject_required` and `grant_workflow_not_registered`. A
  host route pattern beginning `me/grant-requests` now collides with
  Control's and is refused at start-up.
- **Behaviour no API diff shows:** the approval workflow's poller now learns a
  tenant when a proxy of that tenant opens its event subscription, so after a
  restart a decision is applied in every tenant with a live proxy within one
  `grants.workflow_poll_interval`, with nobody reading the request.
- **Migration:** `0010_grant_requests_by_subject.sql` adds one index. Run
  `hoplock-control migrate` before starting this release.
- Two texts name Control's phases by their new numbers, after the queue was
  renumbered to fit phase 0018: `ReportProvider`'s `ControlShips` in
  `ext.Points()` now reads "(phases 0010, 0019)", and the start-up warning on
  the two temporary operator listeners now calls each "a pre-0019 operator
  path". Nothing else changes.

## v0.2.0

A host binary can start Control.

- **Phase:** 0015, the public server package and host routes.
- **Public packages:** `server` is added, beside `ext`, and is the same
  compatibility promise. `server.Main` is Control's whole command line and
  `server.Run` the daemon alone; `server.Options` carries what a host adds:
  `Provider`, `Registry`, `HostSections`, `HostConfig`, `Routes` (`server.Route`)
  and `ErrorCodes`. A host route reads its caller with `server.CallerFrom`
  (`server.Caller`) and answers a failure with `server.WriteError`. `ext` is
  unchanged.
- **Permissions:** two codes are added to the closed set. `report:write` is
  held by `admin` alone; `license:read` by every role except `integration`.
  Control serves no route that needs either: a host's routes name them.
- **`hoplock-control`** is one call to `server.Main` and otherwise unchanged:
  the same flags, subcommands, output, configuration and routes. Two things a
  build or an operator can see:
  - an interrupt or `SIGTERM` now cancels a running subcommand, which stops
    with an error, where it used to end the process outright;
  - the `VERSION=` override lands in `internal/daemon`, so an `-X
    main.version=…` passed by hand no longer sets anything. Use
    `make build VERSION=…`.

## v0.1.0

The first release.

- **Phases:** 0001–0013, and 0014, which cut this release. `docs/PLAN.md` §10
  says what each one delivered.
- **Public packages:** `ext`, as `ext/README.md` describes it. `ext.Points()` is
  the authoritative list of its extension points. It is the module's only
  public package.
- **`hoplock-control --version`** reports Control's module version from the
  build's own information: the tag at a release, a pseudo-version after one, a
  `+dirty` suffix for a modified tree, and `dev` when the build has no version.
  Built into another program, it reports the version of Control that program
  depends on, never the program's own. A Control swapped in by a `replace`
  directive reports `dev` and names its replacement.
