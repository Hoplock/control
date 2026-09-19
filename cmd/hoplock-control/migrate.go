// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/store"
)

// runMigrate is the `hoplock-control migrate` subcommand.
//
// Migrations are applied by an explicit command and never on boot (PLAN §8):
// two nodes starting together must not race to build the schema, and a server
// that migrates on startup is a server that does. This is that command, and
// the server has no code path that calls it.
func runMigrate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("hoplock-control migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to the YAML configuration file")
	dryRun := fs.Bool("dry-run", false, "print what would be applied and change nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	plan, err := st.PlanMigrations(ctx)
	if err != nil {
		return err
	}

	if *dryRun {
		return printPlan(stdout, plan)
	}

	applied, err := st.Migrate(ctx)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		_, err := fmt.Fprintf(stdout, "schema is up to date at version %04d\n",
			highestApplied(plan))
		return err
	}
	for _, m := range applied {
		if _, err := fmt.Fprintf(stdout, "applied %04d_%s\n", m.Version, m.Name); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "%d migration(s) applied\n", len(applied))
	return err
}

// printPlan renders a dry run. It prints what is already applied as well as
// what is pending, because "nothing to do" and "the database is not the one
// you think it is" look identical when only the pending half is shown.
func printPlan(w io.Writer, plan store.MigrationPlan) error {
	if _, err := fmt.Fprintf(w, "dry run: nothing will be changed\n"); err != nil {
		return err
	}
	for _, a := range plan.Applied {
		if _, err := fmt.Fprintf(w, "  applied  %04d_%s\n", a.Version, a.Name); err != nil {
			return err
		}
	}
	for _, m := range plan.Pending {
		if _, err := fmt.Fprintf(w, "  pending  %04d_%s\n", m.Version, m.Name); err != nil {
			return err
		}
	}
	if len(plan.Pending) == 0 {
		_, err := fmt.Fprintf(w, "schema is up to date at version %04d\n", highestApplied(plan))
		return err
	}
	_, err := fmt.Fprintf(w, "%d migration(s) would be applied\n", len(plan.Pending))
	return err
}

// highestApplied reports the version the plan says the database is at.
func highestApplied(plan store.MigrationPlan) int64 {
	var highest int64
	for _, a := range plan.Applied {
		if a.Version > highest {
			highest = a.Version
		}
	}
	return highest
}
