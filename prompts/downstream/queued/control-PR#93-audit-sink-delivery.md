# control-PR#93 — audit sink delivery

Queued by [control#93](https://github.com/Hoplock/control/pull/93), Control's
phase 0026, which answers `Hoplock/enterprise#26` item 2: Control delivers
every committed record to every registered `ext.AuditSink`, and retention never
waits on a sink. It cuts `v0.13.0`, a MINOR with no `### Breaking`: no
signature changed, and `AuditSink`'s doc comment now says what is true. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`,
the repository that asked.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/93. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, all holding from v0.13.0, never from main —
(1) Its 0006 (SIEM export), "What Control does not do yet", rewritten from
what merged, and no longer built around the gap: Export gets every committed
record of every tenant, one tenant's and one stream's records per batch,
ascending Sequence, at least once; nil confirms and Control moves a durable
position, an error confirms nothing and the same records are offered again
after a backoff for that stream alone, so Export stays idempotent on RecordID;
the position is Control's, per sink, tenant and stream, so a sink keeps no
cursor and resumption after a restart is Control's to prove; a sink is named
by its Registration.Provider — re-registering resumes, a new name is a new
sink starting each stream at its chain start; the concurrency contract —
Export must be safe for concurrent use, Control never has two batches of one
stream in flight and has up to audit.sinks.max_in_flight (default 4) of
different streams, and a call past its deadline keeps its place until it
returns; retention never waits on a sink — one a pass overtakes resumes after
the marker, the run is counted on its lag, and the pass's
operator.audit.retained record (stream, last sequence deleted, its hash = the
PrevHash of the first record after the gap) explains it; delivery writes no
audit record, and capture bytes never cross to a sink; each sink's lag on its
registration in GET …/extensions for the caller's tenant (streams_behind,
oldest_unconfirmed_at, failing, skipped_by_retention); configured by
audit.sinks.* (batch_records 500, attempt_timeout 30s, idle_interval 5s,
backoff_cap 5m, max_in_flight 4). Turn its skipped end-to-end criteria into
real ones.
(2) Its plan: §4's AuditSink row and its misreadings, and wherever E6 or E7
says what reaches a SIEM and how — every record of every tenant, no capture
bytes, retention never waiting on a SIEM, the archive being where records are
kept past retention.
(3) Its pin: nothing fails to compile against v0.13.0, but Export is now
called, and called concurrently.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
