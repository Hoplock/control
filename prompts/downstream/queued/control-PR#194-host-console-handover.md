# control-PR#194 — a host's console reaches the shell

Queued by [control#194](https://github.com/Hoplock/control/pull/194), Control's
phase 0069. It answers no request: it is the sync a release owes its consumer
when it adds to `server/` (`docs/CROSS-REPO-PROTOCOL.md` §4.1). The
obligations are control#194's `## Cross-repo impact` section's, for
`hoplock/enterprise`. Nothing is owed to `hoplock/proxy`.

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/194. Do not implement any queued prompt in this
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
section: for hoplock/enterprise, from Control's release v0.56.0 — the release
control#194 cuts — and never from Control's main:
(1) The field. server.Options gains Console *server.Console{Patterns
[]string, Catalogue []string, Handler http.Handler}; nil is none. Control
mounts it on the north-bound listener under /host-console/hoplock/enterprise/
and nowhere else (M27), served as Control's console is: files and a history
fallback, without Control's authentication, wrapped by its recovery, access
log, body limit and deadline. Patterns are absolute and method-less under
that prefix, and one must serve locales/en.json. Catalogue is every key the
host's catalogue holds, each hoplock/enterprise:<key>. Refused when Main or
Run is called, before the configuration is read: no Provider, no Handler, no
Patterns, a pattern outside the prefix (or with a method, a space, a "." or
".." or empty segment, or one the mux cannot mount), no pattern serving
locales/en.json, and a key that is empty, not hoplock/enterprise:<key>, or
one of Control's own (judged against Control's real en.json).
(2) What Control's shell does with it. When a host console is mounted, the
shell's page names the host and its catalogue's directory on <html>
(data-host, data-host-catalogue) and the shell loads
/host-console/hoplock/enterprise/locales/<locale>.json (en.json: English is
the only locale, M21; pseudolocalised in Control's en-XA/en-XB builds) from
the same origin, keeping only hoplock/enterprise:<key> keys, each an ICU
message with no arguments. Control's Reports screen names each of
Enterprise's report packs' reports at
hoplock/enterprise:reports.<pack provider>.<report> and each parameter at
….parameters.<name>; where the catalogue names none, or cannot be loaded
(refused, not JSON, no answer within 10 s), it shows the code and names the
gap. Where this repository's plan or prompts describe Enterprise's report
names as codes on Control's screen, or its catalogue as not handed over to
Control, they say this, and the phase that builds Enterprise's catalogue
writes those keys.
(3) What is not there yet. Control's shell loads nothing else from a host's
console: loading Enterprise's screen modules, and a navigation entry for
them, is Control's queued phase 0073 (prompts/queued/0073-host-screens-in-the-shell.md).
Where this repository's plan or prompts describe Enterprise's screens as
reached from Control's console, they name 0073 as what makes that possible,
and build nothing that depends on it before it is released.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
names the request file this sync answers (none: this is a release's sync), and
says how you searched for stale references — the actual grep, not "I looked
carefully".
```
