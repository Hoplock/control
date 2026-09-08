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
2. Read `docs/PLAN.md` (the architecture source of truth).
3. Read the **summary block** of each file in `docs/learnings/` (read a full
   learnings file only if it's relevant to your prompt).
4. Take the **lowest-numbered** prompt in `prompts/queued/` (unless the user
   names a specific one). That prompt is your entire task.
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
2. `docs/PLAN.md` — always. This is the architecture. Do not re-derive it.
3. `docs/learnings/*` — read **only the summary block** at the top of each file
   first. Open the full body **only** when its summary shows it's relevant.
4. Your target prompt in `prompts/queued/`.
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
  decisions (M1–M15). If reality forces a deviation, update `docs/PLAN.md` in the
  **same PR** and call it out in the PR description and learnings.
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
- [ ] `docs/PLAN.md` updated if the architecture changed.
- [ ] The prompt file moved from `prompts/queued/` → `prompts/implemented/`
      (same filename) in this PR.
- [ ] A learnings file added to `docs/learnings/` (Section 5).
- [ ] Prompt-numbering invariants still hold (Section 6).
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

---

## 6. Prompt numbering invariants

Prompts are named `NNNN-short-description.md` with a **4-digit** zero-padded
prefix indicating implementation order.

- **Uniqueness:** no number may repeat across `queued/` **or** `implemented/`.
- **Implemented names are frozen:** never rename a file in `prompts/implemented/`.
- **When you add new prompts:** if a new prompt must run before existing queued
  prompts, **renumber the queued prompts** (only queued ones) so order is correct
  and numbers stay unique, and record the mapping in `docs/PLAN.md` §10 — older
  learnings files will still refer to the old numbers.
- Each new prompt must be **self-contained** (Section 7).

---

## 7. Writing a self-contained prompt

Any prompt must be runnable by a **fresh** session with no prior context. It must:

- State its objective, in-scope and out-of-scope items.
- Reference `docs/PROTOCOL.md`, `docs/PLAN.md`, and the relevant
  `docs/learnings/` summaries at the top ("Read first").
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
