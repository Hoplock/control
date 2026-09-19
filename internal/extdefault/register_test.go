// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package extdefault_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/extdefault"
)

// phaseRef matches a phase number as the prompts and PLAN §10 spell one. The
// module-root guard checks that the numbers named here are real phases; this
// only insists that a promise of core behaviour names one at all.
var phaseRef = regexp.MustCompile(`\b0\d{3}\b`)

// sealed builds the registry the way the daemon does.
func sealed(t *testing.T) *ext.Extensions {
	t.Helper()
	reg := ext.NewRegistry()
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatalf("register defaults: %v", err)
	}
	x, err := reg.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return x
}

// TestEveryPointHasADefaultOrADocumentedAbsence is the acceptance test for
// M15's second invariant. Every extension point must be in exactly one of
// three states, and this is what makes "we will ship a default" fail to build
// rather than fail to happen.
func TestEveryPointHasADefaultOrADocumentedAbsence(t *testing.T) {
	x := sealed(t)
	byPoint := make(map[ext.Point]ext.Status)
	for _, st := range x.Status() {
		byPoint[st.Info.Point] = st
	}

	for _, info := range ext.Points() {
		st := byPoint[info.Point]
		switch info.WhenAbsent {
		case ext.WhenAbsentDefault:
			// Control's own wiring must have registered it, and the
			// registration must be Control's.
			if !st.Registered() {
				t.Errorf("%s promises an implementation from Control's wiring and extdefault.Register supplied none", info.Interface)
				continue
			}
			if !st.Registrations[0].Default || st.Registrations[0].Provider != ext.ProviderControl {
				t.Errorf("%s is filled by %v rather than by Control's default", info.Interface, st.Registrations[0])
			}
		case ext.WhenAbsentCore:
			// The behaviour is Control's own code path, so nothing is
			// registered — but the catalogue must name what supplies it
			// and the phase that builds it, so the promise is checkable
			// against the delivery plan rather than taken on trust.
			if st.Registered() {
				t.Errorf("%s is documented as core behaviour yet something registered a default for it; "+
					"decide which it is", info.Interface)
			}
			if !phaseRef.MatchString(info.ControlShips) {
				t.Errorf("%s says Control supplies %q but names no phase that builds it",
					info.Interface, info.ControlShips)
			}
		case ext.WhenAbsentDisabled:
			if st.Registered() {
				t.Errorf("%s is documented as disabled when absent yet Control registered something for it", info.Interface)
			}
		default:
			t.Errorf("%s has an unknown disposition %d", info.Interface, int(info.WhenAbsent))
		}
	}
}

func TestRegisterFillsInANodeIdentityWhenNothingSuppliesOne(t *testing.T) {
	x := sealed(t)
	coord, ok := x.ClusterCoordinator()
	if !ok {
		t.Fatal("no cluster coordinator after Control's wiring ran")
	}
	node, err := coord.Node(t.Context())
	if err != nil {
		t.Fatalf("Node: %v", err)
	}
	if node.ID == "" {
		t.Error("the default coordinator reported an empty node id; a deployment must start before phase 0015 gives it a real identity")
	}
}

func TestRegisterRejectsANilRegistry(t *testing.T) {
	if err := extdefault.Register(nil, extdefault.Deps{}); err == nil {
		t.Fatal("Register accepted a nil registry")
	}
}

// TestLogNamesEveryPointInPlayAndEveryPointThatIsNot is the "an extension is
// visible" requirement: an invisible extension is indistinguishable from a bug
// in Control, and so is an absent one whose absence explains the behaviour.
func TestLogNamesEveryPointInPlayAndEveryPointThatIsNot(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	reg := ext.NewRegistry()
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatalf("register defaults: %v", err)
	}
	if err := reg.RegisterAuditSink(ext.Registration{Provider: "example.com/siem", Version: "3.1"}, nopSink{}); err != nil {
		t.Fatalf("register sink: %v", err)
	}
	x, err := reg.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	extdefault.Log(log, x)

	points := make(map[string]map[string]any)
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		if p, ok := rec["point"].(string); ok {
			points[p] = rec
		}
	}
	for _, info := range ext.Points() {
		rec, ok := points[info.Interface]
		if !ok {
			t.Errorf("%s never appeared in the start-up log", info.Interface)
			continue
		}
		if _, registered := rec["provider"]; !registered {
			if rec["absent_behaviour"] != info.Absent {
				t.Errorf("%s logged absent behaviour %v, want %q", info.Interface, rec["absent_behaviour"], info.Absent)
			}
		}
	}
	if got := points["AuditSink"]["provider"]; got != "example.com/siem" {
		t.Errorf("the registered sink logged provider %v, want example.com/siem", got)
	}

	// A nil logger or a nil set must not panic: logging is never the reason
	// a server fails to start.
	extdefault.Log(nil, x)
	extdefault.Log(log, nil)
}
