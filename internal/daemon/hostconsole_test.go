// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"net/http"
	"slices"
	"testing"

	"github.com/hoplock/control/internal/console"
	"github.com/hoplock/control/internal/httpapi/north"
)

// A host's console reaches the mount under the host's own provider, always,
// and the shell is told about it only when there is one (0069).
func TestAHostsConsoleIsMountedAsTheHostsAndOnlyWhenItHasOne(t *testing.T) {
	control := north.Console{Patterns: console.Patterns(), Handler: http.NotFoundHandler()}

	alone := Host{}
	if got := alone.consoles(control); len(got) != 1 || got[0].Provider != "" {
		t.Fatalf("Control alone mounts %+v", got)
	}
	if alone.shellHost() != (console.Host{}) {
		t.Fatalf("Control alone tells the shell %+v", alone.shellHost())
	}
	if err := alone.checkConsole(); err != nil {
		t.Fatalf("Control alone: %v", err)
	}

	const provider = "hoplock/enterprise"
	h := Host{Provider: provider, Console: &HostConsole{
		Patterns:  []string{north.HostCatalogueDefault(provider)},
		Catalogue: []string{provider + ":approvals.title"},
		Handler:   http.NotFoundHandler(),
	}}
	got := h.consoles(control)
	if len(got) != 2 || got[1].Provider != provider || !slices.Equal(got[1].Patterns, h.Console.Patterns) ||
		!slices.Equal(got[1].Catalogue, h.Console.Catalogue) {
		t.Fatalf("the host's console is mounted as %+v", got)
	}
	if want := (console.Host{Provider: provider, Catalogue: "/host-console/hoplock/enterprise/locales/"}); h.shellHost() != want {
		t.Fatalf("the shell is told %+v, want %+v", h.shellHost(), want)
	}
	if err := h.Validate(); err != nil {
		t.Fatalf("a host whose console only serves its catalogue was refused: %v", err)
	}
}
