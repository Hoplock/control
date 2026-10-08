// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// controlModule is this module's path (PLAN §8). A build reports this
// module's version, whichever program it is built into.
const controlModule = "github.com/hoplock/control"

// version overrides the version a build reports. It is for a build outside a
// git checkout, where Go has nothing to stamp: `make build VERSION=v0.1.0`.
// Nothing sets it by default, because Go's own stamp is the one spelling of a
// build's version (PLAN M23).
var version = ""

// devVersion is what a build calls itself when it has no version to report:
// no provenance, or a working copy swapped in by a replace directive. It is
// deliberately not a number, so a build that is not a release is never
// mistaken for one.
const devVersion = "dev"

// versionString returns the line --version prints: Control's module version,
// then the toolchain and the platform.
func versionString() string {
	info, _ := debug.ReadBuildInfo() // nil when the binary carries none
	return describeBuild(info, version).String()
}

// build is what a binary knows about the Control inside it.
type build struct {
	// Version is Control's module version, or devVersion.
	Version string
	// Replaced is set when a replace directive swapped Control out:
	// "<path> <version> replaced by <path>[ <version>]".
	Replaced string
	// Revision and Time are the VCS stamps, read only when Control is the
	// main module. In another program's binary they describe that program.
	Revision, Time string
}

// describeBuild reads Control's version out of info. Go stamps the main
// module's version from the repository: the tag at a tagged commit, a
// pseudo-version after it, a +dirty suffix for a modified tree, and "(devel)"
// when it had nothing to stamp. Control is the main module when this binary is
// hoplock-control, and a dependency when another program is built around it;
// either way the version reported is Control's and never the other program's.
func describeBuild(info *debug.BuildInfo, override string) build {
	b := build{Version: devVersion}
	switch {
	case info == nil:
	case info.Main.Path == controlModule:
		if v := info.Main.Version; v != "" && v != "(devel)" {
			b.Version = v
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				b.Revision = s.Value
			case "vcs.time":
				b.Time = s.Value
			}
		}
	default:
		for _, dep := range info.Deps {
			if dep.Path != controlModule {
				continue
			}
			if r := dep.Replace; r != nil {
				// A replaced Control is a working copy, not the release it
				// replaces, so it must not read as that release (Enterprise's E3).
				b.Replaced = strings.TrimSpace(dep.Path+" "+dep.Version) +
					" replaced by " + strings.TrimSpace(r.Path+" "+r.Version)
			} else if dep.Version != "" {
				b.Version = dep.Version
			}
			break
		}
	}
	if override != "" {
		b.Version = override
	}
	return b
}

// String is the line --version prints, for example
//
//	hoplock-control v0.1.0 (<revision>, <time>) go1.27.0 linux/amd64
//	hoplock-control v0.1.0 go1.27.0 linux/amd64
//	hoplock-control dev (github.com/hoplock/control v0.1.0 replaced by ../control) go1.27.0 linux/amd64
func (b build) String() string {
	var s strings.Builder
	s.WriteString("hoplock-control ")
	s.WriteString(b.Version)
	detail := b.Replaced
	if detail == "" && b.Revision != "" {
		detail = b.Revision
		if b.Time != "" {
			detail += ", " + b.Time
		}
	}
	if detail != "" {
		s.WriteString(" (" + detail + ")")
	}
	s.WriteString(" " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH)
	return s.String()
}
