// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"time"
)

// PolicyBundleRef identifies one version of a policy bundle.
type PolicyBundleRef struct {
	// Tenant owns the bundle.
	Tenant Tenant
	// Version is the bundle's version, assigned by Control.
	Version string
	// Digest is the content hash, so a validator can be certain it is
	// looking at the bytes that will be activated.
	Digest string
}

// PolicyDocument is a bundle as authored, handed to a validator before it is
// compiled and activated. It is the source rather than the compiled program,
// because a governance rule is written about what an author wrote.
type PolicyDocument struct {
	// Ref identifies the bundle.
	Ref PolicyBundleRef
	// Source is the bundle's bytes.
	Source []byte
	// ContentType is the media type of Source.
	ContentType string
	// AuthorID is the subject proposing the bundle.
	AuthorID string
	// ProposedAt is when they proposed it.
	ProposedAt time.Time
	// ActiveVersion is the version currently serving, empty for a
	// deployment's first bundle. A validator comparing proposed against
	// active — "this removes the rule that required approval for production"
	// — needs to know what it is replacing.
	ActiveVersion string
}

// FindingSeverity grades a validator's finding. It is a closed enum, and the
// distinction that matters is whether activation may proceed.
type FindingSeverity int

const (
	// FindingAdvice is worth telling the author and blocks nothing.
	FindingAdvice FindingSeverity = iota
	// FindingWarning is a likely mistake and blocks nothing.
	FindingWarning
	// FindingBlocking stops activation. It is the only severity that does.
	FindingBlocking
)

// String renders the severity as the stable code that crosses the wire.
func (s FindingSeverity) String() string {
	switch s {
	case FindingAdvice:
		return "advice"
	case FindingWarning:
		return "warning"
	case FindingBlocking:
		return "blocking"
	}
	return "advice"
}

// Finding is one thing a validator has to say about a bundle.
type Finding struct {
	// Severity grades it; only FindingBlocking stops activation.
	Severity FindingSeverity
	// Code is the finding's stable code, which the console renders (M21)
	// and automation matches on.
	Code string
	// Path locates the finding inside the bundle — a rule name, a document
	// pointer. A finding an author cannot locate is a finding they cannot
	// act on.
	Path string
	// Detail carries the finding's structured fields.
	Detail map[string]string
	// Message is an untranslated operator diagnostic. It is a convenience,
	// not the finding: Code is the thing anything else keys on.
	Message string
}

// Blocking reports whether this finding stops activation.
func (f Finding) Blocking() bool { return f.Severity == FindingBlocking }

// PolicyValidator adds checks a policy bundle must pass before it may be
// activated. What varies is governance: a rule that production targets may
// never be reached without an approval obligation, that a bundle touching a
// regulated zone needs a second author, that a named label may not be removed.
// None of those are statements about whether the policy is *valid* — they are
// statements about whether this organisation permits it — which is why they
// sit beside the compiler rather than inside it.
//
// Validators form a chain, and every registered validator runs on every
// proposed bundle: a chain that stops at the first blocking finding tells an
// author one problem at a time, and they deserve the whole list. Any blocking
// finding stops activation, whoever produced it.
//
// When no implementation is registered, the policy compiler's own checks are
// the whole of validation, and they always run. Those checks are not a
// formality: the input and output vocabularies are closed, so an unreachable
// rule, a contradiction, or a reference to something that does not exist is a
// compile error rather than a runtime surprise (M3). A validator adds rules
// about who may change what; it never becomes the only thing standing between
// a bad bundle and the fleet.
//
// Validate must be pure with respect to the bundle: it may read Control's state
// through whatever the implementation was constructed with, but it must not
// change the document or activate anything. Returning an error is an outage,
// not a rejection — a rejection is a blocking Finding, and the difference
// decides whether an author sees "your bundle was refused because" or
// "validation is down".
type PolicyValidator interface {
	// Validate returns every finding the validator has about the document.
	// No findings means it has nothing to say.
	Validate(ctx context.Context, doc PolicyDocument) ([]Finding, error)
}
