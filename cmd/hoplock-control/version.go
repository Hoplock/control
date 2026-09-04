// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Build metadata. The Makefile stamps these with -ldflags from git; a plain
// `go build ./...` leaves them empty and version() falls back to the VCS
// stamps the toolchain embeds, so the binary reports something true either
// way.
var (
	version = ""
	commit  = ""
	date    = ""
)

// devVersion is what an unstamped, un-versioned build calls itself. It is
// deliberately not a number: a build with no provenance must not be mistaken
// for a release.
const devVersion = "dev"

// version returns the human-readable version string, derived from git.
func versionString() string {
	v, c, d := version, commit, date

	if v == "" || c == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			bv, bc, bd, dirty := fromBuildInfo(info)
			if v == "" {
				v = bv
			}
			if c == "" {
				c = bc
				if dirty && c != "" {
					c += "-dirty"
				}
			}
			if d == "" {
				d = bd
			}
		}
	}

	if v == "" {
		v = devVersion
	}

	var b strings.Builder
	b.WriteString("hoplock-control ")
	b.WriteString(v)
	if c != "" {
		b.WriteString(" (")
		b.WriteString(c)
		if d != "" {
			b.WriteString(", built ")
			b.WriteString(d)
		}
		b.WriteString(")")
	}
	b.WriteString(" ")
	b.WriteString(runtime.Version())
	b.WriteString(" ")
	b.WriteString(runtime.GOOS + "/" + runtime.GOARCH)
	return b.String()
}

// fromBuildInfo pulls the module version and the VCS stamps the Go toolchain
// embeds when it builds from a git working tree.
func fromBuildInfo(info *debug.BuildInfo) (version, commit, date string, dirty bool) {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.time":
			date = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return version, commit, date, dirty
}
