// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"encoding/json"
	"time"
)

// MFA orchestration is entirely this server's concern: the proxy relays the
// password and polls, and never contacts a provider itself (PLAN §6). What is
// below is the seam a provider sits behind; what is in service.go is the part
// that is NOT a provider's business and must not be delegated to one —
// challenge lifetime, poll-rate enforcement, single use, and expiry as a deny.

// MFAResult is where a provider says a challenge has got to. Closed set, so a
// caller switching on it is checked for exhaustiveness (M13) — a fourth answer
// must be decided about rather than fall into a default branch.
type MFAResult string

const (
	// MFAPending means the user has not answered yet.
	MFAPending MFAResult = "pending"
	// MFAApproved means they approved it.
	MFAApproved MFAResult = "approved"
	// MFARefused means they refused it, or the provider did.
	MFARefused MFAResult = "refused"
)

// MFATerms are the terms a provider sets when it starts a challenge.
//
// The provider proposes; [Service] disposes — it clamps the TTL and the poll
// interval to the bounds it was configured with, because a provider that
// returned a 24-hour TTL would otherwise hold a connection open for a day.
type MFATerms struct {
	// Ref is the provider's own handle on the out-of-band factor, stored
	// opaquely and handed back on every poll.
	Ref string
	// Prompt is shown to the user by the proxy, so it must be safe to
	// disclose.
	Prompt string
	// PollAfter is how often the provider is willing to be asked. Zero
	// takes the service's default.
	PollAfter time.Duration
	// TTL is how long the provider believes the factor is good for. Zero
	// takes the service's default.
	TTL time.Duration
}

// MFAProvider delivers a second factor out of band.
//
// It knows nothing about tokens, expiry or replay: those are the orchestrator's
// and are the same whichever provider is in play. A provider's whole job is
// "start one" and "how is it going".
//
// 0011 implements a real one (push, TOTP, an IdP's own step-up). What ships
// here is [ScriptedMFA], which is deterministic on purpose — the proxy's mock
// models MFA with a pending-polls counter and the conformance suite depends on
// that behaviour being reproducible on this side.
type MFAProvider interface {
	// Name is the value stored in `subject_mfa.provider` and on every
	// challenge, so a challenge issued by one provider is never polled
	// against another.
	Name() string
	// Begin starts a challenge for an identity. The config is the
	// subject's enrollment row, opaque to everything above this interface.
	Begin(ctx context.Context, id Identity, config json.RawMessage) (MFATerms, error)
	// Poll reports where the challenge has got to. `polls` is how many
	// times this challenge has been polled INCLUDING this one, which is
	// what lets a deterministic provider be a pure function of its config.
	//
	// An error is an OUTAGE — the provider could not be asked — and never a
	// refusal. A refusal is MFARefused (M11 one layer down).
	Poll(ctx context.Context, config json.RawMessage, ref string, polls int) (MFAResult, error)
}
