// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Host sections: the top-level keys a host binary owns (M15).
//
// Hoplock Enterprise starts Control from its own binary and keeps its own
// settings in the same file, under a key of its own. The strict decoder refuses
// any key Control does not define, and that refusal is worth keeping for every
// key but the host's: a typo in `listeners` must still stop the server. So a
// host DECLARES its keys, the decoder accepts exactly those, and each one is
// handed to the host undecoded, because what it means is the host's to say.

// HostSection is one host-owned section as the document carried it.
type HostSection struct {
	// Name is the top-level key.
	Name string
	// Raw is the section's value re-encoded as YAML, for the host to decode
	// with a strict decoder of its own.
	Raw []byte
}

// hostSectionName is the shape a host key must have: Control's own keys are
// lower-case words joined by underscores, and a host's sit beside them.
var hostSectionName = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// CheckHostSections refuses a set of host section names before any file is
// read: a malformed or repeated name, and above all a name Control's own
// schema defines. A host may not shadow `listeners`.
func CheckHostSections(names []string) error {
	control := TopLevelKeys()
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		switch {
		case !hostSectionName.MatchString(name):
			return fmt.Errorf("config: host section %q is not a lower-case key like Control's own", name)
		case slices.Contains(control, name):
			return fmt.Errorf("config: host section %q is a key Control's configuration defines; a host may not shadow it", name)
		case seen[name]:
			return fmt.Errorf("config: host section %q is declared twice", name)
		}
		seen[name] = true
	}
	return nil
}

// TopLevelKeys returns every top-level key Control's configuration defines,
// read from the schema itself so that a key added to [Config] is reserved on
// the day it is added.
func TopLevelKeys() []string {
	t := reflect.TypeOf(Config{})
	keys := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	slices.Sort(keys)
	return keys
}

// hostSections finds the declared sections in a document. It returns them
// re-encoded, and the refusal the strict decoder will raise for each one,
// spelled exactly as it raises it.
//
// A document that does not parse yields nothing and no error: the strict
// decode that follows reports the same failure in the words Parse has always
// used.
func hostSections(doc []byte, host []string) ([]HostSection, []string, error) {
	if len(host) == 0 {
		return nil, nil, nil
	}
	var root yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(doc)).Decode(&root); err != nil {
		return nil, nil, nil
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, nil, nil
	}
	mapping := root.Content[0]
	var (
		sections []HostSection
		expected []string
	)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key.Kind != yaml.ScalarNode || !slices.Contains(host, key.Value) {
			continue
		}
		raw, err := yaml.Marshal(value)
		if err != nil {
			return nil, nil, fmt.Errorf("config: section %s: %w", key.Value, err)
		}
		sections = append(sections, HostSection{Name: key.Value, Raw: raw})
		expected = append(expected, unknownKeyRefusal(key.Line, key.Value))
	}
	return sections, expected, nil
}

// unknownKeyRefusal is the line yaml.v3's strict decoder writes for a top-level
// key Config does not define.
//
// DROPPING THESE LINES, RATHER THAN DELETING THE KEYS AND DECODING WHAT IS LEFT,
// IS DELIBERATE. Re-encoding a document renumbers it — blank lines go — so an
// operator's "line 12: field listner not found" would name a line that is not
// line 12 of their file. Decoding the bytes they wrote keeps every other refusal
// exactly as Parse reports it. The cost is that this spelling is yaml.v3's, and
// it fails CLOSED: if a release of the library words it differently, nothing is
// dropped and a declared section is refused at start-up, which
// TestAHostSectionIsAcceptedAndHandedBackUndecoded catches the day the module
// moves.
func unknownKeyRefusal(line int, key string) string {
	return fmt.Sprintf("line %d: field %s not found in type %s", line, key, reflect.TypeOf(Config{}))
}

// withoutHostKeys drops the strict decoder's refusals of declared host keys and
// returns what is left, or nil when nothing is.
func withoutHostKeys(err error, expected []string) error {
	var te *yaml.TypeError
	if len(expected) == 0 || !errors.As(err, &te) {
		return err
	}
	var left []string
	for _, e := range te.Errors {
		if !slices.Contains(expected, e) {
			left = append(left, e)
		}
	}
	if len(left) == 0 {
		return nil
	}
	return &yaml.TypeError{Errors: left}
}
