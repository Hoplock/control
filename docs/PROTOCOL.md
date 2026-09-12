# Hoplock Control — Session Protocol

> **Every implementation session MUST read this file first, in full.** It is
> short by design. It tells a fresh Claude Code session exactly how to pick up
> work, implement one prompt, and hand off cleanly to the next session.

This protocol exists to keep sessions consistent, keep context windows small
(target **< 60% context per session**), and reduce hallucination by grounding
every session in the same durable artifacts. It mirrors Hoplock Proxy's
protocol on purpose: the two repositories are worked the same way, so a session
that has done one already knows how to do the other.

To start a session, see `docs/KICKOFF.md` for the exact kickoff prompts to
paste — including the downstream sync a merged cross-repo change owes this
repository, which is not a phase and has no prompt number.

---

## 0. TL;DR of a session

1. Read this protocol.
2. Take the **lowest-numbered** prompt in `prompts/queued/` (unless the user
   names a specific one). That prompt is your entire task, and its "Read first"
   block names what to read next.
3. Navigate `docs/PLAN.md` (the architecture source of truth): its section
   headings and its decision register, then the sections and decisions your
   prompt names (Section 1).
4. Read the **summary block** of each file in `docs/learnings/` (read a full
   learnings file only if it's relevant to your prompt).
5. Create a fresh branch off the default branch.
6. Implement the prompt. Keep it in scope. Meet the Definition of Done.
7. Move the prompt file from `prompts/queued/` to `prompts/implemented/`
   (unchanged name) **in the same PR**.
8. Write a learnings file to `docs/learnings/`.
9. Open a PR. Iterate with the user until they are happy — and once it is green
   and mergeable, **go idle and wait** rather than polling it (§8).
10. **The session ends when the PR is merged.**

---

## 1. Startup reading order (context budget)

Read in this order and **stop reading as soon as you have what you need**:

1. `docs/PROTOCOL.md` — this file (always, in full).
2. Your target prompt in `prompts/queued/` — before the plan, not after it.
   Its "Read first" block (Section 7) is what tells you which of the plan you
   need.
3. `docs/PLAN.md` — **navigated, not read front to back.** This is the
   architecture; do not re-derive it. Read its section headings
   (`grep -n '^#\+ ' docs/PLAN.md` — those headings are the index) and the
   **decision register** at the head of §2, then read the sections and
   decisions your prompt names in its "Read first" block (Section 7).

   Two guards, and neither is optional:

   - **Widen whenever you are about to make a decision the plan may already
     have made.** The register tells you in one line whether some `M` settles
     it; read that decision in full before you decide anything. The context
     budget is never a reason to re-derive a decision the plan already made —
     that mistake costs far more than reading the plan whole would have.
   - **A prompt that names no sections is a defective prompt**, not a licence
     to read everything. Fix the prompt (Section 7) or ask the user; do not
     quietly absorb the whole plan and call it thorough.

   The rule is written this way from the start on purpose. "Always, in full" is
   affordable today and will stop being affordable without any single phase
   being the one that made it so.
4. `docs/learnings/*` — read **only the summary block** at the top of each file
   first. Open the full body **only** when its summary shows it's relevant.
5. `contract/control.yaml` — **only the endpoints your prompt touches.** It is
   a large document and reading it whole will cost you the context budget you
   need for the work.

Do **not** read the whole codebase, and do **not** read the Hoplock Proxy repository
unless your prompt names a file in it. If you find yourself reading broadly,
stop and re-scope — the prompt or a learnings file should already point you at
the right places. Staying under ~60% context is a hard goal; if you're
approaching it, prefer finishing a smaller, correct slice over reading more.

---

## 2. Branching

- Branch off the **latest default branch** (`main`):
  `git fetch origin main && git checkout -B <branch> origin/main`.
- **Use the branch the session was given.** These sessions are normally started
  with one already assigned — `claude/queued-prompt-implementation-<suffix>` or
  similar — and it is not yours to rename. **That is not a deviation and must
  not be written up as one.** A rule nobody can follow is not a rule, it is a
  recurring apology: sessions that recorded it as a deviation spent a reviewer's
  attention on a fact about the harness rather than about the change.
- **When the name *is* yours to choose**, use `claude/NNNN-short-description`
  matching the prompt (e.g. `claude/0005-policy-engine`), or
  `claude/sync-<short-description>` for a cross-repo sync
  (`docs/CROSS-REPO-PROTOCOL.md` §5).
- **The PR carries what the name was meant to carry.** §8 already requires the
  PR description to state which prompt it implements, and that is the link a
  reviewer and a future session actually follow. A name that cannot be chosen
  cannot be relied on to identify anything, so nothing relies on it.
- **Never push to `main`.** Never push to another prompt's or another session's
  branch.
- If a prior PR for your branch name was already merged, start fresh from `main`
  (do not stack on merged history).

---

## 3. Doing the work

- **Scope discipline.** Implement exactly what the prompt specifies. If you
  discover work that belongs to a later phase, do **not** do it here — note it in
  your learnings file and/or add a new queued prompt (Section 6).
- **Follow the plan.** Match `docs/PLAN.md`: package layout, interfaces, naming,
  and the decisions in its §2 (the register at the head of that section lists
  them). If reality forces a deviation, update `docs/PLAN.md` in the **same PR**
  and call it out in the PR description and learnings.
- **The plan is written in the present tense.** When your phase changes what the
  plan says, **revise the affected text** so the section states what is now
  true. Never append a dated layer — "As built (phase 0014)", "As corrected
  (phase 0015)" — beneath the text it supersedes. Eight such layers on one
  section means the current behaviour is knowable only by reading all eight and
  composing them, and every session after yours pays that cost forever. Where
  the reasoning for the change is worth keeping — and it usually is — it goes in
  **your learnings file** (Section 5), or in a clearly marked history note that
  no session needs to read in order to know the current state. `git log` holds
  the rest.
- **A decision's current status is visible without reading the decision.**
  `docs/PLAN.md` §2 opens with a **register**: one row per decision — what it
  settles, its status (`live`, `amended by M<n>`, `withdrawn`), and where it is
  rendered. If your phase adds a decision, amends one, or withdraws one, update
  its row **in the same PR**, and say so in the decision's **first line** rather
  than four hundred words in. A reader who learns about an amendment only by
  reading the entry to the end has already spent the context the register exists
  to save.
- **Cross-repo changes follow `docs/CROSS-REPO-PROTOCOL.md`.** This repo sits in
  the middle of the chain: it consumes the proxy's contract (M1) and owns `ext/`,
  which Hoplock Enterprise imports (M15). Both directions create work that has no
  prompt number, so nothing in *this* file covers it. That one does: the ordering
  (upstream merges first), the downstream-impact check your PR owes — including
  the ready-to-run sync kickoff it must hand the user for Hoplock Enterprise
  (§4), taken from `docs/KICKOFF.md`'s "Downstream sync" block — and the
  conventions for a sync PR. It lists the shared surfaces in its Section 1; if
  your change touches none of them, you do not need to read it.
- **Never edit `contract/` (M1).** That directory is vendored from the proxy
  repository and is generated, not authored. If the contract is wrong or missing
  something you need, **stop and tell the user**: the change is made in the
  Hoplock Proxy repository, merged there, and pulled in with `make contract-sync`. Editing
  the local copy makes CI green while the two components silently diverge, which
  is the exact failure this rule exists to prevent.
- **Never import Hoplock Enterprise (M15).** The dependency runs one way:
  Enterprise imports this module, never the reverse. If a phase seems to need
  something from Enterprise, it needs an **extension point** in `ext/` instead —
  and a real default implementation here, because Control alone must be a
  complete product. An import-graph test enforces this; do not work around it.
- **`ext/` is a compatibility promise.** It is the only non-`internal` package
  in the module. Changing a signature there breaks Enterprise builds, so treat
  it like the wire contract: change it deliberately, say so in your learnings,
  and never as a drive-by.
- **`401` is a decision (M11).** Never return it for a database failure, a
  timeout, a compile error, or a panic. The proxy will faithfully tell a real
  user "access denied" and send your operator to debug permissions during an
  outage. Deny on purpose; `5xx` for everything else.
- **Migrations are forward-only.** Never edit a migration that has been merged;
  add a new one. Every table carries the tenant column (M12).
- **Match the codebase.** Mirror existing structure, naming, error handling, and
  test style. Add the per-file license header (see `docs/LICENSE-HEADER.md`).
- **No secrets in code, logs, errors, or fixtures.** Never commit keys, tokens,
  IdP client secrets, or real hostnames. Fixtures are test data.

---

## 4. Definition of Done (all must hold before requesting merge)

- [ ] The prompt's stated deliverables and acceptance criteria are met.
- [ ] `go build ./...`, `go vet ./...`, and `go test ./...` pass locally.
- [ ] Linter (`golangci-lint run`) passes, or new findings are justified.
- [ ] New/changed behavior has unit tests; integration tests updated if relevant.
- [ ] `make contract-check` passes — the vendored contract is unmodified and
      matches its pinned upstream commit.
- [ ] `make conform` passes, once phase 0002 has landed and once this server
      serves any endpoint the suite covers.
- [ ] `docs/PLAN.md` updated if the architecture changed — **revised in place**,
      not appended to, with any decision added/amended/withdrawn reflected in
      the §2 register in this PR (Section 3).
- [ ] The prompt file moved from `prompts/queued/` → `prompts/implemented/`
      (same filename) in this PR.
- [ ] A learnings file added to `docs/learnings/` (Section 5).
- [ ] Prompt-numbering invariants still hold (Section 6), and any prompt this PR
      adds or modifies has a "Read first" naming plan sections and decision ids
      (Section 7).
- [ ] CI is green on the PR.

---

## 5. Learnings file (the hand-off to future sessions)

Before opening the PR, add `docs/learnings/NNNN-short-description-learnings.md`
where `NNNN-short-description` **matches the prompt you implemented**.

It MUST begin with a **summary block** so future sessions can decide whether to
read further without spending tokens on the whole file:

```markdown
# 0005 — policy engine — Learnings

## Summary
- What shipped: <1–3 lines>
- Key packages/files: <paths>
- Key interfaces/types added or changed: <names>
- Database tables/migrations added: <names>
- Decisions made/affected: <M-ids, or new decisions>
- Gotchas / non-obvious constraints: <1–3 lines>
- What the NEXT session must know: <1–3 lines>

## Details
<Everything else: rationale, how to extend, test notes, follow-ups, deviations.>
```

Keep the summary block tight (aim ≤ ~14 lines). Put depth in Details. If you
created follow-up prompts, list them here. If your phase needs a **contract
change in the Hoplock Proxy repository**, say so explicitly and describe the exact field —
that is a cross-repo dependency and the next session must not discover it by
being blocked.

**Why that length is a cap and not a suggestion.** Every session reads every
summary block in this directory at startup, so what this directory costs is
(number of phases × size of each block) — and neither factor ever shrinks. Ten
lines you could have cut are ten lines paid by every session for the life of
the repository. Details are free, because nobody opens them unless the summary
says they need to, so when a summary runs long the fix is to move text down
rather than to widen the block.

---

## 6. Prompt numbering invariants

Prompts are named `NNNN-short-description.md` with a **4-digit** zero-padded
prefix indicating implementation order.

- **Uniqueness:** no number may repeat across `queued/` **or** `implemented/`.
- **Implemented names are frozen:** never rename a file in `prompts/implemented/`.
- **When you add new prompts:** if a new prompt must run before existing queued
  prompts, **renumber the queued prompts** (only queued ones) so order is correct
  and numbers stay unique, and record *why* in a renumbering note in
  `docs/PLAN.md` §10 — older learnings files will still refer to the old
  numbers, which is what the next bullet is for.
- **When numbers move, regenerate the composed mapping in the same PR.** Each
  renumber adds a **renumbering note** to `docs/PLAN.md` §10 saying *why* it
  happened; keep those, they are the record. But no reader may be asked to
  compose the notes by hand to resolve an old number. §10 also carries **one
  table** — "what an old number resolves to" — already composed through every
  later revision, and your PR regenerates it. Done from the first renumber this
  costs a row. Left to accumulate it becomes a pile of notes over numbers that
  are each simultaneously a live phase and a historical alias of a different
  one, and resolving one by its digits is guesswork.
- Each new prompt must be **self-contained** (Section 7).

---

## 7. Writing a self-contained prompt

Any prompt must be runnable by a **fresh** session with no prior context. It must:

- State its objective, in-scope and out-of-scope items.
- Open with a **"Read first"** block that names — **by section number and by
  decision id** — the `docs/PLAN.md` sections (`§5.2`) and decisions (`M3`,
  `M16`) the phase needs, alongside `docs/PROTOCOL.md` and the relevant
  `docs/learnings/` summaries. This is the precondition for Section 1: a session
  can only read "the sections its prompt names" if the prompt names them, and a
  prompt that names none is defective rather than an instruction to read
  everything. Use **this repository's** decision ids — `M*` here, `E*` for
  Enterprise, `D*` for the proxy — and cite them by id, never by a bare number
  (`docs/CROSS-REPO-PROTOCOL.md` §1).
- **This binds a prompt you modify as much as one you add.** If you touch a
  prompt whose "Read first" names nothing, fix it while you are there.
- Name the exact packages/files to create or change.
- Specify interfaces/types precisely enough to implement without guessing.
- Define acceptance criteria and required tests.
- Name any cross-repo dependency explicitly.
- Assume nothing about session history beyond the durable docs.

---

## 8. Commits & PR

- **Commit style:** Conventional Commits — `type(scope): summary` in the
  imperative mood. Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`,
  `ci`, `build`. Scope = package or phase (e.g. `feat(policy): ...`). Keep the
  subject ≤ 72 chars; explain the "why" in the body when non-obvious.
- **One prompt = one PR.** The PR description states which prompt it implements,
  summarizes changes, lists any plan deviations, and confirms the Definition of
  Done checklist.
- **Iterate on the PR** with the user's review feedback until they're happy.
- **The session's job is done when the PR is merged.** Do not start the next
  prompt in the same session.
- Do not create a PR for work the user hasn't asked to be turned into a PR; the
  normal implementation flow above does open one.

### Say each thing once

A phase produces three records, with three different readers:

| Record | Reader | For how long |
| --- | --- | --- |
| the learnings file (Section 5) | the next session, and every one after it | forever |
| the PR description | the reviewer | the life of the PR |
| what the session says in chat | the user | once |

Only the third is not a record, and it is the most expensive of the three: it is
output the user pays for, and input every later turn of the session carries. So
**each fact goes in exactly one of the first two**, and the session says only
what neither can deliver:

- **At PR open** — the link, plus what the description cannot carry: a question
  you need answered, or a ready-to-run kickoff for the user to paste
  (`docs/CROSS-REPO-PROTOCOL.md` §4).
- **At green** — one line (below).
- **At merge** — one line, and stop, unless the user has to *act* on something.

Do not restate the learnings file in the PR description, the PR description in
chat, or the diff in any of them. A summary of a document the reader can open
is not a service to them. This rule is independent of how large the repository
is; it applies from the first phase.

### Waiting for review is waiting, not polling

Once the PR is open, **green and mergeable**, the session's remaining job is to
wait. Hand it over in one line — "green, mergeable, waiting on your review" —
and then **go idle**.

- **Do not schedule recurring check-ins** on the PR, and cancel any you already
  scheduled once it goes green. No timers, no hourly re-reads, no "still green"
  status messages.
- A healthy PR only changes when a human acts on it or CI reports something, and
  **both of those arrive as events** that wake the session on their own. Polling
  for them discovers nothing a wake-up would not have delivered.
- Every unnecessary wake costs the user real money and tells them nothing. Ten
  check-ins reporting "no change" are ten times the cost of zero.

Two exceptions, and only two:

- **CI is red, or the branch has a merge conflict.** That is work, not waiting:
  diagnose it, fix it, push. Only a *green, mergeable* head waits for a human —
  a broken one is never "waiting on review".
- **The user asked for a specific check** ("tell me when CI finishes"). Do that
  one check, report it, and go idle again.

The next thing the user hears from a waiting session should be a reply to
something they said — not a heartbeat.

This rule is **not** carried by `docs/CROSS-REPO-PROTOCOL.md` and is not a
shared surface: each repository's `docs/PROTOCOL.md` is its own (§1 of that
file). It was written in `hoplock/proxy` first (its §8, commit `2c62ea5`) and
adopted here deliberately, because the behaviour it corrects — a session filling
the wait with scheduled re-reads — is not proxy-specific. A later change to the
proxy's wording does not travel here on its own.

---

## 9. Guardrails (reduce hallucination)

- The durable truth is: this protocol, `docs/PLAN.md`, `docs/learnings/`, and
  `contract/control.yaml`. Trust them over memory. If they conflict, the
  contract wins for wire shapes, the plan wins for architecture, and the protocol
  wins for process — and you flag the conflict to the user.
- **Never invent a contract shape.** If an endpoint, field, or enum value is not
  in `contract/control.yaml`, it does not exist. Adding one is an upstream
  change (Section 3).
- If a prompt seems to contradict the plan, **stop and ask the user** rather than
  guessing.
- Don't expand scope to "be helpful" — smaller, correct, well-documented PRs are
  the point.
