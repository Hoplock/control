// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/hoplock/control/internal/decision"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// SIMULATION is the feature that makes a policy change reviewable instead of a
// leap. A candidate is a source or a stored version; it is evaluated against
// synthetic questions (a dry run), against the questions recorded over a window
// (a replay), or both. It is PURE: the engine takes time as an input, replay
// asks each recorded question at its recorded instant from its recorded
// inputs, and nothing re-probes an external system or re-reads the fleet
// (decision.Replay). A relabel is simulated by naming the new labels, which is
// how a bulk relabel appears in simulation like any other policy change.

// DryRunInput is one synthetic question, as a client writes it.
type DryRunInput struct {
	Name    string        `json:"name,omitempty"`
	Now     time.Time     `json:"now"`
	Subject DryRunSubject `json:"subject"`
	Target  DryRunTarget  `json:"target"`
	Context DryRunContext `json:"context"`
	Device  *DryRunDevice `json:"device,omitempty"`
	Grants  []DryRunGrant `json:"grants,omitempty"`
}

// DryRunSubject is the asking principal.
type DryRunSubject struct {
	ID         string            `json:"id"`
	Source     string            `json:"source,omitempty"`
	Groups     []string          `json:"groups,omitempty"`
	Claims     map[string]string `json:"claims,omitempty"`
	AuthMethod string            `json:"auth_method,omitempty"`
	MFA        bool              `json:"mfa,omitempty"`
}

// DryRunTarget is the host asked for.
type DryRunTarget struct {
	Hostname string            `json:"hostname"`
	Port     int32             `json:"port,omitempty"`
	Zone     string            `json:"zone,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// DryRunContext is where the question came from.
type DryRunContext struct {
	ProxyID    string `json:"proxy_id,omitempty"`
	SourceAddr string `json:"source_addr,omitempty"`
}

// DryRunDevice is endpoint posture.
type DryRunDevice struct {
	Posture map[string]string `json:"posture"`
}

// DryRunGrant is a live grant the question is asked under.
type DryRunGrant struct {
	ID        string            `json:"id"`
	Origin    string            `json:"origin,omitempty"`
	Scope     string            `json:"scope"`
	Targets   []string          `json:"targets,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Zones     []string          `json:"zones,omitempty"`
	NotBefore time.Time         `json:"not_before"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// input renders the question for the engine. A source address that does not
// parse is refused rather than treated as none: a dry run that silently dropped
// it would answer a different question from the one written.
func (in DryRunInput) input() (model.Input, error) {
	out := model.Input{
		Now: in.Now,
		Subject: model.Subject{
			ID: in.Subject.ID, Source: in.Subject.Source, Groups: in.Subject.Groups,
			Claims: in.Subject.Claims, AuthMethod: model.AuthMethod(in.Subject.AuthMethod), MFA: in.Subject.MFA,
		},
		Target: model.Target{
			Hostname: in.Target.Hostname, Port: in.Target.Port, Zone: in.Target.Zone, Labels: in.Target.Labels,
		},
		Context: model.Context{ProxyID: in.Context.ProxyID},
	}
	if out.Now.IsZero() {
		return model.Input{}, fmt.Errorf("case %q: `now` is required; time is an input to evaluation", in.Name)
	}
	if in.Context.SourceAddr != "" {
		addr, err := netip.ParseAddr(in.Context.SourceAddr)
		if err != nil {
			return model.Input{}, fmt.Errorf("case %q: source_addr %q is not an address", in.Name, in.Context.SourceAddr)
		}
		out.Context.SourceAddr = addr
	}
	if in.Device != nil {
		out.Device = &model.Device{Posture: in.Device.Posture}
	}
	for _, g := range in.Grants {
		origin := model.GrantOrigin(g.Origin)
		if origin == "" {
			origin = model.GrantOriginAdministrator
		}
		out.Grants = append(out.Grants, model.Grant{
			ID: g.ID, Subject: in.Subject.ID, Origin: origin,
			Scope:     model.GrantScope{Name: g.Scope, Targets: g.Targets, Labels: g.Labels, Zones: g.Zones},
			NotBefore: g.NotBefore, ExpiresAt: g.ExpiresAt,
		})
	}
	return out, nil
}

// ReplayWindow is the recorded window a replay covers.
type ReplayWindow struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Limit int       `json:"limit,omitempty"`
}

// SimulateRequest names a candidate and the questions to ask it.
type SimulateRequest struct {
	// Source or Version is the candidate; neither is the active version,
	// which is how a relabel alone is simulated.
	Source  []byte
	Version int64
	Cases   []DryRunInput
	Replay  *ReplayWindow
	// Labels replace named targets' labels in the replay.
	Labels decision.LabelOverride
}

// SimulateResult is what the candidate answered.
type SimulateResult struct {
	// Candidate names what was simulated: a digest, and the version when it
	// was a stored one.
	Digest  string                  `json:"digest"`
	Version int64                   `json:"version,omitempty"`
	DryRun  []decision.DryRunResult `json:"dry_run"`
	Replay  *decision.ReplayReport  `json:"replay,omitempty"`
}

// ErrSimulation is a simulation request this server will not run.
type ErrSimulation struct{ Reason string }

func (e *ErrSimulation) Error() string { return "authoring: " + e.Reason }

// MaxReplayRecords bounds a replay, whatever the request asks.
const MaxReplayRecords = 10000

// Simulate evaluates a candidate.
func (s *Service) Simulate(ctx context.Context, tenant store.Tenant, req SimulateRequest) (SimulateResult, error) {
	source := req.Source
	var version int64
	switch {
	case len(source) > 0 && req.Version != 0:
		return SimulateResult{}, &ErrSimulation{Reason: "name a candidate source or a stored version, not both"}
	case len(source) == 0 && req.Version != 0:
		b, err := s.st.PolicyBundles().Get(ctx, tenant, req.Version)
		if err != nil {
			return SimulateResult{}, err
		}
		source, version = b.Source, b.Version
	case len(source) == 0:
		b, err := s.st.PolicyBundles().GetActive(ctx, tenant)
		if err != nil {
			return SimulateResult{}, err
		}
		source, version = b.Source, b.Version
	}
	rejections, cand := Compile(tenant, source)
	if cand == nil {
		return SimulateResult{}, &InvalidError{Report: Report{Digest: Digest(source), Rejections: rejections, Findings: []Finding{}}}
	}
	if len(req.Cases) == 0 && req.Replay == nil {
		return SimulateResult{}, &ErrSimulation{Reason: "a simulation asks something: give dry-run cases, a replay window, or both"}
	}

	out := SimulateResult{Digest: cand.Digest, Version: version, DryRun: []decision.DryRunResult{}}
	if len(req.Cases) > 0 {
		cases := make([]decision.DryRunCase, 0, len(req.Cases))
		for _, c := range req.Cases {
			in, err := c.input()
			if err != nil {
				return SimulateResult{}, &ErrSimulation{Reason: err.Error()}
			}
			cases = append(cases, decision.DryRunCase{Name: c.Name, Input: in})
		}
		out.DryRun = decision.DryRun(cand.Program, cases)
	}
	if w := req.Replay; w != nil {
		if w.From.IsZero() || w.To.IsZero() || !w.From.Before(w.To) {
			return SimulateResult{}, &ErrSimulation{Reason: "a replay window needs `from` before `to`"}
		}
		limit := w.Limit
		if limit <= 0 || limit > MaxReplayRecords {
			limit = MaxReplayRecords
		}
		rep, err := decision.Replay(ctx, s.st, tenant, cand.Program, decision.ReplayQuery{
			From: w.From, To: w.To, Limit: limit, Labels: req.Labels,
		})
		if err != nil {
			return SimulateResult{}, err
		}
		out.Replay = &rep
	}
	return out, nil
}
