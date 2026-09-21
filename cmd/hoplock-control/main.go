// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Command hoplock-control is the Hoplock Control server daemon. It will
// eventually serve both listeners (PLAN M2); at this phase it loads its
// configuration, reports who it is, and waits to be told to stop.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/store"
)

// defaultConfigPath is where the server looks when --config is not given.
const defaultConfigPath = "config.yaml"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "hoplock-control: %v\n", err)
		os.Exit(1)
	}
}

// run is main's testable body: it returns an error instead of exiting, so the
// startup path can be exercised without a process.
func run(args []string, stdout, stderr io.Writer) error {
	// Subcommands are dispatched before flags are parsed, so `migrate` can
	// carry flags of its own without the daemon's flag set having to know
	// about them. A leading argument that is not a flag and not a known
	// subcommand is an error rather than something to ignore.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "migrate":
			return runMigrate(args[1:], stdout, stderr)
		case "seed":
			return runSeed(args[1:], stdout, stderr)
		case "audit-verify":
			return runAuditVerify(args[1:], stdout, stderr)
		case "identity":
			return runIdentity(args[1:], stdout, stderr)
		case "ca":
			return runCA(args[1:], stdout, stderr)
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

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{
		Level: cfg.Log.SlogLevel(),
	}))
	slog.SetDefault(log)

	// Extensions are registered before anything starts and are immutable
	// afterwards (PLAN M15), so a request can never see a half-registered
	// seam. In this binary only Control's own defaults are registered; the
	// Hoplock Enterprise binary does the same and adds its own before
	// sealing. The sealed set is logged point by point, because an operator
	// debugging behaviour has to be able to see what is in play.
	registry := ext.NewRegistry()
	if err := extdefault.Register(registry, extdefault.Deps{Node: ext.Node{
		Version:   versionString(),
		StartedAt: time.Now().UTC(),
	}}); err != nil {
		return err
	}
	extensions, err := registry.Seal()
	if err != nil {
		return err
	}

	log.Info("starting",
		"version", versionString(),
		"tenant", cfg.Tenant,
		"south_listener", cfg.Listeners.South,
		"north_listener", cfg.Listeners.North,
		"extension_providers", extensions.Providers(),
	)
	extdefault.Log(log, extensions)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The store is opened and NOT migrated. Migrations are an explicit
	// command (PLAN §8): two nodes starting together must not race to build
	// the schema.
	st, err := store.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	// The south-bound listener is bound; the north-bound one is not (0014).
	// They are two listeners from here on rather than two fields (M2).
	if err := serve(ctx, cfg, st, log); err != nil {
		return err
	}

	log.Info("shutting down")
	return nil
}
