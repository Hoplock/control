// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// The vendored contract's drift check, checked.
//
// `make contract-check` is the discovery point for the one failure M1 exists to
// prevent: a local edit of contract/control.yaml produces two green test suites
// and one broken product, because this side then conforms to a document the
// proxy has never seen. A check that is asserted rather than proven is exactly
// as useful as no check, so this file runs the real script against a real
// mutated copy.
package control_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestContractCheckPassesOnACleanTree(t *testing.T) {
	out, err := runContractCheck(t, repoRootDir(t))
	if err != nil {
		t.Fatalf("contract-check failed on a clean tree: %v\n%s", err, out)
	}
	if !strings.Contains(out, "matches") {
		t.Errorf("contract-check said nothing about what it matched:\n%s", out)
	}
}

func TestContractCheckFailsOnASingleChangedByte(t *testing.T) {
	work := copyContractTree(t)

	target := filepath.Join(work, "contract", "control.yaml")
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	// One byte, in a comment-free place, so the document still parses. A drift
	// check that only catches a mangled file catches nothing: the edit this
	// rule exists for is a plausible one somebody made on purpose.
	mutated := strings.Replace(string(b), "openapi: 3.0.3", "openapi: 3.0.4", 1)
	if mutated == string(b) {
		t.Fatal("the vendored contract no longer begins with an openapi version to mutate")
	}
	if err := os.WriteFile(target, []byte(mutated), 0o600); err != nil {
		t.Fatalf("write mutated copy: %v", err)
	}

	out, err := runContractCheck(t, work)
	if err == nil {
		t.Fatalf("contract-check passed on an edited contract:\n%s", out)
	}
	for _, want := range []string{"does not match", "recorded:", "actual:", "contract-sync"} {
		if !strings.Contains(out, want) {
			t.Errorf("the failure does not name %q, so it does not name itself:\n%s", want, out)
		}
	}
}

func TestContractCheckFailsWhenProvenanceIsMissing(t *testing.T) {
	work := copyContractTree(t)
	if err := os.Remove(filepath.Join(work, "contract", "UPSTREAM")); err != nil {
		t.Fatalf("remove UPSTREAM: %v", err)
	}
	out, err := runContractCheck(t, work)
	if err == nil {
		t.Fatalf("contract-check passed with no provenance file:\n%s", out)
	}
	if !strings.Contains(out, "contract-sync") {
		t.Errorf("the failure does not say how to fix it:\n%s", out)
	}
}

// runContractCheck runs the real script against a tree laid out like the
// repository, and returns everything it said.
func runContractCheck(t *testing.T, root string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the checks are shell scripts")
	}
	cmd := exec.Command(filepath.Join(root, "scripts", "contract-check.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// copyContractTree makes a scratch copy of just the two directories the check
// reads, so a test may mutate the contract without touching the working tree.
func copyContractTree(t *testing.T) string {
	t.Helper()
	root := repoRootDir(t)
	work := t.TempDir()
	for _, dir := range []string{"contract", "scripts"} {
		if err := os.MkdirAll(filepath.Join(work, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s/%s: %v", dir, e.Name(), err)
			}
			mode := os.FileMode(0o644)
			if strings.HasSuffix(e.Name(), ".sh") {
				mode = 0o755
			}
			if err := os.WriteFile(filepath.Join(work, dir, e.Name()), b, mode); err != nil {
				t.Fatalf("write %s/%s: %v", dir, e.Name(), err)
			}
		}
	}
	return work
}

func repoRootDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}
