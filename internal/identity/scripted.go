// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ScriptedMFAName is the provider name a scripted enrollment carries.
const ScriptedMFAName = "scripted"

// ScriptedMFAConfig is one subject's script, stored in `subject_mfa.config`.
//
// It mirrors the proxy mock's MFA fixture field for field, and that is the
// point rather than a coincidence: the conformance suite drives approve, deny
// and expiry through the same shapes against both servers, so a case that
// passes there and fails here is a real disagreement rather than two
// differently-configured fixtures.
//
// It carries YAML tags as well as JSON ones because the same shape is written
// by hand in a seed document and stored as JSON in the column. One struct with
// two tag sets keeps the file and the row from drifting; a separate seed type
// would be a second place to add a field to.
type ScriptedMFAConfig struct {
	// PendingPolls is how many polls answer "still pending" before the
	// challenge resolves.
	PendingPolls int `json:"pending_polls,omitempty" yaml:"pending_polls"`
	// Decision is what it resolves to: "approve" or "deny".
	Decision string `json:"decision,omitempty" yaml:"decision"`
	// PollAfterMS and TTLMS are the terms. Zero takes the service default.
	PollAfterMS int `json:"poll_after_ms,omitempty" yaml:"poll_after_ms"`
	TTLMS       int `json:"ttl_ms,omitempty" yaml:"ttl_ms"`
	// Prompt is what the user is shown.
	Prompt string `json:"prompt,omitempty" yaml:"prompt"`
}

// The two decisions a script can carry.
const (
	ScriptApprove = "approve"
	ScriptDeny    = "deny"
)

// ScriptedMFA is the deterministic provider this phase ships.
//
// IT IS A TEST AND CI FACILITY AND IT IS NOT A SECOND FACTOR. Nothing is
// delivered out of band and nobody is asked anything: it resolves the way its
// configuration says it will, after the number of polls its configuration
// names. A deployment that enrolls a subject with it has given that subject a
// second factor that approves itself, which is why the server logs the
// provider name on every challenge it issues.
//
// It holds no state of its own. Everything it needs is the stored config plus
// the poll count the orchestrator keeps, so two nodes answer a poll
// identically and a restart changes nothing (M5).
type ScriptedMFA struct{}

// Name implements MFAProvider.
func (ScriptedMFA) Name() string { return ScriptedMFAName }

// Begin implements MFAProvider. The ref carries nothing because a scripted
// challenge has nothing out of band to refer to.
func (ScriptedMFA) Begin(_ context.Context, id Identity, config json.RawMessage) (MFATerms, error) {
	cfg, err := parseScript(config)
	if err != nil {
		return MFATerms{}, err
	}
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = fmt.Sprintf("Approve the sign-in for %s", id.Login)
	}
	return MFATerms{
		Prompt:    prompt,
		PollAfter: time.Duration(cfg.PollAfterMS) * time.Millisecond,
		TTL:       time.Duration(cfg.TTLMS) * time.Millisecond,
	}, nil
}

// Poll implements MFAProvider.
//
// It is a pure function of the config and the poll count. A poll the server
// rate-limited still counts — determinism here is about being reproducible,
// not about modelling how long a person takes to reach for their phone.
func (ScriptedMFA) Poll(_ context.Context, config json.RawMessage, _ string, polls int) (MFAResult, error) {
	cfg, err := parseScript(config)
	if err != nil {
		return "", err
	}
	if polls <= cfg.PendingPolls {
		return MFAPending, nil
	}
	switch cfg.Decision {
	case ScriptApprove:
		return MFAApproved, nil
	case ScriptDeny, "":
		// An unset decision refuses. A scripted second factor that
		// approved by default would make a half-filled enrollment row
		// indistinguishable from a configured one, in the direction that
		// grants access.
		return MFARefused, nil
	default:
		return "", fmt.Errorf("identity: scripted MFA decision %q is neither %q nor %q",
			cfg.Decision, ScriptApprove, ScriptDeny)
	}
}

func parseScript(config json.RawMessage) (ScriptedMFAConfig, error) {
	var cfg ScriptedMFAConfig
	if len(config) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		// Unreadable configuration is an OUTAGE, not a refusal: this server
		// cannot tell what the subject is enrolled with, and answering
		// "denied" would report an operator's typo as the user's fault.
		return ScriptedMFAConfig{}, fmt.Errorf("identity: unreadable scripted MFA config: %w", err)
	}
	if cfg.PendingPolls < 0 {
		return ScriptedMFAConfig{}, fmt.Errorf("identity: scripted MFA pending_polls is %d", cfg.PendingPolls)
	}
	return cfg, nil
}
