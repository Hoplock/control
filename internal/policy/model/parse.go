// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	// tzdata is embedded on purpose. A bundle names an IANA location and the
	// compiler resolves it, so without this the same bundle compiles on a
	// developer's machine and fails in a scratch container that ships no
	// zoneinfo — and, worse, two hosts with different tzdata releases could
	// disagree about a DST boundary while both reporting success. Embedding
	// it makes compilation a function of the bundle and this binary, which
	// is what determinism means here. It costs a few hundred kilobytes once.
	_ "time/tzdata"

	"gopkg.in/yaml.v3"
)

// Parse reads a policy bundle, rejecting anything outside the closed
// vocabulary. It returns Rejections — every reason at once, in source order,
// because an author who has to compile five times to find five mistakes stops
// trusting the compiler.
//
// Parse establishes only that the *document* decoded: valid YAML, no unknown
// key, no value outside an enum. It fails only where there is no usable bundle
// to go on with. Everything else — the document's own shape (Validate) and
// everything semantic: an unreachable rule, a contradiction, a reference to
// something that does not exist, an unconstrained axis — belongs to the
// compiler, so that an author sees the whole list in one compile rather than
// one class of mistake at a time.
func Parse(src []byte) (*Bundle, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, Rejections{Reject(CodeDocumentMalformed, "", yamlLine(err), "detail", yamlDetail(err))}
	}

	var b Bundle
	dec := yaml.NewDecoder(bytes.NewReader(src))
	// Strict: a key this server does not know is a policy somebody believes
	// is in force and is not. Silently ignoring it is how a bundle means one
	// thing to its author and another to the fleet.
	dec.KnownFields(true)
	// An empty document decodes to io.EOF. That is not a malformed
	// document, it is a bundle with no rules, and Validate has a message for
	// it that tells the author something useful.
	if err := dec.Decode(&b); err != nil && !errors.Is(err, io.EOF) {
		return nil, decodeRejections(err)
	}

	sum := sha256.Sum256(src)
	b.digest = hex.EncodeToString(sum[:])
	b.source = bytes.Clone(src)
	attachRuleLines(&root, &b)

	// Resolved best-effort: an unknown location leaves the bundle reading
	// times in UTC and Validate reports it, rather than throwing away every
	// other rejection in the document over one misspelt zone name.
	if loc, err := time.LoadLocation(b.Timezone); err == nil {
		b.location = loc
	} else {
		b.location = time.UTC
	}
	return &b, nil
}

// Validate checks the document's shape: that the version is one this server
// understands, that the bundle names a tenant and its vocabularies, and that
// every rule has a unique id and an effect it can act on. The compiler calls
// it, so a bundle built in Go rather than parsed gets exactly the same checks.
func (b *Bundle) Validate() Rejections {
	var rs Rejections

	if b.SchemaVersion != SchemaVersion {
		rs = append(rs, Reject(CodeSchemaVersionUnsupported, "", 0,
			"found", strconv.Itoa(b.SchemaVersion),
			"supported", strconv.Itoa(SchemaVersion)))
	}
	if strings.TrimSpace(string(b.Tenant)) == "" {
		rs = append(rs, Reject(CodeTenantMissing, "", 0))
	}
	for key, values := range b.Labels {
		if len(values) == 0 {
			rs = append(rs, Reject(CodeLabelVocabularyMalformed, "", 0, "label_key", key))
		}
	}
	for _, g := range b.Groups {
		if strings.TrimSpace(g) == "" {
			rs = append(rs, Reject(CodeGroupVocabularyMalformed, "", 0))
			break
		}
	}
	if len(b.Rules) == 0 {
		rs = append(rs, Reject(CodeNoRules, "", 0))
	}
	if b.Timezone != "" {
		if _, err := time.LoadLocation(b.Timezone); err != nil {
			rs = append(rs, Reject(CodeTimezoneUnknown, "", 0, "timezone", b.Timezone))
		}
	}

	seen := make(map[string]int, len(b.Rules))
	for _, r := range b.Rules {
		if strings.TrimSpace(r.ID) == "" {
			rs = append(rs, Reject(CodeRuleIDMissing, "", r.line))
			continue
		}
		if prev, dup := seen[r.ID]; dup {
			rs = append(rs, Reject(CodeRuleIDDuplicate, r.ID, r.line,
				"id", r.ID, "other_line", strconv.Itoa(prev)))
		} else {
			seen[r.ID] = r.line
		}
		switch r.Effect {
		case EffectAllow:
			if r.Route == nil {
				rs = append(rs, Reject(CodeRuleAllowNeedsRoute, r.ID, r.line))
			}
		case EffectDeny:
			if r.Route != nil {
				rs = append(rs, Reject(CodeRuleDenyCarriesRoute, r.ID, r.line))
			}
			if strings.TrimSpace(r.Reason) == "" {
				rs = append(rs, Reject(CodeRuleDenyNeedsReason, r.ID, r.line))
			}
		default:
			rs = append(rs, Reject(CodeRuleEffectMissing, r.ID, r.line))
		}
	}
	return rs.Sorted()
}

// attachRuleLines walks the parsed document for the line each rule starts on.
// Rules are a sequence, so the i-th element is the i-th rule; a document whose
// shape does not match simply leaves the lines at zero, and a rejection with no
// line is still a rejection that names its rule.
func attachRuleLines(root *yaml.Node, b *Bundle) {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "rules" {
			continue
		}
		seq := doc.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return
		}
		for j := range b.Rules {
			if j < len(seq.Content) {
				b.Rules[j].line = seq.Content[j].Line
			}
		}
		return
	}
}

// decodeRejections turns yaml.v3's errors into rejections that keep the line.
// yaml.TypeError carries one string per problem, each already prefixed with
// "line N:", which is the position an author needs and the only place it is
// available.
func decodeRejections(err error) Rejections {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		rs := make(Rejections, 0, len(te.Errors))
		for _, e := range te.Errors {
			line, detail := splitYAMLError(e)
			rs = append(rs, Reject(CodeDocumentMalformed, "", line, "detail", detail))
		}
		return rs.Sorted()
	}
	return Rejections{Reject(CodeDocumentMalformed, "", yamlLine(err), "detail", yamlDetail(err))}
}

func splitYAMLError(msg string) (int, string) {
	const prefix = "line "
	if !strings.HasPrefix(msg, prefix) {
		return 0, msg
	}
	rest := msg[len(prefix):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return 0, msg
	}
	n, err := strconv.Atoi(rest[:colon])
	if err != nil {
		return 0, msg
	}
	return n, strings.TrimSpace(rest[colon+1:])
}

func yamlLine(err error) int      { l, _ := splitYAMLError(cleanYAML(err)); return l }
func yamlDetail(err error) string { _, d := splitYAMLError(cleanYAML(err)); return d }

// cleanYAML strips yaml.v3's own prefix so the author reads the problem rather
// than the library's name.
func cleanYAML(err error) string {
	s := err.Error()
	s = strings.TrimPrefix(s, "yaml: unmarshal errors:\n  ")
	s = strings.TrimPrefix(s, "yaml: ")
	return strings.TrimSpace(s)
}

// nodeErr reports a problem at the node it was found on.
//
// It is a *yaml.TypeError rather than a plain error on purpose: yaml.v3 aborts
// the whole decode on a plain error from an Unmarshaler and drops the position,
// while a TypeError is collected and the decode carries on. So this is what
// makes "every reason at once, each with its line" true for the enums as well
// as for the keys.
func nodeErr(n *yaml.Node, format string, args ...any) error {
	return &yaml.TypeError{
		Errors: []string{fmt.Sprintf("line %d: %s", n.Line, fmt.Sprintf(format, args...))},
	}
}

// decodeEnum decodes one member of a closed vocabulary, naming every member in
// the error. An enum error is the most common thing an author hits, and "not
// one of a, b, c" is the difference between a fix and a search.
func decodeEnum[T ~string](n *yaml.Node, out *T, valid []T, axis string) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v := T(s)
	if !oneOf(v, valid) {
		return nodeErr(n, "%q is not a known %s; use one of %s", s, axis, joinEnum(valid))
	}
	*out = v
	return nil
}

// joinEnum renders a vocabulary for an error message.
func joinEnum[T ~string](valid []T) string {
	parts := make([]string, 0, len(valid))
	for _, v := range valid {
		parts = append(parts, strconv.Quote(string(v)))
	}
	return strings.Join(parts, ", ")
}

// parseTimeWindow reads `HH:MM-HH:MM`.
func parseTimeWindow(s string) (TimeWindow, bool) {
	from, to, ok := strings.Cut(s, "-")
	if !ok {
		return TimeWindow{}, false
	}
	f, ok := parseMinuteOfDay(strings.TrimSpace(from))
	if !ok {
		return TimeWindow{}, false
	}
	t, ok := parseMinuteOfDay(strings.TrimSpace(to))
	if !ok {
		return TimeWindow{}, false
	}
	return TimeWindow{FromMinutes: f, ToMinutes: t}, true
}

func parseMinuteOfDay(s string) (int, bool) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, false
	}
	hh, err := strconv.Atoi(h)
	if err != nil || hh < 0 || hh > 23 {
		return 0, false
	}
	mm, err := strconv.Atoi(m)
	if err != nil || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// Weekday maps a Day onto the standard library's, so that the mapping is one
// switch the linter checks rather than an index into a slice nobody rechecks.
func (d Day) Weekday() (time.Weekday, bool) {
	switch d {
	case Sunday:
		return time.Sunday, true
	case Monday:
		return time.Monday, true
	case Tuesday:
		return time.Tuesday, true
	case Wednesday:
		return time.Wednesday, true
	case Thursday:
		return time.Thursday, true
	case Friday:
		return time.Friday, true
	case Saturday:
		return time.Saturday, true
	}
	return time.Sunday, false
}

// ValidHostPattern reports whether a pattern is an exact hostname or a single
// leading wildcard (`*.prod.example.com`). Anything else — a wildcard in the
// middle, a bare `*`, an empty string — is refused: a pattern language nobody
// can predict is a policy nobody can review.
func ValidHostPattern(p string) bool {
	if p == "" || p == "*" {
		return false
	}
	if rest, ok := strings.CutPrefix(p, "*."); ok {
		return rest != "" && !strings.Contains(rest, "*")
	}
	return !strings.Contains(p, "*")
}

// MatchesHostPattern reports whether a hostname matches one pattern. Matching
// is case-insensitive, because DNS is.
func MatchesHostPattern(host, pattern string) bool {
	host = strings.ToLower(host)
	pattern = strings.ToLower(pattern)
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+suffix)
	}
	return host == pattern
}

// matchesAnyHostPattern reports whether a hostname matches any of the patterns.
func matchesAnyHostPattern(host string, patterns []string) bool {
	for _, p := range patterns {
		if MatchesHostPattern(host, p) {
			return true
		}
	}
	return false
}

// HostPatternCovers reports whether pattern a matches everything pattern b
// does. It is what the compiler's unreachability check is built from, and it is
// deliberately conservative: where it cannot prove coverage it says no, so an
// unreachable rule may go unreported but a reachable one is never refused.
func HostPatternCovers(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if a == b {
		return true
	}
	suffix, ok := strings.CutPrefix(a, "*.")
	if !ok {
		return false
	}
	if bs, bok := strings.CutPrefix(b, "*."); bok {
		return bs == suffix || strings.HasSuffix(bs, "."+suffix)
	}
	return strings.HasSuffix(b, "."+suffix)
}
