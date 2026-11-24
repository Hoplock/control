// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Command host is the Control the end-to-end topology runs (deploy/README.md):
// Hoplock Control exactly as hoplock-control is, started the way a host starts
// it, so that the phase adding fake extensions (0072) registers them as a host
// registers its own (M15).
//
// It starts Control through internal/daemon, which server.Main forwards to,
// because nothing in this module but cmd/hoplock-control may import the public
// façade (architecture_test.go) — the console's browser-check host
// (ui/e2e/fixture/host) does the same. It is built only under the e2e tag, by
// deploy/build.sh, and never ships: nothing imports it, `go build ./...` does
// not see it, and it binds nothing hoplock-control does not.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hoplock/control/internal/daemon"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := daemon.Main(ctx, os.Args[1:], os.Stdout, os.Stderr, host())
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hoplock-control: %v\n", err)
		os.Exit(1)
	}
}
