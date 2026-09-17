// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import "fmt"

// Point names one extension point. It is a closed enum rather than a set of
// strings so that a switch over the points can be checked for exhaustiveness
// by the linter (PLAN M13): adding a seam then fails to build in every place
// that has to decide something about it, rather than falling into a default
// branch and being forgotten.
type Point int

const (
	// PointAuditSink exports audit records to something outside Hoplock.
	PointAuditSink Point = iota
	// PointArchiveStore keeps audit records past the local retention window.
	PointArchiveStore
	// PointGrantWorkflow decides who may hold a grant and who must approve it.
	PointGrantWorkflow
	// PointIdentitySync provisions and de-provisions identities ahead of login.
	PointIdentitySync
	// PointKeyStore holds the private keys this deployment signs with.
	PointKeyStore
	// PointNotifier delivers an operator-facing notification.
	PointNotifier
	// PointClusterCoordinator supplies node membership, cluster-wide
	// singletons, and the cross-node event bus.
	PointClusterCoordinator
	// PointActionHandler is the inbound channel an external system uses to act
	// on this deployment.
	PointActionHandler
	// PointReportProvider offers packaged reports over what this deployment
	// recorded.
	PointReportProvider
	// PointPolicyValidator adds checks a policy bundle must pass before it may
	// be activated.
	PointPolicyValidator
	// PointAccessContextProvider supplies evidence from a system outside
	// Hoplock about whether an access is legitimate right now (M16).
	PointAccessContextProvider
)

// WhenAbsent classifies what happens at a point where nothing is registered.
// It exists because M15's second invariant — "a seam is not a hole where core
// functionality used to be" — is a property of each point that can be written
// down and tested, rather than a promise in prose.
type WhenAbsent int

const (
	// WhenAbsentCore means the capability is core Control behaviour and runs
	// in Control's own code path. The seam is additive: registering an
	// implementation adds to, redirects, or supersedes that path, and
	// registering none costs a deployment nothing it had.
	WhenAbsentCore WhenAbsent = iota
	// WhenAbsentDefault means Control's own wiring registers an
	// implementation at start-up (internal/extdefault), so the point is never
	// genuinely empty in a running server.
	WhenAbsentDefault
	// WhenAbsentDisabled means the capability is simply off. This is only
	// legitimate for a capability Control never claimed to have — never for
	// one that was moved out of the core to make room for a licence.
	WhenAbsentDisabled
)

// String renders the disposition for logs and test failures.
func (w WhenAbsent) String() string {
	switch w {
	case WhenAbsentCore:
		return "core"
	case WhenAbsentDefault:
		return "default"
	case WhenAbsentDisabled:
		return "disabled"
	}
	return "unknown"
}

// PointInfo is everything the rest of the system needs to know about a point
// without knowing its interface: what it is called, whether more than one
// implementation may register, what Control does when none has, and what
// Control itself ships for it.
//
// ControlShips is M15's second invariant in machine-readable form. A point
// that leaves it empty is asserting that Control never had this capability at
// all, and a test requires such a point to be WhenAbsentDisabled — so the only
// way to ship a seam with no Control-side answer is to say out loud that the
// capability is an addition rather than a subtraction.
type PointInfo struct {
	// Point is the point this describes.
	Point Point
	// Interface is the name of the Go interface in this package. A test
	// checks that every exported extension interface here has a row, and
	// that every row names a real interface.
	Interface string
	// Multiple reports whether several implementations may register. A
	// single-implementation point rejects a second registration outright; a
	// multiple-implementation point rejects only a second registration by the
	// same provider.
	Multiple bool
	// WhenAbsent classifies the no-registration case.
	WhenAbsent WhenAbsent
	// Absent states, in one line, what Control does when nothing is
	// registered here. It is the sentence an operator reading the registry
	// listing needs, and it is required to be non-empty.
	Absent string
	// ControlShips names what this repository itself supplies for the point
	// and the phase that builds it, or is empty when Control supplies
	// nothing.
	ControlShips string
}

// points is the catalogue, in Point order. It is the single place that says
// what each seam promises, and both the registry and the guard tests read it
// rather than restating it.
var points = [...]PointInfo{
	PointAuditSink: {
		Point:        PointAuditSink,
		Interface:    "AuditSink",
		Multiple:     true,
		WhenAbsent:   WhenAbsentCore,
		Absent:       "audit records are written to the local tamper-evident store and exported nowhere",
		ControlShips: "the local append-only audit store, its hash chain, verifier and query API (phase 0010)",
	},
	PointArchiveStore: {
		Point:      PointArchiveStore,
		Interface:  "ArchiveStore",
		WhenAbsent: WhenAbsentDisabled,
		Absent:     "retention deletes records when their window closes and there is no long-term copy",
	},
	PointGrantWorkflow: {
		Point:        PointGrantWorkflow,
		Interface:    "GrantWorkflow",
		WhenAbsent:   WhenAbsentCore,
		Absent:       "an authorised administrator creates a time-boxed grant directly",
		ControlShips: "manual time-boxed grants, their expiry and their revocation (phase 0012)",
	},
	PointIdentitySync: {
		Point:      PointIdentitySync,
		Interface:  "IdentitySync",
		WhenAbsent: WhenAbsentDisabled,
		Absent:     "identities arrive at login from the federated IdP and nothing is provisioned ahead of time",
	},
	PointKeyStore: {
		Point:        PointKeyStore,
		Interface:    "KeyStore",
		WhenAbsent:   WhenAbsentCore,
		Absent:       "Control holds its own software keys and signs with them",
		ControlShips: "software key custody and the tenant SSH certificate authority (phase 0011)",
	},
	PointNotifier: {
		Point:        PointNotifier,
		Interface:    "Notifier",
		Multiple:     true,
		WhenAbsent:   WhenAbsentCore,
		Absent:       "notifications are delivered to the webhook the deployment configured, and nowhere else",
		ControlShips: "the outbound webhook notifier (phase 0012)",
	},
	PointClusterCoordinator: {
		Point:        PointClusterCoordinator,
		Interface:    "ClusterCoordinator",
		WhenAbsent:   WhenAbsentDefault,
		Absent:       "Control's own wiring registers the single-node coordinator, so this point is never empty in a running server",
		ControlShips: "the single-node, in-process coordinator: one member, leadership always held, an in-process event bus (phase 0004)",
	},
	PointActionHandler: {
		Point:      PointActionHandler,
		Interface:  "ActionHandler",
		WhenAbsent: WhenAbsentDisabled,
		Absent:     "no external system can ask this deployment to kill a session or lock a subject out",
	},
	PointReportProvider: {
		Point:        PointReportProvider,
		Interface:    "ReportProvider",
		Multiple:     true,
		WhenAbsent:   WhenAbsentCore,
		Absent:       "the north-bound audit and decision queries are the whole of the reporting available",
		ControlShips: "audit query, decision explain, and policy simulation over the recorded history (phases 0010, 0014)",
	},
	PointPolicyValidator: {
		Point:        PointPolicyValidator,
		Interface:    "PolicyValidator",
		Multiple:     true,
		WhenAbsent:   WhenAbsentCore,
		Absent:       "the policy compiler's own checks are the whole of validation, and they always run",
		ControlShips: "the compiler's exhaustive authoring-time checks (phase 0005)",
	},
	PointAccessContextProvider: {
		Point:        PointAccessContextProvider,
		Interface:    "AccessContextProvider",
		Multiple:     true,
		WhenAbsent:   WhenAbsentDisabled,
		Absent:       "no external access context is consulted and no window is opened by one",
		ControlShips: "the declarative HTTP provider — a probe URL, its authentication, a request template, assertions over the response, a TTL, and a webhook field mapping, configured rather than coded (phase 0013, M16)",
	},
}

// Points returns every extension point, in Point order. Callers that build an
// operator-facing listing should use this rather than hard-coding the set, so
// that a seam added later shows up without their having to be changed.
func Points() []PointInfo {
	out := make([]PointInfo, len(points))
	copy(out, points[:])
	return out
}

// Lookup returns the catalogue entry for p.
func Lookup(p Point) (PointInfo, bool) {
	if int(p) < 0 || int(p) >= len(points) {
		return PointInfo{}, false
	}
	return points[p], true
}

// String renders the point as its interface name, which is what an operator
// reading a log line or the north-bound registry listing recognises.
func (p Point) String() string {
	if info, ok := Lookup(p); ok {
		return info.Interface
	}
	return fmt.Sprintf("Point(%d)", int(p))
}
