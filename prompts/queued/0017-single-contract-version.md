# 0017 — One contract version, end to end

## Read first
- `docs/PROTOCOL.md` — session workflow, especially §3 ("never edit
  `contract/`") and §9.
- `docs/PLAN.md` — especially **§4** (the vocabulary-negotiation obligation and
  the contract v3.1 case beneath it), **M1** (the contract is vendored,
  read-only), **M11** (`401` is a decision; everything else is `5xx`), and
  **M17** (declared capabilities).
- `docs/learnings/` — read summaries; open `0002` (the vendored contract, the
  generated constants, and the conformance suite's expectation format), `0006`
  (the enrolled contract version and the pre-publish capability query), and
  `0008` (how the authorize response is assembled).
- `contract/control.yaml` — `info.version` and the `policy_version` field only.
  You do not need the rest of the document for this phase.

## Objective
Make Hoplock Control implement **exactly one contract version** — the vendored
one — and say so in exactly one place. Remove the machinery that exists to serve
several.

This is an audit and a narrowing, and it runs last on purpose: everything that
could hold a version number has been built by now, so this phase can see all of
it at once. Earlier phases should already prefer a single constant, and if they
did their job this phase finds little; a phase that finds nothing is a pass, and
should say so rather than inventing work.

### Why

The contract's negotiation rules exist for a fleet **mid-upgrade across contract
revisions**: proxies of several builds, a server that must answer each within the
vocabulary it can read, and a hard rule that dropping a restriction is a breach
rather than a degradation. That is a real problem, and it is not yet this
product's problem. Hoplock Control has no installed base: the first proxy and the
first server ship together, and every deployment that exists moves them as a
pair.

Version-thinning machinery carried before it is needed is the worst kind of
code — it is never exercised, so it is wrong exactly when it is finally used, and
it makes every field in the snapshot owe a "since" tag that nothing tests.
Carrying one supported version instead makes the failure mode loud, testable and
present from the first day.

The obligations in PLAN §4 do not go away here. They are the reason the refusal
this phase installs is **loud** rather than lenient.

## In scope

### One number, in one place

- `internal/contract` exposes the single supported `policy_version` and the
  vendored document version (`info.version`), **derived from
  `contract/control.yaml`** rather than typed in beside it. A test asserts each
  matches the document, so a `make contract-sync` that bumps either one fails
  loudly and a human decides what to do — which is the point at which supporting
  a second version would become a deliberate decision rather than an accident.
- Note that these are two different numbers and both are single-valued here: the
  document version (`3.1.0` as vendored) and the negotiated vocabulary
  (`policy_version`, `3`) move independently upstream, and this phase does not
  couple them.
- No other literal version anywhere in the tree — code, fixtures, deployment
  manifests, or seed data. Add a check that keeps it that way and name it in your
  learnings.

### One answer for anything else: a loud refusal

- An authorize request declaring **any other** `policy_version` — lower or
  higher — is refused with `5xx` naming the mismatch: the version the caller
  declared, the version this server serves, and the `proxy_id`. An operator
  reading that must conclude "this proxy is the wrong build", not "the server is
  broken".
- Never `401`. A deny is a decision (M11), and a version mismatch is not a
  decision about access — answering `401` sends an operator to debug permissions
  during what is actually a rollout error.
- Never a thinned `200`. This is the failure the whole obligation exists to
  prevent: dropping a *permission* is merely wrong, dropping a *restriction* is a
  silently widened session.
- The version-aware half of 0008's assembly goes: one assembly mode, no per-field
  "introduced in" table, no downgrade path. If 0008 built one, remove it and say
  so; if it did not, say that instead.

### The conformance suite keeps its teeth, and gains one (0002)

The suite's negotiation assertion is **not** deleted. It is the only assertion
that catches a silently thinned answer, and that failure is no less severe under
a single-version rule. What changes is what it expects: the low-version request
must produce exactly the refusal above, deterministically — a thinned `200` is
still a failure, a `401` is still wrong. Add the mirror case: a request declaring
a version **higher** than this server's is also refused, not answered
best-effort.

**Where these assertions live matters, and getting it wrong will break CI.**
0002 runs the same suite against Hoplock Proxy's `cmd/mock-control`, which
implements the contract's multi-version behaviour correctly and would fail a
universal "always refuse" assertion. The single-version expectation is a fact
about *this server*, not about the contract, so it belongs in this server's
expectation file. The shared, contract-level assertion stays what it always was:
a thinned answer that drops a restriction is a failure, and a refusal naming the
mismatch is a pass. Keep the two layers distinct and say in your learnings which
assertions sit in which — a later session moving one up a layer is how the mock
starts failing for no reason anyone can see.

### The fleet view names the wrong proxy before a user does (0006)

The enrolled contract version is already stored as operational data. With one
supported version, an enrolled proxy declaring a different one is a
fleet-readiness failure: surface it in the fleet view and in the pre-publish
"which proxies can satisfy this policy" query, so it is found by an operator
looking at a screen rather than by a user whose session became an outage. Never
route around it silently — silence here is indistinguishable from a healthy
fleet.

### The documents stop describing a fleet that does not exist

PLAN §4's negotiation obligation, the contract-v3.1 passage beneath it, 0002's
note on contract versions and 0006's note on the enrolled version are all written
for a multi-version fleet. Rewrite them to state the single-version position and
its deployment consequence: **proxy and server versions move together**, and a
mid-upgrade fleet is a rollout-ordering problem rather than a code path.

Keep the *reasoning* in place while you do it. "A dropped restriction is a
breach" is why the refusal is loud, and a future session that wants to add
version support back needs the argument, not just the conclusion.

## What this phase may not do

- **It may not change the contract.** `policy_version` stays on the wire, is
  still read on every authorize request, and this server still never answers with
  vocabulary outside what the caller declared. This narrows what Control
  *supports* to one value; it does not remove the field, and it does not edit
  `contract/` (M1, PROTOCOL §3).
- **The refusal is conformant, and stricter — never looser.** The contract itself
  sanctions `5xx` naming the mismatch when a server cannot express its policy
  within the declared version (upstream `api/README.md`, "Versioning: additive
  fields, and a proxy that fails closed"). This phase makes that the only answer.
  If you find a case where the single-version rule would make Control answer
  something the contract forbids, stop — that is a finding for the user, not
  something to reconcile locally.
- **It may not narrow the `device_field.` namespace.** One supported contract
  version does not mean one closed set of device fields. That namespace is
  deliberately open and is **not** a version axis: contract v3.1 added it without
  moving `policy_version`, the names a driver accepts are a capability question
  (M17, 0006), and a rung naming a field the driver does not declare is a skipped
  rung rather than an error. Collapsing "one version" into "one known set of
  fields" would break every customer-written driver (proxy D13) and is the most
  likely way to get this phase wrong.
- **It may not delete the negotiation tests**, only re-aim them (above).

## Out of scope
- Vendoring a new contract version. That is a downstream sync, not a phase
  (`docs/CROSS-REPO-PROTOCOL.md` §3.1) — and note that when upstream does bump
  the version, this phase's single constant plus its failing test is exactly what
  makes that sync visible instead of silent.
- Any change to how a request declaring the **supported** version is answered.
- Changing anything upstream. If the audit concludes the contract needs a change,
  that is `docs/CROSS-REPO-PROTOCOL.md` §3.2: stop and tell the user.

## Acceptance criteria
- Exactly one place in the tree defines the supported `policy_version`, and one
  the document version; a test ties each to `contract/control.yaml` and fails
  when they diverge. Prove the failure, don't assume it.
- A repository-wide check finds no second version literal, and is wired so it
  keeps holding.
- Authorize declaring a **lower** version, and authorize declaring a **higher**
  version, both return `5xx` naming both numbers and the `proxy_id`; authorize
  declaring the supported version is unchanged. All three tested.
- No thinning path remains in the assembly, and a test or a stated review note
  says how you know.
- `make conform` passes against this server with the single-version expectations,
  **and** still passes against Hoplock Proxy's `cmd/mock-control` with the
  contract-level ones. Both, in CI. If the mock fails, the expectation was put in
  the wrong layer.
- An enrolled proxy declaring a different contract version is visible as a
  fleet-readiness failure in the fleet view and in the pre-publish query.
- `docs/PLAN.md` §4 and the affected prompts read as a single-version product,
  with the reasoning preserved.

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0017-single-contract-version-learnings.md`. The summary block
MUST give: where the supported version and the document version are defined and
how they are tied to the vendored file, the exact refusal behaviour and status
code, which conformance assertions are contract-level and which are this
server's, and what the next contract sync must do when upstream bumps a
version — because that sync will be the first thing this phase's test stops.
