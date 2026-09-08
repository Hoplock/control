# 0004 — Extension points: the seam Hoplock Enterprise implements

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M15, the open-source/Enterprise boundary)**
  and §3 (`ext/` is a **public** package; everything else is `internal/`).
- `docs/learnings/` — read summaries; open `0001` (module layout) and `0003`
  (storage interfaces, which several extension points hand back).

## Objective
Define the public seam that Hoplock Enterprise plugs into, so Enterprise can
**extend** Control rather than fork it. This is the phase that decides whether
the two repositories stay one product or drift into two.

Two rules bound everything here:

1. **Control never imports Enterprise.** The dependency runs one way. A build
   of Control with no Enterprise present must be a complete, useful product.
2. **An extension point is not a hole where core functionality used to be.**
   Every seam ships a real default implementation in Control that a self-hosted
   user is happy with. Enterprise replaces defaults with *more* — never with
   the only working option.

## In scope

### The package (`ext/`)
`ext` is the only non-`internal` package in this module: Enterprise imports it
by path, so its API is a compatibility promise. Keep it small, interface-only,
and free of Control's implementation types beyond the domain models it must
name.

Define, at minimum:

| Interface | Control's default | What Enterprise adds |
| --- | --- | --- |
| `AuditSink` | write to the local audit store | Splunk / Sentinel / Elastic export |
| `ArchiveStore` | none (retention deletes) | long-term archive, tiering, session search |
| `GrantWorkflow` | manual grants (0012): an admin creates a time-boxed grant | request → notify → N-of-M approval → auto-expiry |
| `IdentitySync` | none (identities arrive at login) | SCIM provisioning and de-provisioning |
| `KeyStore` | software keys on disk | HSM / KMS |
| `Notifier` | webhook | Slack, Teams, email, on-call routing |
| `ClusterCoordinator` | single-node, in-process | leader election, shared event bus, HA |
| `ActionHandler` | none | SOAR inbound: kill session, lock out user |
| `ReportProvider` | basic audit queries | compliance reporting packs |
| `PolicyValidator` (chain) | the compiler's own checks | governance rules, approval-before-activation |
| `AccessContextProvider` | a **declarative HTTP provider** — probe URL, auth, request template, response assertions, TTL, plus a webhook mapping (M16) | packaged Qualys and BMC Helix integrations |

`AccessContextProvider`'s default deserves a note, because it is the clearest
case of M15's second invariant: the declarative provider is not a placeholder
for the real thing, it **is** how a self-hosting customer integrates a scanner
or an ITSM system nobody has heard of. Enterprise's version adds vendor
packaging and support, not the capability. Phase 0013 builds both the interface
and that default; this phase only has to define the point so 0013 has somewhere
to put it. Note also that this provider returns **evidence, not a verdict** — say
so in its doc comment, because the first instinct of anyone implementing it will
be to return a boolean, and that quietly relocates the policy engine into a
vendor integration.

Each interface gets: a doc comment saying what varies and why, a context on
every method, typed errors, and an explicit statement of what Control does when
no implementation is registered.

### Registration
A registry that a host binary populates at start-up — Enterprise's `main`
imports Control's server package, registers its implementations, and starts it.
No plugin loading, no reflection, no build tags: a Go program wiring
interfaces, which is the idiom this codebase should use.

- Registration happens **before** start and is immutable afterwards, so a
  request can never see a half-registered extension.
- Registering twice for the same point is an error naming both registrants —
  silent last-wins is how two Enterprise modules fight invisibly.
- Every registration is **logged and visible in the north-bound API** (0014):
  an operator debugging behaviour must be able to see that an extension is in
  play. An invisible extension is indistinguishable from a bug in Control.

### Guardrails
- A test asserting **no package in this module imports `hoplock/enterprise`**
  (walk the import graph; fail loudly). This is the architectural invariant of
  the whole product and it must be enforced by CI, not by good intentions.
- A test asserting every interface in `ext` has a working default registered by
  Control's own wiring, or is explicitly documented as "absent means disabled".
- `ext` carries its own short `README.md`: how to implement a point, how to
  register it, and the compatibility promise (this package is versioned with
  the module; breaking it breaks Enterprise builds).

## Out of scope
- Implementing anything behind these interfaces beyond the defaults named above
  — later phases fill them in, and Enterprise fills in the rest.
- A plugin/IPC mechanism. Enterprise is a Go program that imports Control.

## Acceptance criteria
- `ext` compiles standalone and has no dependency on any `internal/` package
  that would drag the world in.
- The import-graph test fails if a package imports Enterprise (prove it by
  temporarily adding one).
- Duplicate registration is rejected with an error naming both sides.
- Every extension point has either a default implementation or a documented
  "disabled when absent" behaviour, asserted by a test.
- A worked example in `ext/README.md` compiles (an example test), showing an
  out-of-tree implementation registering itself.

## Cross-repo impact

`ext/` is a shared surface (`docs/CROSS-REPO-PROTOCOL.md` §1) and this phase
creates it, so `hoplock/enterprise` is a consumer from the moment this merges.
State the impact per repository under a `## Cross-repo impact` heading in your
PR, spelled exactly that way (§4) — and because Enterprise has obligations,
**end its part of that section with a ready-to-run sync kickoff, already filled
in**: the "Downstream sync" block in `docs/KICKOFF.md`, verbatim except for its
blanks (this PR's URL and the obligations just stated). There is no branch blank
to fill — the sync session uses whatever branch it was given (§5), so do not
invent a suffix for it. Repeat that kickoff in your reply to the user, saying
it needs a **fresh session with `hoplock/enterprise` checked out** (§4, "Hand
over a runnable sync kickoff"). You do not do that sync here: this PR merges
first and the sync runs afterwards, in its own session, downstream (§2). Every
later phase that adds or changes an `ext` interface owes the same.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0004-extension-points-learnings.md`. Summary block MUST list
every interface with its exact signature, its default, and its
absent-when-unregistered behaviour. **Hoplock Enterprise's phases are written
against this list**, so it is the single most cross-referenced summary in
either repository — if a signature changes later, say so loudly.
