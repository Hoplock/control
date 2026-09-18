// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package extdefault

import (
	"fmt"
	"log/slog"

	"github.com/hoplock/control/ext"
)

// Deps is what Control's defaults need from the process wiring them. It is a
// struct rather than a list of arguments so that a later phase adding a default
// with a new dependency does not change this function's signature in every
// caller.
type Deps struct {
	// Node is this process's membership record. A zero ID is filled in with
	// a placeholder, because a deployment must still start when nothing has
	// given it an identity yet — phase 0015 makes that identity real (M19).
	Node ext.Node
}

// Register adds Control's own implementations to reg. It is the wiring the
// registry's seal check is written against: every extension point whose
// catalogue entry says ext.WhenAbsentDefault is registered here, and a test
// asserts that the two sets are the same, so a seam added later cannot ship
// claiming a default that nothing supplies.
//
// It is called before any Enterprise registration is required to have happened,
// and it does not care about the order: a default and an extension at the same
// single-implementation point are not a conflict — the extension supersedes it
// and the registry listing records that it did.
func Register(reg *ext.Registry, deps Deps) error {
	if reg == nil {
		return fmt.Errorf("extdefault: a registry is required")
	}
	node := deps.Node
	if node.ID == "" {
		node.ID = "local"
	}

	registration := ext.Registration{Provider: ext.ProviderControl, Default: true}
	if err := reg.RegisterClusterCoordinator(registration, NewSingleNode(node)); err != nil {
		return fmt.Errorf("extdefault: register cluster coordinator: %w", err)
	}
	return nil
}

// Log writes one line per extension point, including the points where nothing
// is registered. An operator debugging behaviour has to be able to see that an
// extension is in play, because an invisible extension is indistinguishable
// from a bug in Control — and "nothing is registered here, so Control does X"
// is exactly as useful to them as the other kind of line.
//
// The north-bound API exposes the same information (phase 0014); this is the
// copy that exists before anyone can call an API.
func Log(log *slog.Logger, x *ext.Extensions) {
	if log == nil || x == nil {
		return
	}
	for _, st := range x.Status() {
		if !st.Registered() {
			log.Debug("extension point",
				"point", st.Info.Point.String(),
				"registered", false,
				"when_absent", st.Info.WhenAbsent.String(),
				"absent_behaviour", st.Info.Absent,
			)
			continue
		}
		for _, r := range st.Registrations {
			log.Info("extension registered",
				"point", st.Info.Point.String(),
				"provider", r.Provider,
				"version", r.Version,
				"control_default", r.Default,
			)
		}
	}
}
