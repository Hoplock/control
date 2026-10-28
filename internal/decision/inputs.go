// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/accessctx"
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

// Subjects resolves a subject id into the identity policy matches on, and into
// the standing denials on the id.
//
// It is a narrow interface rather than the store so that a federated directory
// drops in where the local one is today. It answers in ONE round trip (M5): the
// subject's row, if there is one, and every standing denial on the id, if any
// stands (M28). An id with neither is not an error — a lock holds for a subject
// this server has no row for.
type Subjects interface {
	SubjectStanding(ctx context.Context, tenant store.Tenant, subjectID string) (store.SubjectStanding, error)
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
	// mappingVersion and breakGlass are read off the subject row and go
	// straight into the decision record (M7). They are NOT policy inputs:
	// the engine's vocabulary is closed (M3) and adding an axis is 0005's to
	// do, not a side effect of wiring federation. What they are is the two
	// facts a reader of the record needs in order to know what produced the
	// attributes it matched on, and whether the credential behind it was a
	// break-glass one.
	mappingVersion int
	breakGlass     bool
	// grants are the live grant rows the engine's grant input was made from.
	// The record renders them rather than the engine's copy because a
	// record also names what the engine does not read — who created a grant
	// and why — and a record that has to be joined to the grant table to
	// say so is only as durable as that table (M4).
	//
	// With external context (M16) they are the grants the engine COUNTED:
	// a pushed window awaiting its probe is here only once the probe
	// confirmed it, and a window only a probe asserted is here as the grant
	// built for this decision.
	grants []store.Grant
	// external is every external window this decision asked about, for the
	// record.
	external []accessctx.Entry
	// standing are the standing denials that cover this call (M28): a lock on
	// the subject, a quarantine on the target. While any stands the call is a
	// deny whatever policy would have said, and the engine is not asked. Each
	// is read in the round trip that reads the row it covers.
	standing []standingDenial
}

// The reason codes a standing denial carries into the decision record, the
// deny and `explain`. They are stable: a report groups by them.
const (
	// CodeSubjectLocked is a deny because a lock stands on the subject.
	CodeSubjectLocked = "subject_locked"
	// CodeTargetQuarantined is a deny because a quarantine stands on the
	// target.
	CodeTargetQuarantined = "target_quarantined"
	// CodeSubjectDeactivated is a deny because the directory deactivated the
	// subject (0040). Only the directory lifts it.
	CodeSubjectDeactivated = "subject_deactivated"
)

// standingDenial is one standing denial as the decision recorded it.
type standingDenial struct {
	code        string
	subjectID   string
	targetID    string
	placedAt    time.Time
	placedBy    store.StandingActor
	reasonCode  string
	reason      string
	externalRef string
	actionID    string
}

func lockedDenial(l store.SubjectLock) standingDenial {
	return standingDenial{
		code: CodeSubjectLocked, subjectID: l.SubjectID, placedAt: l.LockedAt, placedBy: l.LockedBy,
		reasonCode: l.ReasonCode, reason: l.Reason, externalRef: l.ExternalRef, actionID: l.ActionID,
	}
}

// deactivatedDenial is the directory's deactivation as the decision records it:
// placed by the sync, with the directory's reason code, and the directory's id
// for the person as the external reference that ties the denial to its cause.
func deactivatedDenial(subjectID string, d store.SubjectDirectory) standingDenial {
	return standingDenial{
		code: CodeSubjectDeactivated, subjectID: subjectID, placedAt: d.DeactivatedAt,
		placedBy:   d.DeactivatedBy(),
		reasonCode: d.DeactivationReason, reason: access.DeactivatedByDirectoryReason, externalRef: d.ExternalID,
	}
}

func quarantinedDenial(q store.TargetQuarantine) standingDenial {
	return standingDenial{
		code: CodeTargetQuarantined, targetID: q.TargetID, placedAt: q.QuarantinedAt, placedBy: q.QuarantinedBy,
		reasonCode: q.ReasonCode, reason: q.Reason, externalRef: q.ExternalRef, actionID: q.ActionID,
	}
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

	// The subject's row, the lock on the id and the directory's deactivation,
	// in ONE round trip (M5, M28).
	standing, err := s.subjects.SubjectStanding(ctx, tenant, req.Identity.Subject)
	if err != nil {
		return assembled{}, err
	}
	if standing.Known {
		subject := standing.Subject
		out.subjectKnown = true
		out.input.Subject.Source = subject.Source
		out.input.Subject.Groups = subject.Groups
		out.input.Subject.Claims = subject.Claims
		out.mappingVersion = subject.MappingVersion
		out.breakGlass = subject.BreakGlass
	} else {
		// A subject this server holds no record of gets NO groups and NO
		// claims — not the ones the request carries. The request's copy
		// came from this server originally, but by the time it comes back
		// it is a value the caller controls, and a group read off the wire
		// is a group a compromised proxy can award itself. What survives is
		// the subject id, which names who the rest of the decision is
		// about and grants nothing on its own.
		out.input.Subject.Groups = nil
		out.input.Subject.Claims = nil
	}
	if standing.Lock != nil {
		out.standing = append(out.standing, lockedDenial(*standing.Lock))
	}
	if standing.Deactivated() {
		// A second standing denial of its own: the directory placed it and
		// only the directory lifts it, so an operator's unlock does not
		// re-admit the person and a re-activation does not lift a lock.
		out.standing = append(out.standing, deactivatedDenial(req.Identity.Subject, *standing.Directory))
	}

	// The target's row and the quarantine on it, likewise in one round trip.
	target, err := s.store.Targets().StandingByHostname(ctx, tenant, req.Target)
	switch {
	case err == nil:
		out.known = true
		out.targetID = target.Target.ID
		out.targetZone = fleet.Zone(target.Target.Zone)
		out.input.Target.Zone = target.Target.Zone
		out.input.Target.Labels = target.Target.Labels
		if target.Quarantine != nil {
			out.standing = append(out.standing, quarantinedDenial(*target.Quarantine))
		}
	case store.IsNotFound(err):
		// Not an error: a target with no row has no labels and no zone.
		// Policy decides what that means — in practice the default-deny —
		// and the routing layer will refuse to invent a zone for it.
	default:
		return assembled{}, err
	}

	if len(out.standing) > 0 {
		// A standing denial decides, and the engine is not asked: the grants
		// are not read, because nothing they say can matter, and the call
		// costs the same two round trips an unlocked one spends before it.
		return out, nil
	}

	// Live grants are read at THIS call's instant, and that read is the whole
	// of expiry (M10, 0012): a grant whose window has closed is not returned,
	// whether or not anything has run since it closed.
	grants, err := s.store.Grants().ListLive(ctx, tenant, req.Identity.Subject, now)
	if err != nil {
		return assembled{}, err
	}
	out.grants = grants
	out.input.Grants = access.PolicyGrants(grants)

	return out, nil
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
