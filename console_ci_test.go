// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package control_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEveryConsoleSpecRunsInCI holds the console's browser checks to CI. The
// console-e2e jobs name the spec files they run, so a phase that adds a spec
// and no job for it has acceptance checks that pass on the author's machine
// and never on a pull request — 0051's ran that way until this test. Every
// spec under ui/e2e/ must be named in some console-e2e entry's `args` or
// `then`.
func TestEveryConsoleSpecRunsInCI(t *testing.T) {
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []map[string]string `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("%s: %v", workflowPath, err)
	}
	job, ok := wf.Jobs["console-e2e"]
	if !ok {
		t.Fatalf("%s has no console-e2e job", workflowPath)
	}
	var named strings.Builder
	for _, entry := range job.Strategy.Matrix.Include {
		named.WriteString(" " + entry["args"] + " " + entry["then"] + " ")
	}
	specs, err := filepath.Glob(filepath.Join("ui", "e2e", "*.spec.ts"))
	if err != nil || len(specs) == 0 {
		t.Fatalf("no specs under ui/e2e (%v): the check cannot mean anything", err)
	}
	for _, spec := range specs {
		name := "e2e/" + filepath.Base(spec)
		if !strings.Contains(named.String(), " "+name+" ") {
			t.Errorf("%s is named by no console-e2e entry in %s: it never runs in CI — add an entry whose args name it", spec, workflowPath)
		}
	}
}
