# 0004 — extension points — Learnings

## Summary
- **What shipped:** `ext/` — 11 extension-point interfaces, a write-only
  `Registry` that seals into an immutable `*Extensions`, a typed error kind
  system, and a machine-readable catalogue (`ext.Points()`); `internal/extdefault`
  with Control's single-node `ClusterCoordinator`; the import-graph guard; and
  `ext/README.md` with a compiling worked example.
- **Key packages/files:** `ext/{doc,point,errors,registry,types,audit,access,identity,notify,cluster,policy}.go`,
  `ext/README.md`, `ext/example_test.go`, `internal/extdefault/{cluster,register}.go`,
  `architecture_test.go` (module root).
- **Registration:** `ext.NewRegistry()` → `RegisterX(ext.Registration{Provider,
  Version, Default}, impl) error` → `Seal() (*Extensions, error)`. **Nothing can
  be read out of an unsealed registry** (no implementation accessor exists on
  `Registry`), which is how "a request never sees a half-registered extension"
  is structural rather than a rule. `Registry.Status()` exposes registration
  *metadata* only. Duplicate → `*ext.DuplicateError` naming both sides.
  `Default: true` requires `Provider == ext.ProviderControl` (`"hoplock/control"`).
  A Control default and an extension at one single point is **not** a conflict —
  the extension wins, either registration order, and the listing records it.
- **Reading:** single points → `(T, bool)`; multiple points → `[]ext.Bound[T]`
  (`{Registration, Impl}`), in registration order. `Extensions.Status()` returns
  one row **per point including the empty ones**; `Providers()` is the short form.
- **Errors:** `ext.Errorf(point, provider, op, kind, format, …)` → `*ext.Error`
  wrapping a sentinel. Kinds: `Internal` (**zero value = outage, M11**),
  `Unavailable`, `Invalid`, `NotFound`, `Conflict`, `Disabled`, `Malformed`,
  `Denied`. `KindOf`/`Is*` helpers. **`KindDenied` is the only kind that may
  become a `401`**; an unclassified error from a vendor integration is an outage.
  `ErrNoEvidence` is neither a denial nor a failure.
- **Database tables/migrations added:** none.
- **Decisions made/affected:** **M15 revised in place, not amended** — its
  second invariant now names the three forms an answer takes (see below); the §2
  register row is unchanged (`live`). M11, M16, M21, M13 rendered. PLAN §3 gained
  `internal/extdefault` in the layout and in Component responsibilities.
- **Gotcha:** `KeyStore`, `AuditSink`, `GrantWorkflow`, `Notifier`,
  `ReportProvider` and `PolicyValidator` are **`WhenAbsentCore`, not defaults
  registered here** — Control's behaviour without them is its own code path in a
  later phase, so do not go looking for an `extdefault` implementation of them.
- **NEXT session:** `ext` is a compatibility promise. Adding an interface without
  a `PointInfo` row fails the build, and so does one whose doc comment omits the
  literal sentence `When no implementation is registered`. `internal/extdefault`
  is where a `WhenAbsentDefault` implementation goes; nothing else belongs there.

### Every interface, its signature, its default, and its absence

Hoplock Enterprise's phases are written against this list. **If a signature
below changes in a later phase, say so loudly in that phase's learnings and
emit a downstream sync** — Enterprise builds against these exact declarations.

Supporting types referenced: `Tenant string`, `Severity`, `Window{NotBefore,
NotAfter time.Time}`, `Subject{ID, ExternalID, Username string, Groups []string}`,
`Target{ID, Hostname, Zone string, Labels map[string]string}`.

| Interface | Signature | Control's answer | When nothing is registered |
| --- | --- | --- | --- |
| `AuditSink` (multi) | `Export(ctx, batch []AuditRecord) error` | local audit store, hash chain, verifier, query (0010) | **core** — records stay in the local store and are exported nowhere |
| `ArchiveStore` | `Archive(ctx, batch []AuditRecord) error`; `Search(ctx, ArchiveQuery) (ArchivePage, error)` | nothing | **disabled** — retention deletes when the window closes; no long-term copy |
| `GrantWorkflow` | `Submit(ctx, GrantRequest) (GrantDecision, error)`; `Status(ctx, Tenant, workflowRef string) (GrantDecision, error)`; `Cancel(ctx, Tenant, workflowRef, reasonText string) error` | manual time-boxed grants, expiry, revocation (0012) | **core** — an authorised administrator creates a grant directly |
| `IdentitySync` | `Start(ctx, sink SyncSink) error`; `Stop(ctx) error` | nothing | **disabled** — identities arrive at login; nothing is provisioned ahead of time |
| `KeyStore` | `Generate(ctx, KeyRef, KeyAlgorithm) (KeyInfo, error)`; `Describe(ctx, KeyRef) (KeyInfo, error)`; `Signer(ctx, KeyRef) (crypto.Signer, error)`; `List(ctx, Tenant) ([]KeyInfo, error)`; `Destroy(ctx, KeyRef) error` | software custody + tenant SSH CA (0011) | **core** — Control holds its own software keys and signs with them |
| `Notifier` (multi) | `Notify(ctx, Notification) error` | the outbound webhook notifier (0012) | **core** — notifications go to the configured webhook and nowhere else |
| `ClusterCoordinator` | `Node(ctx) (Node, error)`; `Members(ctx) ([]Node, error)`; `Lead(ctx, singleton string) (Leadership, error)`; `Publish(ctx, ClusterEvent) error`; `Subscribe(ctx, topic string) (<-chan ClusterEvent, func(), error)` | `extdefault.SingleNode` (0004) | **default** — never empty: one member, every singleton held, in-process bus |
| `ActionHandler` | `Start(ctx, exec ActionExecutor) error`; `Stop(ctx) error` | nothing | **disabled** — no external system can kill a session or lock a subject out |
| `ReportProvider` (multi) | `Reports(ctx, Tenant) ([]ReportDescriptor, error)`; `Generate(ctx, ReportRequest) (Report, error)` | audit query, explain, simulation (0010, 0014) | **core** — the north-bound queries are the reporting available |
| `PolicyValidator` (multi) | `Validate(ctx, PolicyDocument) ([]Finding, error)` | the compiler's exhaustive authoring-time checks (0005) | **core** — the compiler's own checks are the whole of validation, and always run |
| `AccessContextProvider` (multi) | `Probe(ctx, AccessContextQuery) (AccessEvidence, error)` | the declarative HTTP provider (0013, M16) | **disabled** — no external access context is consulted |

Supporting interfaces, which are **not** points: `Leadership` (`Held() bool`;
`Changes() <-chan bool`; `Release(ctx) error`), `SyncSink` and `ActionExecutor`
— the last two are **Control's** side, implemented here and passed *to* the
extension at `Start`.

## Details

### The three-way disposition, and why it is not a dodge

The prompt's table read "Control's default" for every point, and taken
literally that would have meant building, in this phase, a webhook notifier, a
disk key store, a manual grant path, an audit exporter and a reporting layer —
each of which is a later phase's job and each of which would have been written
before the domain it serves exists. The alternative temptation was worse: ship
the seams with nothing behind them and call the gap "Enterprise adds this".

What the code does instead is make the distinction explicit and testable.
`ext.PointInfo.WhenAbsent` is one of:

- **`WhenAbsentCore`** — the capability is Control's own code path and the seam
  is additive. `ControlShips` names what supplies it and the phase that builds
  it, and `architecture_test.go` checks that phase exists in PLAN §10.
- **`WhenAbsentDefault`** — everything above the seam goes through it, so
  Control must put an implementation behind it. `internal/extdefault.Register`
  supplies it, and `Registry.Seal` **refuses to start** a server where one was
  promised and is missing.
- **`WhenAbsentDisabled`** — the capability is an addition Control never
  claimed.

The invariant that makes this honest rather than a taxonomy: a point whose
`ControlShips` is empty **must** be `WhenAbsentDisabled`, asserted in
`ext/seam_test.go`. That is M15's second invariant as a build failure. Core
functionality cannot be moved out to make room for a licence, because doing so
means either naming a phase that will bring it back or admitting in the
catalogue that Control never had it.

PLAN M15's second invariant was **revised in place** to say this. It is not an
amendment: what M15 settles — Enterprise extends, never forks — is unchanged,
and the register row stays `live`. The old wording ("every extension point
ships a real default here") described the same intent less precisely than the
code now enforces it.

### Why the registry is two types rather than one

"Registration happens before start and is immutable afterwards" is the kind of
rule that a comment cannot keep. `Registry` has no accessor for an
implementation at all — only `RegisterX` and `Status`, which returns
registration metadata. Reading requires `Seal`, which hands back a different
type. So "a request sees a half-registered extension" is not a bug that can be
introduced by a careless caller; there is no expression that would do it.

`Registry.Status()` exists so a host binary can log its own wiring before
handing the registry over, and because the worked example in `README.md` needs
something to print without standing up Control's defaults.

### Duplicate registration, and the one case that is not a duplicate

Two extensions at a single point, or two Control defaults at one point, or one
provider registering twice at a multiple-implementation point: all
`*ext.DuplicateError`, and its message names both sides.

A Control default plus one extension at a single point is deliberately *not* an
error — that is the override, and it works in either registration order, so a
host binary need not care whether it wires its own implementations before or
after Control adds its defaults. The listing records which one is in play, and
`Registration.Default` is refused to anyone but `hoplock/control`, so the
listing cannot be made to lie about provenance.

### `AccessContextProvider` returns evidence, not a verdict

Written up here because it is the mistake the interface exists to prevent and
the doc comment alone will not stop it. A provider that returns a boolean has
taken the policy decision out of the bundle: the rule stops being visible, stops
being simulated, and stops being explainable, and the decision record inherits
somebody else's yes with no way to say why. So `AccessEvidence` carries a
`Reference`, an `Assertions` map, a `Window` and an `ObservedAt`, and nothing
that resembles an answer.

Three error shapes matter to 0013 and are defined here: `KindUnavailable` (the
external system could not be reached), `KindMalformed` (it answered and the
answer made no sense) — 0013's acceptance criteria require a decision record to
tell those apart — and `ErrNoEvidence`, which is neither a denial nor a failure.

### The import guard is proven, not asserted

`TestNoPackageImportsHoplockEnterprise` walks every `.go` file in the repository,
tests included, with `go/parser` in `ImportsOnly` mode. Source parsing rather
than `go list` on purpose: an import of a module that is not in `go.mod` breaks
`go list` in a confusing way, and the guard has to give a clear message in
exactly the case it exists for.

`TestTheImportGuardActuallyCatchesOne` points the same scanner at a throwaway
tree containing an offending import, which is how the prompt's "prove it by
temporarily adding one" is satisfied without breaking the build. It also checks
that a module path merely *starting* with the same letters is not a false
positive.

`TestExtImportsNothingInternal` is the other half: `ext` must stay cheap to
import.

### The single-node coordinator is real, not a stub

`extdefault.SingleNode` answers honestly: there genuinely is one member, it
genuinely holds every singleton, and `Publish`/`Subscribe` genuinely reach every
subscriber in the deployment, because the deployment is this process. Two
consequences worth knowing:

- **Asking twice for a held singleton is a conflict, not a second holder.** Two
  callers believing they hold the same singleton is the exact bug the singleton
  exists to prevent, and one node is not an excuse.
- **Leadership ends when the context passed to `Lead` is cancelled**, and
  `Changes()` closes. 0015's "losing leadership drops the registration, gaining
  it re-registers" is therefore testable with no cluster — cancel the context,
  campaign again.
- The bus drops a subscriber that has fallen more than 64 events behind rather
  than blocking the publisher. The control plane's durable record is the
  database; a stuck console stream is not a reason for policy activation to hang.

### What a later phase has to do when it fills a seam

For a `WhenAbsentCore` point, nothing changes in `ext`: build the core path and
read the seam through `Extensions` where it exists. For a point that needs a
registered default, add it to `internal/extdefault.Register` and flip the
catalogue row to `WhenAbsentDefault` — `Seal` then enforces it.

0014 has a new obligation, landed in its prompt in this PR: expose the sealed
registry read-only on the north-bound API, one entry per point *including the
empty ones*. `Extensions.Status()` already produces exactly that shape.

### Deviations

- **`internal/extdefault` is a new package** not in PLAN §3's layout. Added to
  the layout and to Component responsibilities in this PR, with the reason it is
  not inside `ext`.
- **`make lint` needs the pinned linter, not whatever is on `PATH`.** The
  binary preinstalled in this environment is v2.5.0, built with Go 1.25, and it
  refuses a module targeting 1.27.0 outright — exactly the coupling 0001's
  learnings warn about, seen from the other side.
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`
  (the version CI pins) then reports **0 issues**. `go build`, `go vet`,
  `make test` (race), `make license-check` and `make contract-check` all pass.
  Two findings the pinned linter would have caught and that are worth knowing
  before writing code here: with `default-signifies-exhaustive: false`, a
  `switch` over a `Point` with a `default` is still non-exhaustive, and so is a
  `map[Point]bool` used as a set — `check:` includes `map`. Use a slice.
- **`make conform` was not run**: this server still serves no endpoint the suite
  covers (PROTOCOL §4 makes that conditional).
- The summary block above is longer than PROTOCOL §5's ~14-line aim. That is
  deliberate and this prompt asked for it by name: the signature table is what
  Hoplock Enterprise's phases are written from, and moving it into Details would
  mean every Enterprise session opens the file anyway.

### Follow-ups, deliberately not done here

- No new queued prompts. Everything discovered belongs to phases that already
  exist (0005, 0010, 0011, 0012, 0013, 0014, 0015).
- `ext` names no server entry point yet. Enterprise's `main` will eventually
  need a public way to hand a populated `*ext.Registry` to Control and start it;
  today `internal/extdefault.Register` + `Registry.Seal` is wired in
  `cmd/hoplock-control`, and nothing outside the module can start a server
  because there is nothing to start. Whichever phase stands the listeners up
  owes that entry point, and it is a cross-repo obligation when it lands.
