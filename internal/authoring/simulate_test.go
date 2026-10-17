// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/policy/model"
)

// Every field a dry-run case carries reaches the engine input it names, and
// nothing reaches it that the case did not say.
func TestADryRunCaseMapsFieldForField(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	in := DryRunInput{
		Name: "full", Now: now,
		Subject: DryRunSubject{ID: "alice", Source: "oidc", Groups: []string{"sre"}, Claims: map[string]string{"dept": "ops"},
			AuthMethod: "password-mfa", MFA: true},
		Target:  DryRunTarget{Hostname: "h.example.com", Port: 2222, Zone: "edge", Labels: map[string]string{"env": "prod"}},
		Context: DryRunContext{ProxyID: "proxy-1", SourceAddr: "10.0.0.7"},
		Device:  &DryRunDevice{Posture: map[string]string{"disk": "encrypted"}},
		Grants: []DryRunGrant{{ID: "g-1", Origin: "external", Scope: "ops", Targets: []string{"h.example.com"},
			Labels: map[string]string{"env": "prod"}, Zones: []string{"edge"}, NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}},
	}
	got, err := in.input()
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	want := model.Input{
		Now: now,
		Subject: model.Subject{ID: "alice", Source: "oidc", Groups: []string{"sre"}, Claims: map[string]string{"dept": "ops"},
			AuthMethod: model.AuthMethodPasswordMFA, MFA: true},
		Target:  model.Target{Hostname: "h.example.com", Port: 2222, Zone: "edge", Labels: map[string]string{"env": "prod"}},
		Context: model.Context{ProxyID: "proxy-1", SourceAddr: netip.MustParseAddr("10.0.0.7")},
		Device:  &model.Device{Posture: map[string]string{"disk": "encrypted"}},
		Grants: []model.Grant{{ID: "g-1", Subject: "alice", Origin: model.GrantOriginExternal,
			Scope:     model.GrantScope{Name: "ops", Targets: []string{"h.example.com"}, Labels: map[string]string{"env": "prod"}, Zones: []string{"edge"}},
			NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mapped\n %+v\nwant\n %+v", got, want)
	}

	// The defaults of what a case may omit: no device, no grants, an open
	// start, and an administrator's grant.
	minimal, err := DryRunInput{Now: now, Grants: []DryRunGrant{{ID: "g", Scope: "ops", ExpiresAt: now.Add(time.Hour)}}}.input()
	if err != nil {
		t.Fatalf("minimal: %v", err)
	}
	if minimal.Device != nil || minimal.Grants[0].Origin != model.GrantOriginAdministrator || !minimal.Grants[0].NotBefore.IsZero() {
		t.Errorf("minimal = %+v", minimal)
	}
}

// A case whose omission or typo would make the engine answer a different
// question than the one written is refused, never answered: a grant with no
// expiry is never live, and an origin or a method the vocabulary does not
// have matches no rule that names one.
func TestADryRunCaseThatAsksAnotherQuestionIsRefused(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		in   DryRunInput
		says string
	}{
		{"no instant", DryRunInput{}, "now"},
		{"a source address that is not one", DryRunInput{Now: now, Context: DryRunContext{SourceAddr: "office"}}, "source_addr"},
		{"a grant with no expiry", DryRunInput{Now: now, Grants: []DryRunGrant{{ID: "g", Scope: "ops"}}}, "expires_at"},
		{"an origin nobody grants from", DryRunInput{Now: now, Grants: []DryRunGrant{{ID: "g", Scope: "ops", Origin: "admin", ExpiresAt: now.Add(time.Hour)}}}, "origin"},
		{"a method nobody authenticates with", DryRunInput{Now: now, Subject: DryRunSubject{AuthMethod: "password"}}, "auth_method"},
	} {
		_, err := tc.in.input()
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v, want a refusal naming %s", tc.name, err, tc.says)
		}
	}
}
