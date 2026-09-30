// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"encoding/json"

	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// A grant as the policy engine reads it (M10).
//
// THIS IS THE WHOLE TRANSLATION, and it has no branch on origin that changes
// what a grant grants. The origin is copied across so a rule MAY constrain it
// (`grant.origins`) and so the decision record can name it; the scope, the
// window and the provenance are copied the same way whichever of the three
// paths produced the grant. That is what keeps simulation and "explain why"
// telling one story for an administrator's grant, a workflow's and an external
// system's — and it is asserted by a test rather than promised by a comment.

// PolicyGrants converts stored grants into the engine's input vocabulary.
func PolicyGrants(rows []store.Grant) []model.Grant {
	if len(rows) == 0 {
		return nil
	}
	out := make([]model.Grant, 0, len(rows))
	for _, g := range rows {
		out = append(out, PolicyGrant(g))
	}
	return out
}

// PolicyGrant converts one stored grant.
//
// A field this server does not hold is left zero rather than guessed, because a
// guessed scope is a grant covering more than anybody authored.
func PolicyGrant(g store.Grant) model.Grant {
	grant := model.Grant{
		ID:      g.ID,
		Subject: g.SubjectID,
		Origin:  PolicyOrigin(g.Origin),
		Scope: model.GrantScope{
			Name:    g.Scope,
			Targets: g.ScopeTargets,
			Labels:  g.ScopeLabels,
			Zones:   g.ScopeZones,
		},
		NotBefore:  g.NotBefore,
		ExpiresAt:  g.ExpiresAt,
		Approvers:  g.Approvers,
		RequestRef: g.RequestID,
	}
	if g.ExternalRef != "" || g.External.System != "" || !g.External.WindowStart.IsZero() ||
		!g.External.WindowEnd.IsZero() || g.External.AdditionalKind != "" {
		// An external window, or a ticket a requester cited. The system is
		// set only where an external system asserted something (M16):
		// inventing one would put a value in the session's audit records
		// that no system ever asserted.
		grant.External = &model.ExternalReference{
			System:            g.External.System,
			Reference:         g.ExternalRef,
			WindowStart:       g.External.WindowStart,
			WindowEnd:         g.External.WindowEnd,
			AdditionalContext: additionalContext(g.External),
		}
	}
	return grant
}

// PolicyOrigin maps the stored origin onto the engine's.
//
// The two vocabularies do not spell the first one the same way — the column
// says `manual` and a rule matches on `administrator` — so this is a mapping
// and never a cast. A cast would compile, produce an origin no rule can match,
// and silently stop every `grant.origins` constraint from ever firing.
func PolicyOrigin(o store.GrantOrigin) model.GrantOrigin {
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

// additionalContext reads `additional_context` back into the one of its two
// shapes it was stored as. Text that does not parse as the shape it claims is
// dropped rather than coerced: it is carried verbatim for an auditor, and a
// coerced value is one nobody asserted.
func additionalContext(e store.GrantExternal) *model.AdditionalContext {
	switch e.AdditionalKind {
	case store.AdditionalContextString:
		var s string
		if json.Unmarshal([]byte(e.Additional), &s) != nil {
			return nil
		}
		return &model.AdditionalContext{Text: s}
	case store.AdditionalContextObject:
		var fields map[string]any
		if json.Unmarshal([]byte(e.Additional), &fields) != nil || fields == nil {
			return nil
		}
		return &model.AdditionalContext{Fields: fields}
	default:
		return nil
	}
}
