// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package daemon is Hoplock Control's command line and its bring-up: the
// daemon that serves both listeners (M2), and the operator subcommands —
// migrate, seed, audit-verify, identity and ca.
//
// It is the one start-up path. hoplock-control's main is a call to the public
// server package, which forwards here, and a host binary such as Hoplock
// Enterprise's makes the same call with its own server.Options (M15), so the
// two binaries cannot drift: there is no second wiring to keep in step. What a
// host adds arrives as a [Host], and every way a host can be wrong is refused
// by [Host.Validate] before anything is read, opened or bound.
//
// It lives under internal/ because nothing here is a promise: the public shape
// is the server package, and this is the code behind it.
package daemon
