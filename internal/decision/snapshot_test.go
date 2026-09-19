// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/eval"
	"github.com/hoplock/control/internal/policy/model"
)

// The mapping and the refusals, tested without a database.
//
// Everything here is a pure function over an evaluated snapshot, which is what
// makes it worth testing at this level: the assembly is where a contract
// revision lands, and a test that had to stand a listener and a Postgres up to
// assert "absent concurrency means uncapped" would be a test nobody runs while
// changing the mapping.

// evaluateFixture compiles a bundle and evaluates it, so these tests assert
// against a snapshot the ENGINE produced rather than one a test author wrote.
// A hand-built snapshot can hold a shape the compiler would have refused.
func evaluateFixture(t *testing.T, bundleYAML string, in model.Input) (*model.Snapshot, eval.Explanation) {
	t.Helper()
	bundle, err := model.Parse([]byte(bundleYAML))
	if err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	prog, err := compile.Compile(bundle)
	if err != nil {
		t.Fatalf("compile bundle: %v", err)
	}
	return eval.Evaluate(prog, in)
}

func baseInput() model.Input {
	return model.Input{
		Now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
		Subject: model.Subject{
			ID:         "alice@example.com",
			Source:     "local",
			Groups:     []string{"sre"},
			AuthMethod: model.AuthMethodCert,
		},
		Context: model.Context{ProxyID: "proxy-1"},
		Target: model.Target{
			Hostname: "host.example.com",
			Zone:     "edge",
			Labels:   map[string]string{"env": "prod"},
		},
	}
}

const minimalBundle = `
schema_version: 1
tenant: acme
labels:
  env: [prod]
groups: [sre]
rules:
  - id: allow-prod
    effect: allow
    match:
      subject:
        groups: [sre]
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: svc-admin
          credential_ref: vault://prod
`

// A route that says nothing about the newer vocabulary must emit none of it.
// Absence is the documented default on each of these fields, and an emitted
// default is noise in a record whose whole purpose is to say what was in force.
func TestARouteThatSaysNothingEmitsNothing(t *testing.T) {
	t.Parallel()
	snap, _ := evaluateFixture(t, minimalBundle, baseInput())
	if snap == nil {
		t.Fatal("the fixture bundle denied; it is supposed to allow")
	}

	resp := assembleSnapshot(snap, routed{
		routeType: contract.RouteTypeDirect,
		target:    "host.example.com",
	}, "d_test")

	if resp.Enforcement != nil {
		t.Errorf("enforcement = %+v, want absent: the route stands on the two proxy-side defaults", resp.Enforcement)
	}
	if resp.SessionDeadline != "" {
		t.Errorf("session_deadline = %q, want absent", resp.SessionDeadline)
	}
	if resp.RequireSessionCapture != nil {
		t.Errorf("require_session_capture = %v, want absent: absent means false", *resp.RequireSessionCapture)
	}
	if resp.Concurrency != nil {
		t.Errorf("concurrency = %+v, want absent rather than a pair of zeroes", resp.Concurrency)
	}
	if resp.AlgorithmProfile != "" {
		t.Errorf("algorithm_profile = %q, want absent: `default` is the only value that is not a weakening", resp.AlgorithmProfile)
	}
	if resp.GrantContext != nil {
		t.Errorf("grant_context = %+v, want absent: no grant supplied this access", resp.GrantContext)
	}
	if resp.Cache != nil {
		t.Errorf("cache = %+v, want absent: the rule authored no hint", resp.Cache)
	}

	// The two fields the contract requires must be present even when they
	// say "nothing": an absent `permitted_channels` means something
	// different from an empty one.
	if resp.PermittedChannels == nil {
		t.Error("permitted_channels is null; the contract requires the field and an empty list denies every channel")
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{`"route_type"`, `"target"`, `"permitted_channels"`, `"filter_policy"`} {
		if !strings.Contains(string(body), field) {
			t.Errorf("the response omits %s, which the contract requires", field)
		}
	}
}

const richBundle = `
schema_version: 1
tenant: acme
labels:
  env: [prod]
groups: [sre]
rules:
  - id: rich
    effect: allow
    match:
      subject:
        groups: [sre]
    route:
      intent: hops-permitted
      channels: [session]
      requests:
        types: [exec]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands:
            - executable: /usr/bin/uptime
              form: exact
              argv: ["-p"]
      credentials:
        - method: ephemeral-account
          username: hoplock-svc
          platform: fortigate
          credential_kind: password
          expiry_posture: target-enforced
          lifetime_seconds: 600
          device_fields:
            vdom: root
      algorithm_profile: legacy-device
      enforcement:
        execution: account-restricted
      max_session_duration: 2h
      require_session_capture: true
      concurrency:
        max_sessions_per_subject: 2
`

// The other half of every pair above: a route that DOES say something emits
// it, in the shape the contract states. Without this half the assertions above
// pass against an assembly that cannot express the fields at all.
func TestARouteThatSaysSomethingEmitsItInTheContractsShape(t *testing.T) {
	t.Parallel()
	in := baseInput()
	snap, _ := evaluateFixture(t, richBundle, in)
	if snap == nil {
		t.Fatal("the fixture bundle denied; it is supposed to allow")
	}
	resp := assembleSnapshot(snap, routed{routeType: contract.RouteTypeDirect, target: "host.example.com"}, "d_test")

	// An ABSOLUTE instant, not a duration.
	deadline, ok := resp.Deadline()
	if !ok {
		t.Fatal("session_deadline is absent on a route that authored a maximum duration")
	}
	at, err := time.Parse(time.RFC3339, deadline)
	if err != nil {
		t.Fatalf("session_deadline %q does not parse as RFC 3339; a duration would not: %v", deadline, err)
	}
	if want := in.Now.Add(2 * time.Hour); !at.Equal(want) {
		t.Errorf("session_deadline = %v, want %v (anchored once, at evaluation)", at, want)
	}

	if !resp.CaptureRequired() {
		t.Error("require_session_capture is not set on a route that requires it")
	}
	if sub, _ := resp.Caps(); sub != 2 {
		t.Errorf("max_sessions_per_subject = %d, want 2", sub)
	}
	if resp.Profile() != contract.AlgorithmProfileLegacyDevice {
		t.Errorf("algorithm_profile = %q, want %q", resp.Profile(), contract.AlgorithmProfileLegacyDevice)
	}
	if resp.EnforcedExecution() != contract.ExecutionAccountRestricted {
		t.Errorf("execution rung = %q, want %q", resp.EnforcedExecution(), contract.ExecutionAccountRestricted)
	}

	state, ladder := resp.Ladder()
	if state != contract.LadderWalk || len(ladder) != 1 {
		t.Fatalf("ladder state %q with %d entries, want one walked entry", state, len(ladder))
	}
	entry := ladder[0]
	if entry.Params[contract.ParamUsername] != "hoplock-svc" {
		t.Errorf("params.username = %q, want hoplock-svc", entry.Params[contract.ParamUsername])
	}
	if got := entry.Params[contract.DeviceFieldPrefix+"vdom"]; got != "root" {
		t.Errorf("device_field.vdom = %q, want root: a field dropped on the way through makes the "+
			"provisioned administrator global", got)
	}
	if entry.Params[contract.ParamLifetimeSeconds] != "600" {
		t.Errorf("params.lifetime_seconds = %q, want 600", entry.Params[contract.ParamLifetimeSeconds])
	}
}

// A policy naming exactly one method yields a ONE-ENTRY LADDER. The singular
// `target_auth` field is gone from the contract, so a response carrying one is
// an unknown field the proxy fails closed on — an outage in front of a user.
func TestOneMethodIsAOneEntryLadderAndNeverABareObject(t *testing.T) {
	t.Parallel()
	snap, _ := evaluateFixture(t, minimalBundle, baseInput())
	resp := assembleSnapshot(snap, routed{routeType: contract.RouteTypeDirect, target: "h"}, "d_test")

	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), `"target_auth"`) {
		t.Fatalf("the response carries `target_auth`, which is no longer a field: %s", body)
	}
	if !strings.Contains(string(body), `"target_auth_ladder"`) {
		t.Fatalf("the response carries no ladder: %s", body)
	}
}

// ---------------------------------------------------------------------------
// the refusals
// ---------------------------------------------------------------------------

func TestAResponseTheProxyWouldRefuseIsRefusedHere(t *testing.T) {
	t.Parallel()

	username := map[string]string{contract.ParamUsername: "svc"}
	brokered := contract.TargetAuth{Method: contract.TargetAuthBrokeredKey, Params: username}
	ephemeral := contract.TargetAuth{Method: contract.TargetAuthEphemeralUser, Params: username}

	cases := []struct {
		name string
		resp *contract.AuthorizeResponse
		want string
	}{
		{
			name: "a ladder entry with no username",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{
					{Method: contract.TargetAuthBrokeredKey, Params: map[string]string{}},
				},
			},
			want: "params.username",
		},
		{
			name: "an applied rung on a ladder that provisions nothing",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{brokered},
				FilterPolicy:     contract.FilterPolicy{Mode: contract.FilterModeWhitelist, ExecMode: contract.ExecModeRestricted},
				Enforcement:      &contract.EnforcementPolicy{Execution: contract.ExecutionAccountRestricted},
			},
			want: "no ladder entry provisions the target",
		},
		{
			name: "no-interactive-shell beside permitted_requests that still allows shell",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder:  contract.TargetAuthLadder{ephemeral},
				PermittedRequests: &contract.RequestPolicy{Types: []contract.RequestType{contract.RequestTypeShell}},
				Enforcement:       &contract.EnforcementPolicy{Execution: contract.ExecutionNoInteractiveShell},
			},
			want: "still allows",
		},
		{
			name: "no-interactive-shell with no permitted_requests at all",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{ephemeral},
				Enforcement:      &contract.EnforcementPolicy{Execution: contract.ExecutionNoInteractiveShell},
			},
			want: "needs permitted_requests",
		},
		{
			name: "account-restricted without restricted exec",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{ephemeral},
				FilterPolicy:     contract.FilterPolicy{Mode: contract.FilterModeBlacklist},
				Enforcement:      &contract.EnforcementPolicy{Execution: contract.ExecutionAccountRestricted},
			},
			want: "exec_mode",
		},
		{
			name: "platform-authorized with no platform_role",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{ephemeral},
				Enforcement:      &contract.EnforcementPolicy{Execution: contract.ExecutionPlatformAuthorized},
			},
			want: "platform_role",
		},
		{
			name: "account-egress-restricted with no destinations",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{ephemeral},
				Enforcement:      &contract.EnforcementPolicy{Reach: contract.ReachAccountEgressRestricted},
			},
			want: "permitted_destinations",
		},
		{
			name: "an attested rung with no attestation",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{brokered},
				Enforcement:      &contract.EnforcementPolicy{Execution: contract.ExecutionPlatformAttested},
			},
			want: "unattributable",
		},
		{
			name: "an attestation with an empty reference",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{brokered},
				Enforcement: &contract.EnforcementPolicy{
					Execution:   contract.ExecutionPlatformAttested,
					Attestation: &contract.Attestation{AssertedBy: "netops"},
				},
			},
			want: "asserted_by and reference",
		},
		{
			name: "an attestation beside applied rungs",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{ephemeral},
				Enforcement: &contract.EnforcementPolicy{
					Execution:   contract.ExecutionProxyInspected,
					Attestation: &contract.Attestation{AssertedBy: "netops", Reference: "CR-1"},
				},
			},
			want: "beside applied rungs",
		},
		{
			name: "a device field whose name is not the contract's shape",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{{
					Method: contract.TargetAuthEphemeralAccount,
					Params: map[string]string{
						contract.ParamUsername:            "svc",
						contract.DeviceFieldPrefix + "VD": "root",
					},
				}},
			},
			want: "lowercase letters",
		},
		{
			name: "a device field with an empty value",
			resp: &contract.AuthorizeResponse{
				TargetAuthLadder: contract.TargetAuthLadder{{
					Method: contract.TargetAuthEphemeralAccount,
					Params: map[string]string{
						contract.ParamUsername:              "svc",
						contract.DeviceFieldPrefix + "vdom": "",
					},
				}},
			},
			want: "non-empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkServeable(tc.resp, "a-rule")
			if err == nil {
				t.Fatalf("the response was accepted; the proxy would refuse it as a contract violation")
			}
			var unserveable *UnserveableError
			if !asUnserveable(err, &unserveable) {
				t.Fatalf("error %v is not an *UnserveableError, so it would not be answered as an outage", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "a-rule") {
				t.Errorf("error = %v, want it to name the rule that produced the snapshot", err)
			}
		})
	}
}

// The other side of the applied-rung rule: an ATTESTED rung on a brokered-key
// route is fine, and refusing it would make the appliance estate unauthorable.
// That is the whole reason the attested kind exists.
func TestAnAttestedRungOnABrokeredKeyRouteIsServed(t *testing.T) {
	t.Parallel()
	resp := &contract.AuthorizeResponse{
		TargetAuthLadder: contract.TargetAuthLadder{{
			Method: contract.TargetAuthBrokeredKey,
			Params: map[string]string{contract.ParamUsername: "netadmin"},
		}},
		Enforcement: &contract.EnforcementPolicy{
			Execution:   contract.ExecutionPlatformAttested,
			Reach:       contract.ReachPlatformAttested,
			Attestation: &contract.Attestation{AssertedBy: "network-engineering", Reference: "CR-4711"},
		},
	}
	if err := checkServeable(resp, "appliance"); err != nil {
		t.Fatalf("an attested rung on an appliance route was refused: %v", err)
	}
}

// asUnserveable is errors.As, spelled out so this file does not import errors
// for one call.
func asUnserveable(err error, target **UnserveableError) bool {
	if e, ok := err.(*UnserveableError); ok {
		*target = e
		return true
	}
	return false
}

const grantBundle = `
schema_version: 1
tenant: acme
groups: [sre]
rules:
  - id: granted
    effect: allow
    match:
      grant:
        required: true
    route:
      intent: hops-permitted
      channels: [session]
      filter:
        mode: blacklist
      credentials:
        - method: brokered-key
          username: netadmin
          credential_ref: vault://x
`

// `grant_context.additional_context` admits a JSON STRING or a JSON OBJECT, and
// nothing else. The shape that was read is the shape that is written: a number,
// a list or a boolean is a contract violation rather than something to coerce,
// because this is stored verbatim for an auditor.
func TestAdditionalContextRoundTripsAsBothShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ctx  *model.AdditionalContext
		want string
	}{
		{
			name: "a string",
			ctx:  &model.AdditionalContext{Text: "change window agreed on the call"},
			want: `"additional_context":"change window agreed on the call"`,
		},
		{
			name: "an object",
			ctx:  &model.AdditionalContext{Fields: map[string]any{"severity": "sev1"}},
			want: `"additional_context":{"severity":"sev1"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := baseInput()
			in.Grants = []model.Grant{{
				ID:        "g-1",
				Subject:   in.Subject.ID,
				Origin:    model.GrantOriginExternal,
				Scope:     model.GrantScope{Name: "incident"},
				NotBefore: in.Now.Add(-time.Hour),
				ExpiresAt: in.Now.Add(time.Hour),
				External: &model.ExternalReference{
					System:            "servicenow",
					Reference:         "INC-4711",
					WindowStart:       in.Now.Add(-time.Hour),
					WindowEnd:         in.Now.Add(time.Hour),
					AdditionalContext: tc.ctx,
				},
			}}

			snap, _ := evaluateFixture(t, grantBundle, in)
			if snap == nil {
				t.Fatal("the grant rule denied")
			}
			resp := assembleSnapshot(snap, routed{routeType: contract.RouteTypeDirect, target: "h"}, "d_test")
			if resp.GrantContext == nil {
				t.Fatal("no grant_context on a route a grant supplied")
			}
			if resp.GrantContext.System != "servicenow" || resp.GrantContext.Reference != "INC-4711" {
				t.Errorf("grant_context = %+v, want the external system and its reference", resp.GrantContext)
			}
			// Recorded, not enforced: the window looks like a deadline and is
			// not one, and the bound the proxy enforces is session_deadline.
			if resp.GrantContext.WindowStart == "" || resp.GrantContext.WindowEnd == "" {
				t.Error("the asserted window is not carried")
			}

			body, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(body), tc.want) {
				t.Fatalf("additional_context did not round-trip as %s: %s", tc.name, body)
			}
		})
	}
}
