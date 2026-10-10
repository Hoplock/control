// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/hoplock/control/internal/config"
)

// A host binary's own section (M15): accepted because it was declared, handed
// back undecoded, and nothing else about strict decoding changes.

const hostSection = `
enterprise:
  licence_file: /etc/hoplock/licence
  archive:
    retention: 2160h
`

func TestAHostSectionIsAcceptedAndHandedBackUndecoded(t *testing.T) {
	cfg, sections, err := config.ParseHost(strings.NewReader(validYAML+hostSection), []string{"enterprise"})
	if err != nil {
		t.Fatalf("ParseHost: %v", err)
	}
	if cfg.Tenant != "acme" {
		t.Errorf("Control's own keys were not decoded: tenant %q", cfg.Tenant)
	}
	if len(sections) != 1 || sections[0].Name != "enterprise" {
		t.Fatalf("sections = %+v, want the one enterprise section", sections)
	}

	// The bytes are YAML a host decodes with its own strict decoder.
	var got struct {
		LicenceFile string `yaml:"licence_file"`
		Archive     struct {
			Retention string `yaml:"retention"`
		} `yaml:"archive"`
	}
	if err := yaml.Unmarshal(sections[0].Raw, &got); err != nil {
		t.Fatalf("the section's bytes are not YAML: %v\n%s", err, sections[0].Raw)
	}
	if got.LicenceFile != "/etc/hoplock/licence" || got.Archive.Retention != "2160h" {
		t.Errorf("the section came back as %+v", got)
	}
}

func TestAnAbsentHostSectionIsNotHandedBack(t *testing.T) {
	_, sections, err := config.ParseHost(strings.NewReader(validYAML), []string{"enterprise"})
	if err != nil {
		t.Fatalf("ParseHost: %v", err)
	}
	if len(sections) != 0 {
		t.Errorf("sections = %+v, want none: the file carries no enterprise key", sections)
	}
}

// Without the declaration the same file is refused, exactly as before.
func TestAnUndeclaredSectionIsStillRefused(t *testing.T) {
	_, err := config.Parse(strings.NewReader(validYAML + hostSection))
	if err == nil || !strings.Contains(err.Error(), "field enterprise not found") {
		t.Fatalf("Parse = %v, want the strict decoder's refusal of enterprise", err)
	}
	_, _, err = config.ParseHost(strings.NewReader(validYAML+hostSection), []string{"reporting"})
	if err == nil || !strings.Contains(err.Error(), "field enterprise not found") {
		t.Fatalf("ParseHost declaring another section = %v, want enterprise refused", err)
	}
}

// Declaring a section opens that key and nothing else: a typo beside it is
// refused in today's words, naming the line of the file the operator wrote.
func TestAnUnknownKeyBesideAHostSectionIsRefusedWithTodaysMessage(t *testing.T) {
	typo := "\n\n\nlistener_south: :8443\n"

	_, alone := config.Parse(strings.NewReader(validYAML + typo))
	if alone == nil {
		t.Fatal("Parse accepted the typo")
	}
	_, _, hosted := config.ParseHost(strings.NewReader(validYAML+typo+hostSection), []string{"enterprise"})
	if hosted == nil {
		t.Fatal("ParseHost accepted the typo")
	}
	if alone.Error() != hosted.Error() {
		t.Errorf("the refusal changed with a host section declared:\n  alone:  %v\n  hosted: %v", alone, hosted)
	}

	// The host section comes FIRST here, so a decoder that deleted it and
	// re-encoded the rest would renumber the typo's line.
	_, _, before := config.ParseHost(strings.NewReader(hostSection+"\n\n"+validYAML+typo), []string{"enterprise"})
	wantLine := strings.Count(hostSection+"\n\n"+validYAML+typo, "\n")
	if before == nil || !strings.Contains(before.Error(), "line "+strconv.Itoa(wantLine)+": field listener_south") {
		t.Errorf("ParseHost = %v, want the typo named at line %d of the file as written", before, wantLine)
	}
}

func TestAHostMayNotShadowAKeyControlDefines(t *testing.T) {
	for _, name := range config.TopLevelKeys() {
		if err := config.CheckHostSections([]string{name}); err == nil {
			t.Errorf("host section %q was accepted, and Control defines it", name)
		}
	}
	for _, want := range []string{"listeners", "database", "access_context"} {
		if !slices.Contains(config.TopLevelKeys(), want) {
			t.Errorf("TopLevelKeys() = %v, missing %q", config.TopLevelKeys(), want)
		}
	}
}

func TestAMalformedOrRepeatedHostSectionIsRefused(t *testing.T) {
	for name, sections := range map[string][]string{
		"empty":       {""},
		"upper case":  {"Enterprise"},
		"punctuation": {"enterprise,inline"},
		"repeated":    {"enterprise", "enterprise"},
	} {
		if err := config.CheckHostSections(sections); err == nil {
			t.Errorf("%s: %q was accepted", name, sections)
		}
	}
	if err := config.CheckHostSections([]string{"enterprise", "enterprise_reports"}); err != nil {
		t.Errorf("two well-formed sections were refused: %v", err)
	}
}

// The declaration is refused before the file is read: a host that shadows
// `listeners` is wrong whatever any file says.
func TestAShadowingSectionIsRefusedBeforeTheFileIsRead(t *testing.T) {
	_, _, err := config.LoadHost(filepath.Join(t.TempDir(), "absent.yaml"), []string{"listeners"})
	if err == nil || strings.Contains(err.Error(), "absent.yaml") {
		t.Fatalf("LoadHost = %v, want the shadowing refused before the file was opened", err)
	}
	_, _, err = config.ParseHost(failingReader{t}, []string{"listeners"})
	if err == nil {
		t.Fatal("ParseHost accepted a section named listeners")
	}
}

type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Fatal("the document was read before the host sections were checked")
	return 0, nil
}
