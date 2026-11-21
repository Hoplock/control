// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"maps"
	"slices"
)

// WORDED PARAMETERS (0065; PLAN M21). Most of a rejection's parameters are
// data — a rule id, a group, a line, a field of the schema — and a client
// shows them as they are. A few are drawn from a closed set and stand for
// words: which problem made a document not YAML, what shape a value is and
// what shape the schema wanted, which vocabulary a value is not in. Those
// travel as identifiers, never as English, and each side says them in its own
// words: Message() in this file's English, the console in its catalogue, from
// the same identifiers (Enumerations). A value added here without words fails
// the build, because these maps are keyed by enums and `exhaustive` checks
// them.

// YAMLProblem is why a document is not YAML: the `problem` of
// CodeDocumentMalformed. It is classified from the YAML library's own message
// (yamlProblemOf), so an author reads what to do rather than the parser's
// vocabulary.
type YAMLProblem string

// The problems a document that is not YAML can have.
const (
	// YAMLTabIndentation is a tab where YAML indents with spaces.
	YAMLTabIndentation YAMLProblem = "tab_indentation"
	// YAMLStructure is a key, a list item or a value that is not where its
	// indentation puts it: the commonest mistake, and the parser's least
	// readable message.
	YAMLStructure YAMLProblem = "structure"
	// YAMLUnterminated is a quoted string, a flow list or a flow mapping
	// opened and never closed.
	YAMLUnterminated YAMLProblem = "unterminated"
	// YAMLCharacter is a character that cannot stand where it is unquoted,
	// or an escape that does not exist.
	YAMLCharacter YAMLProblem = "character"
	// YAMLEncoding is text that is not UTF-8, or holds a control character.
	YAMLEncoding YAMLProblem = "encoding"
	// YAMLTag is a malformed `!tag` or `%directive`; a bundle needs neither.
	YAMLTag YAMLProblem = "tag"
	// YAMLAlias is an anchor or an alias that is undefined, contains itself
	// or expands too far.
	YAMLAlias YAMLProblem = "alias"
	// YAMLUnreadable is any other problem the parser reports.
	YAMLUnreadable YAMLProblem = "unreadable"
)

var yamlProblemWords = map[YAMLProblem]string{
	YAMLTabIndentation: "a tab character is used for indentation here, and YAML indents with spaces only",
	YAMLStructure:      "a key, a list item or a value is not where its indentation puts it; check that this line is indented like its siblings and that each `key:` is followed by a space",
	YAMLUnterminated:   "a quoted string, a `[` list or a `{` mapping is opened and never closed",
	YAMLCharacter:      "a character here cannot stand unquoted, or an escape sequence does not exist; quote the value",
	YAMLEncoding:       "the text is not valid UTF-8, or holds a control character",
	YAMLTag:            "a `!` tag, a tagged value or a `%` directive is malformed; a policy bundle needs no tag and no directive",
	YAMLAlias:          "an anchor or alias is undefined, contains itself, or expands too far",
	YAMLUnreadable:     "the parser cannot read the document from this line on",
}

// Shape is what a YAML value is, or what the schema expects one to be: the
// `found` and `expected` of CodeValueWrongType.
type Shape string

// The shapes a value can take.
const (
	ShapeString    Shape = "string"
	ShapeInteger   Shape = "integer"
	ShapeNumber    Shape = "number"
	ShapeBoolean   Shape = "boolean"
	ShapeList      Shape = "list"
	ShapeMapping   Shape = "mapping"
	ShapeNull      Shape = "null"
	ShapeTimestamp Shape = "timestamp"
	// ShapeOther is a shape outside the ones above: a binary value, a
	// custom tag, a Go type the schema does not name.
	ShapeOther Shape = "other"
)

var shapeWords = map[Shape]string{
	ShapeString:    "a string",
	ShapeInteger:   "a whole number",
	ShapeNumber:    "a number",
	ShapeBoolean:   "true or false",
	ShapeList:      "a list",
	ShapeMapping:   "a mapping of keys to values",
	ShapeNull:      "empty",
	ShapeTimestamp: "a timestamp",
	ShapeOther:     "a value of another kind",
}

// Vocabulary names a closed vocabulary of the bundle: the `vocabulary` of
// CodeValueUnknown. Each is one of the kinds in kind.go, or the derivations a
// username has.
type Vocabulary string

// The vocabularies a value can fall outside of.
const (
	VocabularyEffect            Vocabulary = "effect"
	VocabularyRouteIntent       Vocabulary = "route_intent"
	VocabularyChannelType       Vocabulary = "channel_type"
	VocabularyRequestType       Vocabulary = "request_type"
	VocabularyCredentialMethod  Vocabulary = "credential_method"
	VocabularyCredentialKind    Vocabulary = "credential_kind"
	VocabularyExpiryPosture     Vocabulary = "expiry_posture"
	VocabularyAlgorithmProfile  Vocabulary = "algorithm_profile"
	VocabularyAlgorithmFloor    Vocabulary = "algorithm_floor"
	VocabularyFilterMode        Vocabulary = "filter_mode"
	VocabularyExecMode          Vocabulary = "exec_mode"
	VocabularyFilterAction      Vocabulary = "filter_action"
	VocabularyCommandForm       Vocabulary = "command_form"
	VocabularyArgumentKind      Vocabulary = "argument_kind"
	VocabularyExecutionRung     Vocabulary = "execution_rung"
	VocabularyReachRung         Vocabulary = "reach_rung"
	VocabularyObligationKind    Vocabulary = "obligation_kind"
	VocabularyGrantOrigin       Vocabulary = "grant_origin"
	VocabularyUnanswered        Vocabulary = "unanswered"
	VocabularyCacheKeyComponent Vocabulary = "cache_key_component"
	VocabularyAuthMethod        Vocabulary = "auth_method"
	VocabularyDay               Vocabulary = "day"
	VocabularyUsernameSource    Vocabulary = "username_source"
)

var vocabularyWords = map[Vocabulary]string{
	VocabularyEffect:            "effect",
	VocabularyRouteIntent:       "route intent",
	VocabularyChannelType:       "channel type",
	VocabularyRequestType:       "request type",
	VocabularyCredentialMethod:  "credential method",
	VocabularyCredentialKind:    "credential kind",
	VocabularyExpiryPosture:     "expiry posture",
	VocabularyAlgorithmProfile:  "algorithm profile",
	VocabularyAlgorithmFloor:    "algorithm floor",
	VocabularyFilterMode:        "filter mode",
	VocabularyExecMode:          "exec mode",
	VocabularyFilterAction:      "filter action",
	VocabularyCommandForm:       "command form",
	VocabularyArgumentKind:      "argument kind",
	VocabularyExecutionRung:     "execution rung",
	VocabularyReachRung:         "reach rung",
	VocabularyObligationKind:    "obligation kind",
	VocabularyGrantOrigin:       "grant origin",
	VocabularyUnanswered:        "unanswered setting",
	VocabularyCacheKeyComponent: "cache key component",
	VocabularyAuthMethod:        "authentication method",
	VocabularyDay:               "day",
	VocabularyUsernameSource:    "username source (a literal is written as a plain string)",
}

// worded is, for each code with a worded parameter, that parameter's words.
//
//exhaustive:ignore only the codes with a worded parameter are listed; every other code's parameters are data.
var worded = map[Code]map[string]map[string]string{
	CodeDocumentMalformed: {"problem": stringKeyed(yamlProblemWords)},
	CodeValueWrongType:    {"expected": stringKeyed(shapeWords), "found": stringKeyed(shapeWords)},
	CodeValueUnknown:      {"vocabulary": stringKeyed(vocabularyWords)},
}

func stringKeyed[K ~string](m map[K]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

// Enumerations names, for each code with a worded parameter, every value that
// parameter can take, sorted. The console says each in its own words (an ICU
// select), and a test holds its catalogue to this list.
func Enumerations() map[Code]map[string][]string {
	out := make(map[Code]map[string][]string, len(worded))
	for code, params := range worded {
		out[code] = make(map[string][]string, len(params))
		for name, words := range params {
			out[code][name] = slices.Sorted(maps.Keys(words))
		}
	}
	return out
}

// wordsFor is the English a worded parameter's value reads as, or the value
// itself when it is not worded.
func wordsFor(code Code, param, value string) string {
	if w, ok := worded[code][param][value]; ok {
		return w
	}
	return value
}

// ParameterWords is the English a worded parameter's value reads as, or the
// value itself when it is not worded: what Message() substitutes, for a
// caller that must say one parameter alone — a north-bound client of a
// version that was sent prose in its place.
func ParameterWords(code Code, param, value string) string { return wordsFor(code, param, value) }
