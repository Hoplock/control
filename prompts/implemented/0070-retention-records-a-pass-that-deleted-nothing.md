# 0070 — A retention pass that deleted nothing is recorded too

## Read first
- `docs/PROTOCOL.md` — session workflow; §3 on revising the plan in place;
  §8's "A console phase shows what it built" (this phase changes a screen
  under `ui/`).
- `docs/PLAN.md` — §2: **M8** (audit is append-only and tamper-evident;
  retention is a policy "that records what it deleted"; with an archive
  registered, retention never removes what the archive has not confirmed),
  **M11** (a refusal is not an outage), **M23** (every phase is a release; a
  behaviour change no API diff sees is still listed); §7 at "Retention has to
  remove a chain's TAIL, never a record out of its middle" — in particular
  "With an archive registered, retention waits on it" — and at "Export"
  (what a sink is handed, and how `operator.audit.retained` explains a gap to
  it).
- `docs/API-VERSIONING.md` — whether a behaviour change with no new route or
  parameter moves the north-bound version (the answer's shape does not change
  here).
- `ui/DESIGN.md` §7, the paragraph "Stream gaps and retention in the audit
  view".
- `docs/learnings/` — read summaries; open **in full**
  `0057-console-audit-gaps-and-retention` (its Details: "Noticed, not
  changed", "The fixture", "Registered with the harnesses"). Read the summaries
  of `0024-signed-retention-markers`, `0025-archive-before-retention` and
  `0026-audit-sink-delivery`.

## Objective
Every retention pass by instant that completes is an operator's act, and the
trail says so — including one that deleted nothing. Today the two passes
disagree:

- the **capture** pass (`audit.Emitter.RetainCaptures`) records a pass that
  emptied nothing — its doc comment: "A pass that emptied nothing is recorded
  too: it is still an operator's act";
- the **prefix** pass (`audit.Emitter.RetainPrefix`) returns before writing
  anything when the pass by instant deletes nothing. That is when the instant
  reaches no record past the stream's marker, or when an archive holds it back
  entirely (`internal/audit/retention.go`, the `n <= prior.ThroughSeq` and
  `n == 0` returns). The answer says `held_by_archive: true`, and the trail
  holds no trace that anyone asked.

0057 found this while building the retention view. With the fixture's archive
confirming nothing, a pass by instant answers "held back by the archive" and
the trail stays empty. PLAN §7 says such a pass "is sealed and recorded as
any pass is, and deletes nothing when the archive is at or behind the marker",
and is ambiguous on whether "recorded" covers the pass that deleted nothing.
The user ruled that it does: it is recorded, as the capture pass already is.

## In scope
- **`audit.Emitter.RetainPrefix`, a pass by instant that deletes nothing**
  (both early returns) writes an `operator.audit.retained` record on the
  control stream, in the pass's own transaction (`OperatorEvent`, as the
  deleting path does), with:
  - `operator.stream`, and `operator.deleted` = `0`;
  - `after` = the sequence the stream is retained through: the existing
    marker's `through_seq`, or `0` with no marker;
  - `operator.through_hash`, `operator.key` and `operator.signature` = the
    existing marker's, when there is one, and absent when there is none;
  - with an archive registered, `operator.held_by_archive` and
    `operator.archived_through_seq`, as the deleting path writes them;
  - a message that says nothing was retained, and — when held — that the
    archive held it back.

  No marker is written, moved or re-signed: nothing was deleted, so there is
  nothing new for a seal to vouch for, and `audit-verify` sees no change. The
  answer is unchanged in shape and in values.
- **Unchanged, and asserted unchanged:** a pass by sequence refused with
  `409 retention_not_archived`, or by the store (at or behind the marker, or
  at the head), is refused whole — nothing deleted, sealed **or recorded**
  (PLAN §7). A pass refused with `503 retention_key_not_configured` or
  `409 retention_unverified` records nothing. A pass that deletes records is
  recorded exactly as today.
- **The console's retention view** (`ui/src/screens/Retention.tsx`). The trail
  now holds passes that deleted nothing; each shows `Deleted` `0` and, when
  held, "Held back: archived through N". The trail's empty state and its
  sentence no longer say a pass is recorded only when it deletes. A completed
  pass's answer beside the form says it is recorded in the trail whether or
  not it deleted, and the trail re-reads after it. New catalogue keys for the
  changed sentences: a key is never reused for a different sentence
  (`ui/DESIGN.md` §11).
- **The browser checks** (`ui/e2e/`). A real pass by instant now writes a
  record, so it may no longer run in the `checks` project, where it would
  change the records view's baseline:
  - `audit-retention-held` in `screens.ts` and the held test in
    `audit-gaps.spec.ts` take the server's answer **substituted in the
    browser** (`held_by_archive: true`, `deleted: 0`,
    `archived_through_seq: 0`), captioned as substituted. The refused pass by
    sequence stays real, since it still records nothing.
  - A new test in `mutations.spec.ts` runs the real pass by instant as admin
    against the fixture's archive, which confirms nothing. It asserts the
    answer is held, then that the trail holds one new pass with `Deleted` `0`
    and "Held back: archived through 0", then that `audit-verify`'s view is
    unchanged: no marker on the stream.
  - Update the comments that say the held pass "deletes or records nothing"
    (`screens.ts`'s `GAPS` header, `fixture/host/retention.go`).
- **The plan:** revise PLAN §7's sentence in place so it states that a pass
  that deletes nothing writes no marker and no seal and is still recorded.
  Revise M8 in place where it says what a pass records; its register row's
  status stays `live`.

## Out of scope
- Recording a **refused** pass (by sequence, unverified, no key store): a
  refusal deletes, seals and records nothing, and that does not change.
- The capture pass: it already records a pass that emptied nothing.
- Any change to the marker, the seal, the verifier, the archive feed or sink
  delivery, beyond the extra record they are handed.
- The retention form's fields, and the archive's lag.

## Acceptance criteria

**Function** (`internal/audit/retention_test.go`,
`internal/httpapi/north/retention_test.go`, `archive_test.go` as fits)
- With an archive registered that has confirmed nothing, a pass by instant
  answers `held_by_archive: true`, `deleted: 0`. It writes exactly one
  `operator.audit.retained` record, carrying `operator.deleted` `0`,
  `operator.held_by_archive` `true` and `operator.archived_through_seq` `0`.
  No row is added to `audit_retention`, and the stream still verifies with no
  marker.
- With no archive, a pass by instant before an existing marker's reach
  answers `deleted: 0` and writes one record whose `after`,
  `operator.through_hash`, `operator.key` and `operator.signature` are the
  existing marker's. The marker row is byte-for-byte unchanged.
- A pass by instant that deletes records is recorded exactly as before
  (regression).
- A pass by sequence beyond the archive (`409 retention_not_archived`), one
  the store refuses, one with no key store (`503`) and one over a start that
  does not verify (`409 retention_unverified`) each leave the control stream's
  head where it was.
- The record and the answer agree on every fact both carry.
- A registered sink is handed the new record like any other, and its delivery
  counts no skip for it.

**Console and browser checks**
- The new mutations test passes. The checks project runs no pass that writes
  a record, so the records view's baseline does not move.
- The retention view's changed screens pass every browser check, with their
  baseline updated deliberately and each named in the PR. Screenshots in the
  PR per `docs/PROTOCOL.md` §8.
- 0049's static checks stay green, the catalogue key test among them.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move this prompt to `implemented/`; add
`docs/learnings/0070-retention-records-a-pass-that-deleted-nothing-learnings.md`.
Its summary names the record's fields for a pass that deleted nothing, and
says that no marker is written for one.

**Release.** Every phase is a release (M23): the next MINOR after the latest
tag, which `make release-check` names. The answer's shape is unchanged. A
pass that deleted nothing now adds a record to the control stream, which every
sink and the archive are handed. That is a behaviour change no API diff sees,
so it is listed in `CHANGELOG.md`, under `### Breaking` if M23's rule puts it
there.

**Cross-repo.** `hoplock/enterprise` is owed a downstream sync
(`docs/CROSS-REPO-PROTOCOL.md` §4.1): an `ext.AuditSink` or `ext.ArchiveStore`
it registers is now handed `operator.audit.retained` records with
`operator.deleted` `0`, which explain no gap. `hoplock/proxy`: none.
