# 0054 — Management console 6: locks, quarantines and the directory's subjects

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — §2: **M28** (a lock, a quarantine and the directory's
  deactivation are standing denials, including its paragraph on the
  directory's deactivation), **M11** (a lock is a deny made on purpose, never
  an outage), **M7**, **M2**, **M20**, **M21** and **M23**; §6 at "The
  directory door".
- **`ui/DESIGN.md` — in full.** §6 is the status vocabulary every state below
  is rendered through.
- `docs/learnings/` — read summaries; open `0049-console-shell`,
  `0050-console-components-and-fleet` and `0053-console-inventory` **in
  full**: the shell, the component set, how a screen is registered with every
  harness, and the inventory screens this phase adds to. Read the summary of
  `0051-console-explain-and-audit` for where Explain shows the mapping
  version.
- **`docs/learnings/0039-action-executor-lock-and-quarantine-learnings.md`
  (open it in full), for the subject lock and the target quarantine, which
  this console shows and lets an operator place and lift.** The routes are
  `POST …/subjects/{subject}/lock` and `…/unlock` and
  `POST …/targets/{target}/quarantine` and `…/release`, all under
  `session:revoke`. `GET …/subjects` and `…/targets` carry `locked`, `lock`,
  `quarantined` and `quarantine`. The error codes are `target_quarantined` and
  `standing_denial_undelivered`. Enterprise's inbound action channel relies on
  an operator being able to release a lockout here.
- **`docs/learnings/0040-identity-sync-and-deprovisioning-learnings.md` and
  `docs/learnings/0042-directory-source-on-the-sync-sink-learnings.md` (their
  summaries), for a subject a registered identity sync provisions, which this
  console shows and does not let an operator edit.** `GET …/subjects` and
  `GET …/subjects/{subject}` carry `directory_managed`, `directory`
  (`provider`, `source`, `external_id`), `deactivated` and `deactivation`
  (`deactivated_at`, `reason_code`); `PUT` and `DELETE` of such a subject are
  `409 subject_directory_managed`. `directory.source` is the directory the
  person comes from, and is not the subject's top-level `source`.
  `GET /api/v1/session/methods` carries a tied connector's `directory_claim`
  and `directory_source`.
- **`docs/learnings/0043-directory-scoped-group-entries-learnings.md` (its
  summary), for the claim mapping's directory-scoped group entries.**
  `GET …/identity/claim-mapping` carries `directories`, `require_directories`
  and `stale_directory_subjects`; a mapping write is refused with
  `mapping.directory_name`, `mapping.directories_empty` and
  `mapping.directory_duplicate` beside the older codes; and a decision
  record's subject, and explain's `mapping`, carry `directory_source`.

## The series
The console is ten phases (PLAN §10, the console-split note), each a release
and a PR of its own:

| Phase | Builds |
| --- | --- |
| 0049 | the shell: build and embed, the mount under `/console/`, sign-in, tokens and themes, the catalogue machinery, the deployment and tenant chrome, and the static checks |
| 0050 | the component set, the browser checks, the seeded fixture and the RBAC harness, and Fleet |
| 0051 | Explain and the audit view |
| 0052 | Policy |
| 0053 | Inventory |
| **0054** | locks, quarantines and the directory's subjects (this phase) |
| 0055 | Extensions and Reports |
| 0056 | stream gaps, the archive's lag and retention, in the audit view |
| 0057 | algorithm posture and the emergency runbook |
| 0058 | the consistency audit of every screen |

Build only this phase's row. It adds to screens earlier phases built — the
inventory's subjects and targets, Explain, the sign-in page — through 0050's
component set and harnesses, and builds no screen-local variant of anything
the set has.

## Objective
Show every standing denial and every directory fact wherever the subject or
the target it belongs to is shown, and offer exactly the controls the API
offers over them: an operator must be able to release a lockout here, and must
never be told they can lift what only the directory can.

## In scope
- **A subject that is locked and a target that is quarantined say so wherever
  they are shown** (M28, 0039): in the list and in the detail, with the lock's
  or the quarantine's facts — when it was placed, by whom, the reason code, the
  external reference, and the inbound action behind it if one placed it — and
  as a different state from a disabled or an unknown subject. A lock is a deny
  made on purpose, and the console renders it with a deny's colour and words
  (M11), never an outage's. A subject whose row was deleted while locked is
  listed by the lock (`record_missing`), and its lock can still be lifted.
- The console **offers lock, unlock, quarantine and release** to a holder of
  `session:revoke`, and to nobody else. Each is a destructive action and
  confirms as one. Locking asks for the text the holder is shown (one line, at
  most 512 bytes) and shows it before sending, because it is displayed
  verbatim as their sessions end; a quarantine says plainly that it ends no
  session and drops every cached decision in the tenant, and points to the
  revocation workflow for ending the ones running.
  `503 standing_denial_undelivered` is rendered as "in force, not yet
  delivered" with a retry that asks again, never as a failure to lock.
  Deleting a quarantined target is refused (`target_quarantined`), and the
  console says release it first.
- **A subject the directory manages says so wherever it is shown** (0040,
  0042): whose it is (the sync's provider, the directory it comes from —
  `directory.source`, shown wherever the directory's id is, because one id is
  two people once two directories feed a tenant — and the directory's id for
  the person), and a **deactivation** as a state of its own — when the
  directory applied it and its reason code — distinct from a lock, with a
  deny's colour and words. A subject both locked and deactivated shows both,
  and the console never suggests that unlocking re-admits a deactivated
  person: only the directory lifts a deactivation, and the console offers no
  control that claims to. It **offers no edit and no delete of a
  directory-managed subject** (the API refuses both with
  `409 subject_directory_managed`, rendered as "managed by your directory;
  change it there"); the lock, role bindings and local group membership stay
  offered. A subject the directory deactivated before it created it is listed
  with `record_missing`, as a deleted locked subject is. The sign-in page and
  the identity screens show a connector's `directory_claim` and, beside it,
  its `directory_source`, so an operator can tell a connector that reaches
  only provisioned people — and from which directory — from one that creates
  subjects at login.
- **Where the console shows the claim mapping** (0043), it shows each group
  entry's `directories` (none means it serves logins and every directory not
  listed for its name), the tenant's `require_directories`, and, while
  `stale_directory_subjects` is not zero, how many directory subjects are
  still being brought onto the new mapping. It renders the three new
  rejection codes (`mapping.directory_name`, `mapping.directories_empty`,
  `mapping.directory_duplicate`) with their field, as it renders the older
  ones.
- **Explain**, beside the mapping version, shows the directory the subject's
  groups came from (`mapping.directory_source`, 0043) when a directory
  provisioned the subject.
- **The vocabulary.** A lock, a quarantine and a deactivation are deny-class
  states: add their labels to DESIGN.md §6's deny row and render them through
  0050's status component, never beside it.

## Out of scope
- The identity sync's row on the extensions screen (`started`, `start_error`):
  0055's.
- Every other screen: the series table's.
- Re-opening the design, and any locale other than English.

## Acceptance criteria

**Function**
- Against a seeded fixture, the inventory shows a locked subject and a
  quarantined target in both the list and the detail with their facts, a
  locked subject whose row was deleted by the lock alone, and offers lock,
  unlock, quarantine and release to a `grant-admin` or `admin` session and to
  no other role. Locking and quarantining each confirm; the lock shows the
  holder text before sending. `standing_denial_undelivered` renders as in
  force and retryable. Assert the API refusal for a role without
  `session:revoke`, not only the hidden button.
- Deleting a quarantined target renders `target_quarantined` as "release it
  first".
- Against a seeded fixture, a directory-managed subject shows its provider,
  its `directory.source` and the directory's id for the person, in the list
  and the detail; no edit and no delete is offered, and the API's
  `409 subject_directory_managed` for each renders as managed by the
  directory — assert the API refusal; the lock, role bindings and local group
  membership stay offered.
- A deactivated subject shows its deactivation (when, and its reason code) as
  a state of its own with a deny's colour and words; a subject both locked and
  deactivated shows both, and no control claims to lift the deactivation; a
  subject the directory deactivated before it created it is listed with
  `record_missing`. Assert each.
- The sign-in page and the identity screens show a tied connector's
  `directory_claim` beside its `directory_source`.
- The claim mapping shows each entry's `directories`, the tenant's
  `require_directories`, and the stale count while it is not zero; each of the
  three new rejection codes renders with its field.
- Explain shows `mapping.directory_source` beside the mapping version for a
  provisioned subject, and nothing there for one that is not.
- The RBAC harness and the route test stay green with this phase's calls in
  them.

**Design and localisation**
- Every screen this phase changed passes every browser check again, with its
  baseline updated deliberately and each changed screen named in the PR.
- A lock, a quarantine and a deactivation render with a deny's colour and
  words and never an outage's, asserted in a test (M11).
- 0049's static checks stay green, the catalogue key test among them.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0054-console-standing-denials-and-directory-learnings.md`. Its
summary block MUST give the routes and fields each screen now consumes, and
the states this phase added to the vocabulary. Details MUST record **any
change made to `ui/DESIGN.md` in this PR**, with the reason.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names.
