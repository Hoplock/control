# 0005 — Policy model & decision engine

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M3, M4, M5)** and **§5 (inputs, outputs,
  evaluation, cache hints)**. Read **M13's second and third paragraphs** too:
  they say why this package is represented the way the next section requires.
- `docs/learnings/` — read summaries; open `0002` (the contract's output shapes
  — the snapshot this engine produces must be expressible in them) and `0003`
  (the bundle table).
- In the **Hoplock Proxy repository**, `docs/PLAN.md`: **D5a** (the three policy axes),
  **D6a** (credential methods), **D11** (hop direction), **D12** (the three
  filtering tiers). This engine's output vocabulary is exactly what the proxy
  can enforce, and those four decisions define it.

## Objective
Build the heart of the product: a policy bundle that can be authored, validated,
compiled and evaluated, producing a whole-connection snapshot **and an
explanation**. Pure Go — no HTTP, no database, no ambient clock. This is the
package that must be exhaustively correct, because every promise the product
makes is kept or broken here.

## In scope

### The bundle (`internal/policy/model`)
A declarative document (YAML) with a closed vocabulary (M3). Parse, validate,
and version it.

**Inputs a rule may match on** (PLAN §5.1): subject id, IdP source, groups,
claims, authentication method and whether MFA was used; device posture
attributes (optional, may be absent); time and day; source network/CIDR and
the proxy asking (`conn.proxy_id`, which on a chained session is an inner hop
rather than the user's entry proxy); target hostname, labels, and zone; and live
grants. `conn.hop_trail` is **not** on this list and must not be added to it:
PLAN §5.3 says why — it is a routing input that may only ever narrow an answer.

**What a rule emits** (PLAN §5.2) — the snapshot, whose vocabulary is the
proxy's enforcement surface:
- route intent (which target, and whether the path may traverse hops);
- permitted **channel types**;
- permitted **in-channel requests**, subsystems named individually;
- permitted **forwarding destinations** (host/CIDR + port);
- permitted **global requests**;
- **filter policy**: an ordered rule list **or** a restricted-exec allow-list,
  never both (proxy D12);
- **target credential method** + parameters (proxy D6a), and on an
  `ephemeral-account` method the open `device_field.<name>` namespace contract
  v3.1 adds beside the five named parameters (PLAN §5.2). Model it as what it is:
  an **open map of string to string**, opaque to this engine. Do not enumerate
  the names in the closed vocabulary and do not give `vdom` a field of its own —
  the contract refuses to enumerate them because customer-written drivers are
  first-class (proxy D13), and a closed list here would make an estate's own
  driver unauthorable without a release of this server;
- **enforcement rung** per axis (`execution` and `reach`, contract v4) — where
  the route's claim is actually enforced. Two independent axes, each with a small
  **closed** enum, and both are properly closed here: unlike `device_field`, the
  contract enumerates every rung, so model them as enums and let an unknown value
  be a compile error. The rung is a property of the **route**, never of a ladder
  entry, so it has one slot in the snapshot and not one per entry. The engine
  must never synthesise a weaker rung as a fallback — a silent downgrade is what
  the vocabulary exists to prevent — and the absent value on both axes is
  proxy-side enforcement only, which is exactly what a rule that says nothing
  about enforcement should emit;
- **session bounds** (contract v4): a `session_deadline` as an **absolute
  instant** — the engine already takes time as an input, so an instant is
  computable and total, and a duration would re-anchor on each hop of a chain;
  `require_session_capture`; `concurrency` caps per subject and/or target; and a
  `grant_context` carried from the grant that supplied it (M10, M16), whose
  `additional_context` is a string **or** an object and is never read for a
  decision here or downstream;
- **cache hint** (§5.4) — authored per rule, not global;
- **obligations**: record, require approval, require step-up.

### How the vocabulary is represented (read before writing any of it)

M3's closed vocabulary is a promise the Go type system cannot hold on its own,
and PLAN M13 says so plainly: no sum types, no exhaustive matching, so adding an
obligation kind or an output axis will not fail any build that forgot to handle
it. This package is where that gap does the most damage, so the representation
is a requirement, not a preference:

- **Every variant axis is a named `Kind` enum** — a defined type over `int` or
  `string` with its members declared as constants in one block, next to the type.
  Channel type, request type, credential method, obligation kind, enforcement
  rung, decision outcome, filter mode: each gets one. **Not** an open interface,
  and **not** a bare `string` passed around, wherever the set is closed. The
  `exhaustive` linter is enabled in `.golangci.yml` precisely so that a switch or
  a lookup map over one of these fails the build when a member is unhandled, and
  it only sees named enums — an open interface is invisible to it.
- **`default` does not excuse a missing case.** The linter is configured that
  way deliberately (defaulting is how a new case silently inherits old
  behaviour, which here means a policy output nobody authored). Where a switch
  is genuinely open-ended, mark it `//exhaustive:ignore` **with a reason on the
  line above**; a bare ignore is a review comment.
- **Prove the guard works.** Add a test or a documented check that a switch
  missing a member is actually rejected — a linter that is enabled but silent is
  worse than none, and this one is load-bearing for M3.
- **Keep the package pure** (no HTTP, no database, no `time.Now()`), so that
  M3's "the compiler is a boundary" stays a real escape hatch: if the evaluator
  ever has to stop being Go, that boundary is where it is replaced.

### The compiler (`internal/policy/compile`)
Bundle → decision program. Compilation is where authoring mistakes become
errors instead of production surprises. It MUST reject:
- unreachable rules (fully shadowed by an earlier rule);
- contradictory obligations within one rule;
- references to labels, groups, or credential methods that do not exist;
- a rule permitting `direct-tcpip` **without** a destination list, or a
  `subsystem` permission without naming subsystems. An unconstrained axis is a
  mistake, not a wildcard — the compiler says so rather than quietly opening the
  estate;
- a filter policy setting both a rule list and restricted exec;
- a cache hint with no key, or a key that could be shared across identities;
- a `device_field.<name>` that is the wrong **shape** — a name outside lowercase
  letters, digits, hyphens and underscores or longer than 64 characters, an empty
  value or one longer than 256 characters, or more than 16 fields on one ladder
  entry. The contract validates exactly this and nothing more, and so does the
  compiler: shape is checkable here, meaning is the driver's. Reject the shape at
  authoring time rather than letting a route that can never be served reach a
  proxy — and do not reject an unrecognised *name*, which is a capability
  question the compiler cannot answer (M17, 0006) and, on the proxy, a skipped
  rung rather than an error;
- a device field on a rung whose method is not `ephemeral-account`. The namespace
  is scoped to that method; anywhere else it is a typo that would be silently
  carried;
- an **enforcement rung that contradicts the rest of the rule** (contract v4).
  These are internal-consistency checks the compiler can make with no knowledge of
  the fleet, and every one of them is a route that could otherwise only fail at
  connect time, in front of a user: `no-interactive-shell` beside a
  `permitted_requests` that still allows `shell` or `pty-req`;
  `account-restricted` or `account-confined` without
  `filter_policy.exec_mode: restricted`; `platform-authorized` without a
  `platform_role`; `account-egress-restricted` without a non-empty
  `permitted_destinations`; an `attestation` on a rung that is not
  `platform-attested`, or a `platform-attested` rung without one carrying both
  `asserted_by` and `reference`;
- an **applied** enforcement rung on a route whose every credential-ladder entry
  is `brokered-key` or `static-key`. An applied rung needs the proxy to administer
  the account, which only `ephemeral-user` and `ephemeral-account` do, so such a
  response is one the proxy refuses outright. An **attested** rung on that same
  route is valid and must not be rejected — it is how an appliance carries a real
  enforcement claim, and a compiler that refuses it makes the appliance estate
  unauthorable.

  What the compiler must **not** try to decide is whether a given proxy or target
  can *provide* a rung it has correctly authored. That is a capability question
  answered from the fleet registry (M17, 0006), on the issue path in 0008 and at
  publish time in 0014 — the same split as an unrecognised device-field name.

Device fields are **policy metadata, never credential material** — nothing here
may treat one as a secret to broker, and nothing may read one back out as an
authorisation input.

Every rejection names the rule, the line, and what to do instead. This error
text is a product surface: it is what a policy author sees.

### The evaluator (`internal/policy/eval`)
- Ordered rules, **first match wins**, with an explicit **default-deny** that is
  always present and always recorded as the reason when it fires.
- Time is an **input**, never `time.Now()` inside the engine — otherwise
  simulation over historical traffic (0014) is impossible and tests are flaky.
- Produces `(snapshot, explanation)` where the explanation names the matched
  rule, the input values that made it match, the obligations emitted, and — for
  a deny — which rule denied or that nothing matched. This is M4, and it is
  the feature 0014 exposes and the proxy's disclosure rule depends on.
- **Bounded**: evaluation is linear in rule count with no unbounded constructs.
  Add a benchmark and state the budget it must fit in (M5).

## Out of scope
- Storing bundles or decisions (0003 has the tables; 0008 writes decisions).
- HTTP, IdP, grants' lifecycle (0012 — but the engine reads a grant as an input
  now, so define that input type here and make it complete).
- Simulation and the authoring API (0014) — but keep evaluation pure so both are
  possible without touching this package.

## Acceptance criteria
- Table-driven tests over every input axis and every output axis, including the
  interaction that makes this product distinct: a policy expressing **"may open
  a shell on `env=prod`, may not run `sftp`, may tunnel only to
  `postgres.prod:5432`, may never open a listener, and may only run the argv
  shapes on this list"** compiles and evaluates to exactly that snapshot.
- Every compiler rejection above has a test asserting the error names the rule
  and is actionable.
- First-match-wins is tested with overlapping rules, and default-deny is tested
  with an input nothing matches.
- The explanation is tested as a **first-class output**: for both an allow and a
  deny, assert it names the matched rule and the deciding inputs. A wrong
  explanation is a product bug, not a logging bug.
- Determinism: the same inputs produce byte-identical snapshots and
  explanations. Property-test this if practical.
- Every closed axis is a named `Kind` enum, `golangci-lint run` passes with
  `exhaustive` enabled, and a switch with an unhandled member is demonstrably
  rejected. Any `//exhaustive:ignore` carries a reason.
- A benchmark shows evaluation within the stated budget for a realistically
  large bundle (state the size you chose and why).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0005-policy-model-and-engine-learnings.md`. Summary block MUST
give the bundle's top-level schema, every input and output field name, the
`Kind` enums and their members, the compiler's rejection list, the evaluator's
signature, the explanation type, and the measured evaluation budget. Phases 0008, 0014 and 0012 all build directly on
this and will read nothing else about it.
