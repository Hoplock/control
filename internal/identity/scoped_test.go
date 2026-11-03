// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/identity"
)

// Directory-scoped group entries (0043): a `groups.map` entry may list the
// directories it applies to, and a tenant may require that every directory
// group match only such an entry.

// mappingScoped is the request's own example (enterprise#51).
const mappingScoped = `
schema_version: 1
groups:
  claim: groups
  map:
    - external: BigCorporationAdmins
      directories: [de, fr, jp]
      group: corp-admins
    - external: admins
      directories: [de]
      group: de-admins
    - external: admins
      directories: [fr]
      group: fr-admins
    - external: engineering
      group: engineering
`

// withUnscopedAdmins adds `admins → all-admins` with no directories.
func withUnscopedAdmins(doc string) string {
	return doc + "    - external: admins\n      group: all-admins\n"
}

// requiringDirectories sets `groups.require_directories: true`.
func requiringDirectories(doc string) string {
	return strings.Replace(doc, "  claim: groups\n", "  claim: groups\n  require_directories: true\n", 1)
}

func TestADirectoryGroupMatchesTheEntriesThatListItsSource(t *testing.T) {
	t.Parallel()
	every := []string{"BigCorporationAdmins", "admins", "engineering"}
	cases := []struct {
		name     string
		document string
		source   string
		groups   []string
		want     []string
	}{
		// The request's example, end to end.
		{"de", mappingScoped, "de", every, []string{"corp-admins", "de-admins", "engineering"}},
		{"fr", mappingScoped, "fr", every, []string{"corp-admins", "engineering", "fr-admins"}},
		{"jp", mappingScoped, "jp", every, []string{"corp-admins", "engineering"}},
		{"a directory no entry lists gets the unscoped entries only", mappingScoped, "us", every, []string{"engineering"}},
		// Instead of, not as well as.
		{"a scoped entry wins over the unscoped one", withUnscopedAdmins(mappingScoped), "de", []string{"admins"}, []string{"de-admins"}},
		{"the unscoped entry serves a directory no entry lists", withUnscopedAdmins(mappingScoped), "us", []string{"admins"}, []string{"all-admins"}},
		// Requiring scoping.
		{"required: a listed directory keeps its scoped entries", requiringDirectories(withUnscopedAdmins(mappingScoped)), "de", every, []string{"corp-admins", "de-admins"}},
		{"required: an unlisted directory gets nothing", requiringDirectories(withUnscopedAdmins(mappingScoped)), "us", every, nil},
		{"required with no scoped entry maps nothing", requiringDirectories("schema_version: 1\ngroups:\n  claim: groups\n  map:\n    - external: engineering\n      group: engineering\n"), "de", every, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mustParse(t, tc.document).ApplyDirectory(tc.source, nil, "", tc.groups).Groups
			if !slices.Equal(got, tc.want) {
				t.Errorf("a %s subject in %v got %v, want %v", tc.source, tc.groups, got, tc.want)
			}
		})
	}
}

// A login's group claim matches the unscoped entries only: an entry that lists
// directories is the directories', whatever require_directories says.
func TestALoginNeverMatchesAScopedEntry(t *testing.T) {
	t.Parallel()
	for name, document := range map[string]string{
		"not required": withUnscopedAdmins(mappingScoped),
		"required":     requiringDirectories(withUnscopedAdmins(mappingScoped)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := mustParse(t, document).Apply(identity.Assertion{
				MultiClaims: map[string][]string{"groups": {"BigCorporationAdmins", "admins", "engineering"}},
			}).Groups
			if want := []string{"all-admins", "engineering"}; !slices.Equal(got, want) {
				t.Errorf("a login got %v, want %v: a scoped entry matched a login, or an unscoped one stopped serving logins", got, want)
			}
		})
	}
}

// An attribute that shares the group claim's name is never read as a group
// list, as before 0043.
func TestADirectoryAttributeNamedLikeTheGroupClaimIsNoGroup(t *testing.T) {
	t.Parallel()
	got := mustParse(t, mappingScoped).ApplyDirectory("de", map[string]string{"groups": "engineering"}, "", nil)
	if len(got.Groups) != 0 {
		t.Fatalf("an attribute named like the group claim became groups %v", got.Groups)
	}
}

func TestAMappingWithNeitherKeyMapsAsBefore(t *testing.T) {
	t.Parallel()
	mapping := mustParse(t, mappingSRE)
	got := mapping.ApplyDirectory("any-directory", map[string]string{"department": "platform"}, "a@example.com",
		[]string{"okta-sre", "okta-readonly", "unmapped"})
	if want := []string{"auditors", "sre"}; !slices.Equal(got.Groups, want) {
		t.Errorf("groups %v, want %v", got.Groups, want)
	}
	if got.Claims["department"] != "platform" || got.Claims["email"] != "a@example.com" {
		t.Errorf("claims %v", got.Claims)
	}
	if mapping.RequiresDirectories() || len(mapping.MappedDirectories()) != 0 {
		t.Errorf("a mapping with neither key reports directories %v, required %t",
			mapping.MappedDirectories(), mapping.RequiresDirectories())
	}
}

func TestTheMappingReportsTheDirectoriesItLists(t *testing.T) {
	t.Parallel()
	mapping := mustParse(t, requiringDirectories(withUnscopedAdmins(mappingScoped)))
	if got, want := mapping.MappedDirectories(), []string{"de", "fr", "jp"}; !slices.Equal(got, want) {
		t.Errorf("directories %v, want %v", got, want)
	}
	if !mapping.RequiresDirectories() {
		t.Error("require_directories was not reported")
	}
	if got, want := mapping.MappedGroupNames(), []string{"all-admins", "corp-admins", "de-admins", "engineering", "fr-admins"}; !slices.Equal(got, want) {
		t.Errorf("groups %v, want the scoped entries' too: %v", got, want)
	}
}

func TestScopedEntriesAreRejectedForEachFault(t *testing.T) {
	t.Parallel()
	head := "schema_version: 1\ngroups:\n  claim: groups\n  map:\n"
	cases := []struct {
		name, entries, code, field string
	}{
		{"a name that is not a source", "    - external: admins\n      directories: [de, Okta]\n      group: a\n",
			identity.RejectMappingDirectoryName, "groups.map[0].directories[1]"},
		{"an empty list", "    - external: admins\n      directories: []\n      group: a\n",
			identity.RejectMappingDirectoriesEmpty, "groups.map[0].directories"},
		{"a source listed twice in one entry", "    - external: admins\n      directories: [de, de]\n      group: a\n",
			identity.RejectMappingDirectoryDup, "groups.map[0].directories[1]"},
		{"a source listed twice across two entries",
			"    - external: admins\n      directories: [de]\n      group: a\n    - external: admins\n      directories: [fr, de]\n      group: b\n",
			identity.RejectMappingDirectoryDup, "groups.map[1].directories[1]"},
		{"two unscoped entries for one name", "    - external: admins\n      group: a\n    - external: admins\n      group: b\n",
			identity.RejectMappingGroupDup, "groups.map[1].external"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := identity.ParseMapping([]byte(head + tc.entries))
			var rejected *identity.MappingRejected
			if !errors.As(err, &rejected) || len(rejected.Rejections) != 1 {
				t.Fatalf("want exactly one rejection, got %v", err)
			}
			if r := rejected.Rejections[0]; r.Code != tc.code || r.Field != tc.field {
				t.Fatalf("got %s at %s, want %s at %s", r.Code, r.Field, tc.code, tc.field)
			}
		})
	}

	t.Run("the duplicate names the entry that listed it first", func(t *testing.T) {
		t.Parallel()
		_, err := identity.ParseMapping([]byte(head + cases[3].entries))
		if err == nil || !strings.Contains(err.Error(), "groups.map[0]") {
			t.Fatalf("the rejection does not name the first entry: %v", err)
		}
	})

	t.Run("several faults are all returned", func(t *testing.T) {
		t.Parallel()
		_, err := identity.ParseMapping([]byte(head +
			"    - external: admins\n      directories: [Bad]\n      group: a\n" +
			"    - external: ops\n      directories: []\n      group: b\n" +
			"    - external: eng\n      directories: [de, de]\n      group: c\n"))
		var rejected *identity.MappingRejected
		if !errors.As(err, &rejected) {
			t.Fatalf("want a *MappingRejected, got %v", err)
		}
		var codes []string
		for _, r := range rejected.Rejections {
			codes = append(codes, r.Code)
		}
		want := []string{identity.RejectMappingDirectoryName, identity.RejectMappingDirectoriesEmpty, identity.RejectMappingDirectoryDup}
		if !slices.Equal(codes, want) {
			t.Fatalf("codes %v, want %v", codes, want)
		}
	})

	t.Run("a scoped and an unscoped entry with one name are accepted", func(t *testing.T) {
		t.Parallel()
		mustParse(t, withUnscopedAdmins(mappingScoped))
	})

	t.Run("require_directories anywhere but under groups is an unknown key", func(t *testing.T) {
		t.Parallel()
		_, err := identity.ParseMapping([]byte("schema_version: 1\nrequire_directories: true\n"))
		assertRejected(t, err, identity.RejectMappingUnparseable)
	})
}

// The bound counts a scoped entry once for each directory it lists.
func TestTheBoundCountsWhatAScopedEntryMatches(t *testing.T) {
	t.Parallel()
	entries := func(n int, directories string) string {
		var b strings.Builder
		b.WriteString("schema_version: 1\ngroups:\n  claim: groups\n  map:\n")
		for i := range n {
			fmt.Fprintf(&b, "    - external: g%d\n", i)
			if directories != "" {
				fmt.Fprintf(&b, "      directories: %s\n", directories)
			}
			fmt.Fprintf(&b, "      group: l%d\n", i)
		}
		return b.String()
	}
	_, err := identity.ParseMapping([]byte(entries(300, "[de, fr]")))
	assertRejected(t, err, identity.RejectMappingTooLarge)
	mustParse(t, entries(identity.MaxMappingEntries, ""))
	mustParse(t, entries(identity.MaxMappingEntries/2, "[de, fr]"))
}
