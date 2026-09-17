# `ext` — the extension seam

This is the only non-`internal` package in `github.com/hoplock/control`. It is
what Hoplock Enterprise imports, and it is what anybody else imports to extend a
self-hosted deployment without forking it (PLAN M15).

It is interface-only. Importing it costs you the interfaces and nothing else —
no database driver, no HTTP stack, no policy compiler — and a test at the module
root fails the build if that ever stops being true.

## The compatibility promise

`ext` is versioned with the module, and changing a signature here breaks a
downstream build. Treat it the way this repository treats the vendored wire
contract: change it deliberately, say so in the phase's learnings file, and
never as a drive-by. A phase that adds or changes an interface here owes a
`## Cross-repo impact` section and a ready-to-run sync kickoff for
`hoplock/enterprise` (`docs/CROSS-REPO-PROTOCOL.md` §4).

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
| `ReportProvider` | the north-bound audit and decision queries are the reporting available | query, explain, simulation (0010, 0014) |
| `PolicyValidator` | the compiler's own checks are the whole of validation, and always run | the compiler's exhaustive authoring-time checks (0005) |
| `AccessContextProvider` | no external access context is consulted | the declarative HTTP provider — configured, not coded (0013, M16) |

`AccessContextProvider` returns **evidence, not a verdict**. The first instinct
is to return a boolean, and that quietly relocates the policy engine into a
vendor integration: the rule stops being visible in the bundle, stops being
simulated, and stops being explainable. Report what the external system
asserted; policy decides what it is worth.

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
its own defaults, seals it, and starts the server.

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
  exposes the same listing (0014). An invisible extension is indistinguishable
  from a bug in Control.

Control's default supersedes nothing and is superseded by anything: registering
an extension at a point where Control registered a default is not a conflict,
the extension wins, and the listing records which one is in play. Only
`hoplock/control` may register with `Default` set.

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
