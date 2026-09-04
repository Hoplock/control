// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run --version: unexpected error: %v", err)
	}

	got := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(got, "hoplock-control ") {
		t.Errorf("--version printed %q, want it to name the binary", got)
	}
	if strings.HasSuffix(got, "hoplock-control") || strings.Contains(got, "hoplock-control  ") {
		t.Errorf("--version printed %q, want a version after the name", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("--version wrote %q to stderr, want nothing", stderr.String())
	}
}

// A stamped build must report the stamp rather than the build-info fallback.
func TestVersionStringUsesStamps(t *testing.T) {
	version, commit, date = "v1.2.3", "abc1234", "2026-09-04T00:00:00Z"
	t.Cleanup(func() { version, commit, date = "", "", "" })

	got := versionString()
	for _, want := range []string{"v1.2.3", "abc1234", "2026-09-04T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("versionString() = %q, want it to contain %q", got, want)
		}
	}
}

func TestRunReportsConfigError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--config", filepath.Join(t.TempDir(), "absent.yaml")}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run: want an error for a missing config file, got nil")
	}
	if !strings.Contains(err.Error(), "absent.yaml") {
		t.Errorf("run error = %q, want it to name the file", err)
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--nope"}, &stdout, &stderr); err == nil {
		t.Fatal("run: want an error for an unknown flag, got nil")
	}
}

// The bare binary looks for config.yaml in the working directory; nothing
// about that path may depend on a file this repository ships.
func TestDefaultConfigPathIsNotCommitted(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "..", defaultConfigPath)); err == nil {
		t.Fatalf("%s is committed to the repository; it must stay local (see .gitignore)", defaultConfigPath)
	}
}
