// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/policy/model"
)

// Snapshot assembly: the engine's output vocabulary rendered as the contract's.
//
// This file is the ONLY place the two vocabularies meet on the way out — the
// engine's types never reach the wire and the wire's never reach the engine
// (M3). Two properties follow, and both are load-bearing:
//
//   - a contract revision lands here and stops, and
//   - the checks that have to run "before the response is written" have one
//     place to run in, rather than being sprinkled through a mapping where the
//     next reader cannot tell whether they all still run.
//
// Anything the engine can express and the contract cannot carry is a CONTRACT
// GAP: it is reported (PROTOCOL §3), never approximated, and `contract/` is
// never edited to close it.

// assembleSnapshot renders an evaluated snapshot as the contract's response.
//
// It does not decide anything: every value here was decided by the engine or by
// the routing layer. What it does do is refuse to write a response that the
// proxy would refuse — see [checkServeable], which runs after this and before
// anything is returned.
func assembleSnapshot(snap *model.Snapshot, rt routed, decisionID string) *contract.AuthorizeResponse {
	resp := &contract.AuthorizeResponse{
		RouteType:               rt.routeType,
		Target:                  rt.target,
		TargetPort:              rt.port,
		PermittedChannels:       channels(snap.Channels),
		PermittedRequests:       requests(snap.Requests),
		PermittedForwards:       forwards(snap.Forwards),
		PermittedGlobalRequests: globalRequests(snap.GlobalRequests),
		TargetAuthLadder:        ladder(snap.Credentials),
		FilterPolicy:            filterPolicy(snap.Filter),
		Enforcement:             enforcement(snap.Enforcement),
		GrantContext:            grantContext(snap.GrantContext),
		Concurrency:             concurrency(snap.Concurrency),
		Hop:                     rt.hop,
		DecisionID:              decisionID,
	}

	// `default` is the only profile that is not a weakening, and absent
	// means `default` — so the field is emitted only where policy genuinely
	// named one of the other two.
	if snap.AlgorithmProfile != model.AlgorithmProfileUnset &&
		snap.AlgorithmProfile != model.AlgorithmProfileDefault {
		resp.AlgorithmProfile = contract.AlgorithmProfile(snap.AlgorithmProfile)
	}

	// An ABSOLUTE instant, never a duration: a duration re-anchors at each
	// hop of a chained route and silently multiplies the window. The engine
	// already resolved it against its time input, so a chained call carries
	// the instant the first leg was decided under rather than computing a
	// new one.
	if !snap.SessionDeadline.IsZero() {
		resp.SessionDeadline = snap.SessionDeadline.UTC().Format(time.RFC3339)
	}

	// Absent means `false`, so the field is emitted only when the route
	// actually requires capture. An emitted `false` would be a reader
	// having to tell "the policy said no" from "the policy said nothing",
	// which on this field is the same answer.
	if snap.RequireSessionCapture {
		required := true
		resp.RequireSessionCapture = &required
	}
	return resp
}

// channels always renders a list, never null: the contract requires the field,
// and an empty list denies every channel, which is a policy an author may mean.
func channels(in []model.ChannelType) []string {
	out := make([]string, 0, len(in))
	for _, c := range in {
		out = append(out, string(c))
	}
	return out
}

func requests(p *model.RequestPermissions) *contract.RequestPolicy {
	if p == nil {
		// Absent means the axis is not policed. An empty object here would
		// deny every in-channel request, which is the opposite policy.
		return nil
	}
	out := &contract.RequestPolicy{}
	for _, t := range p.Types {
		out.Types = append(out.Types, contract.RequestType(t))
	}
	if p.Subsystems != nil {
		out.Subsystems = append([]string(nil), *p.Subsystems...)
	}
	return out
}

func forwards(p *model.ForwardPermissions) *contract.ForwardPolicy {
	if p == nil {
		return nil
	}
	return &contract.ForwardPolicy{
		DirectTCPIP:    destinations(p.DirectTCPIP),
		ForwardedTCPIP: destinations(p.ForwardedTCPIP),
	}
}

func destinations(in []model.Destination) []contract.ForwardDestination {
	if len(in) == 0 {
		return nil
	}
	out := make([]contract.ForwardDestination, 0, len(in))
	for _, d := range in {
		dest := contract.ForwardDestination{Host: d.Host, Port: d.Port}
		if d.PortRange != nil {
			dest.PortRange = &contract.PortRange{From: d.PortRange.From, To: d.PortRange.To}
		}
		out = append(out, dest)
	}
	return out
}

func globalRequests(p *model.GlobalRequestPermissions) *contract.GlobalRequestPolicy {
	if p == nil {
		return nil
	}
	out := &contract.GlobalRequestPolicy{}
	for _, t := range p.Types {
		out.Types = append(out.Types, string(t))
	}
	return out
}

// ladder renders the ordered credential ladder.
//
// `target_auth_ladder` is the ONLY way to name a credential method: the
// singular `target_auth` field is gone from the contract, and a response
// carrying one would be an unknown field the proxy fails closed on — an outage
// in front of a user rather than a deny. So a policy naming exactly one method
// yields a ONE-ENTRY LADDER, never a bare object, and nothing here synthesises
// a fallback entry the policy did not write.
func ladder(entries []model.CredentialEntry) contract.TargetAuthLadder {
	if entries == nil {
		// Absent leaves the proxy its locally configured method. It is
		// distinct from an empty ladder, which is a denial written as a
		// list, and the engine keeps the two apart.
		return nil
	}
	out := make(contract.TargetAuthLadder, 0, len(entries))
	for _, e := range entries {
		out = append(out, credentialEntry(e))
	}
	return out
}

func credentialEntry(e model.CredentialEntry) contract.TargetAuth {
	params := make(map[string]string, 4+len(e.DeviceFields))
	// Required on EVERY method the contract defines. The engine resolved it
	// against the subject; what travels is a concrete account name, because
	// that is what the target's own audit trail and file ownership will be
	// made of.
	params[contract.ParamUsername] = e.Username.Resolve("")
	if e.KeyType != "" {
		params[contract.ParamKeyType] = e.KeyType
	}
	if e.CredentialRef != "" {
		params[contract.ParamCredentialRef] = e.CredentialRef
	}
	if e.Platform != "" {
		params[contract.ParamPlatform] = e.Platform
	}
	if e.CredentialKind != "" {
		params[contract.ParamCredentialKind] = string(e.CredentialKind)
	}
	if e.ExpiryPosture != "" {
		params[contract.ParamExpiryPosture] = string(e.ExpiryPosture)
	}
	if e.LifetimeSeconds != 0 {
		params[contract.ParamLifetimeSeconds] = strconv.Itoa(int(e.LifetimeSeconds))
	}
	// The open namespace, carried through as DATA this server does not
	// interpret. `device_field.vdom` on a FortiGate scopes the provisioned
	// administrator to one virtual domain, and its ABSENCE makes that
	// administrator global — the strongest account the device has — so a
	// field is never dropped on the way through and never invented here.
	for name, value := range e.DeviceFields {
		params[contract.DeviceFieldPrefix+name] = value
	}
	return contract.TargetAuth{Method: contract.TargetAuthMethod(e.Method), Params: params}
}

func filterPolicy(f model.FilterPolicy) contract.FilterPolicy {
	out := contract.FilterPolicy{
		Mode:     contract.FilterMode(f.Mode),
		ExecMode: contract.ExecMode(f.ExecMode),
	}
	for _, r := range f.Rules {
		out.Rules = append(out.Rules, contract.FilterRule{
			Match:   r.Match,
			Action:  contract.FilterAction(r.Action),
			Message: r.Message,
		})
	}
	if f.RestrictedExec != nil {
		cmds := make([]contract.RestrictedCommand, 0, len(f.RestrictedExec.Commands))
		for _, c := range f.RestrictedExec.Commands {
			cmd := contract.RestrictedCommand{
				Executable: c.Executable,
				Form:       contract.CommandForm(c.Form),
				Argv:       append([]string(nil), c.Argv...),
			}
			for _, a := range c.Args {
				cmd.Args = append(cmd.Args, contract.ArgumentSpec{
					Kind:     contract.ArgumentKind(a.Kind),
					Value:    a.Value,
					Values:   append([]string(nil), a.Values...),
					Optional: a.Optional,
				})
			}
			cmds = append(cmds, cmd)
		}
		out.RestrictedExec = &contract.RestrictedExecPolicy{Commands: cmds}
	}
	return out
}

// enforcement renders the rung, and emits NOTHING where the route stands on the
// two proxy-side defaults.
//
// That is the documented absent-value default and not a fallback this server
// picks: an emitted default is noise in a record whose entire purpose is to say
// which rung was in force.
func enforcement(e model.Enforcement) *contract.EnforcementPolicy {
	if !e.Stated() {
		return nil
	}
	out := &contract.EnforcementPolicy{
		Execution:             contract.ExecutionRung(e.Execution),
		Reach:                 contract.ReachRung(e.Reach),
		PlatformRole:          e.PlatformRole,
		PermittedDestinations: destinations(e.PermittedDestinations),
	}
	if e.Attestation != nil {
		out.Attestation = &contract.Attestation{
			AssertedBy: e.Attestation.AssertedBy,
			Reference:  e.Attestation.Reference,
			AssertedAt: e.Attestation.AssertedAt,
		}
	}
	return out
}

// grantContext copies the justification from the grant that supplied the
// access (M10, M16).
//
// It is emitted only when there is something to say. The engine's grant context
// always names the grant and its origin, and neither is a contract field — so a
// grant with no external system behind it produces an empty object, and an
// empty object is a field an auditor has to decide means nothing.
func grantContext(gc *model.GrantContext) *contract.GrantContext {
	if gc == nil {
		return nil
	}
	out := &contract.GrantContext{
		System:    gc.System,
		Reference: gc.Reference,
	}
	if !gc.WindowStart.IsZero() {
		out.WindowStart = gc.WindowStart.UTC().Format(time.RFC3339)
	}
	if !gc.WindowEnd.IsZero() {
		out.WindowEnd = gc.WindowEnd.UTC().Format(time.RFC3339)
	}
	if gc.AdditionalContext != nil {
		// A JSON string OR a JSON object, and nothing else. The shape that
		// was read is the shape that is written: a number, a list or a
		// boolean is a contract violation rather than something to coerce,
		// because this is stored verbatim for an auditor.
		out.AdditionalContext = &contract.AdditionalContext{
			Text:   gc.AdditionalContext.Text,
			Fields: gc.AdditionalContext.Fields,
		}
	}
	if out.System == "" && out.Reference == "" && out.WindowStart == "" &&
		out.WindowEnd == "" && out.AdditionalContext == nil {
		return nil
	}
	return out
}

// concurrency emits a ceiling only where one exists. Absent and `0` are the
// same answer — uncapped — so `0` is never sent as if it were a decision.
func concurrency(c model.Concurrency) *contract.ConcurrencyLimits {
	if c.MaxSessionsPerSubject == 0 && c.MaxSessionsPerTarget == 0 {
		return nil
	}
	return &contract.ConcurrencyLimits{
		MaxSessionsPerSubject: c.MaxSessionsPerSubject,
		MaxSessionsPerTarget:  c.MaxSessionsPerTarget,
	}
}

// ---------------------------------------------------------------------------
// the checks that run before a response is written
// ---------------------------------------------------------------------------

// UnserveableError is a policy this server cannot answer with.
//
// It is an OUTAGE, not a denial, and the distinction is the whole reason the
// type exists: nobody authored a refusal of this user. What happened is that
// the response assembly produced something the proxy would refuse as a contract
// violation — and a policy that can only fail at connect time has already
// failed, in front of a user, with an operator nowhere near it. Refusing here
// moves the failure to where it can be read.
type UnserveableError struct {
	// Rule is the rule that produced the snapshot.
	Rule string
	// Problem is what is wrong, in the operator's terms.
	Problem string
}

func (e *UnserveableError) Error() string {
	return fmt.Sprintf("decision: rule %q produced a response this server may not send: %s",
		e.Rule, e.Problem)
}

// checkServeable is the second net, after the compiler's (0005).
//
// Every rule below is one the proxy enforces on receipt, and every one of them
// is cheaper to catch here: a snapshot the proxy refuses is an outage a user
// experiences, while a snapshot refused here is a 5xx an operator reads with
// the rule id in it. The compiler already rejects each of these at authoring
// time — this exists for a snapshot assembled from anywhere else.
func checkServeable(resp *contract.AuthorizeResponse, rule string) error {
	bad := func(format string, args ...any) error {
		return &UnserveableError{Rule: rule, Problem: fmt.Sprintf(format, args...)}
	}

	state, entries := resp.Ladder()
	if state == contract.LadderWalk {
		for i, entry := range entries {
			// The contract's one TIGHTENING: `username` is required on
			// every method it defines — `ephemeral-user`,
			// `ephemeral-account`, `static-key` and `brokered-key`
			// alike. `brokered-key` is included and the reasoning that
			// once excluded it is dead: without a name the proxy fell
			// back to the identity's client-typed `login`, and a
			// deployment that configured nothing locally logged in as
			// whatever string the user typed.
			//
			// There is no version to gate this on and there cannot be: a
			// tightening adds no field and changes no field's meaning,
			// so a proxy that was never told parses the route exactly as
			// it always did and refuses it exactly as a current proxy
			// does.
			if entry.Params[contract.ParamUsername] == "" {
				return bad("ladder entry %d (%s) names no params.username, "+
					"which the proxy refuses at the first authorize call", i, entry.Method)
			}
			if err := checkDeviceFieldShape(entry, i); err != nil {
				return &UnserveableError{Rule: rule, Problem: err.Error()}
			}
		}
	}

	if resp.Enforcement == nil {
		return nil
	}
	exec, reach := resp.EnforcedExecution(), resp.EnforcedReach()

	// An APPLIED rung is one the proxy configures per session onto an
	// account it administers, so it needs a ladder entry that provisions the
	// target. An ATTESTED rung on that same route is fine and is the whole
	// reason the kind exists: it is how the appliance estate carries a real
	// enforcement claim instead of "none available".
	if state == contract.LadderWalk {
		provisions := false
		for _, entry := range entries {
			if entry.Method.Provisions() {
				provisions = true
				break
			}
		}
		if !provisions {
			if exec.RequiresProvisioning() {
				return bad("execution rung %q is applied to an account the proxy administers, "+
					"and no ladder entry provisions the target", exec)
			}
			if reach.RequiresProvisioning() {
				return bad("reach rung %q is applied to an account the proxy administers, "+
					"and no ladder entry provisions the target", reach)
			}
		}
	}

	// The claim has to agree with the rest of the snapshot, because the
	// proxy refuses a response that disagrees with itself.
	switch exec {
	case contract.ExecutionNoInteractiveShell:
		if !resp.PermittedRequests.Policed() {
			return bad("execution rung %q needs permitted_requests, and the response polices none", exec)
		}
		for _, t := range []contract.RequestType{contract.RequestTypeShell, contract.RequestTypePTYReq} {
			if permits(resp.PermittedRequests, t) {
				return bad("execution rung %q beside permitted_requests that still allows %q", exec, t)
			}
		}
	case contract.ExecutionAccountRestricted, contract.ExecutionAccountConfined:
		if resp.FilterPolicy.Tier() != contract.ExecModeRestricted {
			return bad("execution rung %q needs filter_policy.exec_mode `restricted`, and the response is %q",
				exec, resp.FilterPolicy.Tier())
		}
	case contract.ExecutionPlatformAuthorized:
		if resp.Enforcement.PlatformRole == "" {
			return bad("execution rung %q needs enforcement.platform_role, and there is no default to fall back on", exec)
		}
	case contract.ExecutionProxyInspected, contract.ExecutionPlatformAttested:
	}
	if resp.Enforcement.PlatformRole != "" && exec != contract.ExecutionPlatformAuthorized {
		return bad("enforcement.platform_role is set beside execution rung %q, which forbids it", exec)
	}

	switch reach {
	case contract.ReachAccountEgressRestricted:
		// `permitted_destinations` reuses ForwardDestination's SHAPE and
		// shares none of its meaning: `permitted_forwards` is a rule about
		// SSH channels the proxy sees, this is a rule about sockets the
		// target's kernel sees. One is never assembled from the other, and
		// neither ever widens the other.
		if len(resp.Enforcement.PermittedDestinations) == 0 {
			return bad("reach rung %q needs a non-empty enforcement.permitted_destinations; "+
				"they are the whole content of the rung", reach)
		}
	case contract.ReachProxyChannelPolicy, contract.ReachAccountNetworkIsolated, contract.ReachPlatformAttested:
	}
	if len(resp.Enforcement.PermittedDestinations) > 0 && reach != contract.ReachAccountEgressRestricted {
		return bad("enforcement.permitted_destinations is set beside reach rung %q, which forbids it", reach)
	}

	attested := exec.Attested() || reach.Attested()
	switch {
	case attested:
		// The system verifies none of an attestation, so what the contract
		// asks for instead is attributability: "trust us" and an empty
		// string are the same answer.
		if resp.Enforcement.Attestation == nil {
			return bad("an attested rung with no attestation: the claim is unverified and now also unattributable")
		}
		if resp.Enforcement.Attestation.AssertedBy == "" || resp.Enforcement.Attestation.Reference == "" {
			return bad("an attestation needs both asserted_by and reference")
		}
	case resp.Enforcement.Attestation != nil:
		return bad("an attestation beside applied rungs (%s/%s), which the contract forbids", exec, reach)
	}
	return nil
}

// checkDeviceFieldShape holds a ladder entry's device fields to the shape the
// contract states. Which names are meaningful is the driver's business and is
// checked elsewhere, against what the enforcing proxy declared (M17).
func checkDeviceFieldShape(entry contract.TargetAuth, index int) error {
	names := make([]string, 0, len(entry.Params))
	for key := range entry.Params {
		if len(key) > len(contract.DeviceFieldPrefix) && key[:len(contract.DeviceFieldPrefix)] == contract.DeviceFieldPrefix {
			names = append(names, key[len(contract.DeviceFieldPrefix):])
		}
	}
	// Sorted so that a route with two malformed fields reports the same one
	// on every run; a message that moves between runs reads as flakiness.
	sort.Strings(names)
	if len(names) > contract.DeviceFieldMaxPerEntry {
		return fmt.Errorf("ladder entry %d carries %d device fields, over the %d the contract allows",
			index, len(names), contract.DeviceFieldMaxPerEntry)
	}
	for _, name := range names {
		if !model.ValidDeviceFieldName(name) {
			return fmt.Errorf("ladder entry %d device field %q: the name must be lowercase letters, "+
				"digits, hyphens and underscores, %d characters or fewer",
				index, name, contract.DeviceFieldMaxNameLen)
		}
		if !model.ValidDeviceFieldValue(entry.Params[contract.DeviceFieldPrefix+name]) {
			return fmt.Errorf("ladder entry %d device field %q: the value must be non-empty and "+
				"%d characters or fewer", index, name, contract.DeviceFieldMaxValueLen)
		}
	}
	return nil
}

// permits reports whether an allow-list permits a request type. A nil policy is
// an unpoliced axis, which permits everything.
func permits(p *contract.RequestPolicy, t contract.RequestType) bool {
	if p == nil {
		return true
	}
	for _, have := range p.Types {
		if have == t {
			return true
		}
	}
	return false
}
