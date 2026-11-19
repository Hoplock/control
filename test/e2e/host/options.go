// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package main

import "github.com/hoplock/control/internal/daemon"

// host is what this binary hands Control: nothing yet, so it is
// hoplock-control exactly. 0071 registers the fakes its scenarios drive here.
func host() daemon.Host {
	return daemon.Host{}
}
