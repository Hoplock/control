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
