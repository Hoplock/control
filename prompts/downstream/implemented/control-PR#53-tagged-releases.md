# control-PR#53 — tagged releases

Queued by [control#53](https://github.com/Hoplock/control/pull/53), which cuts
Control's first release, `v0.1.0`, and makes every Control phase a release
(Control's M23). It answers enterprise#8's request for a release tag. The
obligations are its `## Cross-repo impact` section's, for `hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/53. Do not implement any queued prompt in this
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
section: (1) Confirm the release first: v0.1.0 must resolve through the module
proxy (`go list -m github.com/hoplock/control@v0.1.0`). If it does not,
Control's release job failed on control#53's merge commit: stop and say so
(§2), and write no prompt against a version that does not exist. (2) 0001's
"Dependency on Control": rewrite the paragraph that begins "If Control has
published no release tag" from what merged. Pin Control's newest release,
never a pseudo-version, and drop the stop and the visibly unresolved pin.
--version reads Control's version from debug.BuildInfo, in the dependency
entry for github.com/hoplock/control, which is the pin; Control's own
--version reports the same once started through server.Main, and a replaced
Control reports dev and names its replacement. The learnings item "the pinned
Control version and how to bump it" names Control's CHANGELOG.md and Control's
M23. (3) The plan: E3 and §5 cite Control's M23 by id for what a version
promises, instead of describing Control's versioning themselves; E3's changelog
is Control's CHANGELOG.md. M23: every Control phase is a release, an immutable
vMAJOR.MINOR.PATCH tag of the one module, with ext versioned with it; MINOR for
a phase or an incompatible change to a public package, PATCH otherwise; every
break listed under ### Breaking, and an added interface method is a break.
(4) Wherever an Enterprise prompt waits for a Control phase to be in "the
pinned Control version", it now waits for the release that phase cut, which
Control's CHANGELOG.md names. Its 0008 does this for Control's north-bound API
(0018).

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
