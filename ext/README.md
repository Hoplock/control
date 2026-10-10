# `ext` — the extension seam

This is one of the two public packages in `github.com/hoplock/control`. It is
what Hoplock Enterprise implements, and it is what anybody else imports to extend
a self-hosted deployment without forking it (PLAN M15). The other is `server`,
which starts Control with those extensions registered — see
[Starting Control from a host](#starting-control-from-a-host).

It is interface-only. Importing it costs you the interfaces and nothing else —
no database driver, no HTTP stack, no policy compiler — and a test at the module
root fails the build if that ever stops being true.

## The compatibility promise

`ext` is versioned with the module, and changing a signature here breaks a
downstream build. Treat it the way this repository treats the vendored wire
contract: change it deliberately, say so in the phase's learnings file, and
never as a drive-by.

A consumer pins a **release**: an immutable tag `vMAJOR.MINOR.PATCH` of the
whole module. There is no `ext` version of its own (`docs/PLAN.md` M23). While
MAJOR is `0`, a release is a **MINOR** when it contains a phase or an
incompatible change to a public package, and a **PATCH** for anything else.
Every incompatible change is listed under `### Breaking` in that release's
section of `CHANGELOG.md`. "Incompatible" is read from the implementer's side
as well as the caller's: a method added to an interface here breaks every
implementation outside this module, so it is a break although no caller
notices. `make release-check` holds this against the previous release on every
pull request. It diffs this package, and any other public one, and refuses a
release that does not declare what it finds.

A phase that adds or changes an interface here owes a
`## Cross-repo impact` section, and queues a ready-to-run sync kickoff for
`hoplock/enterprise` in `prompts/downstream/queued/`
(`docs/CROSS-REPO-PROTOCOL.md` §4.1, §4.3). Traffic runs the other way too:
when `hoplock/enterprise` needs a seam here that does not exist, it raises an
**upstream request** (§3.2, §4.2) and queues, in its own
`prompts/upstream/queued/`, the kickoff that turns it into a queued prompt in
this repository — `docs/KICKOFF.md`'s "Upstream request" block. The "Next
cross-repo request" kickoff answers it here (§4.3), and a phase that answers one
owes the sync back to Enterprise like any other consumer (§5).

## Two rules that bound everything here

1. **Control never imports Enterprise.** The dependency runs one way. A build of
   Control with no Enterprise present is a complete, self-hostable product.
2. **An extension point is not a hole where core functionality used to be.**
   Every seam has a real answer in this repository. Where that answer is
   Control's own code path — the local audit store, manual grants, its own
   software keys, the compiler's checks — the seam is *additive*: registering
   nothing costs a deployment nothing it had. Where a point genuinely adds a
   capability Control never claimed, it says so, and `ext.PointInfo` records
   which of those it is so the claim is checkable rather than aspirational.

## The points

`ext.Points()` is the authoritative list; this is it in prose.

| Interface | When nothing is registered | What Control ships |
| --- | --- | --- |
| `AuditSink` | records stay in the local tamper-evident store and are exported nowhere | the store, its hash chain, verifier and query API (0010) |
| `ArchiveStore` | retention deletes records when their window closes | nothing — an archive is an addition |
| `GrantWorkflow` | an authorised administrator creates a time-boxed grant directly | manual grants, expiry, revocation (0012) |
| `IdentitySync` | identities arrive at login; nothing is provisioned ahead of time | nothing — provisioning is an addition |
| `KeyStore` | Control holds its own software keys and signs with them | software custody and the tenant SSH CA (0011) |
| `Notifier` | notifications go to the deployment's configured webhook | the webhook notifier (0012) |
| `ClusterCoordinator` | **never empty:** Control's wiring registers the single-node coordinator | one member, every singleton held, an in-process bus (0004) |
| `ActionHandler` | no external system can act on this deployment | nothing — inbound automation is an addition |
| `ReportProvider` | the north-bound audit and decision queries are the reporting available | query, explain, simulation (0010, 0019) |
| `PolicyValidator` | the compiler's own checks are the whole of validation, and always run | the compiler's exhaustive authoring-time checks (0005) |
| `AccessContextProvider` | no external access context is consulted | the declarative HTTP provider — configured, not coded (0013, M16) |

`AccessContextProvider` returns **evidence, not a verdict**. The first instinct
is to return a boolean, and that quietly relocates the policy engine into a
vendor integration: the rule stops being visible in the bundle, stops being
simulated, and stops being explainable. Report what the external system
asserted; policy decides what it is worth.

It has two directions and three answers (M16, phase 0013). `Probe` asks the
external system on the authorize path and answers `WindowConfirmed` or
`WindowNotConfirmed` — or an error, which is **could not determine**:
`KindUnavailable` for a network (wrap `context.DeadlineExceeded` on a timeout)
and `KindMalformed` for an answer that made no sense. `Interpret` reads a push
into a `WindowAssertion`. `Describe` names the external system — the key a
tenant's scope binding is filed under — and says which directions you
implement. Scope, replay, clock skew and the window ceiling are Control's, not
yours: you are handed only pushes that already passed authentication, and
everything you assert is checked after you return.

## How to implement a point

Implement the interface. That is the whole of it — no plugin loading, no
reflection, no build tags.

Four things every implementation owes:

- **Respect the context.** Every method takes one, and on the authorize path it
  carries a deadline that M5 governs. A method that ignores it delays everything
  behind it.
- **Classify your errors.** Return `*ext.Error` through `ext.Errorf`. An
  unclassified failure is `ext.KindInternal`, which Control treats as an
  outage — and `ext.KindDenied` is the only kind that can ever become "access
  denied" (M11). A timed-out integration must never be able to tell a user their
  access was refused.
- **Be idempotent where the doc says so.** Control retries what it could not
  confirm.
- **Carry codes, not prose.** Everything beneath the console stores and
  transmits codes; the console holds the only string catalogue (M21).

## How to register

A host binary builds a registry, registers what it brings, and hands it to
Control, which adds its own defaults and seals it before anything starts.

```go
type queueSink struct {
	topic string
}

// Export delivers one batch. It is idempotent on RecordID, because Control
// retries a batch it could not confirm and a sink that doubles on retry
// produces an audit trail that double-counts.
func (s *queueSink) Export(ctx context.Context, batch []ext.AuditRecord) error {
	for _, rec := range batch {
		if err := ctx.Err(); err != nil {
			// Control's deadline for this attempt has passed. Report it
			// as unavailable rather than as a bare error: an
			// unclassified failure is an outage (M11), and this one has
			// a name.
			return ext.Errorf(ext.PointAuditSink, "example.com/queue-sink", "AuditSink.Export",
				ext.KindUnavailable, "deadline reached after %d records: %v", rec.Sequence, err)
		}
		_ = rec // publish to s.topic, keyed by rec.RecordID
	}
	return nil
}

// Example shows an out-of-tree implementation registering itself. A host
// binary — Hoplock Enterprise's, or anybody's — builds a registry, registers
// what it brings, and hands it to Control, which adds its own defaults and
// seals it before anything starts.
func Example() {
	registry := ext.NewRegistry()

	me := ext.Registration{Provider: "example.com/queue-sink", Version: "1.0.0"}
	if err := registry.RegisterAuditSink(me, &queueSink{topic: "hoplock.audit"}); err != nil {
		fmt.Println("registration failed:", err)
		return
	}

	// Registering the same provider twice at one point is an error naming
	// both sides, rather than a silent last-wins that would let two modules
	// of one product disagree invisibly.
	if err := registry.RegisterAuditSink(me, &queueSink{topic: "other"}); err != nil {
		fmt.Println("second registration refused")
	}

	// A host binary can log its own wiring before handing the registry over.
	// Implementations are not readable from a Registry at all: that is what
	// makes a half-registered extension unreachable rather than merely
	// discouraged.
	shown := []ext.Point{ext.PointAuditSink, ext.PointArchiveStore}
	for _, st := range registry.Status() {
		if slices.Contains(shown, st.Info.Point) {
			fmt.Println(st)
		}
	}

	// Output:
	// second registration refused
	// AuditSink: example.com/queue-sink@1.0.0
	// ArchiveStore: none registered (disabled) — retention deletes records when their window closes and there is no long-term copy
}
```

That is not decoration and it is not a paraphrase: it is copied verbatim from
`example_test.go`, it compiles and runs under `go test ./ext/`, and a test in
that file fails if this block and the source ever drift apart.

In a host binary the last step is handing `registry` to Control, which registers
its own defaults, seals it, and starts the server — with `server.Main` or
`server.Run` ([below](#starting-control-from-a-host)).

Three rules the registry enforces:

- **Registration happens before start and is immutable afterwards.** `Seal`
  yields an `*ext.Extensions`, and only that can be read from. Registering after
  `Seal` is an error.
- **Registering twice for the same point is an error naming both registrants.**
  At a single-implementation point any second extension is refused; at a
  multiple-implementation point a second registration by the *same* provider is.
  Silent last-wins is how two modules of one product fight invisibly.
- **Every registration is visible.** `Registration.Provider` is required, the
  start-up log prints one line per point — including the points where nothing is
  registered, because that explains behaviour too — and the north-bound API
  exposes the same listing (0019). An invisible extension is indistinguishable
  from a bug in Control.

Control's default supersedes nothing and is superseded by anything: registering
an extension at a point where Control registered a default is not a conflict,
the extension wins, and the listing records which one is in play. Only
`hoplock/control` may register with `Default` set.

## Starting Control from a host

`github.com/hoplock/control/server` is the other public package, and the other
half of the promise. A host binary's `main` registers what it brings into an
`ext.Registry` and makes one call:

- **`server.Main(ctx, args, stdout, stderr, server.Options)`** is Control's
  whole command line — the daemon and every subcommand (`migrate`, `seed`,
  `audit-verify`, `identity`, `ca`). `hoplock-control` is this call with zero
  `Options`, so the two binaries start Control the same way. A host owns its
  signals and its exit code, and dispatches commands of its own before calling
  it.
- **`server.Run(ctx, config, server.Options)`** is the daemon alone, for tests
  and for a host that builds its own command line.

`Options` carries what a host adds, and nothing else:

| Field | What Control does with it |
| --- | --- |
| `Provider` | names the host in the route listing and the start-up log; required with any of the fields below, and never `hoplock/control` |
| `Registry` | registers Control's defaults into it, seals it — an extension beats a default — and serves with the sealed set |
| `HostSections`, `HostConfig` | the strict decoder accepts those top-level keys, and `Run` hands each one present to `HostConfig` as YAML, before sealing and before anything serves; a key Control defines is refused, and every other unknown key is still an error |
| `Routes` | mounted on the north-bound listener only, at `/api/v1/tenants/{tenant}/<Pattern>`, behind Control's authentication, tenant resolution and RBAC; `Permission` names one of Control's codes, never a new one |
| `ErrorCodes` | the codes `server.WriteError` will render beside Control's own; anything else, and any `401`, renders `500 internal` |

A handler reads who is calling with `server.CallerFrom` — the tenant Control
resolved, the subject, the credential, the break-glass flag and the correlation
id — and answers a failure with `server.WriteError`, in the envelope every
north-bound error carries (M21). Everything a host can get wrong is refused
before the configuration is read, a database is opened or a port is bound.

```go
// archive is a host's extension: a long-term archive Control does not ship
// (ext.ArchiveStore is disabled when nothing registers one).
type archive struct{ retention string }

func (a *archive) Archive(context.Context, []ext.AuditRecord) error { return nil }

func (a *archive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

// A host binary's main: register an extension, declare a configuration section
// and a route, and start Control. A real host calls server.Main instead of
// server.Run, so that its binary carries Control's subcommands too.
func Example() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store := &archive{}
	registry := ext.NewRegistry()
	if err := registry.RegisterArchiveStore(ext.Registration{Provider: "example/archive", Version: "v1.0.0"}, store); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	config, err := os.Open("config.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer func() { _ = config.Close() }()

	err = server.Run(ctx, config, server.Options{
		Provider: "example/host",
		Registry: registry,

		// `example:` in Control's configuration file is this host's. It is
		// handed over undecoded, before the registry is sealed.
		HostSections: []string{"example"},
		HostConfig: func(section string, raw []byte) error {
			var cfg struct {
				Retention string `yaml:"retention"`
			}
			if err := yaml.Unmarshal(raw, &cfg); err != nil {
				return err
			}
			store.retention = cfg.Retention
			return nil
		},

		// GET /api/v1/tenants/{tenant}/archive/{record}, for a caller holding
		// audit:read in the tenant Control resolved.
		Routes: []server.Route{{
			Method:     http.MethodGet,
			Pattern:    "archive/{record}",
			Permission: "audit:read",
			Summary:    "one archived record",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				caller, _ := server.CallerFrom(r.Context())
				server.WriteError(w, r, http.StatusNotFound, "archive_record_not_found",
					map[string]any{"record": r.PathValue("record"), "tenant": string(caller.Tenant)},
					"the archive holds no such record")
			}),
		}},
		ErrorCodes: []string{"archive_record_not_found"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
```

This block is `server/example_test.go` verbatim. It compiles under
`go test ./server/`, and a test there fails if the two drift apart.

## Adding a point

Adding an interface here without a `PointInfo` row fails the build, and so does
a row whose interface never says what happens when nobody implements it. The
steps:

1. Declare the interface, with a doc comment that says what varies and why, and
   that contains the sentence **"When no implementation is registered"**.
2. Add a `PointInfo` row in `point.go`: the interface name, whether several may
   register, the disposition, the absent behaviour, and what Control ships.
   A point that ships nothing from Control must be `WhenAbsentDisabled` — that
   is M15's second invariant as a test.
3. Add a `RegisterX` method to `Registry` and an accessor to `Extensions`.
4. If the disposition is `WhenAbsentDefault`, register the implementation in
   `internal/extdefault`. `Seal` refuses to start a server that promised one and
   has none.
