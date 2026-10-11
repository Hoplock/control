# control-PR#65 — ending an external window

Queued by [control#65](https://github.com/Hoplock/control/pull/65), Control's
phase 0017, which answers enterprise#12's `## Upstream request`: a push that
ends the window its id opened, and the sessions the window backed. The shape is
met as asked, `ext.WindowAssertion.Ended`. Four things around it differ, and the
side question is answered no. It cuts `v0.4.0`. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`. The field, the
answers and `ExternalEndReason` are verbatim in Control's
`docs/learnings/0017-ending-external-windows-learnings.md` summary.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/65. Do not implement any queued prompt in this
session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — never approximate it, and do not stop at telling me: name
the exact shape and queue the "Upstream request" kickoff from docs/KICKOFF.md,
filled in, in this repository's prompts/upstream/queued/ (§4.3).

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: for hoplock/enterprise —
(1) Its 0008, "Ending a window early — raised upstream", is rewritten from
what merged, and stops building around the gap.
`ext.WindowAssertion.Ended` is verbatim in Control's 0017 learnings summary.
On an end, Control reads the id, and records the reference, issued-at and
additional context when set. Nothing on an end is refused for its age, scope
or window. The answers are 200 with outcome revoked, already_ended or
recorded, and 503 grant_revocation_undelivered, which means push the end
again. Every one is safe to retry. Ended stays ended, including an end that
arrives before its open, so an integration need neither order nor buffer what
it pushes. A reopened or rescheduled Helix change, or a re-run Qualys scan, is
a NEW assertion id: derive ids that differ per window, such as the change id
plus its revision, or the reopened window is refused (400 window_closed,
field id). The holder is shown ExternalEndReason ("The access window this
session depended on has ended."), never the vendor's text. Drop "behind a
seam named for it and visibly unwired". The skipped live-session criterion
becomes a real one: a Helix cancellation ends a person's live session with
session_kill. It stays subject to 0008's other skips: no binding route before
Control's 0019, and the pinned version (E3).
(2) Its "Qualys specifically" and "BMC Helix specifically": "closing reaches
new connections only" stays true only for a vendor that cannot push. Record
the side question's answer: a probe that stops confirming revokes nothing, so
a probe-only deployment's live session runs to the end its probe stated, or to
the ceiling, and the docs say so. If that is not acceptable for Qualys, the
re-probe of live windows is a new upstream request, and Control's 0017 prompt
(prompts/implemented/0017-ending-external-windows.md) is where its reasoning
starts.
(3) Its plan: wherever E7 says what an inbound push may do, it may now end a
window its own system opened, never another system's, through the same
credential and binding. Wherever E13 or §4 describes the seam, the
declarative provider ends windows too (push.ended), so ending is not
something the packaged integrations add.
(4) Its pin: every obligation above holds from v0.4.0, which control#65's
merge cuts, never from main. Enterprise's pin moves to v0.4.0.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
