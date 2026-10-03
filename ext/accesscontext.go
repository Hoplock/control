// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"encoding/json"
	"time"
)

// AccessContextInfo is what a provider says about itself, once, before the
// server starts.
//
// Control keys every access-context provider by Name rather than by its
// Registration: the registration names the CODE (a module path, for a
// listing), while Name names the external SYSTEM — the thing a tenant's scope
// binding is written against and an auditor recognises.
type AccessContextInfo struct {
	// Name is the external system this provider speaks for, as a short
	// stable code: "qualys", "itsm-prod". It is the key a tenant's scope
	// binding is filed under, the path segment of the push receiver, and the
	// `system` recorded on every grant the provider's windows become — and so
	// in every session record the proxy writes under one
	// (`grant_context.system`).
	//
	// Lower-case letters, digits and hyphens, starting with a letter or a
	// digit, at most 63 characters. Unique among the providers one server
	// runs: two providers answering to one name is a start-up failure, never
	// a last-wins.
	Name string
	// Probes reports that Probe asks the external system something. A
	// provider that only receives pushes says false, and Control never calls
	// Probe on it.
	Probes bool
	// Pushes reports that Interpret can read what the external system
	// pushes. A provider that only probes says false, and Control refuses a
	// push naming it without calling Interpret.
	Pushes bool
}

// WindowState is what an external system said about a window — three answers,
// not two.
type WindowState int

const (
	// WindowUndetermined means the provider could not find out. It is the
	// zero value on purpose: an answer nobody filled in reads as "could not
	// determine", never as either of the other two.
	//
	// It is an outage of that provider (M11), and Control never reads it as
	// "no window": collapsing the two turns an outage into a denial that an
	// operator then debugs as a permissions problem. What a decision does
	// about it is policy's — the scope's `unanswered` setting, closed for
	// anything the policy marks privileged — and the decision record says
	// which way it fell.
	WindowUndetermined WindowState = iota
	// WindowConfirmed means the external system says a window is open for
	// this access now. Reference names it and Window bounds it.
	WindowConfirmed
	// WindowNotConfirmed means the external system answered, and there is no
	// open window for this access. It is an answer, not a denial: policy
	// decides what the absence of a window is worth.
	WindowNotConfirmed
)

// String renders the state as the stable code a decision record carries.
func (s WindowState) String() string {
	switch s {
	case WindowUndetermined:
		return "undetermined"
	case WindowConfirmed:
		return "confirmed"
	case WindowNotConfirmed:
		return "not_confirmed"
	}
	return "undetermined"
}

// AccessContextQuery asks an external system whether an access is legitimate
// right now. It names the access rather than asking a general question,
// because a provider that has to be told the whole request to answer is a
// provider that has been handed the policy decision.
type AccessContextQuery struct {
	// Tenant is required.
	Tenant Tenant
	// Subject is who is asking.
	Subject Subject
	// Target is what they are reaching.
	Target Target
	// Privileges are the stable codes for what they are asking to do. Control
	// sends the grant scope the provider's binding produces — the one
	// privilege a window confers.
	Privileges []string
	// ExternalRef is the reference of the window being confirmed, when a push
	// already opened one: the probe is then asked about THAT window — "is
	// SCAN-1183 still running against this host?" — rather than about any
	// window at all. Empty means the provider must decide from the subject
	// and target alone.
	ExternalRef string
	// At is the instant the question is being asked about, supplied rather
	// than read from a clock so that simulation over historical inputs is
	// total (M4).
	At time.Time
}

// AccessEvidence is what an external system says about an access — and it is
// deliberately not a verdict.
//
// The first instinct of anyone implementing AccessContextProvider is to return
// a boolean, and that quietly relocates the policy engine into a vendor
// integration: the rule "a privileged session needs an approved change window"
// stops being visible in the policy bundle, stops being simulated, and stops
// being explainable, because the only thing that survives to the decision
// record is somebody else's yes. So a provider reports what the external
// system asserted — a ticket is approved and runs until 18:00, a scan is in
// progress, an incident is open — and policy decides what that is worth.
//
// State says whether a window is open. That is evidence about the external
// system, not about access: a confirmed window becomes a grant the engine reads
// like any other (M10), and only a policy rule decides what a grant permits.
type AccessEvidence struct {
	// State is the answer: WindowConfirmed or WindowNotConfirmed. A provider
	// that could not find out returns an error instead (see
	// AccessContextProvider), and Control records the state as
	// WindowUndetermined.
	State WindowState
	// Reference is the external system's identifier for what it asserted:
	// the ticket, the scan, the incident. It is recorded on the grant, copied
	// into every session record (`grant_context.reference`) and shown by
	// explain, so it must be the identifier a human can look up on the other
	// side. Required with WindowConfirmed: a window nobody can name cannot be
	// explained, and Control reads a confirmation without one as malformed.
	// When the query named a pushed window, Reference is that window's
	// reference or empty — a confirmation of some other window is malformed.
	Reference string
	// Assertions are further facts the external system stated, as stable
	// codes and values. They are recorded with the decision so an auditor
	// can see what was said; nothing reads them to decide.
	Assertions map[string]string
	// Window is the period the external system asserted. Control applies its
	// own ceiling to it regardless of what is claimed (M16), measured from
	// the instant of the decision, and an empty NotAfter is not "forever".
	// A window whose NotBefore is still in the future is not open now.
	Window Window
	// AdditionalContext is whatever else the external system wanted
	// recorded about the window: a JSON string or a JSON object, verbatim,
	// and nothing else — a number, a list or a boolean is a contract
	// violation rather than something to coerce, because it is copied into
	// every session record for an auditor (`grant_context.additional_context`).
	// Empty means none.
	AdditionalContext json.RawMessage
	// ObservedAt is when the provider learned this. A cached answer carries
	// the instant it was fetched, not the instant it was replayed, so that a
	// stale probe is visible as stale rather than as fresh.
	ObservedAt time.Time
	// TTL is how long Control may reuse this answer for the same access
	// rather than ask again, because the same scan is asked about once per
	// connection and a scanner opens many. Zero means do not reuse it.
	// Control caps it at its own maximum, never reuses a confirmation past
	// the end of its window, and never caches an answer it could not get.
	TTL time.Duration
	// Cached reports that this answer came from a cache rather than from a
	// live call. Control sets it when it replays an answer of its own.
	Cached bool
}

// AccessContextPush is one inbound push, handed to the provider it names after
// Control has authenticated the caller, checked it may push for that provider,
// rate-limited it and bounded its size.
//
// Reading it is the provider's whole job. Everything that makes a push safe to
// act on — the integration's scope binding, replay and idempotency, clock
// skew, the server-side ceiling — belongs to Control and happens after
// Interpret returns (M16). A customer-written provider gets those for free
// precisely because they are not its job.
type AccessContextPush struct {
	// Tenant is the tenant the push was made in, resolved from the caller's
	// credential (M18) — never from the body.
	Tenant Tenant
	// ContentType is the media type the caller declared.
	ContentType string
	// Body is the request body, verbatim.
	Body []byte
	// ReceivedAt is when Control received it.
	ReceivedAt time.Time
}

// WindowAssertion is what one push asserts: that a window has opened for one
// subject on some targets. It is a claim, not a grant. Control turns it into a
// grant only after the integration's scope binding admits every part of it.
type WindowAssertion struct {
	// ID identifies this assertion in the external system, and it is what
	// makes a push idempotent: the same ID twice is the same window, not two,
	// and a revoked window stays revoked however often it is pushed again.
	// Required.
	ID string
	// Reference is the ticket, scan or incident the window belongs to — the
	// identifier a human looks up on the other side. Empty means ID.
	Reference string
	// Subject is who the window is for: the subject id the proxy sends,
	// which is what a decision looks grants up by. Required.
	Subject string
	// Targets are the hosts it names, each an exact hostname or a single
	// leading wildcard (`*.db.example.com`). At least one: a window that names
	// no host is one nobody can audit.
	Targets []string
	// Scope is the grant scope the window asks for. Empty means the scope
	// the integration is bound to; any other value is outside the binding and
	// is refused as an escalation attempt.
	Scope string
	// Window is the period asserted. NotAfter is required: a push has no
	// later chance to say when the window ends, and Control will not invent
	// one. A zero NotBefore means "from now".
	Window Window
	// IssuedAt is when the external system made the assertion, when it says.
	// Control refuses one older than it accepts, or further in the future
	// than the clock skew it tolerates — an old assertion replayed under a
	// new id is the replay an id alone cannot catch. Zero skips that check.
	IssuedAt time.Time
	// AdditionalContext is whatever else the external system wanted
	// recorded: a JSON string or a JSON object, verbatim, and nothing else.
	// Empty means none.
	AdditionalContext json.RawMessage
}

// AccessContextProvider supplies evidence from a system outside Hoplock about
// whether an access is legitimate right now (M16): a vulnerability scan is
// running against this host, a change ticket is approved and inside its window,
// an incident is open. What varies is which system an organisation keeps that
// truth in, and there is no end to that list — which is why the seam exists and
// why Control's own default is configured rather than coded.
//
// It returns evidence, not a verdict. See AccessEvidence.
//
// When no implementation is registered, no external access context is consulted
// and no window is opened by one. Everything else about the decision is
// unchanged: policy still evaluates, grants still apply, and a deployment that
// integrates nothing is not missing a feature it was promised.
//
// THE TWO DIRECTIONS (M16), and they are not redundant:
//
//   - Push: the external system tells Control that a window has opened, and
//     Interpret reads what it sent into a WindowAssertion. Control checks it
//     against the integration's scope binding — which subjects it may grant
//     to, which targets it may name, the longest window it may open, whether
//     it may open privileged access at all — and refuses anything outside it
//     as an attempted privilege escalation, audited as one. What it admits is
//     a grant with origin `external`, created and audited like any other.
//   - Probe: Control asks the external system, on the authorize path, whether
//     a window is open for this subject on this target now. A confirmed
//     window counts as a grant for that decision; with a push in front of it
//     (the default composition), a pushed window counts only while its probe
//     confirms it.
//
// THE THREE ANSWERS, and how each is given:
//
//   - Confirmed or not confirmed: return evidence with that State and a nil
//     error. ErrNoEvidence, kept from before State existed, is read as
//     WindowNotConfirmed: an external system with nothing to say about this
//     access has confirmed no window.
//   - Could not determine: return an error. KindUnavailable says the external
//     system could not be reached, refused Control, or timed out — wrap
//     context.DeadlineExceeded when it is a timeout, so the record can say so
//     — and KindMalformed says it answered and the answer made no sense. The
//     decision record tells the two apart, because one is a network and the
//     other is a bug. Anything else is read as a failure of the provider. A
//     nil error with WindowUndetermined, or a confirmation with no Reference,
//     is read as malformed.
//
// Interpret answers in the same vocabulary: KindMalformed or KindInvalid when
// the push cannot be read (the caller is told so), KindDisabled when this
// provider takes no pushes, and anything else is an outage the caller may
// retry.
//
// Probe runs on the authorize path, inside the latency budget M5 governs. The
// context carries the deadline for the probe phase's share of it; a provider
// that has not answered by then is abandoned — its answer, if it ever comes, is
// discarded — and the access is recorded as undetermined. A provider that
// ignores its context does not hold a user's handshake open; it only wastes its
// own goroutine, and Control bounds how many of those it will start.
//
// Control itself ships the declarative HTTP provider (phase 0013): a probe URL,
// its authentication, a request template, assertions over the response, a TTL,
// and a field mapping for pushes, configured rather than coded. Each configured
// integration is registered as its own provider, named
// `hoplock/control/declarative/<name>`, so the registry listing shows one row
// per system. That is not a placeholder standing in for the real thing: it is
// how a self-hosting customer integrates a scanner or an ITSM system nobody has
// heard of, without writing Go. Packaged vendor-specific integrations are
// Hoplock Enterprise's, and what they add is packaging and support rather than
// capability (M15).
type AccessContextProvider interface {
	// Describe names the provider and the directions it implements. It is
	// called once, before the server starts, and its answer must not change
	// afterwards.
	Describe() AccessContextInfo
	// Probe asks about one access.
	Probe(ctx context.Context, q AccessContextQuery) (AccessEvidence, error)
	// Interpret reads one push into the window it asserts.
	Interpret(ctx context.Context, p AccessContextPush) (WindowAssertion, error)
}
