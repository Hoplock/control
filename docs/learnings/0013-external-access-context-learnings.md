# 0013 — external access context — Learnings

## Summary
- **What shipped:** M16 end to end — the revised `ext.AccessContextProvider`; scope
  bindings and their internal API; the push receiver (`POST
  /api/v1/tenants/{tenant}/access-context/{provider}/push`, permission
  `access-context:push`, held only by the new **`integration`** role, which reads
  nothing); the probe path inside a fixed share of the authorize budget; a bundle's
  `scopes:` section; Control's declarative HTTP provider. Migration **`0009`**.
- **Key files:** `ext/accesscontext.go`, `internal/accessctx/{service,binding,push,probe,cache,provider,ratelimit,events,errors}.go`,
  `internal/accessctx/declarative/*`, `internal/access/external.go`,
  `internal/audit/contextevent.go`, `internal/decision/{service,record}.go`,
  `internal/httpapi/north/accesscontext.go`, `internal/config/accesscontext.go`,
  `cmd/hoplock-control/accesscontext.go`.
- **The seam, verbatim** (an `ext` change — owes `hoplock/enterprise` a sync):
  ```go
  type AccessContextProvider interface {
      Describe() AccessContextInfo // {Name string; Probes, Pushes bool}
      Probe(ctx context.Context, q AccessContextQuery) (AccessEvidence, error)
      Interpret(ctx context.Context, p AccessContextPush) (WindowAssertion, error)
  }
  ```
  `Name` is the external SYSTEM (`^[a-z0-9][a-z0-9-]{0,62}$`, unique per server):
  binding key, push path segment, the grant's `external_system`. The registration
  names the code. `AccessEvidence` gained `State`, `TTL`, `AdditionalContext`.
- **Three outcomes:** `WindowConfirmed` (a window is open now; `Reference`
  required) → counts as a grant. `WindowNotConfirmed` (answered, no window;
  `ErrNoEvidence` reads the same) → does not. **Could not determine** = an error:
  `KindUnavailable` (wrap `context.DeadlineExceeded` on a timeout), `KindMalformed`,
  anything else "failed"; the zero `WindowUndetermined` with a nil error reads as
  malformed. It falls per the scope's `unanswered`: `closed`, `outage` (5xx only if
  the decision depends on it), `open` (a pushed window counts unconfirmed).
  **Privileged always falls closed.** Default `access_context.unanswered: outage`.
- **Scope binding** (`access_context_bindings`, one per provider per tenant):
  `mode` push|probe|push-probe; ONE grant `scope`; `subjects`/`subject_groups`
  (≥1); a target selector `targets`/`target_labels`/`target_zones` (≥1, labels
  and zones ride on every grant); `max_window`; `privileged` (must agree with the
  policy marking the scope privileged); `push_principals` (the north-bound
  credentials that may push; required iff it pushes); `enabled`. Outside it ⇒
  refused, audited `critical` as `access_context.escalation_attempt`, no grant.
- **Budget split:** every probe of one decision runs concurrently under
  `access_context.probe_budget` (500 ms, ≤ half of `decision.budget`, refused at
  load and in `decision.New` otherwise); the rest is the decision's own. Answers
  reused for the provider's TTL (capped by `max_probe_ttl`, never past a
  confirmed window's end); one call in flight per question; `max_probes_in_flight`
  per provider, past which a probe is "saturated".
- **Ceiling rule:** granted end = min(asserted end, opening + min(binding
  `max_window`, `access_context.max_window` 12 h)). Clamped, never refused; the
  `grant.created` record carries `grant_external_clamped`/`_ceiling_seconds`.
- **Replay:** assertion id unique per (tenant, system): same id ⇒ same grant
  (200, `replayed`), a different window under it ⇒ 409, revoked stays revoked.
  Stale (`max_assertion_age` 15 m) or future (`clock_skew` 2 m) ⇒ refused.
- **Declarative config** (`access_context.providers[]`): `name`; `probe:{url,
  method GET|POST, headers, body (JSON template), auth:{type none|bearer|basic|
  header, token_env|username+password_env|header+value_env}, timeout, ttl (30s),
  absent_status ([404]), confirm:[{path, equals|not_equals|one_of|exists|
  contains}], reference, window_start, window_end, additional_context,
  assertions:{code: path}}`; `push:{id, subject, targets, window_end (required),
  reference, scope, window_start, issued_at, additional_context}`. Placeholders:
  `{{tenant}} {{subject.id}} {{target.hostname}} {{target.zone}} {{reference}}
  {{scope}} {{at}}`. Egress: `access_context.egress.allow_cidrs`.
- **Decisions:** M16 **revised in place** (register row `live`, now rendered in
  §3 too); PLAN §3, §5.1, new §5.5, §7, §10 revised. No decision added. **No
  upstream request**: the contract's `GrantContext` already carries all of it.
- **NEXT (0014):** there is **no operator path to a binding** until its routes
  land — its prompt now lists them, plus explain and simulation duties.

## Details

The summary runs long for the reason 0004's and 0005's did, and the prompt asked
for it by name: Hoplock Enterprise writes its Qualys and BMC Helix integrations
from the summary above and from nothing else.

### The seam: why `Describe`, and why the registry rules did not change

The prompt said "registration follows 0004's rules unchanged" and "the registry
keys them by name". Those collide: a multiple-implementation point refuses a
second registration by the same `Registration.Provider`, and Control's
declarative provider is one piece of code a deployment may configure twice. The
answer that leaves 0004's rules untouched separates two names that were never the
same thing:

- `Registration.Provider` names the CODE, as everywhere else in `ext`. Each
  declarative integration registers as `hoplock/control/declarative/<name>`
  (`declarative.RegistrationPrefix`) — not `Default`, because nothing is defaulted:
  an operator configured it, and the listing shows one row per system.
- `Describe().Name` names the external SYSTEM. `accessctx.NewProviders` keys by it
  and refuses two providers answering to one name, naming both registrations —
  because a binding trusts a name, and a binding that silently starts trusting
  different code is the failure 0004's duplicate rule exists for.

A Qualys integration registers as `github.com/hoplock/enterprise/qualys` and
describes itself as `qualys`; the grant says `system: qualys`, which is what an
auditor recognises and what the contract's example shows.

`Interpret` is on the interface because Enterprise cannot import `internal/`: a
packaged integration's only way to turn a vendor's webhook into a window is to be
handed the body. Everything that makes a push safe happens after it returns.

### Three answers, and where "privileged" lives

"The default for anything the policy marks privileged is deny" settles that
privileged-ness is the POLICY's to say. It is a bundle section rather than a rule
attribute because two readers need it before any rule is chosen: the push
receiver (may this binding open this scope at all?) and the probe path (which way
does this unanswered window fall?). `model.ScopeDecl{Privileged, Unanswered}`,
two rejection codes (`scope.name_invalid`, `scope.privileged_fails_open`), and
`compile.Program.Scope(name)`. The engine is untouched; an undeclared scope is the
zero declaration.

The binding's own `privileged` flag must agree: two people — the policy's author
and the integration's — sign off on a scanner opening root.

`outage` is applied only when the decision DEPENDS on the window
(`decision.dependsOn`): evaluate without the outage windows; if that denies,
evaluate again with them; denied-without-allowed-with is the outage. A denial
that stands on its own stays a denial, and an allow from another rule stands — a
deliberate trade: if including the window would have made an EARLIER rule match,
the served answer is the one that needs no unconfirmed fact.

### The three shapes, and why a probe-only grant is not stored

A push-only window is a stored grant (`external_mode = 'push'`). A push-probe
window is a stored grant that the probe path counts only when confirmed, narrowed
to the end the probe states. A probe-only window is built per decision
(`accessctx.ProbeGrantID`, derived from tenant, system and reference, prefix
`xg_`) and lives in the decision record. Storing it would put a grant insert and
a hash-chain append on the authorize path once per window; what it would buy —
revoking a probe-only window — is better done by disabling the binding, after
which nothing is asked. The record (`inputs.grants[]`, `decisions.grant_id`) keeps
"which sessions did this scan back" answerable.

`decision` without a prober (`Options.External` nil) counts push-only grants and
drops push-probe ones: a window that needs a probe never counts without one.

### The push receiver's order

Provider → binding → credential named by the binding → mode → rate → Interpret →
shape → **replay** → staleness/skew/closed → scope → ceiling → grant. Replay runs
before the scope check so that a retry after a binding was narrowed is
idempotent rather than an "escalation". The credential check runs before the
mode check, so a push to a probe-only binding (which names no credential) is
`push_not_permitted` — an escalation — rather than a warning: whoever holds an
integration token and pushes where no push is accepted is the case to page on.
A refusal that cannot be audited is an outage, not a refusal. Rate-limited pushes
are logged, not audited: a flood must not become a flood of chain appends.

### Egress (the SSRF answer)

A template fills a path or a query, never a scheme, host or port (checked at
start-up with a sentinel); values are URL-escaped so a subject id cannot add a
parameter. Every connection is checked in `net.Dialer.Control` against the
address actually dialled — loopback, RFC 1918, link-local (metadata), CGNAT,
benchmark, reserved, NAT64, multicast, unspecified refused unless
`allow_cidrs` admits them. Redirects are not followed; no environment proxy is
used (it would hide the destination from the dial check). https, or http to a
loopback host only. Answers are capped at 1 MiB.

### Other things that changed shape

- `ext/access.go` keeps the grant workflow; the access-context seam moved to
  `ext/accesscontext.go`. `ErrNoEvidence` survives, read as not confirmed.
- `store.GrantExternal` gained `AssertionID` and `Mode`; `GrantRepository` gained
  `GetByAssertion`; new `AccessContextBindingRepository`.
- `access.Event` gained `External *ExternalAct`; `access.Service.CreateExternal`
  is the external path beside `Create`.
- Decision records: `inputs.external_context[]` (always present),
  `inputs.grants[].external.{mode,assertion_id}`, `explanation.external`,
  `explanation.unanswered`.
- `identity`: `PermAccessContextPush`, `RoleIntegration` (the one role without
  the read set — its credential sits in somebody else's software).
- `north.Options.AccessContext` (nil ⇒ every push is `provider_not_found`),
  `MaxPushBytes`; 14 new error codes, accessctx's verbatim.
- The tenancy guards now cover 0011's ten identity repositories, closing 0012's
  finding; all of them already refused an empty tenant before any SQL.

### Config added

`access_context.{probe_budget 500ms, max_window 12h, clock_skew 2m,
max_assertion_age 15m, unanswered outage, max_probe_ttl 5m,
max_probes_in_flight 32, push_rate 5, push_burst 20, max_push_bytes 65536,
egress.allow_cidrs, providers}` — documented in `config.example.yaml`.

### Findings and follow-ups, not done here

- **No operator path to a binding before 0014.** `seed` writes rows directly
  and would bypass validation and audit, so it was not extended; 0014's prompt
  now carries the routes, the two permissions, and the explain/simulation duties.
- Disabling or deleting a binding stops its pushes and probes, and a push-probe
  window stops counting; a push-only grant it produced stays until it expires or
  is revoked. A "disable and revoke" act belongs to 0014's surface if wanted.
- Push authentication is the north-bound bearer token (E7's standard). Vendor
  webhook signatures (HMAC) are not verified; a deployment needing them fronts
  the receiver or uses Enterprise's packaged integration.
- The declarative provider does not use an egress proxy. A locked-down network
  that requires one needs an explicit proxy setting checked by the same rules.
- `outage` is a 500 with `CodeInternal` on the south-bound surface, like every
  unclassified failure (M11). The record says which window, which provider, why.

### Verification

`go build`, `go vet`, `go test -race ./...` against Postgres 16, golangci-lint
v2.13.2 (0 issues), `exhaustive-guard`, `license-check`, `contract-check`, and the
conformance suite against this server with a declarative provider configured
(36/36). The binary was run: an `integration` token pushed a three-hour window
(201, clamped to the binding's hour), the same push again (200, replayed), a host
outside the binding (403 `outside_scope`), and was refused the grant list (403);
`audit-verify` OK with `grant.created` at `warn` and the escalation at `critical`.
