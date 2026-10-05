# control-PR#3 — an ext point for external access context (M16)

Backfilled from [control#3](https://github.com/Hoplock/control/pull/3), which
predates the request queues and handed over no kickoff: its `## Cross-repo
impact` section, verbatim. enterprise#2 was a roadmap revision, not a sync: it
added a decision and a phase, which a sync may not. Answered by
[enterprise#2](https://github.com/Hoplock/enterprise/pull/2).

> **`hoplock/proxy`** — None. This PR consumes decisions that merged upstream in #5; it defines no contract field and edits no vendored artifact. The proxy's own 0013 and 0015 carry sync appendices that will sharpen the text above from the obligation to each field's real name and absent-value default.
>
> **`hoplock/enterprise`** — It gains a real `ext` point to implement. Its branch adds E13 and a phase for the packaged Qualys and BMC Helix integrations, written entirely against this seam. **Per §2 it must not merge before this PR does**, and it is not open yet for that reason. Its phase additionally cannot *start* until 0013 has merged, since it is written from that phase's learnings summary.
