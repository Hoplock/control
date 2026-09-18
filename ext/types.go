// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import "time"

// Tenant is the tenant a call is made on behalf of. Every operation across
// this seam carries one, for the same reason every store method does (M12,
// M18): tenancy is a dimension of the request, not a property of the process,
// and an extension that forgets it is an extension that can leak one
// customer's estate into another's export.
//
// It is a distinct type from the storage layer's tenant identifier on purpose.
// This package names no internal type, because Hoplock Enterprise builds
// against it and every type it borrows becomes something this repository
// cannot change without breaking that build.
type Tenant string

// Severity grades how much an operator should care. It is a closed enum so a
// switch over it can be checked for exhaustiveness.
type Severity int

const (
	// SeverityInfo is something that happened and was expected.
	SeverityInfo Severity = iota
	// SeverityWarning is something that may need attention and has not
	// stopped anything.
	SeverityWarning
	// SeverityError is something that failed.
	SeverityError
	// SeverityCritical is something that needs somebody now.
	SeverityCritical
)

// String renders the severity as the stable code that crosses the wire.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	case SeverityCritical:
		return "critical"
	}
	return "info"
}

// Window is a period an external assertion or a grant is valid for. Both ends
// are explicit: an extension that returns a duration instead forces every
// caller to decide what it is a duration from, and they will not all decide
// the same thing.
type Window struct {
	// NotBefore is when the window opens. A zero value means "already open".
	NotBefore time.Time
	// NotAfter is when it closes. A zero value means the asserting system
	// stated no end, which Control treats as the shortest thing it is allowed
	// to treat it as — never as "forever" (M16's server-side ceiling).
	NotAfter time.Time
}

// Contains reports whether at falls inside the window.
func (w Window) Contains(at time.Time) bool {
	if !w.NotBefore.IsZero() && at.Before(w.NotBefore) {
		return false
	}
	if !w.NotAfter.IsZero() && !at.Before(w.NotAfter) {
		return false
	}
	return true
}

// Subject identifies who is asking. It carries the identifiers Control already
// knows rather than a whole identity record, because an extension that needs
// more should ask Control for it rather than have it pushed at every call.
type Subject struct {
	// ID is Control's own identifier for the subject.
	ID string
	// ExternalID is the identifier the identity provider uses, when there is
	// one.
	ExternalID string
	// Username is the login name.
	Username string
	// Groups are the group names the subject resolved to.
	Groups []string
}

// Target identifies what is being reached.
type Target struct {
	// ID is Control's own identifier for the target.
	ID string
	// Hostname is the name the user typed.
	Hostname string
	// Zone is the fleet zone the target sits in (M6).
	Zone string
	// Labels are the target's policy-visible labels.
	Labels map[string]string
}

// A note on prose, which applies to every type in this package that carries
// text. Everything beneath the console stores and transmits codes, and the
// console holds the string catalogues (M21). So the fields that cross this
// seam are stable codes and structured attributes; where a human-readable line
// appears it is an untranslated operator diagnostic, and an implementation
// that renders for an end user renders from the code and the attributes.
