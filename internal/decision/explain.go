// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hoplock/control/internal/store"
)

// Explain (M4): the other half of the proxy's disclosure rule.
//
// The user is told "access denied" and a session id — deliberately vague, so
// the proxy is not an oracle for probing the estate — and an operator resolves
// that id HERE into everything: the inputs, the matched rule and its line, the
// claim mapping version that produced the attributes, the obligations, the
// snapshot, and — if a grant was involved — which one and who approved it. The
// pair only works if this is total, so a record that cannot be explained is
// made LOUD rather than rendered as a partial story: [ErrUnexplainable].
//
// Everything is read from the record. The grant comes out of `inputs.grants[]`
// by the id `explanation.grant` names, whole — scope, window, origin, creator
// and reason, request, workflow, approvers and any external assertion — so
// explain never joins the grant table, which may have changed since.

// ErrUnexplainable is a stored decision that does not carry its own story. It
// is an integrity fault in this server's records, never a refusal: the handler
// answers it as an outage and logs it at error.
var ErrUnexplainable = errors.New("decision: the record does not carry its own explanation")

// ErrNoSession is an explain asked for the empty session id. "" is no session
// (Hoplock/proxy#71), and a lookup of it would answer with every record that
// belongs to none.
var ErrNoSession = errors.New("decision: an empty session id is no session")

// Story is one decision, explained whole.
type Story struct {
	DecisionID string    `json:"decision_id"`
	SessionID  string    `json:"session_id,omitempty"`
	ProxyID    string    `json:"proxy_id,omitempty"`
	Effect     string    `json:"effect"`
	DecidedAt  time.Time `json:"decided_at"`
	// Rule and RuleLine are the rule that answered; empty is the default
	// deny, which is itself the answer: nothing matched.
	Rule     string `json:"rule,omitempty"`
	RuleLine int    `json:"rule_line,omitempty"`
	// Mapping is the claim mapping version that produced the subject's
	// attributes when the decision was made, beside the one active now: a
	// decision made under a mapping that has since changed is explained by
	// the mapping it was made under.
	Mapping MappingFacts `json:"mapping"`
	// Obligations are what the decision emitted.
	Obligations []string `json:"obligations,omitempty"`
	// Grant is the `inputs.grants[]` entry the explanation names, whole;
	// nil when no grant was involved.
	Grant json.RawMessage `json:"grant,omitempty"`
	// Inputs, Explanation and Snapshot are the record's own documents:
	// `explanation.external` and `explanation.unanswered` are inside the
	// explanation, `inputs.external_context[]` inside the inputs.
	Inputs      json.RawMessage `json:"inputs"`
	Explanation json.RawMessage `json:"explanation"`
	Snapshot    json.RawMessage `json:"snapshot,omitempty"`
	// Unserved is why an allowed decision could not be answered — an
	// outage, never a denial: an `unserved` record whose external context
	// could not be determined is explained as the outage it was.
	Unserved string `json:"unserved,omitempty"`
}

// MappingFacts names the claim mapping a decision was made under.
type MappingFacts struct {
	// Version is the mapping version recorded with the subject; zero means
	// no mapping was involved.
	Version int `json:"version"`
	// Active is the version active now, zero when none is.
	Active int `json:"active"`
	// Changed reports that the mapping has moved since the decision.
	Changed bool `json:"changed"`
}

// ActiveMapping reports the claim mapping version active now, for explain to
// set beside the recorded one. Zero means none is active.
type ActiveMapping func(ctx context.Context, tenant store.Tenant) (int, error)

// Explain resolves a decision id into its story.
func Explain(ctx context.Context, st *store.Store, active ActiveMapping, tenant store.Tenant, decisionID string) (Story, error) {
	d, err := st.Decisions().Get(ctx, tenant, decisionID)
	if err != nil {
		return Story{}, err
	}
	return story(ctx, active, tenant, d)
}

// ExplainSession resolves a session id — what the user was told — into the
// story of every decision taken for it, newest first: a chained session is one
// decision per hop. The empty id is refused.
//
// A session's decisions are bounded by its hops, so this is not a list the
// caller pages (M26.3): it reads the store's own bound of them, the newest,
// and the boolean reports that the bound cut the answer short.
func ExplainSession(ctx context.Context, st *store.Store, active ActiveMapping, tenant store.Tenant, sessionID string) ([]Story, bool, error) {
	if sessionID == "" {
		return nil, false, ErrNoSession
	}
	records, truncated, err := st.Decisions().ListBySession(ctx, tenant, sessionID, 0)
	if err != nil {
		return nil, false, err
	}
	out := make([]Story, 0, len(records))
	for _, d := range records {
		s, err := story(ctx, active, tenant, d)
		if err != nil {
			return nil, false, err
		}
		out = append(out, s)
	}
	return out, truncated, nil
}

func story(ctx context.Context, active ActiveMapping, tenant store.Tenant, d store.Decision) (Story, error) {
	var (
		in   recordedInputs
		expl recordedExplanation
	)
	if err := json.Unmarshal(d.Inputs, &in); err != nil || len(d.Inputs) == 0 {
		return Story{}, fmt.Errorf("%w: %s's inputs do not decode", ErrUnexplainable, d.ID)
	}
	if in.Now.IsZero() {
		return Story{}, fmt.Errorf("%w: %s's inputs carry no instant", ErrUnexplainable, d.ID)
	}
	if err := json.Unmarshal(d.Explanation, &expl); err != nil || expl.Effect == "" {
		return Story{}, fmt.Errorf("%w: %s carries no effect", ErrUnexplainable, d.ID)
	}
	// The record's effect against its own story. Each of these is a story
	// with a part missing or contradicted, which M4 makes impossible to
	// represent: never rendered, always loud.
	switch d.Effect {
	case EffectAllow:
		if expl.Effect != EffectAllow {
			return Story{}, fmt.Errorf("%w: %s is an allow the engine answered %q", ErrUnexplainable, d.ID, expl.Effect)
		}
		if !hasSnapshot(d.Snapshot) {
			return Story{}, fmt.Errorf("%w: %s is an allow with no snapshot", ErrUnexplainable, d.ID)
		}
	case EffectUnserved:
		if expl.Unserved == "" {
			return Story{}, fmt.Errorf("%w: %s went unserved and does not say why", ErrUnexplainable, d.ID)
		}
	case EffectDeny:
	default:
		return Story{}, fmt.Errorf("%w: %s's effect %q is none this server writes", ErrUnexplainable, d.ID, d.Effect)
	}
	s := Story{
		DecisionID: d.ID, SessionID: d.SessionID, ProxyID: d.ProxyID, Effect: d.Effect,
		DecidedAt: d.DecidedAt.UTC(), Rule: expl.Rule, RuleLine: expl.RuleLine,
		Obligations: expl.Obligations, Inputs: d.Inputs, Explanation: d.Explanation,
		Snapshot: d.Snapshot, Unserved: expl.Unserved,
		Mapping: MappingFacts{Version: in.Subject.MappingVersion},
	}
	if expl.Grant != "" {
		found := false
		var raw struct {
			Grants []json.RawMessage `json:"grants"`
		}
		if err := json.Unmarshal(d.Inputs, &raw); err == nil {
			for i, g := range in.Grants {
				if g.ID == expl.Grant && i < len(raw.Grants) {
					s.Grant, found = raw.Grants[i], true
					break
				}
			}
		}
		if !found {
			// The explanation names a grant the inputs do not hold: the
			// story would be "a grant, but not which", which is exactly the
			// unexplained decision M4 exists to make impossible.
			return Story{}, fmt.Errorf("%w: %s names grant %s, which its inputs do not hold", ErrUnexplainable, d.ID, expl.Grant)
		}
	}
	if active != nil {
		v, err := active(ctx, tenant)
		if err != nil {
			return Story{}, err
		}
		s.Mapping.Active = v
		s.Mapping.Changed = s.Mapping.Version != 0 && v != s.Mapping.Version
	}
	return s, nil
}

// hasSnapshot reports whether a stored snapshot says anything. The store writes
// an absent one as `{}`, and a served allow always names its route.
func hasSnapshot(raw json.RawMessage) bool {
	var doc map[string]json.RawMessage
	return json.Unmarshal(raw, &doc) == nil && len(doc) > 0
}

// StoredActiveMapping reads the active claim mapping from the store. A tenant
// that federates with nobody has none, which is zero rather than an error.
func StoredActiveMapping(st *store.Store) ActiveMapping {
	return func(ctx context.Context, tenant store.Tenant) (int, error) {
		m, err := st.ClaimMappings().Active(ctx, tenant)
		if err != nil {
			if store.IsNotFound(err) {
				return 0, nil
			}
			return 0, err
		}
		return m.Version, nil
	}
}
