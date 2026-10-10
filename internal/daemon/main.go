// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/store"
)

// defaultConfigPath is where the server looks when --config is not given.
const defaultConfigPath = "config.yaml"

// Host is what a host binary adds to Control (M15): the internal form of
// server.Options, which forwards here field for field.
//
// The zero Host is Control alone, and it is what hoplock-control runs with.
// Everything a host can get wrong is refused by [Host.Validate] before any
// file is read, any database is opened or any port is bound, so a host
// binary that will never start says so at once rather than one subcommand at
// a time.
type Host struct {
	// Provider names the host in the route listing and the start-up log.
	Provider string
	// Registry is populated by the host and not sealed. Nil is Control
	// alone.
	Registry *ext.Registry
	// Sections are the top-level configuration keys the host owns.
	Sections []string
	// Config receives each section present in the file, re-encoded as YAML.
	// Only Run calls it.
	Config func(section string, raw []byte) error
	// Routes are the host's north-bound routes.
	Routes []north.HostRoute
	// ErrorCodes are the codes the host's routes may answer with (M21).
	ErrorCodes []string
}

// Validate refuses a host that Control cannot start.
func (h Host) Validate() error {
	hosted := len(h.Routes) > 0 || len(h.Sections) > 0 || len(h.ErrorCodes) > 0
	if hosted && strings.TrimSpace(h.Provider) == "" {
		return fmt.Errorf("server: a host that adds routes, configuration sections or error codes must name itself in Provider, so an operator can tell what is not Control's")
	}
	if h.Provider == ext.ProviderControl {
		return fmt.Errorf("server: Provider %q is Control's own name; a host names itself", ext.ProviderControl)
	}
	if err := config.CheckHostSections(h.Sections); err != nil {
		return err
	}
	if len(h.Sections) > 0 && h.Config == nil {
		// A declared section nobody reads is a section the strict decoder
		// stops guarding and nothing else starts to: a typo in it would be
		// accepted in silence.
		return fmt.Errorf("server: host configuration sections %v are declared but HostConfig is nil, so nothing would read them", h.Sections)
	}
	return north.CheckHost(h.Provider, h.Routes, h.ErrorCodes)
}

// loadConfig is how every subcommand reads the file: Control's schema plus the
// host's sections, which a subcommand accepts and never reads.
func (h Host) loadConfig(path string) (*config.Config, error) {
	cfg, _, err := config.LoadHost(path, h.Sections)
	return cfg, err
}

// Main is Control's command line: the daemon and every subcommand. It returns
// an error instead of exiting, so a host owns its exit code and its signals,
// and a test needs no process.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer, h Host) error {
	if err := h.Validate(); err != nil {
		return err
	}

	// Subcommands are dispatched before flags are parsed, so `migrate` can
	// carry flags of its own without the daemon's flag set having to know
	// about them. A leading argument that is not a flag and not a known
	// subcommand is an error rather than something to ignore.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "migrate":
			return runMigrate(ctx, h, args[1:], stdout, stderr)
		case "seed":
			return runSeed(ctx, h, args[1:], stdout, stderr)
		case "audit-verify":
			return runAuditVerify(ctx, h, args[1:], stdout, stderr)
		case "identity":
			return runIdentity(ctx, h, args[1:], stdout, stderr)
		case "ca":
			return runCA(ctx, h, args[1:], stdout, stderr)
		default:
			return fmt.Errorf(
				"unknown subcommand %q (known: migrate, seed, audit-verify, identity, ca)", args[0])
		}
	}

	fs := flag.NewFlagSet("hoplock-control", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		_, err := fmt.Fprintln(stdout, versionString())
		return err
	}

	cfg, sections, err := config.LoadHost(*configPath, h.Sections)
	if err != nil {
		return err
	}
	return start(ctx, cfg, sections, h, stderr)
}

// Run is the daemon alone, with the configuration read from config. The log
// goes to logOut.
func Run(ctx context.Context, cfgDoc io.Reader, h Host, logOut io.Writer) error {
	if err := h.Validate(); err != nil {
		return err
	}
	cfg, sections, err := config.ParseHost(cfgDoc, h.Sections)
	if err != nil {
		return err
	}
	return start(ctx, cfg, sections, h, logOut)
}

// start runs the daemon from a configuration already read and validated, and
// returns when ctx is done (nil) or a listener fails (the error).
func start(ctx context.Context, cfg *config.Config, sections []config.HostSection, h Host, logOut io.Writer) error {
	log := slog.New(slog.NewJSONHandler(logOut, &slog.HandlerOptions{
		Level: cfg.Log.SlogLevel(),
	}))
	slog.SetDefault(log)

	// The host reads its own sections first, before its registry is sealed,
	// so an extension it builds from its configuration can still be
	// registered. Nothing has opened a database or bound a port yet: a host
	// that refuses its configuration stops a server that never started.
	for _, s := range sections {
		if err := h.Config(s.Name, s.Raw); err != nil {
			return fmt.Errorf("config: section %s: %w", s.Name, err)
		}
	}

	// Extensions are registered before anything starts and are immutable
	// afterwards (PLAN M15), so a request can never see a half-registered
	// seam. Control's own defaults go INTO the host's registry: an extension
	// beats a Control default at a single point in either registration order
	// (0004), so the host's registrations stand. The sealed set is logged
	// point by point, because an operator debugging behaviour has to be able
	// to see what is in play.
	registry := h.Registry
	if registry == nil {
		registry = ext.NewRegistry()
	}
	if err := extdefault.Register(registry, extdefault.Deps{Node: ext.Node{
		Version:   versionString(),
		StartedAt: time.Now().UTC(),
	}}); err != nil {
		return err
	}
	if err := registerDeclarative(registry, cfg.AccessContext); err != nil {
		return err
	}
	extensions, err := registry.Seal()
	if err != nil {
		return err
	}

	attrs := []any{
		"version", versionString(),
		"tenant", cfg.Tenant,
		"south_listener", cfg.Listeners.South,
		"north_listener", cfg.Listeners.North,
		"extension_providers", extensions.Providers(),
	}
	if h.Provider != "" {
		attrs = append(attrs, "host", h.Provider, "host_sections", len(sections), "host_routes", len(h.Routes))
	}
	log.Info("starting", attrs...)
	extdefault.Log(log, extensions)

	// The store is opened and NOT migrated. Migrations are an explicit
	// command (PLAN §8): two nodes starting together must not race to build
	// the schema.
	st, err := store.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	// Both listeners are bound here and share nothing but the bring-up (M2).
	if err := serve(ctx, cfg, st, extensions, h, log); err != nil {
		return err
	}

	log.Info("shutting down")
	return nil
}
