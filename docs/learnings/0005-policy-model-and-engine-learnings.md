# 0005 — policy model & decision engine — Learnings

## Summary
- **What shipped:** `internal/policy` — the pure engine. `model` (bundle, closed
  vocabulary, structured rejections), `compile` (bundle → immutable `*Program`,
  65 rejection codes, unreachability), `eval` (`(*Snapshot, Explanation)`, first
  match wins, always-present default-deny). No HTTP, no database, no clock.
- **Key files:** `internal/policy/model/{kind,bundle,parse,output,input,explain,devicefield,reject}.go`,
  `internal/policy/compile/{compile,match,shadow,program}.go`,
  `internal/policy/eval/eval.go`, `scripts/exhaustive-guard.sh`.
- **Bundle schema (top level):** `schema_version` (must be `1`), `tenant`,
  `description`, `timezone` (IANA, default UTC), `labels` (key → permitted
  values), `groups`, `rules`. Rule: `id`, `description`, `match`, `effect`
  (`allow`/`deny`), `reason` (deny), `route` (allow), `obligations`, `cache`.
- **Match axes** (all ANDed; nil = unconstrained; a list is ORed):
  `subject{ids,sources,groups,claims,auth_methods,mfa}`,
  `device{required,posture}`, `context{days,time_of_day,source_cidrs,proxy_ids}`,
  `target{hostnames,labels,zones}`, `grant{required,scopes,origins}`.
- **Input type:** `model.Input{Now, Subject{ID,Source,Groups,Claims,AuthMethod,MFA},
  Device *{Posture}, Context{SourceAddr,ProxyID}, Target{Hostname,Port,Zone,Labels},
  Grants []Grant}`. `Grant{ID,Subject,Origin,Scope{Name,Targets,Labels,Zones},
  NotBefore,ExpiresAt,Approvers,RequestRef,External *{System,Reference,
  WindowStart,WindowEnd,AdditionalContext}}`. **No tenant and no hop trail** —
  see Details.
- **Route (authored) / Snapshot (produced):** `Intent, Target, Port, Channels,
  Requests{Types,Subsystems}, Forwards{DirectTCPIP,ForwardedTCPIP},
  GlobalRequests{Types}, Filter{Mode,Rules,ExecMode,RestrictedExec{Commands}},
  Credentials[], AlgorithmProfile, Enforcement{Execution,Reach,PlatformRole,
  PermittedDestinations,Attestation}, Concurrency`, plus `Obligations` and
  `Cache` from the rule. The route authors `MaxSessionDuration` and
  `RequireSessionCapture *bool`; the snapshot carries `SessionDeadline` (an
  absolute instant), `RequireSessionCapture bool`, `GrantContext` and `Rule`.
- **Ladder entry:** `CredentialEntry{Method, Username, KeyType, CredentialRef,
  Platform, CredentialKind, ExpiryPosture, LifetimeSeconds, DeviceFields}`.
  `Username{Source,Value}` — sources `literal|subject|subject-local-part`; the
  identity's `login` is **not expressible**, by design.
- **Kind enums (23):** `Effect{allow,deny}`, `Basis{rule,default-deny}`,
  `MatchAxis{subject,device,context,target,grant}`,
  `RouteIntent{direct,hops-permitted}`,
  `ChannelType{session,direct-tcpip,forwarded-tcpip,x11,auth-agent@openssh.com}`,
  `RequestType` (6, contract's), `CredentialMethod` (4), `UsernameSource` (3 +
  unset), `CredentialKind`, `ExpiryPosture`, `AlgorithmProfile`, `FilterMode`,
  `ExecMode`, `FilterAction`, `CommandForm`, `ArgumentKind`, `ExecutionRung`
  (6 + unset), `ReachRung` (4 + unset),
  `ObligationKind{record-session,require-approval,require-step-up}`,
  `GrantOrigin{administrator,workflow,external}`,
  `CacheKeyComponent{subject,target,target-port,proxy,auth-method,rule}`,
  `AuthMethod`, `Day`. `GlobalRequestType` is deliberately **open** (Details).
- **Rejections:** `model.Rejection{Code,Rule,Line,Params}` + `Message()`,
  `Rejections` (sorted, `Has`, `Codes`). Codes are grouped `policy.*` (7),
  `rule.*` (7), `match.*` (6), `route.*` (14), `cache.*` (5), `credential.*` (12),
  `enforcement.*` (9), `obligation.*` (4) — 65 in total, all listed in
  `reject.go`'s `messages` map, which `exhaustive` forces to be complete.
- **Signatures:** `model.Parse([]byte) (*Bundle, error)`,
  `(*Bundle).Validate() Rejections`, `compile.Compile(*model.Bundle) (*Program, error)`,
  `compile.NewSet(...*Program) (*Set, error)` / `(*Set).Program(Tenant)`,
  `eval.Evaluate(*compile.Program, model.Input) (*model.Snapshot, eval.Explanation)`.
  A nil snapshot is a denial. `Explanation{Effect,Basis,Rule,RuleLine,Terms,
  Obligations,DenyReason,Grant,BundleDigest,EvaluatedAt,RulesConsidered}`.
- **Measured budget (M5):** 2,000-rule bundle, ~27µs for the worst case
  (default-deny, every rule scanned, 1 alloc) and ~34µs when the last rule
  matches and a snapshot is built. Ceiling asserted in CI: **300µs**, plus a
  linearity check. `internal/policy/eval/bench_test.go` states the reasoning.
- **Migrations added:** none. `policy_bundles` (0003) already holds the source.
- **Decisions:** none added, amended or withdrawn — the §2 register is
  unchanged. M3, M4, M5, M13, M18, M21 rendered. **PLAN §5.1 revised in place**:
  the "Session" input row is gone, because those axes are outputs (Details).
  §5.2 gained one clause on how the deadline is resolved. **Cross-repo:** none
  owed; `ext/` untouched, `contract/` untouched.
- **NEXT session (0008, 0012, 0014):** evaluation is total and pure — give it
  `Input.Now` and a program you looked up by tenant. The snapshot is already
  deep-copied, so you may hold it. What this engine does **not** do is check
  whether a proxy or target can *provide* what a rule authored: that is M17's
  capability question and it is yours.

## Details

### Why the summary block is long

`docs/PROTOCOL.md` §5 caps a summary at ~14 lines and this one is four times
that. The prompt asked for it explicitly — "Phases 0008, 0014 and 0012 all build
directly on this and will read nothing else about it" — and the alternative is
three later sessions reading `internal/policy` from scratch, which costs far
more than the lines do. It is a considered exception, not a drift; the next
phase's summary should be short again.

### The three rejections that are judgement calls, not mechanics

Most of the 65 codes are checks the prompt or the plan named outright. Three
were decisions taken here:

1. **`cache.key_not_identity_bound`.** PLAN §5.4 says a key is never shared
   across identities, but a key authored as a free-form template makes that
   unenforceable — you cannot tell by inspection whether `"{a}-{b}"` names a
   subject. So the key is a **list of closed components**
   (`CacheKeyComponent`), and "must include `subject`" becomes a check the
   compiler can actually make. 0008 renders the components into the opaque
   server key it puts on the wire; the vocabulary here is what the author
   writes, not what the proxy sees.
2. **`match.empty_term`.** `groups: []` is an axis the author opened and named
   nothing in. It matches nothing, so it is almost never meant, and where it is,
   deleting the key says it more clearly.
3. **`cache.hint_on_deny`** and **`obligation.on_deny`.** A denial is not a
   response the proxy may reuse and a denied session has no obligations, so both
   are dead weight in a bundle that reads as though it does something.

Two further *non*-rejections are as load-bearing as the rejections, and each has
a test named after it: an **attested** rung on an all-`brokered-key`/`static-key`
ladder is valid (it is how an appliance carries a real enforcement claim), and an
**unrecognised device-field name** is accepted (shape is checkable here, meaning
is the driver's; on the proxy it is a skipped rung — M17, 0006).

### The session axes are outputs, and PLAN §5.1 said otherwise

PLAN §5.1's input table carried a "Session" row — requested channel type,
in-channel request, forwarding destination, global request, command — and the
prompt's own list of match axes did not. The prompt is right and the table was
wrong: the proxy asks **once** per connection and enforces for its lifetime
(proxy D2), so at the moment a decision is made no channel has been opened and
no command has been typed. Those axes are the allow-list the engine *emits*.
§5.1 is revised in place and `model.Match` has no session axis; adding one later
would need a per-request authorize call the contract does not have.

`conn.hop_trail` is absent for the reason §5.3 already gives, and the doc comment
on `model.Input` repeats it where somebody would be tempted to add it.

### Purity, and the one thing that touches the filesystem

`time.LoadLocation` runs at **parse** time, never at evaluation time, and
`model/parse.go` imports `_ "time/tzdata"`. Embedding the zone database is
normally a program's choice rather than a library's, and it is made here on
purpose: without it the same bundle compiles on a developer's machine and fails
in a scratch container that ships no zoneinfo, and two hosts with different
tzdata releases could disagree about a DST boundary while both reporting
success. Compilation is then a function of the bundle and this binary, which is
what determinism means here. It costs a few hundred kilobytes in every binary
that links `internal/policy`.

An unknown zone does **not** throw the document away: `Parse` falls back to UTC
and `Validate` reports `policy.timezone_unknown`, so one misspelt zone name does
not hide every other rejection in the file.

### Why `Parse` validates almost nothing

`Parse` fails only where there is no usable bundle: malformed YAML, an unknown
key, a value outside an enum. Everything else — including the document's own
shape — is `Validate`, which `Compile` calls. The first cut had `Parse` run
`Validate` and return early, and the consequence showed up immediately in a
test: a missing deny reason on rule two hid an unknown group on rule one and a
missing username on rule three. An author fixing a bundle one compile at a time
is an author who stops trusting the compiler.

`yaml.v3` needed one specific thing for that to work on the enums: an
`UnmarshalYAML` that returns a plain error aborts the whole decode and loses the
position, while one that returns a `*yaml.TypeError` is collected and the decode
carries on. `model.nodeErr` is that, and it is why an enum mistake reports its
line.

### The `exhaustive` guard, and the linter version trap

`scripts/exhaustive-guard.sh` writes a switch **with a `default:` clause** and an
incomplete lookup map into `internal/policy/model`, runs the repository's linter
over that package, and fails unless the linter names both missing members. It is
wired to `make exhaustive-guard` and to a step in the lint job — the job that has
a linter in it. M3's closed vocabulary rests on that linter and on nothing else
(M13), so the check needed checking.

0001's gotcha bit again while building it and is worth restating with the fix:
golangci-lint type-checks with the `go/types` of the Go it was **built** with, so
`go install golangci-lint@v2.13.2` produced a binary built with go1.26.8 that
refused this module ("the Go language version used to build golangci-lint is
lower than the targeted Go version 1.27.0"). `GOTOOLCHAIN=go1.27.0 go install …`
fixes it. CI is unaffected — `golangci-lint-action` downloads a release built
with a newer Go — but a session that lints locally will hit it.

### Unreachability is conservative on purpose

`compile/shadow.go` reports a rule only when it can *prove* an earlier rule
matches everything it does: OR-lists by subset, key-to-values maps by key, CIDRs
by prefix containment, hostnames by wildcard coverage, time windows expanded
into their one or two plain intervals. Where it cannot prove containment it says
nothing. A missed shadow is a rule nobody needed; a false one is the compiler
refusing a policy that was correct, and that is how authors learn to distrust the
check. `shadow_test.go`'s second half — the pairs that must **not** be reported
— is the more important half.

The scan is quadratic in rule count (2,000 rules ≈ 2M cheap comparisons, well
under a second). That is fine: compilation happens when a bundle is published,
and M5 governs only evaluation.

### `GlobalRequestType` is the one axis left open

Every other closed axis is a named `Kind` enum. Global request names are matched
**exactly against the name on the wire** and that namespace is open —
`tcpip-forward` sits beside `streamlocal-forward@openssh.com` and beside whatever
a vendor ships next — so closing it would make an estate's own request
unauthorable without a release of this server. It is a named string type with the
well-known members declared as constants (so a bundle can be grepped for them)
and nothing switches over it.

### Agreement with the contract is a test, not an import

`internal/policy` does **not** import `internal/contract`, so that M3's "the
compiler is a boundary" stays a real escape hatch. The price is a duplicated
vocabulary, and `model/contract_agreement_test.go` is what stops it drifting: it
compares thirteen enums in **both** directions, checks that
`Attested()`/`Applied()`/`Provisions()` classify rungs and methods the same way
on both sides, and checks the four device-field shape constants. It is an
in-package test (`package model`) because the member lists are unexported.

### Things 0008 will need and should not re-derive

- **Rendering the snapshot onto the wire** is 0008's, and two fields need care.
  `Enforcement.Stated()` reports whether the route stands anywhere other than
  proxy-side enforcement: emit no `enforcement` object when it is false, because
  an emitted default is noise in an audit record. And the ladder's
  `Username.Value` is already resolved — put it in `params.username`.
- **`RouteIntent` is not `route_type`.** Policy states whether the path may
  traverse hops; `direct` vs `nexthop` comes from the fleet graph (0006).
- **The cache key components are not the wire key.** 0008 derives one opaque
  server key from them, and per PLAN §5.4 must not issue a hint at all when the
  revocation stream for that proxy is unhealthy (M9) — a check this package
  cannot make.
- **`Explanation.RulesConsidered`** is the cheapest possible assertion that
  evaluation stayed linear; it is also a useful decision-record column.
- **Tenancy:** look the program up in a `compile.Set` before evaluating. Nothing
  in `internal/policy` reads a tenant from ambient state, and `eval.Evaluate`
  cannot see one at all, which is what stops it becoming a per-request predicate.

### Follow-ups, deliberately not done here

- **Simulation and the authoring API** are 0014's. Evaluation is already pure and
  time is already an input, so neither needs a change here.
- **Capability checking** (M17) is 0006/0008/0014's, as the prompt says.
- **A `policyctl` CLI** (`cmd/policyctl/` in PLAN §3) has no prompt of its own
  yet; 0014 lists it. Everything it needs is exported: `Parse`, `Compile`, and
  `Rejection.Error()` is already the line it should print.
