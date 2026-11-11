# control-PR#156 — the console's Extensions and Reports

Queued by [control#156](https://github.com/Hoplock/control/pull/156), Control's
phase 0056, released as `v0.43.0`. It adds Extensions and Reports to Control's
console — the screen that lists and generates every registered report pack,
Enterprise's included — the `FeedLag` component and eight statuses to the set
Enterprise's screens are built from, and the host-catalogue keys a report is
named under. `ext`, `server`, the contract, the north-bound API version and
the cross-repo protocol are untouched. The obligations are its
`## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/156. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.43.0 and never from main —
(1) its pin, and what its binary now does: no migration; north-bound API
still version 5; Control's console has Extensions at /console/extensions
(extension:read: every ext point, a host's registrations under "In play", a
handler or sync Running or Not started with its Start error's kind, an empty
point Not registered with what Control does instead, each sink's lag on its
own registration) and Reports at /console/reports[?provider=&report=]
(audit:read: every registered pack listed and generated from its descriptor,
the file saved under the server's name); an Enterprise screen links to those
paths under /console/ and never builds its own listing or generation of a
pack, while schedules, campaigns and the history of generated reports stay
Enterprise's under /host-console/hoplock/enterprise/;
(2) a pack's report is named in the HOST's catalogue at
hoplock/enterprise:reports.<pack provider>.<report> and its parameter at
hoplock/enterprise:reports.<pack provider>.<report>.parameters.<name> (pack
provider = the pack's ext.Registration.Provider, by convention a module
path), and by its code wherever no catalogue names it — so the Enterprise
prompt that writes its catalogue carries those keys for every pack it
registers; nothing hands a host's catalogue to Control's shell until
Control's queued 0068 adds server.Options.Console, so until Enterprise pins
that release every report reads by its code, which is correct (M21) and not
a defect to work around;
(3) the component set Enterprise's screens build from gains, at this pin:
FeedLag (ui/src/components/FeedLag.tsx: Caught up, Behind, Failing, a
retention skip with its count and a link to the operator.audit.retained
records, loading, not reported) for any screen showing a sink's or the
archive's lag, and the statuses Running and Caught up (allow), Not started
and Failing (warn), Behind (info), Unavailable and Failed (outage), Not
registered (unknown) in DESIGN.md §6 — an Enterprise screen showing a sink,
an archive, a sync, an action handler or a report pack renders through these
and never invents its own; report_provider_failed,
report_provider_unavailable and report_generation_busy have sentences;
(4) a pack whose Reports fails is shown Unavailable and never as having no
reports, and a body that fails after its first byte is shown Failed with
nothing saved — Enterprise's packs need do nothing new, and an Enterprise
e2e fixture that registers packs, sinks or a handler can follow Control's
ui/e2e/fixture/host/extensions.go. No citation of Control's numbers moves;
nothing was renumbered; 0068 is new.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
