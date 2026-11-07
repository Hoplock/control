# 0053 — Management console 5: Inventory

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M2** (the console is an API client and RBAC applies
  identically), **M7** (identity is federated and short-lived), **M10** (JIT
  grants are policy inputs), **M11**, **M20**, **M21**, **M26** and **M23**;
  §6 (identity, groups, the fixed role set and its one enforcement point).
- **`ui/DESIGN.md` — in full.** §2 and §7 on destructive actions: revoking a
  grant confirms by typing the object's name.
- `docs/learnings/` — read summaries; open `0049-console-shell` and
  `0050-console-components-and-fleet` **in full**: the shell, the component
  set, and how a screen is registered with every harness. Open `0011`
  (identity, groups, roles and RBAC), `0012` (grants: create, list, inspect,
  revoke, and the reason shown when revocation ends a session), `0019`
  (targets and identities CRUD, and each route's required role), `0029`
  (`GET …/targets` and `GET …/subjects` paged), `0033` (`GET …/grants` paged
  newest first; a grant's view says `decisions_truncated`) and `0034`
  (`GET …/role-bindings` paged).

## The series
The console is ten phases (PLAN §10, the console-split note), each a release
and a PR of its own:

| Phase | Builds |
| --- | --- |
| 0049 | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks |
| 0050 | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet |
| 0051 | Explain and the audit view |
| 0052 | Policy |
| **0053** | Inventory (this phase) |
| 0054 | locks, quarantines and the directory's subjects |
| 0055 | Extensions and Reports |
| 0056 | stream gaps, the archive's lag and retention, in the audit view |
| 0057 | algorithm posture and the emergency runbook |
| 0058 | the consistency audit of every screen |

Build only this phase's row. Build every screen from 0050's component set and
register it with 0050's harnesses; a component this phase needs that the set
lacks is added to the set, never built inside the screen.

## Objective
Manage what the policy is about from the console: the targets and their
labels, the identities, their groups and roles, and the grants that widen
access for a while.

## In scope
- **Inventory** — targets and labels, identities, groups, roles, grants: a list
  and a detail for each, through the bounded-list shape (M26), each list's
  filters serialised into the URL under `/console/`.
- The writes the API offers for each, to the roles that hold their permission
  and to nobody else (M2): RBAC (0011) applies identically, and a console that
  can do what the API forbids is a second, unaudited authorisation system.
- **Grants**: create, inspect and revoke. Revoking a grant is a destructive
  action and confirms by typing the object's name (0050's confirm). A failed
  mutation reports inline, never by a toast alone.
- Deep links between them, and to the audit view filtered by the subject or
  the target (0051), all under `/console/`.
- Labels, hostnames and rule names are identifiers and operator-authored data
  (DESIGN.md §9): LTR, mono where §3.5 says so, never translated.

## Out of scope
- A subject's lock, a target's quarantine, a directory-managed subject, a
  deactivation, the claim mapping and a connector's directory fields (0054,
  which adds them to these screens).
- Every other screen: the series table's.
- Re-opening the design, and any locale other than English.

## Acceptance criteria

**Function**
- Every inventory screen works against the seeded database, extended with what
  it shows, through the paged reads.
- End-to-end, headless: a grant is created, shown in the list and the detail,
  and revoked through the typed confirmation.
- An RBAC test: an auditor session sees read-only views and the API refuses the
  mutating calls the console hides — assert the API refusal, not just the
  hidden button. Every inventory write is in the RBAC harness.
- The route test stays green: no API route is reachable from the console that
  RBAC would deny.

**Design and localisation**
- Every inventory screen is registered with every browser check, and each
  passes: axe, the screenshot baseline (both themes, both densities, loading,
  empty and error), offline, reduced motion, and both pseudolocale runs.
- Each screen has all four states implemented and captured.
- 0049's static checks stay green, the catalogue key test among them.

## Definition of Done & hand-off
**Screenshots in the PR** (`docs/PROTOCOL.md` §8, "A console phase shows what
it built"): the description shows every screen and every state this phase
builds or changes, light and dark side by side.

Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0053-console-inventory-learnings.md`. Its summary block MUST
give the routes each screen consumes, its deep links, and where 0054 adds the
lock, the quarantine and the directory's facts (the list and the detail of a
subject and a target). Details MUST record **any change made to
`ui/DESIGN.md` in this PR**, with the reason.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
