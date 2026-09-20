// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke

import (
	"errors"
	"fmt"

	"github.com/hoplock/control/internal/contract"
)

// The publication vocabulary: what an operator may ask this server to say, and
// to whom.
//
// It is a domain vocabulary rather than the wire shapes, for the reason every
// other package here keeps one: a contract revision lands in the translation
// and stops. What it adds beyond translation is the VALIDATION — the rules
// below are the ones a `cache_invalidate` or a `session_kill` is wrong without,
// and they are enforced at the point of publication because an event that is
// already on the wire cannot be taken back.

// Errors a publication is refused with. Every one of them is an operator
// mistake rather than a failure of this server, so they are returned rather
// than logged and swallowed: a revocation that silently did nothing is the
// failure this package exists to prevent.
var (
	// ErrNoAudience is a publication addressed to nobody.
	ErrNoAudience = errors.New("revoke: an event must be addressed to at least one proxy, or to all of them")
	// ErrNoSelector is a payload that selects nothing.
	ErrNoSelector = errors.New("revoke: exactly one selector is required")
	// ErrNoReason is a session_kill with no reason.
	//
	// The reason is SHOWN TO THE USER before their connection closes, so a
	// revoked session is never mistaken for a crash or a network fault. It
	// is refused here rather than defaulted, because a generic message this
	// server invented would be indistinguishable to the operator from one
	// they wrote.
	ErrNoReason = errors.New("revoke: a session_kill must carry a reason, which is shown to the user")
	// ErrClosed is a publication into a bus that has been drained.
	ErrClosed = errors.New("revoke: the event bus is closed")
)

// Audience selects the SUBSCRIBERS an event reaches. It is a different
// question from what the event then does, which the payload selects.
//
// The two are separate on purpose: "end every session for Alice" does not
// require the operator to know where Alice is, so it goes to every proxy with
// `subject` set in the payload and each proxy decides what it holds. Addressing
// is the delivery question; the payload is the effect.
type Audience struct {
	// ProxyIDs are the proxies to reach. Ignored when All is set.
	ProxyIDs []string
	// All addresses every subscriber in the tenant, INCLUDING ones that
	// connect later and replay into it.
	All bool
}

// ToProxy addresses one proxy.
func ToProxy(proxyID string) Audience { return Audience{ProxyIDs: []string{proxyID}} }

// ToProxies addresses a named set.
func ToProxies(proxyIDs ...string) Audience { return Audience{ProxyIDs: proxyIDs} }

// Everyone addresses every subscriber in the tenant.
func Everyone() Audience { return Audience{All: true} }

// reaches reports whether this audience includes a proxy id.
func (a Audience) reaches(proxyID string) bool {
	if a.All {
		return true
	}
	for _, id := range a.ProxyIDs {
		if id == proxyID {
			return true
		}
	}
	return false
}

func (a Audience) validate() error {
	if a.All {
		return nil
	}
	for _, id := range a.ProxyIDs {
		if id != "" {
			return nil
		}
	}
	return ErrNoAudience
}

// String renders an audience for a log line.
func (a Audience) String() string {
	if a.All {
		return "all"
	}
	return fmt.Sprintf("%v", a.ProxyIDs)
}

// Kill ends sessions that are already in flight. Exactly one of SessionIDs,
// Subject and All selects what dies.
type Kill struct {
	SessionIDs []string
	Subject    string
	All        bool
	// Reason is shown to the user verbatim, so it must be safe to disclose:
	// no policy internals, no other users, no infrastructure detail. It is
	// required — see [ErrNoReason].
	Reason string
}

func (k Kill) validate() error {
	if selectors(len(k.SessionIDs) > 0, k.Subject != "", k.All) != 1 {
		return fmt.Errorf("%w: session_kill takes exactly one of session_ids, subject or all", ErrNoSelector)
	}
	if k.Reason == "" {
		return ErrNoReason
	}
	return nil
}

func (k Kill) payload() *contract.SessionKillEvent {
	return &contract.SessionKillEvent{
		SessionIDs: k.SessionIDs,
		Subject:    k.Subject,
		All:        k.All,
		Reason:     k.Reason,
	}
}

// Invalidation drops cached decisions without touching running sessions.
// Exactly one of Keys, Subject and All selects what goes.
type Invalidation struct {
	// Keys are cache keys exactly as this server issued them.
	Keys []string
	// Subject drops every decision cached FOR A PERSON — which is not every
	// decision the proxy holds. See [Invalidation.ReachesHostKeyDecisions].
	Subject string
	// All drops the proxy's entire decision cache.
	All bool
}

func (i Invalidation) validate() error {
	if selectors(len(i.Keys) > 0, i.Subject != "", i.All) != 1 {
		return fmt.Errorf("%w: cache_invalidate takes exactly one of keys, subject or all", ErrNoSelector)
	}
	return nil
}

func (i Invalidation) payload() *contract.CacheInvalidateEvent {
	return &contract.CacheInvalidateEvent{Keys: i.Keys, Subject: i.Subject, All: i.All}
}

// ReachesHostKeyDecisions reports whether this invalidation can withdraw a
// HOST-KEY decision, and the answer is no for a subject-scoped one.
//
// THIS ASYMMETRY IS THE OPERATOR SURFACE, not a detail of the wire format. The
// proxy keys a host-key decision on target, port and key fingerprint — not on a
// person (PLAN §5.4) — so `subject` cannot match one. An operator who publishes
// "invalidate everything for Alice" and believes a target's host key was
// withdrawn by it has been misled by this server, and a revocation that
// silently misses is worse than one that refuses. So every publication says
// what it covered on its [Receipt], and an operator withdrawing a host-key
// decision addresses that decision's own key — the one stored beside the
// host-key record by `fleet` — or publishes `resync`.
func (i Invalidation) ReachesHostKeyDecisions() bool { return i.All || len(i.Keys) > 0 }

// selectors counts how many of a payload's mutually exclusive selectors are set.
func selectors(set ...bool) int {
	n := 0
	for _, s := range set {
		if s {
			n++
		}
	}
	return n
}
