# 0016 — Management console (web UI)

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially §2 (M2 — the console is a north-bound client and
  authenticates like one; **M20** — the design is specified, not improvised),
  §3 (`ui/`).
- **`ui/DESIGN.md` — in full, before writing any markup.** It is the design
  system this phase is built to: tokens, type scale, layout, status vocabulary,
  component inventory, state matrix, and the checks that enforce all of it. It
  is binding under M20, and it is not a style suggestion you may improve on
  mid-implementation — if it is wrong, change it in this PR and say so.
- `docs/learnings/` — read summaries; open `0014` (**the API this consumes** —
  every route and its required role), `0011` (RBAC), `0010` (audit query),
  `0006` (proxy health).

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
- **It is an API client and nothing more.** The console calls the north-bound
  API (0014) with the operator's own session; it never reaches into the database
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
  chose and why in learnings. `ui/DESIGN.md` §10 constrains the toolchain
  (TypeScript + Vite, headless primitives, no visual kit, no CSS-in-JS runtime,
  a bundle budget) — the framework choice inside those constraints is yours.
- **Design and accessibility per `ui/DESIGN.md`** — the section below says what
  that obliges and how it is checked.

### Deployment identity and tenant context (M18, M19)
- The console always says **which deployment** this is: name, instance id,
  version set, and health (0015). An operator with a staging and a production
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

**These are enforced, not reviewed by eye** (DESIGN.md §9), and each is a CI job
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

## Out of scope
- Enterprise screens (approval inboxes, compliance reports). Enterprise adds its
  own, served through the same shell — define how in learnings so it can. They
  inherit these tokens and this component set; a second visual identity inside
  one console is the thing M20 exists to prevent.
- Dashboards and charts beyond fleet health and simple audit counts. The two
  that exist are hand-written SVG; no chart library until there is a third.
- Re-opening the design. DESIGN.md's palette, scale and constraints are settled
  input to this phase. Correct it where it is wrong — in this PR, with the
  reason — but do not spend the session redesigning it.

## Acceptance criteria

**Function**
- `make build` produces one binary serving the console with no Node present.
- Every screen works against a seeded database; end-to-end tests drive the
  critical paths (explain, policy activate, grant create) headlessly.
- An RBAC test: an auditor session sees read-only views and the API refuses the
  mutating calls the console hides — assert the API refusal, not just the hidden
  button.
- No API route is reachable from the console that RBAC would deny.
- The console degrades honestly when an extension point is absent: an Enterprise
  feature that is not installed is not shown as broken.

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

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0016-management-console-learnings.md`. Summary block MUST give
the asset build/embed mechanism, the routes each screen consumes, the auth flow,
and **how Hoplock Enterprise adds screens to this shell** without forking it —
which, per M20, includes how it inherits the tokens and the component set rather
than bringing its own look.

Details MUST also record: the framework chosen and why, within DESIGN.md §10's
constraints; where the token file lives and how the six checks are wired into
CI; and **any change made to `ui/DESIGN.md` in this PR**, with the reason — a
design document that drifts silently from the console is worse than none, and
the next session reads it expecting it to be true.
