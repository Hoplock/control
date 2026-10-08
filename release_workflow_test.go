// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// The release job's invariants (PLAN M23), read from the workflow itself.
//
// They are the kind that rot without anyone deciding they should. A job added
// to the workflow but not to the release job's `needs` lets a release be cut
// before that job has passed on the commit. A `contents: write` copied into
// another job gives it the power to push a tag, which can never be taken back.
// Neither fails anything on its own, so this test is what makes them fail.
package control_test

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const workflowPath = ".github/workflows/ci.yml"

// stringList reads a YAML scalar or sequence of strings, as `needs:` may be
// either.
type stringList []string

func (l *stringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*l = []string{n.Value}
		return nil
	}
	var s []string
	if err := n.Decode(&s); err != nil {
		return err
	}
	*l = s
	return nil
}

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	If   string            `yaml:"if"`
	With map[string]string `yaml:"with"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

type workflowJob struct {
	If          string            `yaml:"if"`
	Needs       stringList        `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress bool   `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Steps []workflowStep `yaml:"steps"`
}

func TestReleaseJobHoldsTheReleaseDecision(t *testing.T) {
	var wf struct {
		Permissions map[string]string      `yaml:"permissions"`
		Jobs        map[string]workflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, workflowPath)), &wf); err != nil {
		t.Fatalf("parse %s: %v", workflowPath, err)
	}
	release, ok := wf.Jobs["release"]
	if !ok {
		t.Fatalf("%s has no release job: nothing cuts a release (PLAN M23)", workflowPath)
	}

	if want := "github.event_name == 'push' && github.ref == 'refs/heads/main'"; release.If != want {
		t.Errorf("release runs if %q, want %q: a release is cut only from a push to main", release.If, want)
	}

	for id := range wf.Jobs {
		if id != "release" && !slices.Contains(release.Needs, id) {
			t.Errorf("release does not need job %q, so a release could be cut before it has passed on the commit", id)
		}
	}

	if got := wf.Permissions["contents"]; got != "read" {
		t.Errorf("the workflow's contents permission is %q, want read: only the release job writes", got)
	}
	if got := release.Permissions["contents"]; got != "write" {
		t.Errorf("release has contents: %q, want write: it pushes the tag", got)
	}
	for id, job := range wf.Jobs {
		for scope, level := range job.Permissions {
			if id != "release" && level == "write" {
				t.Errorf("job %q has %s: write; only the release job writes, because a pushed tag is permanent", id, scope)
			}
		}
	}

	if release.Concurrency.Group == "" || release.Concurrency.CancelInProgress {
		t.Errorf("release concurrency is %+v, want a group that never cancels a run in progress: "+
			"two merges must not race for one version", release.Concurrency)
	}

	steps := release.Steps
	if len(steps) == 0 {
		t.Fatal("release has no steps")
	}
	if c := steps[0]; !strings.HasPrefix(c.Uses, "actions/checkout@") ||
		c.With["ref"] != "${{ github.sha }}" || c.With["fetch-depth"] != "0" {
		t.Errorf("release starts with %+v, want a checkout of ${{ github.sha }} with fetch-depth 0: "+
			"the pushed commit, never a branch head read again, and the history that shows which merge introduced a version", c)
	}

	var planned, tagged bool
	for _, s := range steps {
		if strings.Contains(s.Run, `scripts/release-check.sh plan "$GITHUB_SHA" "$BEFORE"`) &&
			s.Env["BEFORE"] == "${{ github.event.before }}" {
			planned = true
		}
		if strings.Contains(s.Run, "git tag ") {
			tagged = true
			if s.If != "steps.plan.outputs.action == 'release'" {
				t.Errorf("the tagging step runs if %q: it must tag only what the plan says this commit introduced", s.If)
			}
			for _, want := range []string{`"$GITHUB_SHA"`, "--annotate", "--cleanup=verbatim"} {
				if !strings.Contains(s.Run, want) {
					t.Errorf("the tagging step does not say %s", want)
				}
			}
		}
	}
	if !planned || !tagged {
		t.Errorf("release plans the commit: %v, tags it: %v; want both", planned, tagged)
	}

	last := steps[len(steps)-1]
	if last.Env["GOPROXY"] != "https://proxy.golang.org" || !strings.Contains(last.Run, "go mod download") {
		t.Errorf("release ends with %q; want it to resolve the version through https://proxy.golang.org, "+
			"which is what proves Enterprise can pin it", last.Name)
	}
}
