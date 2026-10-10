// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package server starts Hoplock Control from a host binary (M15).
//
// Hoplock Enterprise is a Go program that imports this module, registers its
// implementations of the interfaces in ext, and starts Control — and this is
// the package it starts it with. hoplock-control itself is one call to [Main]
// with zero [Options], so there is one start-up path and not two that drift.
//
// A host gets four things, and nothing else:
//
//  1. Control's whole command line — the daemon and every subcommand — through
//     [Main], so an Enterprise binary can migrate, seed and verify without
//     Control's binary beside it.
//  2. Top-level sections of its own in Control's configuration file
//     ([Options.HostSections]), handed to it undecoded.
//  3. North-bound routes of its own ([Options.Routes]), behind Control's
//     authentication, tenant resolution and RBAC, on the north-bound listener
//     and nowhere else (M2, M18).
//  4. Control's error envelope ([WriteError]), with codes it declares (M21).
//
// THIS PACKAGE IS A COMPATIBILITY PROMISE, exactly as ext is: every exported
// identifier here is something a host builds against, and changing one breaks
// its build. It is a thin façade — the public types, and forwarding into
// Control's internals, where every check a host can fail lives.
package server

import (
	"context"
	"io"
	"os"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/daemon"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
)

// Main is Hoplock Control's command line: the daemon and every subcommand
// (migrate, seed, audit-verify, identity, ca). hoplock-control's main is one
// call to it with zero Options; a host binary's main is one call to it with
// its own.
//
// It returns rather than exiting, and it installs no signal handler: the
// caller owns its exit code and its signals, and cancelling ctx stops the
// daemon. A host with commands of its own dispatches them before calling Main;
// Control does not route to them. Every subcommand that reads the
// configuration accepts the host's sections, and none but the daemon hands
// them to HostConfig.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer, o Options) error {
	return daemon.Main(ctx, args, stdout, stderr, o.host())
}

// Run is the daemon alone: decode the configuration read from config, hand
// each host section to HostConfig, register Control's defaults into
// o.Registry, seal it, mount o.Routes, serve both listeners, and return when
// ctx is done (nil) or a listener fails (the error).
//
// Its structured log goes to standard error, and it becomes slog's default
// logger, so a host's own lines share its format and level. It is exported for
// tests and for a host that builds its own command line; [Main] calls it for
// the daemon.
func Run(ctx context.Context, config io.Reader, o Options) error {
	return daemon.Run(ctx, config, o.host(), os.Stderr)
}

// Options is what a host adds to Control. The zero value is Control alone.
//
// Every way a host can get these wrong is refused when Main or Run is called,
// before the configuration file is read, a database is opened or a port is
// bound: a host binary that will never start says so at once.
type Options struct {
	// Provider names the host in the route listing and the start-up log
	// (e.g. "hoplock/enterprise"). Required when Routes, HostSections or
	// ErrorCodes is non-empty; ext.ProviderControl is refused.
	Provider string

	// Registry is populated by the host and NOT sealed: Control registers
	// its own defaults into it and seals it. An extension beats a Control
	// default at a single-implementation point, in either order. Nil means
	// ext.NewRegistry(), which is Control alone.
	Registry *ext.Registry

	// HostSections are top-level configuration keys the strict decoder
	// accepts without defining them. Each must be a lower-case key like
	// Control's own, and a name Control itself defines is refused: a host
	// may not shadow `listeners`. Every other unknown key is still an
	// error. Declaring sections requires HostConfig.
	HostSections []string
	// HostConfig is called by Run, once per section present in the file and
	// in file order, with that section re-encoded as YAML — before the
	// registry is sealed, so a host may register an extension it builds from
	// its section there, and before anything serves. An error fails
	// start-up. Absent sections are not called.
	HostConfig func(section string, raw []byte) error

	// Routes are the host's north-bound routes. See Route.
	Routes []Route
	// ErrorCodes are the codes the host's handlers may answer with (M21),
	// beside Control's own. Each is snake_case and none may be one of
	// Control's.
	ErrorCodes []string
}

// host forwards Options into Control's internals, field for field.
func (o Options) host() daemon.Host {
	routes := make([]north.HostRoute, 0, len(o.Routes))
	for _, r := range o.Routes {
		routes = append(routes, north.HostRoute{
			Method:     r.Method,
			Pattern:    r.Pattern,
			Permission: identity.Permission(r.Permission),
			Summary:    r.Summary,
			Handler:    r.Handler,
		})
	}
	return daemon.Host{
		Provider:   o.Provider,
		Registry:   o.Registry,
		Sections:   o.HostSections,
		Config:     o.HostConfig,
		Routes:     routes,
		ErrorCodes: o.ErrorCodes,
	}
}
