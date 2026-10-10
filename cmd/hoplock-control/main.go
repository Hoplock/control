// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Command hoplock-control is the Hoplock Control server: both listeners (M2)
// and the operator subcommands.
//
// It is one call to the public server package with zero Options — the same
// call a host binary such as Hoplock Enterprise's makes with its own (M15) — so
// there is one start-up path, and this binary has no wiring of its own to
// drift from it. All this file owns is what a host owns: its signals and its
// exit code.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hoplock/control/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := server.Main(ctx, os.Args[1:], os.Stdout, os.Stderr, server.Options{})
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hoplock-control: %v\n", err)
		os.Exit(1)
	}
}
