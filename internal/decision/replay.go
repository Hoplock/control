// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"sort"
	"time"

	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/eval"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// Simulation (0019): the feature that makes a policy change reviewable instead
// of a leap.
//
// REPLAY IS PURE AND TOTAL. It rebuilds the engine's input from each record's
// stored inputs — the question exactly as it was asked, at the instant it was
// asked — and evaluates the candidate program over it. The engine takes time as
// an input precisely so this works (0005), and it is total over its input, so
// every record that can be read can be replayed. Two things it never does:
//
//   - it NEVER RE-PROBES an external system. A probe's answer is a recorded
//     input — `inputs.grants[]` holds only the windows that counted — and
//     asking a scanner about last week now is a different question;
//   - it never re-reads the fleet, a subject or a target: a group or a label
//     changed since is not the question that was asked. A relabel is simulated
//     by naming the new labels explicitly (LabelOverride), which is a
//     different question asked on purpose.
//
// What replay compares is the ENGINE's effect — allow or deny — on the recorded
// question. Routing, capabilities and the vocabulary gate are what the fleet
// can serve today, not what the policy decides, so a record that allowed and
// went unserved compares as the allow it was.

// ReplayInput rebuilds the engine input a decision record was evaluated with.
// A record whose inputs do not decode is an error: replay is total over the
// records it can read and says which it could not. So is one that decodes into
// a DIFFERENT question — no recorded instant, a source address or a grant
// window that does not parse — because a zero value in its place is not what
// was asked: a grant whose expiry does not parse reads as expired.
func ReplayInput(raw json.RawMessage) (model.Input, error) {
	var in recordedInputs
	if err := json.Unmarshal(raw, &in); err != nil {
		return model.Input{}, fmt.Errorf("decision: the recorded inputs do not decode: %w", err)
	}
	if in.Now.IsZero() {
		return model.Input{}, errors.New("decision: the recorded inputs carry no instant, and time is an input")
	}
	out := model.Input{
		Now: in.Now,
		Subject: model.Subject{
			ID:         in.Subject.ID,
			Source:     in.Subject.Source,
			Groups:     in.Subject.Groups,
			Claims:     in.Subject.Claims,
			AuthMethod: model.AuthMethod(in.Subject.Method),
			MFA:        in.Subject.MFA,
		},
		Context: model.Context{ProxyID: in.Context.ProxyID},
		Target: model.Target{
			Hostname: in.Target.Hostname,
			Port:     in.Target.Port,
			Zone:     in.Target.Zone,
			Labels:   in.Target.Labels,
		},
	}
	if in.Context.SourceAddr != "" {
		addr, err := netip.ParseAddr(in.Context.SourceAddr)
		if err != nil {
			return model.Input{}, fmt.Errorf("decision: the recorded source address %q does not parse", in.Context.SourceAddr)
		}
		out.Context.SourceAddr = addr
	}
	for _, g := range in.Grants {
		grant := model.Grant{
			ID:      g.ID,
			Subject: in.Subject.ID,
			Origin:  model.GrantOrigin(g.Origin),
			Scope: model.GrantScope{
				Name: g.Scope.Name, Targets: g.Scope.Targets,
				Labels: g.Scope.Labels, Zones: g.Scope.Zones,
			},
			Approvers:  g.Approvers,
			RequestRef: g.RequestID,
		}
		if err := parseRecordedTime(g.ID, "not_before", g.NotBefore, &grant.NotBefore); err != nil {
			return model.Input{}, err
		}
		if err := parseRecordedTime(g.ID, "expires_at", g.ExpiresAt, &grant.ExpiresAt); err != nil {
			return model.Input{}, err
		}
		if g.External.System != "" || g.External.Reference != "" {
			ext := &model.ExternalReference{System: g.External.System, Reference: g.External.Reference}
			if err := parseRecordedTime(g.ID, "external.window_start", g.External.WindowStart, &ext.WindowStart); err != nil {
				return model.Input{}, err
			}
			if err := parseRecordedTime(g.ID, "external.window_end", g.External.WindowEnd, &ext.WindowEnd); err != nil {
				return model.Input{}, err
			}
			grant.External = ext
		}
		out.Grants = append(out.Grants, grant)
	}
	return out, nil
}

// parseRecordedTime reads one recorded instant. Empty is the zero time, which
// the record means (an open start, no external end); anything else must parse.
func parseRecordedTime(grant, field, s string, into *time.Time) error {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("decision: grant %s's recorded %s %q does not parse", grant, field, s)
	}
	*into = t
	return nil
}

// recordedEffect is the ENGINE's effect a record carries — the explanation's,
// never the record's `effect` column, which also says whether the fleet could
// serve it. So an allow that went unserved compares as the allow it was, and
// one left undetermined by an unanswered probe (M16) compares as what the
// engine answered on the windows it counted.
func recordedEffect(d store.Decision) (string, string, error) {
	var expl recordedExplanation
	if err := json.Unmarshal(d.Explanation, &expl); err != nil || expl.Effect == "" {
		return "", "", fmt.Errorf("decision: %s carries no explanation to compare against", d.ID)
	}
	return expl.Effect, expl.Rule, nil
}

// LabelOverride replaces a target's labels in a replay: how a bulk relabel is
// simulated like any other policy change.
type LabelOverride map[string]map[string]string

// ReplayQuery selects what a replay covers.
type ReplayQuery struct {
	From, To time.Time
	// Limit bounds how many records are replayed; zero takes the store's
	// default. The report says when it cut the window short.
	Limit int
	// Labels, when set, replaces the labels of the named targets.
	Labels LabelOverride
}

// Flip is one recorded decision whose effect the candidate changes.
type Flip struct {
	DecisionID string    `json:"decision_id"`
	SessionID  string    `json:"session_id,omitempty"`
	Subject    string    `json:"subject"`
	Target     string    `json:"target"`
	DecidedAt  time.Time `json:"decided_at"`
	// From and To are the effects: `allow` → `deny` or `deny` → `allow`.
	From string `json:"from"`
	To   string `json:"to"`
	// WasRule and NowRule are the rules that answered each time; empty is
	// the default deny.
	WasRule string `json:"was_rule,omitempty"`
	NowRule string `json:"now_rule,omitempty"`
}

// FlipGroup is the flips that share a transition and a pair of rules — the
// grouping that lets a human read a replay rather than scroll it.
type FlipGroup struct {
	From    string `json:"from"`
	To      string `json:"to"`
	WasRule string `json:"was_rule,omitempty"`
	NowRule string `json:"now_rule,omitempty"`
	Count   int    `json:"count"`
	Flips   []Flip `json:"flips"`
}

// Unreplayable is a record replay could not read, and why.
type Unreplayable struct {
	DecisionID string `json:"decision_id"`
	Reason     string `json:"reason"`
}

// ReplayReport is what a candidate would have changed over a window.
type ReplayReport struct {
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Replayed  int       `json:"replayed"`
	Unchanged int       `json:"unchanged"`
	// Truncated reports that the limit cut the window short.
	Truncated bool `json:"truncated"`
	// AllowToDeny and DenyToAllow are the flips, grouped.
	AllowToDeny  []FlipGroup    `json:"allow_to_deny"`
	DenyToAllow  []FlipGroup    `json:"deny_to_allow"`
	Unreplayable []Unreplayable `json:"unreplayable,omitempty"`
}

// RecordWindow is everything replay may read: the decisions recorded in one
// window, bounded. It is the whole of replay's access to state, so that what
// "no store read beyond the window" promises is a property of the signature —
// a replay cannot read a subject, a target, a grant or the fleet, because it
// is handed nothing that can. `store.Store.Decisions()` is one.
type RecordWindow interface {
	ListRange(ctx context.Context, tenant store.Tenant, from, to time.Time, limit int) ([]store.Decision, bool, error)
}

// Replay evaluates a candidate program over the decisions recorded in a window
// and reports every one whose effect it changes.
func Replay(ctx context.Context, records RecordWindow, tenant store.Tenant, candidate *compile.Program, q ReplayQuery) (ReplayReport, error) {
	window, truncated, err := records.ListRange(ctx, tenant, q.From, q.To, q.Limit)
	if err != nil {
		return ReplayReport{}, err
	}
	rep := ReplayReport{From: q.From, To: q.To, Truncated: truncated}
	var flips []Flip
	for _, d := range window {
		was, wasRule, err := recordedEffect(d)
		if err != nil {
			rep.Unreplayable = append(rep.Unreplayable, Unreplayable{DecisionID: d.ID, Reason: err.Error()})
			continue
		}
		in, err := ReplayInput(d.Inputs)
		if err != nil {
			rep.Unreplayable = append(rep.Unreplayable, Unreplayable{DecisionID: d.ID, Reason: err.Error()})
			continue
		}
		if labels, ok := q.Labels[in.Target.Hostname]; ok {
			in.Target.Labels = maps.Clone(labels)
		}
		_, expl := eval.Evaluate(candidate, in)
		rep.Replayed++
		now := string(expl.Effect)
		if now == was {
			rep.Unchanged++
			continue
		}
		flips = append(flips, Flip{
			DecisionID: d.ID, SessionID: d.SessionID, Subject: in.Subject.ID, Target: in.Target.Hostname,
			DecidedAt: d.DecidedAt.UTC(), From: was, To: now, WasRule: wasRule, NowRule: expl.Rule,
		})
	}
	rep.AllowToDeny, rep.DenyToAllow = groupFlips(flips)
	return rep, nil
}

// groupFlips groups flips by transition and rule pair, largest group first.
func groupFlips(flips []Flip) (allowToDeny, denyToAllow []FlipGroup) {
	type key struct{ from, to, was, now string }
	groups := map[key]*FlipGroup{}
	var order []key
	for _, f := range flips {
		k := key{f.From, f.To, f.WasRule, f.NowRule}
		g, ok := groups[k]
		if !ok {
			g = &FlipGroup{From: f.From, To: f.To, WasRule: f.WasRule, NowRule: f.NowRule, Flips: []Flip{}}
			groups[k] = g
			order = append(order, k)
		}
		g.Flips = append(g.Flips, f)
		g.Count++
	}
	allowToDeny, denyToAllow = []FlipGroup{}, []FlipGroup{}
	for _, k := range order {
		g := *groups[k]
		if g.From == string(model.EffectAllow) {
			allowToDeny = append(allowToDeny, g)
		} else {
			denyToAllow = append(denyToAllow, g)
		}
	}
	for _, list := range [][]FlipGroup{allowToDeny, denyToAllow} {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Count > list[j].Count })
	}
	return allowToDeny, denyToAllow
}

// DryRunCase is one synthetic question for a dry run.
type DryRunCase struct {
	// Name labels the case in the answer.
	Name  string      `json:"name,omitempty"`
	Input model.Input `json:"-"`
}

// DryRunResult is a candidate's answer to one synthetic question.
type DryRunResult struct {
	Name        string   `json:"name,omitempty"`
	Effect      string   `json:"effect"`
	Rule        string   `json:"rule,omitempty"`
	RuleLine    int      `json:"rule_line,omitempty"`
	Terms       []string `json:"terms,omitempty"`
	DenyReason  string   `json:"deny_reason,omitempty"`
	Obligations []string `json:"obligations,omitempty"`
}

// DryRun evaluates a candidate program against synthetic inputs. It is pure:
// no store, no fleet, no clock but the inputs' own.
func DryRun(candidate *compile.Program, cases []DryRunCase) []DryRunResult {
	out := make([]DryRunResult, 0, len(cases))
	for _, c := range cases {
		_, expl := eval.Evaluate(candidate, c.Input)
		r := DryRunResult{
			Name: c.Name, Effect: string(expl.Effect), Rule: expl.Rule, RuleLine: expl.RuleLine,
			DenyReason: expl.DenyReason,
		}
		for _, term := range expl.Terms {
			r.Terms = append(r.Terms, fmt.Sprint(term))
		}
		for _, o := range expl.Obligations {
			r.Obligations = append(r.Obligations, string(o))
		}
		out = append(out, r)
	}
	return out
}
