# control-PR#46 — the access-context seam

Backfilled from [control#46](https://github.com/Hoplock/control/pull/46), which
predates the request queues: the kickoff its `## Cross-repo impact` section
handed over, verbatim. Answered by
[enterprise#12](https://github.com/Hoplock/enterprise/pull/12).

```
Read docs/CROSS-REPO-PROTOCOL.md and follow it. You are doing a downstream
sync following https://github.com/Hoplock/control/pull/46. Do not implement any queued prompt in this
session.

Before anything else, confirm that upstream change is MERGED; if it is not,
stop and say so (§2). Then update this repository's prompts, plan, and
protocol so the next session builds against what is now true.

A sync changes text, not behaviour: it implements, enforces, and vendors
nothing, hand-edits no vendored artifact, and adds, renames, or renumbers no
prompt. Land each obligation in the prompt that will implement it, not only in
the plan. If the work seems to need something the upstream repository does not
have, that is §3.2 — never approximate it, and do not stop at telling me: name
the exact shape and hand me the "Upstream request" kickoff from
docs/KICKOFF.md already filled in.

The obligations to land are in the upstream PR's "## Cross-repo impact"
section: (1) ext.AccessContextProvider is now Describe() AccessContextInfo
{Name, Probes, Pushes} + Probe(ctx, AccessContextQuery) (AccessEvidence, error)
+ Interpret(ctx, AccessContextPush) (WindowAssertion, error); Name is the
external system (e.g. qualys), unique per server, ^[a-z0-9][a-z0-9-]{0,62}$,
distinct from Registration.Provider; the Qualys and BMC Helix integrations are
written against all three. (2) Probe's three answers: State WindowConfirmed
with a Reference, WindowNotConfirmed (ErrNoEvidence reads the same), or an
error for could-not-determine — KindUnavailable (wrap context.DeadlineExceeded
on a timeout) or KindMalformed; evidence carries TTL and AdditionalContext
(JSON string or object only). (3) Interpret: KindMalformed/KindInvalid refuse
the push, KindDisabled means no pushes, anything else is an outage; scope,
replay, skew and the ceiling are Control's. (4) Probe runs inside a 500 ms
default probe budget; a provider that misses it is abandoned. (5) What an
integration may assert is per-tenant scope binding data (0014 adds its
routes); privileged needs the policy and the binding to agree, and an
unanswered privileged probe denies. (6) Text describing the old single-method
interface, or the declarative provider as future work, is stale.

Work on the branch this session was given, whatever it is named — if the name
is yours to choose, claude/sync-<short-description>. Commit with the `sync`
scope, and open one PR whose body names the upstream PR, confirms it is merged,
and says how you searched for stale references — the actual grep, not "I looked
carefully".
```
