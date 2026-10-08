// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

// TestDescribeBuild covers what a build reports about Control (PLAN M23):
// Control's own module version in every case, never a host's, and never a
// version a working copy has not earned.
func TestDescribeBuild(t *testing.T) {
	const (
		tag      = "v0.1.0"
		after    = "v0.1.1-0.20261008120000-0123456789ab"
		revision = "0123456789abcdef0123456789abcdef01234567"
		hostRev  = "fedcba9876543210fedcba9876543210fedcba98"
		hostVer  = "v0.3.0"
	)
	stamps := func(rev string) []debug.BuildSetting {
		return []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: rev},
			{Key: "vcs.time", Value: "2026-10-08T12:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		}
	}
	control := func(v string) debug.Module { return debug.Module{Path: controlModule, Version: v} }
	host := debug.Module{Path: "github.com/hoplock/enterprise", Version: hostVer}

	cases := []struct {
		name     string
		info     *debug.BuildInfo
		override string
		version  string // what the build must report
		replaced string // set: the build must say it is replaced, in these words
		revision string // the VCS revision the line must carry, if any
	}{{
		name:     "Control as the main module at a tag reports the tag",
		info:     &debug.BuildInfo{Main: control(tag), Settings: stamps(revision)},
		version:  tag,
		revision: revision,
	}, {
		name:     "after the tag, a pseudo-version that sorts after it",
		info:     &debug.BuildInfo{Main: control(after), Settings: stamps(revision)},
		version:  after,
		revision: revision,
	}, {
		name:    "a modified tree says +dirty, because Go's stamp does",
		info:    &debug.BuildInfo{Main: control(after + "+dirty"), Settings: stamps(revision)},
		version: after + "+dirty",
	}, {
		name:    "a main module Go could not stamp is dev",
		info:    &debug.BuildInfo{Main: control("(devel)")},
		version: devVersion,
	}, {
		name: "Control as a dependency reports the dependency's version, never the host's",
		info: &debug.BuildInfo{
			Main:     host,
			Deps:     []*debug.Module{{Path: "gopkg.in/yaml.v3", Version: "v3.0.1"}, ptr(control(tag))},
			Settings: stamps(hostRev),
		},
		version: tag,
	}, {
		name: "a replaced dependency says it is replaced, and is not the release",
		info: &debug.BuildInfo{
			Main: host,
			Deps: []*debug.Module{{Path: controlModule, Version: tag, Replace: &debug.Module{Path: "../control"}}},
		},
		version:  devVersion,
		replaced: "github.com/hoplock/control v0.1.0 replaced by ../control",
	}, {
		name: "a dependency replaced by another module's version is not the release either",
		info: &debug.BuildInfo{
			Main: host,
			Deps: []*debug.Module{{
				Path: controlModule, Version: tag,
				Replace: &debug.Module{Path: "github.com/example/control-fork", Version: tag},
			}},
		},
		version:  devVersion,
		replaced: "github.com/hoplock/control v0.1.0 replaced by github.com/example/control-fork v0.1.0",
	}, {
		name:    "a host without Control in its dependencies has nothing to report",
		info:    &debug.BuildInfo{Main: host, Settings: stamps(hostRev)},
		version: devVersion,
	}, {
		name:    "no build info reports dev",
		info:    nil,
		version: devVersion,
	}, {
		name:     "an explicit override wins, for a build with nothing to stamp",
		info:     nil,
		override: tag,
		version:  tag,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := describeBuild(tc.info, tc.override)
			line := b.String()

			if b.Version != tc.version {
				t.Errorf("version = %q, want %q", b.Version, tc.version)
			}
			if !strings.HasPrefix(line, "hoplock-control "+tc.version+" ") {
				t.Errorf("line = %q, want it to start with %q", line, "hoplock-control "+tc.version+" ")
			}
			if b.Replaced != tc.replaced {
				t.Errorf("replaced = %q, want %q", b.Replaced, tc.replaced)
			}
			if tc.replaced != "" && !strings.Contains(line, "("+tc.replaced+")") {
				t.Errorf("line = %q, want it to say %q", line, tc.replaced)
			}
			if tc.revision != "" && !strings.Contains(line, tc.revision) {
				t.Errorf("line = %q, want the revision %s", line, tc.revision)
			}
			// A host's version and its VCS stamps describe the host. Reporting
			// either under Control's name is the failure this function exists
			// to prevent.
			for _, hosts := range []string{hostVer, hostRev} {
				if strings.Contains(line, hosts) {
					t.Errorf("line = %q reports the host's %s as Control's", line, hosts)
				}
			}
		})
	}

	if !sortsAfter(after, tag) {
		t.Errorf("%s does not sort after %s: a build after a release would read as older than it", after, tag)
	}
}

// TestVersionStringOverride: `make build VERSION=…` is honoured, so a build
// outside a git checkout still reports a version.
func TestVersionStringOverride(t *testing.T) {
	version = "v0.1.0"
	t.Cleanup(func() { version = "" })
	if got := versionString(); !strings.HasPrefix(got, "hoplock-control v0.1.0 ") {
		t.Errorf("versionString() = %q, want the override", got)
	}
}

func ptr(m debug.Module) *debug.Module { return &m }

// sortsAfter reports whether semantic version a sorts after b. It compares
// MAJOR.MINOR.PATCH numerically, and among equal numbers ranks a release above
// its pre-releases, which is all the pseudo-versions Go stamps need.
func sortsAfter(a, b string) bool {
	core := func(v string) (nums [3]int, pre bool) {
		v = strings.TrimPrefix(v, "v")
		if i := strings.IndexByte(v, '+'); i >= 0 {
			v = v[:i]
		}
		if i := strings.IndexByte(v, '-'); i >= 0 {
			v, pre = v[:i], true
		}
		for i, part := range strings.SplitN(v, ".", 3) {
			nums[i], _ = strconv.Atoi(part)
		}
		return nums, pre
	}
	an, ap := core(a)
	bn, bp := core(b)
	for i := range an {
		if an[i] != bn[i] {
			return an[i] > bn[i]
		}
	}
	return bp && !ap
}
