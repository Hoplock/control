# 0005 — Policy model & decision engine

## Read first
- `docs/PROTOCOL.md` — session workflow.
- `docs/PLAN.md` — especially **§2 (M3, M4, M5)** and **§5 (inputs, outputs,
  evaluation, cache hints)**.
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
entry proxy; target hostname, labels, and zone; and live grants.

**What a rule emits** (PLAN §5.2) — the snapshot, whose vocabulary is the
proxy's enforcement surface:
- route intent (which target, and whether the path may traverse hops);
- permitted **channel types**;
- permitted **in-channel requests**, subsystems named individually;
- permitted **forwarding destinations** (host/CIDR + port);
- permitted **global requests**;
- **filter policy**: an ordered rule list **or** a restricted-exec allow-list,
  never both (proxy D12);
- **target credential method** + parameters (proxy D6a);
- **cache hint** (§5.4) — authored per rule, not global;
- **obligations**: record, require approval, require step-up.

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
- a cache hint with no key, or a key that could be shared across identities.

Every rejection names the rule, the line, and what to do instead. This error
text is a product surface: it is what a policy author sees.

### The evaluator (`internal/policy/eval`)
- Ordered rules, **first match wins**, with an explicit **default-deny** that is
  always present and always recorded as the reason when it fires.
- Time is an **input**, never `time.Now()` inside the engine — otherwise
  simulation over historical traffic (0013) is impossible and tests are flaky.
- Produces `(snapshot, explanation)` where the explanation names the matched
  rule, the input values that made it match, the obligations emitted, and — for
  a deny — which rule denied or that nothing matched. This is M4, and it is
  the feature 0013 exposes and the proxy's disclosure rule depends on.
- **Bounded**: evaluation is linear in rule count with no unbounded constructs.
  Add a benchmark and state the budget it must fit in (M5).

## Out of scope
- Storing bundles or decisions (0003 has the tables; 0008 writes decisions).
- HTTP, IdP, grants' lifecycle (0012 — but the engine reads a grant as an input
  now, so define that input type here and make it complete).
- Simulation and the authoring API (0013) — but keep evaluation pure so both are
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
- A benchmark shows evaluation within the stated budget for a realistically
  large bundle (state the size you chose and why).

## Definition of Done & hand-off
Per `docs/PROTOCOL.md`. Move to `implemented/`; add
`docs/learnings/0005-policy-model-and-engine-learnings.md`. Summary block MUST
give the bundle's top-level schema, every input and output field name, the
compiler's rejection list, the evaluator's signature, the explanation type, and
the measured evaluation budget. Phases 0008, 0013 and 0012 all build directly on
this and will read nothing else about it.
