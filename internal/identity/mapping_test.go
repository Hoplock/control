// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/identity"
)

// THIS FILE HOLDS THE PHASE'S SECURITY TEST.
//
// A claim an IdP administrator can set is an attribute a policy rule matches on.
// The mapping is the one place that trust is granted deliberately, so the first
// test below is the one that matters most: a claim the mapping does not name must
// not become an attribute — not renamed, not passed through, not namespaced.

const mappingSRE = `
schema_version: 1
description: the one an operator would actually write
claims:
  - claim: email
    attribute: email
  - claim: department
    attribute: department
    values: [engineering, platform]
groups:
  claim: groups
  map:
    - external: okta-sre
      group: sre
    - external: okta-readonly
      group: auditors
`

func TestAnUnmappedClaimNeverBecomesAnAttribute(t *testing.T) {
	mapping := mustParse(t, mappingSRE)

	attrs := mapping.Apply(identity.Assertion{
		Connector: "okta",
		Subject:   "00u1",
		Claims: map[string]string{
			"email": "alice@example.com",
			// Everything below is a claim an IdP administrator could add,
			// and NONE of it is in the mapping.
			"role":            "admin",
			"department_code": "0042",
			"groups_extra":    "okta-sre",
			"sub":             "00u1",
			"iss":             "https://idp.example.com",
		},
	})

	if got := attrs.Claims["email"]; got != "alice@example.com" {
		t.Fatalf("the mapped claim did not survive: got %q", got)
	}
	for _, unmapped := range []string{"role", "department_code", "groups_extra", "sub", "iss"} {
		if v, ok := attrs.Claims[unmapped]; ok {
			t.Errorf("the unmapped claim %q became attribute %q=%q; the mapping is an allow-list, not a transform",
				unmapped, unmapped, v)
		}
	}
	if len(attrs.Claims) != 1 {
		t.Errorf("the mapping produced %d attributes from a mapping with one matching entry: %v",
			len(attrs.Claims), attrs.Claims)
	}
}

func TestAMappedClaimOutsideItsPermittedValuesProducesNothing(t *testing.T) {
	mapping := mustParse(t, mappingSRE)

	allowed := mapping.Apply(identity.Assertion{Claims: map[string]string{"department": "engineering"}})
	if allowed.Claims["department"] != "engineering" {
		t.Fatalf("a permitted value did not map: %v", allowed.Claims)
	}

	// The point of a value list: the mapping trusts this claim only when it
	// says one of these things.
	refused := mapping.Apply(identity.Assertion{Claims: map[string]string{"department": "finance"}})
	if v, ok := refused.Claims["department"]; ok {
		t.Fatalf("a value outside the permitted list produced department=%q", v)
	}
}

func TestOnlyMappedGroupsSurvive(t *testing.T) {
	mapping := mustParse(t, mappingSRE)

	attrs := mapping.Apply(identity.Assertion{
		MultiClaims: map[string][]string{
			"groups": {"okta-sre", "okta-everyone", "okta-readonly", "admins"},
		},
	})
	want := []string{"auditors", "sre"}
	if !slices.Equal(attrs.Groups, want) {
		t.Fatalf("mapped groups: got %v, want %v (unmapped IdP groups must be dropped)", attrs.Groups, want)
	}
}

func TestASingleValuedGroupClaimIsAccepted(t *testing.T) {
	mapping := mustParse(t, mappingSRE)
	attrs := mapping.Apply(identity.Assertion{Claims: map[string]string{"groups": "okta-sre"}})
	if !slices.Equal(attrs.Groups, []string{"sre"}) {
		t.Fatalf("an IdP asserting one group as a string did not map: %v", attrs.Groups)
	}
}

func TestAReservedAttributeCannotBeMappedFromAClaim(t *testing.T) {
	// `chain_hop_proxy_id` is set by THIS SERVER: it names the proxy whose key
	// authenticated a chain leg. A token that could set it would be a user
	// asserting the identity of the infrastructure in front of them.
	for _, attribute := range []string{identity.ClaimChainHop, "hoplock.anything"} {
		document := "schema_version: 1\nclaims:\n  - claim: x\n    attribute: " + attribute + "\n"
		_, err := identity.ParseMapping([]byte(document))
		assertRejected(t, err, identity.RejectMappingReserved)
	}
}

func TestTwoClaimsCannotProduceOneAttribute(t *testing.T) {
	_, err := identity.ParseMapping([]byte(`
schema_version: 1
claims:
  - claim: department
    attribute: team
  - claim: division
    attribute: team
`))
	assertRejected(t, err, identity.RejectMappingAttributeDup)
}

func TestAnAttributeNameOutsideTheVocabularyIsRejected(t *testing.T) {
	for _, attribute := range []string{"Team", "team!", "1team", "team..x", ""} {
		document := "schema_version: 1\nclaims:\n  - claim: x\n    attribute: \"" + attribute + "\"\n"
		_, err := identity.ParseMapping([]byte(document))
		assertRejected(t, err, identity.RejectMappingAttributeName)
	}
}

func TestAnUnknownKeyIsRejectedRatherThanIgnored(t *testing.T) {
	// A misspelled `attributes:` that parsed silently would be a mapping an
	// operator believes is in force and is not.
	_, err := identity.ParseMapping([]byte("schema_version: 1\nattributes:\n  - claim: x\n"))
	assertRejected(t, err, identity.RejectMappingUnparseable)
}

func TestAnotherSchemaVersionIsRejected(t *testing.T) {
	_, err := identity.ParseMapping([]byte("schema_version: 2\n"))
	assertRejected(t, err, identity.RejectMappingSchemaVersion)
}

func TestEveryRejectionIsReportedRatherThanTheFirst(t *testing.T) {
	_, err := identity.ParseMapping([]byte(`
schema_version: 1
claims:
  - claim: ""
    attribute: "Bad Name"
groups:
  claim: ""
  map:
    - external: ""
      group: ok
`))
	var rejected *identity.MappingRejected
	if !errors.As(err, &rejected) {
		t.Fatalf("want a *MappingRejected, got %v", err)
	}
	if len(rejected.Rejections) < 4 {
		t.Fatalf("an operator fixing a document one round trip per mistake stops using the validator; got %d rejections: %v",
			len(rejected.Rejections), rejected.Rejections)
	}
}

func TestTheEmptyMappingMapsNothing(t *testing.T) {
	// A tenant that federates before authoring a mapping gets identities with
	// no attributes and no groups. Passing claims through until a mapping
	// exists would be a hole that closes only when somebody remembers.
	attrs := identity.EmptyMapping().Apply(identity.Assertion{
		Claims:      map[string]string{"role": "admin", "email": "alice@example.com"},
		MultiClaims: map[string][]string{"groups": {"okta-sre"}},
	})
	if len(attrs.Claims) != 0 || len(attrs.Groups) != 0 {
		t.Fatalf("the empty mapping produced %v / %v", attrs.Claims, attrs.Groups)
	}
}

func TestTheDocumentTheDigestCoversIsTheOneThatWasValidated(t *testing.T) {
	mapping := mustParse(t, mappingSRE)
	if mapping.Document() != mappingSRE {
		t.Fatalf("the stored document is a re-encoding rather than the bytes that were validated")
	}
	if len(mapping.Digest) != 64 {
		t.Fatalf("digest %q is not a hex sha256", mapping.Digest)
	}
	second := mustParse(t, mappingSRE)
	if second.Digest != mapping.Digest {
		t.Fatalf("the digest is not stable over the same bytes")
	}
}

func TestTheMappingReportsWhatItCanProduce(t *testing.T) {
	mapping := mustParse(t, mappingSRE)
	if got := mapping.MappedAttributeNames(); !slices.Equal(got, []string{"department", "email"}) {
		t.Errorf("attribute names: %v", got)
	}
	if got := mapping.MappedGroupNames(); !slices.Equal(got, []string{"auditors", "sre"}) {
		t.Errorf("group names: %v", got)
	}
}

func TestAMappingBeyondTheEntryBoundIsRejected(t *testing.T) {
	var b strings.Builder
	b.WriteString("schema_version: 1\nclaims:\n")
	for i := 0; i <= identity.MaxMappingEntries; i++ {
		b.WriteString("  - claim: c")
		b.WriteString(itoa(i))
		b.WriteString("\n    attribute: a")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	_, err := identity.ParseMapping([]byte(b.String()))
	assertRejected(t, err, identity.RejectMappingTooLarge)
}

func mustParse(t *testing.T, document string) *identity.Mapping {
	t.Helper()
	mapping, err := identity.ParseMapping([]byte(document))
	if err != nil {
		t.Fatalf("the mapping did not parse: %v", err)
	}
	return mapping
}

func assertRejected(t *testing.T, err error, code string) {
	t.Helper()
	var rejected *identity.MappingRejected
	if !errors.As(err, &rejected) {
		t.Fatalf("want a *MappingRejected carrying %s, got %v", code, err)
	}
	for _, r := range rejected.Rejections {
		if r.Code == code {
			return
		}
	}
	t.Fatalf("want rejection %s, got %v", code, rejected.Rejections)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
