# Audits

Prompts that are **run repeatedly against the whole repository**, rather than
once at a point in the delivery sequence (see `docs/PROTOCOL.md` §6).

Nothing in here is queued. `prompts/queued/` is the build order and a session
takes the lowest-numbered prompt from it (`docs/PROTOCOL.md` §0.2); this folder
is outside that order entirely, and a session runs one of these only because the
user named it. That is the whole reason the folder exists: an audit sitting in
`queued/` is either a number that schedules it after the phases it was meant to
protect, or a renumbering of every live phase to put it first.

Files are named `short-description.md` — no `NNNN` prefix, because there is no
position in the order to encode.

Three rules, all enforced by `protocol_test.go`:

- **An audit is never moved.** It is re-run, not completed, so it does not go to
  `prompts/implemented/` when a session finishes with it, and it is not copied
  into `prompts/queued/` to make it "next".
- **Each audit keeps one learnings file**, updated in place:
  `docs/learnings/audit-<short-description>-learnings.md`, newest run at the top,
  earlier runs kept beneath. The history is the point — it is what shows whether
  the same gap keeps reappearing — and it carries the as-of markers that let the
  next run start where the last one stopped instead of repeating the whole
  history.
- **An audit fixes text and ships no behaviour.** One that starts implementing
  has found a phase: queue it in `prompts/queued/` as a numbered prompt
  (`docs/PROTOCOL.md` §6) and keep the audit's own PR to the audit.

Use this folder only for work that is genuinely repeated and has no position in
the build order. Anything built once is a numbered phase, whatever it is called.
