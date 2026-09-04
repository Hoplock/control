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
	"syscall"

	"github.com/hoplock/control/internal/config"
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

	// The listeners are named here but nothing binds them yet: phase 0001
	// stands the repository up and starts no service (PLAN §10).
	log.Info("starting",
		"version", versionString(),
		"tenant", cfg.Tenant,
		"south_listener", cfg.Listeners.South,
		"north_listener", cfg.Listeners.North,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	stop()

	log.Info("shutting down")
	return nil
}
