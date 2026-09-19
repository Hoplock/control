// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// Input assembly: the composition root's first half.
//
// Everything a rule may match on is gathered HERE, once, from this server's own
// records — and the one thing it is never gathered from is the request's claim
// about who is connecting. A proxy relays a login and a key; this server
// decided what that resolves to at `/v1/auth/*` (0007) and it decides again
// here rather than reading the answer back off the wire. The rule is the same
// one that keeps `conn.hop_trail` safe to accept: what a caller sends may
// narrow a decision and may never widen one.

// Subjects resolves a subject id into the identity policy matches on.
//
// It is a narrow interface rather than the store so that 0011's federated
// directory drops in where the local one is today: `identity.Directory`
// satisfies it as written.
type Subjects interface {
	// SubjectByID resolves a subject id. Absent is store.ErrNotFound.
	SubjectByID(ctx context.Context, tenant store.Tenant, subjectID string) (store.Subject, error)
}

// assembled is one request's inputs plus what the rest of the phase needs and
// the engine deliberately does not take.
type assembled struct {
	input model.Input
	// targetID is the row the decision record is filed against, empty for a
	// target this server holds no record of.
	targetID string
	// targetZone is where the fleet must route to.
	targetZone fleet.Zone
	// known reports whether a target row was found. A target with no row is
	// not an error and not a denial: policy decides, and an unknown target
	// simply has no labels and no zone to match on or route to.
	known bool
	// trail is `conn.hop_trail`, kept beside the engine's input rather than
	// inside it. It is a routing input and never a matching axis (PLAN
	// §5.3), and the decision record stores it because "which hop asked,
	// and what had it already been through" is unrecoverable afterwards.
	trail []string
	// subjectKnown reports whether this server holds a record for the
	// subject. A subject it does not know contributes no groups and no
	// claims — see [Service.assemble].
	subjectKnown bool
}

// assemble gathers the inputs for one authorize call.
func (s *Service) assemble(ctx context.Context, tenant store.Tenant, req *contract.AuthorizeRequest, now time.Time) (assembled, error) {
	out := assembled{
		input: model.Input{
			Now: now,
			Subject: model.Subject{
				ID:         req.Identity.Subject,
				Source:     req.Identity.Source,
				AuthMethod: authMethod(req.AuthMethod),
				// A second factor is not a field on this request, and it
				// does not need to be: `password-mfa` IS the flow that
				// used one, and `cert` is the flow that did not. Reading
				// it off the method keeps the two answers from drifting,
				// which they would the moment a caller could send both.
				MFA: req.AuthMethod == contract.AuthMethodPasswordMFA,
			},
			Context: model.Context{
				ProxyID:    req.Conn.ProxyID,
				SourceAddr: clientAddr(req.Conn.ClientAddr),
			},
			Target: model.Target{
				Hostname: req.Target,
				Port:     req.TargetPort,
			},
			// Device posture has no field on this request, so there is
			// none to assemble. A rule that requires posture therefore
			// cannot match, which is the fail-safe direction: the axis
			// arrives with the endpoint integration that can actually
			// assert it (M16), not with a value invented here.
			Device: nil,
		},
		trail: append([]string(nil), req.Conn.HopTrail...),
	}

	subject, err := s.subjects.SubjectByID(ctx, tenant, req.Identity.Subject)
	switch {
	case err == nil:
		out.subjectKnown = true
		out.input.Subject.Source = subject.Source
		out.input.Subject.Groups = subject.Groups
		out.input.Subject.Claims = subject.Claims
	case store.IsNotFound(err):
		// A subject this server holds no record of gets NO groups and NO
		// claims — not the ones the request carries. The request's copy
		// came from this server originally, but by the time it comes back
		// it is a value the caller controls, and a group read off the wire
		// is a group a compromised proxy can award itself. What survives is
		// the subject id, which names who the rest of the decision is
		// about and grants nothing on its own.
		out.input.Subject.Groups = nil
		out.input.Subject.Claims = nil
	default:
		return assembled{}, err
	}

	target, err := s.store.Targets().GetByHostname(ctx, tenant, req.Target)
	switch {
	case err == nil:
		out.known = true
		out.targetID = target.ID
		out.targetZone = fleet.Zone(target.Zone)
		out.input.Target.Zone = target.Zone
		out.input.Target.Labels = target.Labels
	case store.IsNotFound(err):
		// Not an error: a target with no row has no labels and no zone.
		// Policy decides what that means — in practice the default-deny —
		// and the routing layer will refuse to invent a zone for it.
	default:
		return assembled{}, err
	}

	grants, err := s.store.Grants().ListLive(ctx, tenant, req.Identity.Subject, now)
	if err != nil {
		return assembled{}, err
	}
	out.input.Grants = liveGrants(grants)

	return out, nil
}

// liveGrants converts stored grants into the engine's input vocabulary.
//
// The store's grant is deliberately thinner than the engine's: 0012 owns the
// scope model and 0013 the external context (M16), so what is mapped here is
// what the columns actually hold. A field this server does not have is left
// zero rather than guessed, because a guessed scope is a grant covering more
// than anybody authored.
func liveGrants(rows []store.Grant) []model.Grant {
	if len(rows) == 0 {
		return nil
	}
	out := make([]model.Grant, 0, len(rows))
	for _, g := range rows {
		grant := model.Grant{
			ID:         g.ID,
			Subject:    g.SubjectID,
			Origin:     grantOrigin(g.Origin),
			Scope:      model.GrantScope{Name: g.Scope},
			NotBefore:  g.NotBefore,
			ExpiresAt:  g.ExpiresAt,
			RequestRef: g.ApprovalRef,
		}
		if g.ExternalRef != "" {
			// The reference without the system it belongs to is what the
			// column holds today. 0013 widens it; inventing a system name
			// here would put a value in an audit record that no external
			// system ever asserted.
			grant.External = &model.ExternalReference{Reference: g.ExternalRef}
		}
		out = append(out, grant)
	}
	return out
}

// grantOrigin maps the stored origin onto the engine's.
//
// The two vocabularies do not spell the first one the same way — the column
// says `manual` and a rule matches on `administrator` — so this is a mapping
// and never a cast. A cast would compile, produce an origin no rule can match,
// and silently stop every `grant.origins` constraint from ever firing.
func grantOrigin(o store.GrantOrigin) model.GrantOrigin {
	switch o {
	case store.GrantOriginManual:
		return model.GrantOriginAdministrator
	case store.GrantOriginWorkflow:
		return model.GrantOriginWorkflow
	case store.GrantOriginExternal:
		return model.GrantOriginExternal
	default:
		// An origin this build does not know is left unset rather than
		// passed through: a rule constraining origins must not be
		// satisfied by a value neither side understands.
		return ""
	}
}

// authMethod maps the wire's authentication method onto the engine's.
//
// An unknown value maps to the unset method rather than to a default. A rule
// that constrains the method then cannot match it, which is the fail-safe
// direction: an unrecognised method must not satisfy a constraint that names
// a recognised one.
func authMethod(m contract.AuthMethod) model.AuthMethod {
	switch m {
	case contract.AuthMethodCert:
		return model.AuthMethodCert
	case contract.AuthMethodPasswordMFA:
		return model.AuthMethodPasswordMFA
	default:
		return model.AuthMethodUnset
	}
}

// clientAddr parses `conn.client_addr`, which the contract gives as `host:port`.
//
// An address this server cannot parse yields the zero Addr, which a rule
// constraining the source network cannot match (0005). That is deliberate: a
// source constraint that fell back to "matches anything" would be a network
// restriction silently removed.
func clientAddr(s string) netip.Addr {
	if s == "" {
		return netip.Addr{}
	}
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		// Some callers send a bare address. It is worth trying, because
		// the alternative is discarding a usable source network over a
		// missing port.
		host = s
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}
