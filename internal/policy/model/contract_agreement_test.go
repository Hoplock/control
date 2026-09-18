// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"slices"
	"testing"

	"github.com/hoplock/control/internal/contract"
)

// This package does not import internal/contract, and that is deliberate: M3
// keeps the compiler as a boundary, and an engine that imported the wire types
// could not be replaced without them. The price is a duplicated vocabulary, and
// this file is what stops the duplicate drifting.
//
// The comparison runs in BOTH directions, like the contract's own enum test: a
// member added upstream and a member deleted upstream fail here equally loudly.
// The snapshot this engine produces has to be expressible in the contract's
// output shapes, and that is only true member for member.

// agree asserts that two vocabularies hold exactly the same values. The model's
// zero-valued "the author said nothing" members are passed as `extra`: the
// contract has no way to spell them because absence on the wire *is* them.
func agree[A ~string, B ~string](t *testing.T, axis string, mine []A, theirs []B, extra ...A) {
	t.Helper()

	want := make([]string, 0, len(theirs))
	for _, v := range theirs {
		want = append(want, string(v))
	}
	got := make([]string, 0, len(mine))
	for _, v := range mine {
		if slices.Contains(extra, v) {
			continue
		}
		got = append(got, string(v))
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("%s disagrees with the contract:\n  model:    %v\n  contract: %v", axis, got, want)
	}
}

func TestVocabularyAgreesWithTheContract(t *testing.T) {
	agree(t, "request type", requestTypes, []contract.RequestType{
		contract.RequestTypePTYReq, contract.RequestTypeShell, contract.RequestTypeExec,
		contract.RequestTypeEnv, contract.RequestTypeX11Req, contract.RequestTypeAuthAgentReq,
	})
	agree(t, "credential method", credentialMethods, []contract.TargetAuthMethod{
		contract.TargetAuthEphemeralUser, contract.TargetAuthEphemeralAccount,
		contract.TargetAuthBrokeredKey, contract.TargetAuthStaticKey,
	})
	agree(t, "credential kind", credentialKinds, []contract.CredentialKind{
		contract.CredentialKindPassword, contract.CredentialKindPublicKey,
	})
	agree(t, "expiry posture", expiryPostures, []contract.ExpiryPosture{
		contract.ExpiryPostureTargetEnforced, contract.ExpiryPostureProxyEnforced,
		contract.ExpiryPostureAcceptedRisk,
	})
	agree(t, "algorithm profile", algorithmProfiles, []contract.AlgorithmProfile{
		contract.AlgorithmProfileDefault, contract.AlgorithmProfileLegacyRSASHA1,
		contract.AlgorithmProfileLegacyDevice,
	})
	agree(t, "filter mode", filterModes, []contract.FilterMode{
		contract.FilterModeWhitelist, contract.FilterModeBlacklist,
	})
	agree(t, "exec mode", execModes, []contract.ExecMode{
		contract.ExecModeFiltered, contract.ExecModeRestricted,
	})
	agree(t, "filter action", filterActions, []contract.FilterAction{
		contract.FilterActionAllowAndLog, contract.FilterActionBlockCommand,
		contract.FilterActionWarnAndContinue, contract.FilterActionKillSession,
	})
	agree(t, "command form", commandForms, []contract.CommandForm{
		contract.CommandFormExact, contract.CommandFormPositional,
	})
	agree(t, "argument kind", argumentKinds, []contract.ArgumentKind{
		contract.ArgumentKindLiteral, contract.ArgumentKindPrefix,
		contract.ArgumentKindOneOf, contract.ArgumentKindAny,
	})
	agree(t, "execution rung", executionRungs, []contract.ExecutionRung{
		contract.ExecutionProxyInspected, contract.ExecutionNoInteractiveShell,
		contract.ExecutionAccountRestricted, contract.ExecutionAccountConfined,
		contract.ExecutionPlatformAuthorized, contract.ExecutionPlatformAttested,
	})
	agree(t, "reach rung", reachRungs, []contract.ReachRung{
		contract.ReachProxyChannelPolicy, contract.ReachAccountEgressRestricted,
		contract.ReachAccountNetworkIsolated, contract.ReachPlatformAttested,
	})
	agree(t, "authentication method", authMethods, []contract.AuthMethod{
		contract.AuthMethodCert, contract.AuthMethodPasswordMFA,
	})
}

// TestRungClassificationAgrees: which rungs are attested and which the proxy
// applies to an account it administers is a judgement both sides make, and the
// compiler refuses a route on the strength of it. Two implementations that
// disagree here would refuse different policies.
func TestRungClassificationAgrees(t *testing.T) {
	for _, r := range executionRungs {
		theirs := contract.ExecutionRung(r)
		if r.Attested() != theirs.Attested() {
			t.Errorf("execution %q: attested %v here, %v in the contract", r, r.Attested(), theirs.Attested())
		}
		if r.Applied() != theirs.RequiresProvisioning() {
			t.Errorf("execution %q: applied %v here, %v in the contract", r, r.Applied(), theirs.RequiresProvisioning())
		}
	}
	for _, r := range reachRungs {
		theirs := contract.ReachRung(r)
		if r.Attested() != theirs.Attested() {
			t.Errorf("reach %q: attested %v here, %v in the contract", r, r.Attested(), theirs.Attested())
		}
		if r.Applied() != theirs.RequiresProvisioning() {
			t.Errorf("reach %q: applied %v here, %v in the contract", r, r.Applied(), theirs.RequiresProvisioning())
		}
	}
	for _, m := range credentialMethods {
		if m.Provisions() != contract.TargetAuthMethod(m).Provisions() {
			t.Errorf("method %q disagrees about whether the proxy administers the account", m)
		}
	}
}

// TestDeviceFieldShapeAgrees. The shape rules are duplicated rather than
// imported, so this is the check that keeps the duplicate honest.
func TestDeviceFieldShapeAgrees(t *testing.T) {
	if DeviceFieldNamePattern != contract.DeviceFieldNamePattern {
		t.Errorf("name pattern %q != %q", DeviceFieldNamePattern, contract.DeviceFieldNamePattern)
	}
	if DeviceFieldMaxNameLen != contract.DeviceFieldMaxNameLen {
		t.Errorf("max name length %d != %d", DeviceFieldMaxNameLen, contract.DeviceFieldMaxNameLen)
	}
	if DeviceFieldMaxValueLen != contract.DeviceFieldMaxValueLen {
		t.Errorf("max value length %d != %d", DeviceFieldMaxValueLen, contract.DeviceFieldMaxValueLen)
	}
	if DeviceFieldMaxPerEntry != contract.DeviceFieldMaxPerEntry {
		t.Errorf("max fields per entry %d != %d", DeviceFieldMaxPerEntry, contract.DeviceFieldMaxPerEntry)
	}
}

// TestUsernameIsTheContractsRequiredParameter ties the model's Username type to
// the parameter it renders into. The contract requires `username` on every
// method it defines, and the compiler's rejection is written against that name.
func TestUsernameIsTheContractsRequiredParameter(t *testing.T) {
	if contract.ParamUsername != "username" {
		t.Fatalf("the contract's required parameter is %q, not `username`", contract.ParamUsername)
	}
}
