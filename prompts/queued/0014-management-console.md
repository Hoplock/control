# 0014 — Management console (web UI)

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially §2 (M2 — the console is a north-bound client and
  authenticates like one), §3 (`ui/`).
- `docs/learnings/` — read summaries; open `0013` (**the API this consumes** —
  every route and its required role), `0011` (RBAC), `0010` (audit query),
  `0006` (proxy health).

## Objective
Give Hoplock Control an operator console. A self-hosted deployment should be
usable without `curl`: see the proxy fleet's health, manage targets and
identities, author and simulate policy, read the audit trail, and — the one
screen that sells the product — **paste a session id and get the full
explanation of why access was allowed or denied**.

## In scope
- `ui/`: a single-page app served by `cmd/hoplock-control` from embedded assets
  (`embed.FS`), so a deployment is still one binary. No separate web server, no
  Node runtime in production.
- **It is an API client and nothing more.** The console calls the north-bound
  API (0013) with the operator's own session; it never reaches into the database
  and never has a privileged path of its own. Any capability the console has,
  the API has, and RBAC (0011) applies identically — a console that can do what
  the API forbids is a second, unaudited authorisation system.
- Screens, in priority order:
  1. **Fleet** — proxies, zones, health, last heartbeat, contract version, which
     have live relay registrations (0006).
  2. **Explain** — a session or decision id in, the whole story out: identity,
     matched rule, mapping version, grant, obligations, route and hops. Deep
     links from the audit view.
  3. **Audit** — query by subject, target, time, kind, severity; the blocked-
     command view; session replay for recorded sessions.
  4. **Policy** — bundle versions, diff, validation errors inline against the
     source, **simulate** a candidate against recorded traffic, activate,
     roll back.
  5. **Inventory** — targets and labels, identities, groups, roles, grants.
  6. **Extensions** — which `ext` points have an implementation registered
     (0004). An operator debugging behaviour must be able to see that Enterprise
     is in play.
- **Auth**: OIDC for humans, the same session the API issues. No separate login.
- Build: assets built and checked in, or built in CI with a reproducible step;
  `make build` must work on a machine with no Node installed. Say which you
  chose and why in learnings.
- Accessibility and no-colour-only signalling for status; keyboard navigation on
  the audit and explain views, which are the ones people live in.

## Out of scope
- Enterprise screens (approval inboxes, compliance reports). Enterprise adds its
  own, served through the same shell — define how in learnings so it can.
- Dashboards and charts beyond fleet health and simple audit counts.

## Acceptance criteria
- `make build` produces one binary serving the console with no Node present.
- Every screen works against a seeded database; end-to-end tests drive the
  critical paths (explain, policy activate, grant create) headlessly.
- An RBAC test: an auditor session sees read-only views and the API refuses the
  mutating calls the console hides — assert the API refusal, not just the hidden
  button.
- No API route is reachable from the console that RBAC would deny.
- The console degrades honestly when an extension point is absent: an Enterprise
  feature that is not installed is not shown as broken.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0014-management-console-learnings.md`. Summary block MUST give
the asset build/embed mechanism, the routes each screen consumes, the auth flow,
and **how Hoplock Enterprise adds screens to this shell** without forking it.
