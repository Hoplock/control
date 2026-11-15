# 0058 — Management console 10: algorithm posture and the emergency runbook

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M17** (the fleet graph carries capabilities; stale,
  undated and absent are one case, `Not reported`), **M19** (a rolling upgrade
  is in progress, never a fault), **M26** (`GET …/audit/negotiated` is paged),
  **M11**, **M20**, **M21** and **M23**; §4 at "One endpoint carries two
  observations"; §5.2's algorithm bullet (the floor and the bans), including
  from "Refusing exactly what the proxy refuses", for what `Hoplock/proxy#69`
  added and 0019 serves — the impact preview, the fleet coverage view and the
  emergency runbook — and what `Hoplock/proxy#72` added: what each build's
  profiles offer, and a ban's per-build finding.
- In the **Hoplock Proxy repository**, `api/README.md` "Banned algorithms"
  states the runbook. It is cited here and never restated.
- **`ui/DESIGN.md` — in full.** §6's in-progress and `Not reported` states
  carry most of what this phase shows.
- `docs/learnings/` — read summaries; open `0049-console-shell`,
  `0050-console-components-and-fleet` and `0053-console-policy` **in full**:
  the shell, the component set, how a screen is registered with every
  harness, Fleet and the publish-time report this phase adds to. Open `0019`
  (the satisfiability report, the impact preview, the coverage served per
  build, and every step of the runbook callable), `0032`
  (`GET …/fleet/algorithms` paged; the target observations split into
  `…/rungs` and `…/key-exchanges`), `0035` (a build's shortfall is one finding
  per rule naming each build) and `0037` (`GET …/audit/negotiated` gains
  `next`).

## The series
The console is twelve phases (PLAN §10, the console-split, console-look and
fleet-bar notes), each a release
and a PR of its own:

| Phase | Builds |
| --- | --- |
| 0049 | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks |
| 0050 | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet |
| 0051 | Explain and the audit view |
| 0052 | the console's look: three directions in screenshots, then an interview with the operator, round by round, until one is chosen and applied through the tokens |
| 0053 | Policy |
| 0054 | Inventory |
| 0055 | locks, quarantines and the directory's subjects |
| 0056 | Extensions and Reports |
| 0057 | stream gaps, the archive's lag and retention, in the audit view |
| **0058** | algorithm posture and the emergency runbook (this phase) |
| 0059 | Fleet's health bar: solid segments, each labelled with its status glyph and count, in place of patterns |
| 0060 | the consistency audit of every screen |

Build only this phase's row. It adds to Fleet (0050) and to the publish-time
report on Policy (0053), and builds the runbook as a screen of its own, all
through 0050's component set and harnesses.

## Objective
Show an operator what the fleet can enforce before they ask it to, warn —
never block — when a floor or a ban would break something, and walk them
through the day an advisory lands without letting them skip the step that
makes a ban reach running sessions.

## In scope
- **Algorithm-floor coverage on Fleet** (`Hoplock/proxy#69`): which
  `algorithm_floor` levels each proxy's build declares, with the key exchanges
  each level accepts **in that build**, as 0019 serves them. During a rolling
  upgrade two builds may accept different exchanges for one level. Show that
  as *in progress* (M19), never as a fault, and name the proxies that cannot
  enforce a level yet. A proxy that has declared nothing is `Not reported`,
  never "no floor". Beside the levels, show what each profile the build
  accepts offers per axis, before any floor or ban (`Hoplock/proxy#72`, as
  0019 serves it), so an operator reading a ban's per-build finding can see
  what that build offers. A proxy whose build declared no profiles is `Not
  reported`, never "offers nothing".
- **Before a route's `algorithm_floor` is raised, preview the impact**
  (`Hoplock/proxy#69`, 0019's satisfiability report), in the publish-time
  report 0053 renders. Show the targets the raise would break, with the level
  each was seen to meet, what it negotiated or offered, and when. Show targets
  with no observation, or a stale one, as `Not reported`, never as meeting the
  floor. Show the proxies that cannot enforce the level yet. It warns and
  never blocks, because the observation is a past fact about a target that may
  have been upgraded since. The same view explains why `pq-hybrid-kex` needs
  OpenSSH 9.9 or later on the target, and it never promises sntrup761.
- **A ban's warnings.** A ban that names something no proxy declared is a
  warning the author reads and may override (0019). So is a ban that leaves an
  axis empty on some proxies' builds (`Hoplock/proxy#72`). Show it per build:
  the proxies that will refuse the route, the axis, and what each of those
  builds offers there. Show a rolling upgrade's disagreement as *in progress*
  (M19), never as a fault. It warns and never blocks, even when every proxy
  would refuse the route. A proxy whose build declared no offer for the
  route's profile is shown as not judged, never as fine.
- **Emergency runbook** — the workflow for the day an advisory lands
  (`Hoplock/proxy#69`; upstream's `api/README.md` states the steps). It walks
  an operator through three steps, each one a call 0019 serves and audits: add
  the ban to the affected routes and activate it; send `cache_invalidate` with
  `all`, because a cached decision keeps the old policy until its hint runs
  out; then find the running sessions by what they negotiated (for example
  every open session whose `target_cipher_out` is the banned cipher) and end
  them with `session_kill`. `GET …/audit/negotiated` is paged (M26, 0037): the
  workflow reads it to its last page, following `next` until `truncated` is
  false, and ends every `session_id` listed, never only the first page's. The
  `reason` is shown to the user, so the workflow asks for it and shows it
  before sending. Each step is a destructive action and confirms as one. Each
  shows what the call reported covering, and the workflow never skips the
  second step, because a ban that stops at activation reaches only new
  decisions.

## Out of scope
- Every other screen: the series table's.
- Re-opening the design, and any locale other than English.

## Acceptance criteria

**Function**
- The floor's impact preview names a target whose observed level is below a
  candidate floor, shows a target with no observation or a stale one as `Not
  reported`, and names a proxy that cannot enforce the level. The fleet view
  shows each proxy's declared levels and profile offers per build. A ban that
  empties an axis on one of two seeded builds is shown as a per-build warning
  naming that proxy, and it does not block the publish. Assert each against a
  seeded fixture.
- A rolling upgrade's disagreement between two builds renders as in progress,
  never as a fault; a proxy that declared nothing renders as `Not reported`.
- The emergency runbook drives 0019's three calls in order, shows the kill
  `reason` before sending it, and cannot finish without the `cache_invalidate`
  step. An end-to-end test walks it, against a negotiated list longer than one
  page, and asserts every listed session was ended.
- The RBAC harness covers every runbook call: a role without each call's
  permission is offered no control for it, and the API refuses the call —
  assert the API refusal, not just the hidden button. The route test stays
  green.

**Design and localisation**
- The runbook is registered with every browser check, and every screen this
  phase changed passes every check again, with its baseline updated
  deliberately and each changed screen named in the PR.
- The runbook has all four states implemented and captured.
- 0049's static checks stay green, the catalogue key test among them.

## Definition of Done & hand-off
**Screenshots in the PR** (`docs/PROTOCOL.md` §8, "A console phase shows what
it built"): the description shows every screen and every state this phase
builds or changes, light and dark side by side.

Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0058-console-algorithm-posture-and-runbook-learnings.md`. Its
summary block MUST give the routes each view consumes and the runbook's steps
as built. Details MUST record **any change made to `ui/DESIGN.md` in this PR**,
with the reason.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
