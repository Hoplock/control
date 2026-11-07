# control-PR#148 — the console shell

Queued by [control#148](https://github.com/Hoplock/control/pull/148), Control's
phase 0049, released as `v0.36.0`: the operator console's shell, served at
`/console/`, which Enterprise's screens are built on. No shared surface
changed; the obligations are its `## Cross-repo impact` section's, for
`hoplock/enterprise`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/148. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, every one from v0.36.0 and never from main —
(1) its pin, and what its binary now does: it serves Control's console at
/console/ (the shell for any path under it, with a Content-Security-Policy
allowing only its own origin), redirects / to /console/, and its configuration
accepts console.stripe (accent, magenta, teal, orange or slate; strict
decoding, so its configuration documentation names the key); an OIDC
connector used from the console sets redirect_uri to
/console/sign-in/callback/<connector>; (2) the prompts that build its screens
(approval inboxes, report schedules and history, recertification) build to
Control's docs/learnings/0049-console-shell-learnings.md: served from
/host-console/hoplock/enterprise/ and never under /console/; catalogue keys
hoplock/enterprise:<key>, refused at start-up if equal to a key of Control's
en.json; the look inherited from the custom properties of Control's
ui/src/styles/tokens.css on the shell's page, never a token file of its own;
ICU MessageFormat, English only; and its own Hoplock-Api-Version declaration,
since its screens are not in lockstep with the API as Control's console is;
(3) no server.Options field hands a host's screens to Control yet: the prompt
of the phase that first serves Enterprise's screens says it raises an
upstream request for a field shaped as north.Console without Provider —
Patterns []string, Catalogue []string and one Handler http.Handler — when it
starts, and builds behind that seam.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers, and says how you searched for stale
references — the actual grep, not "I looked carefully".
```
