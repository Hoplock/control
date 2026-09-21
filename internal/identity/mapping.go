// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// The claim mapping: the one place an IdP's vocabulary becomes this server's.
//
// WHY IT EXISTS AT ALL. A claim an IdP administrator can set is an attribute a
// policy rule matches on. Trusting a raw claim name straight from a token
// would mean that whoever can add `department: sre` to a token in Okta has
// granted themselves whatever `department == sre` grants here — without
// touching policy, without a review, and without appearing in any diff. The
// mapping is where that trust is granted DELIBERATELY, one claim at a time
// (M7).
//
// Three properties follow, and each has a test:
//
//   - IT IS AN ALLOW-LIST, NOT A TRANSFORM. A claim the mapping does not name
//     does not become an attribute. Not renamed, not passed through, not
//     namespaced — dropped. That is the phase's security test.
//   - IT IS DATA, VALIDATED LIKE POLICY IS. A document that maps two claims
//     onto one attribute, or onto a name this server reserves for itself, is
//     rejected at authoring time rather than resolved at login.
//   - IT IS VERSIONED, AND THE VERSION IS IN THE DECISION RECORD. "Why did
//     Alice match the `sre` rule" is answered by the mapping as often as by
//     the rule (PLAN §6), so a record that names the rule and not the mapping
//     answers half the question.
//
// What is deliberately NOT here is transformation: no regular expressions, no
// templates, no conditional expressions over several claims. Those are
// `ext.IdentitySync`'s and Hoplock Enterprise's (0011's scope note). A mapping
// language is a policy language, and this product already has one.

// MappingSchemaVersion is the only document schema version this build reads.
const MappingSchemaVersion = 1

// MaxMappingEntries bounds a document. A mapping is written by a person and
// read on every login; one with ten thousand entries is a mistake, and a bound
// makes it a rejection rather than a latency incident.
const MaxMappingEntries = 512

// reservedAttributes are names this server sets itself. An IdP must never be
// able to assert one: [ClaimChainHop] says which proxy's key authenticated a
// chain leg, and a token that could set it would be a user asserting the
// identity of the infrastructure in front of them.
var reservedAttributes = []string{ClaimChainHop}

// reservedPrefix is closed to mappings for the same reason, ahead of the next
// claim this server needs to set for itself.
const reservedPrefix = "hoplock."

// attributeNamePattern is what a policy attribute may be called. It is
// deliberately narrower than what an IdP may call a claim: the mapping is a
// translation, and the side this server matches on is the side it constrains.
var attributeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)

// groupNamePattern is what a mapped group may be called. It matches what a
// policy bundle's `groups` accepts, because a mapping that can produce a group
// no rule can name produces nothing.
var groupNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Mapping is a parsed, validated claim mapping.
//
// Version is NOT in the document: it is allocated by the store when the
// document is submitted, the way a policy bundle's version is. A version
// authored by hand is a version two operators can collide on.
type Mapping struct {
	// Version is the number a decision record names. Zero on a document
	// that has been parsed but not yet stored.
	Version int
	// Digest is the SHA-256 of the document bytes, hex-encoded.
	Digest string
	// Description is the operator's own note.
	Description string

	claims    []claimRule
	groupFrom string
	groups    map[string]string
	document  string
}

type claimRule struct {
	claim     string
	attribute string
	values    []string
}

// mappingDocument is the wire shape. It is unmarshalled strictly: an unknown
// key is a rejection, because a misspelled `attributes:` that parsed silently
// would be a mapping an operator believes is in force and is not.
type mappingDocument struct {
	SchemaVersion int                 `yaml:"schema_version"`
	Description   string              `yaml:"description"`
	Claims        []mappingClaimEntry `yaml:"claims"`
	Groups        *mappingGroupsEntry `yaml:"groups"`
}

type mappingClaimEntry struct {
	// Claim is the name as the IdP sends it, matched exactly.
	Claim string `yaml:"claim"`
	// Attribute is the policy attribute it becomes.
	Attribute string `yaml:"attribute"`
	// Values, when given, is the set of claim values that map. A value
	// outside it does not produce the attribute at all — which is how a
	// mapping says "I trust this claim only when it says one of these
	// things".
	Values []string `yaml:"values"`
}

type mappingGroupsEntry struct {
	// Claim names which claim carries the IdP's group names.
	Claim string `yaml:"claim"`
	// Map is the exact external-to-local translation. There is no prefix
	// rule and no pattern: see the note at the top of this file.
	Map []mappingGroupEntry `yaml:"map"`
}

type mappingGroupEntry struct {
	External string `yaml:"external"`
	Group    string `yaml:"group"`
}

// MappingRejection is one reason a document was refused.
//
// It carries a stable code so that a console can render it and a test can
// assert it, in the shape 0005 established for policy rejections: an operator
// fixing a mapping needs to be told which entry and why, not handed a parse
// error.
type MappingRejection struct {
	// Code is stable across releases and is the thing to assert on.
	Code string
	// Field locates the problem in the document.
	Field string
	// Message is the English sentence.
	Message string
}

func (r MappingRejection) Error() string {
	if r.Field == "" {
		return fmt.Sprintf("%s: %s", r.Code, r.Message)
	}
	return fmt.Sprintf("%s: %s: %s", r.Code, r.Field, r.Message)
}

// MappingRejected is the error ParseMapping returns, carrying every rejection
// rather than the first: an operator fixing a document one round trip per
// mistake is an operator who stops using the validator.
type MappingRejected struct{ Rejections []MappingRejection }

func (e *MappingRejected) Error() string {
	parts := make([]string, 0, len(e.Rejections))
	for _, r := range e.Rejections {
		parts = append(parts, r.Error())
	}
	return "identity: the claim mapping was rejected: " + strings.Join(parts, "; ")
}

// The rejection codes. Adding one is deliberate; reusing one for a different
// condition is not allowed, for the reason 0014 gives about error codes.
const (
	RejectMappingUnparseable   = "mapping.unparseable"
	RejectMappingSchemaVersion = "mapping.schema_version"
	RejectMappingClaimEmpty    = "mapping.claim_empty"
	RejectMappingAttributeName = "mapping.attribute_name"
	RejectMappingAttributeDup  = "mapping.attribute_duplicate"
	RejectMappingReserved      = "mapping.attribute_reserved"
	RejectMappingGroupClaim    = "mapping.group_claim"
	RejectMappingGroupName     = "mapping.group_name"
	RejectMappingGroupDup      = "mapping.group_duplicate"
	RejectMappingTooLarge      = "mapping.too_large"
	RejectMappingValueEmpty    = "mapping.value_empty"
)

// ParseMapping parses and validates a mapping document.
//
// It returns every rejection it found. A document that produces none is one
// this build will apply exactly as written.
func ParseMapping(document []byte) (*Mapping, error) {
	var doc mappingDocument
	dec := yaml.NewDecoder(strings.NewReader(string(document)))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, &MappingRejected{Rejections: []MappingRejection{{
			Code:    RejectMappingUnparseable,
			Message: "the document is not a claim mapping this build can read",
		}}}
	}

	var rej []MappingRejection
	if doc.SchemaVersion != MappingSchemaVersion {
		rej = append(rej, MappingRejection{
			Code:    RejectMappingSchemaVersion,
			Field:   "schema_version",
			Message: fmt.Sprintf("this build reads schema version %d", MappingSchemaVersion),
		})
	}

	entries := len(doc.Claims)
	if doc.Groups != nil {
		entries += len(doc.Groups.Map)
	}
	if entries > MaxMappingEntries {
		rej = append(rej, MappingRejection{
			Code:    RejectMappingTooLarge,
			Message: fmt.Sprintf("a mapping may carry at most %d entries", MaxMappingEntries),
		})
	}

	m := &Mapping{
		Description: doc.Description,
		groups:      map[string]string{},
		document:    string(document),
	}
	sum := sha256.Sum256(document)
	m.Digest = hex.EncodeToString(sum[:])

	seenAttribute := map[string]int{}
	for i, e := range doc.Claims {
		field := fmt.Sprintf("claims[%d]", i)
		claim := strings.TrimSpace(e.Claim)
		attribute := strings.TrimSpace(e.Attribute)

		if claim == "" {
			rej = append(rej, MappingRejection{
				Code: RejectMappingClaimEmpty, Field: field + ".claim",
				Message: "a mapping entry must name the claim it reads",
			})
		}
		switch {
		case attribute == "":
			rej = append(rej, MappingRejection{
				Code: RejectMappingAttributeName, Field: field + ".attribute",
				Message: "a mapping entry must name the attribute it produces",
			})
		case !attributeNamePattern.MatchString(attribute):
			rej = append(rej, MappingRejection{
				Code: RejectMappingAttributeName, Field: field + ".attribute",
				Message: "an attribute name is lower-case letters, digits and underscores, optionally dotted",
			})
		case slices.Contains(reservedAttributes, attribute) || strings.HasPrefix(attribute, reservedPrefix):
			// The security rule: an IdP may not assert an attribute
			// this server sets for itself.
			rej = append(rej, MappingRejection{
				Code: RejectMappingReserved, Field: field + ".attribute",
				Message: fmt.Sprintf("%q is set by this server and cannot be mapped from a claim", attribute),
			})
		default:
			if first, dup := seenAttribute[attribute]; dup {
				rej = append(rej, MappingRejection{
					Code: RejectMappingAttributeDup, Field: field + ".attribute",
					Message: fmt.Sprintf("%q is already produced by claims[%d]; one attribute has one source", attribute, first),
				})
			} else {
				seenAttribute[attribute] = i
			}
		}

		for vi, v := range e.Values {
			if strings.TrimSpace(v) == "" {
				rej = append(rej, MappingRejection{
					Code:    RejectMappingValueEmpty,
					Field:   fmt.Sprintf("%s.values[%d]", field, vi),
					Message: "an empty permitted value matches nothing and hides the entry",
				})
			}
		}

		m.claims = append(m.claims, claimRule{
			claim:     claim,
			attribute: attribute,
			values:    slices.Clone(e.Values),
		})
	}

	if doc.Groups != nil {
		m.groupFrom = strings.TrimSpace(doc.Groups.Claim)
		if m.groupFrom == "" {
			rej = append(rej, MappingRejection{
				Code: RejectMappingGroupClaim, Field: "groups.claim",
				Message: "a group mapping must name the claim that carries the IdP's groups",
			})
		}
		for i, e := range doc.Groups.Map {
			field := fmt.Sprintf("groups.map[%d]", i)
			external := strings.TrimSpace(e.External)
			group := strings.TrimSpace(e.Group)
			if external == "" {
				rej = append(rej, MappingRejection{
					Code: RejectMappingGroupName, Field: field + ".external",
					Message: "a group entry must name the IdP group it reads",
				})
				continue
			}
			if !groupNamePattern.MatchString(group) {
				rej = append(rej, MappingRejection{
					Code: RejectMappingGroupName, Field: field + ".group",
					Message: "a group name is letters, digits, dot, dash and underscore",
				})
				continue
			}
			if _, dup := m.groups[external]; dup {
				rej = append(rej, MappingRejection{
					Code: RejectMappingGroupDup, Field: field + ".external",
					Message: fmt.Sprintf("%q is mapped twice", external),
				})
				continue
			}
			m.groups[external] = group
		}
	}

	if len(rej) > 0 {
		return nil, &MappingRejected{Rejections: rej}
	}
	return m, nil
}

// Document returns the bytes the digest covers, so a caller storing a mapping
// stores what was validated rather than a re-encoding of it.
func (m *Mapping) Document() string { return m.document }

// Assertion is what a broker learned from an IdP, before any mapping.
//
// It is deliberately raw: the whole point of this file is that nothing in here
// reaches policy without passing through [Mapping.Apply].
type Assertion struct {
	// Connector is the connector that produced it.
	Connector string
	// Subject is the IdP's stable identifier — `sub`, or the SAML NameID.
	Subject string
	// Login is the username the IdP asserted, where it asserted one.
	Login string
	// DisplayName and Email are for operators.
	DisplayName string
	Email       string
	// Claims are every claim the assertion carried, as strings. A
	// multi-valued claim appears in MultiClaims instead.
	Claims map[string]string
	// MultiClaims are the multi-valued ones — a group list, most often.
	MultiClaims map[string][]string
}

// Attributes are what a mapping produced: the only thing that reaches policy.
type Attributes struct {
	// Claims are the mapped policy attributes.
	Claims map[string]string
	// Groups are the mapped group names, sorted.
	Groups []string
	// MappingVersion is the version that produced them, echoed into the
	// decision and audit records.
	MappingVersion int
}

// Apply turns an assertion into attributes.
//
// EVERYTHING NOT NAMED BY THE MAPPING IS DROPPED. There is no fall-through, no
// pass-through namespace, and no "unknown claims go here" bucket: a bucket is
// an allow-list with the lid off.
func (m *Mapping) Apply(a Assertion) Attributes {
	out := Attributes{Claims: map[string]string{}, MappingVersion: m.Version}
	if m == nil {
		return out
	}

	for _, rule := range m.claims {
		value, ok := m.claimValue(a, rule.claim)
		if !ok {
			continue
		}
		if len(rule.values) > 0 && !slices.Contains(rule.values, value) {
			continue
		}
		out.Claims[rule.attribute] = value
	}

	if m.groupFrom != "" {
		for _, external := range m.assertedGroups(a) {
			if local, ok := m.groups[external]; ok {
				out.Groups = append(out.Groups, local)
			}
		}
		slices.Sort(out.Groups)
		out.Groups = slices.Compact(out.Groups)
	}
	return out
}

// claimValue reads a single-valued claim. A multi-valued claim is NOT
// flattened into one: a policy attribute is a single value, and joining a list
// with a comma would make `groups == "a,b"` a matchable string that means
// nothing.
func (m *Mapping) claimValue(a Assertion, name string) (string, bool) {
	v, ok := a.Claims[name]
	return v, ok
}

func (m *Mapping) assertedGroups(a Assertion) []string {
	if v, ok := a.MultiClaims[m.groupFrom]; ok {
		return v
	}
	// A single-valued group claim is legitimate: an IdP asserting one group
	// may send a string rather than an array.
	if v, ok := a.Claims[m.groupFrom]; ok && v != "" {
		return []string{v}
	}
	return nil
}

// MappedAttributeNames returns every attribute this mapping can produce,
// sorted. The console renders it, and a policy author reads it to know which
// names a rule may match on.
func (m *Mapping) MappedAttributeNames() []string {
	out := make([]string, 0, len(m.claims))
	for _, r := range m.claims {
		out = append(out, r.attribute)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// MappedGroupNames returns every local group this mapping can produce, sorted.
func (m *Mapping) MappedGroupNames() []string {
	out := slices.Collect(maps.Values(m.groups))
	slices.Sort(out)
	return slices.Compact(out)
}

// EmptyMapping is the mapping a tenant has before an operator writes one.
//
// It maps NOTHING, which is the safe direction: a tenant that has federated
// without authoring a mapping gets identities with no attributes and no
// groups, and a policy that grants on attributes grants nothing. The
// alternative — passing claims through until a mapping exists — is a hole that
// closes only when somebody remembers.
func EmptyMapping() *Mapping {
	return &Mapping{groups: map[string]string{}, document: "schema_version: 1\n"}
}
