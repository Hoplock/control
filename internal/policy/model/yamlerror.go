// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// READING THE YAML LIBRARY'S ERRORS AS REJECTIONS (0065; PLAN M21). yaml.v3
// reports a document it cannot read as English: the parser's problem, a field
// "not found in type model.Rule", a value it "cannot unmarshal … into
// []string". That text is neither an author's vocabulary nor one a console can
// translate, so it never leaves this package. Each message is read here into a
// code and typed parameters — the problem's kind, the key, the shapes — and
// the line it names becomes the rejection's position.

// structuredMark begins a TypeError string that carries a rejection rather
// than a sentence. yaml.v3 keeps a TypeError's strings and nothing else of it
// (decode.go appends Errors verbatim), so this is the only channel by which an
// UnmarshalYAML can hand Parse a code and parameters while the decode carries
// on past it. A NUL cannot appear in the library's own messages.
const structuredMark = "\x00hoplock-rejection "

// nodeReject reports a rejection at the node it was found on.
//
// It is a *yaml.TypeError rather than a plain error on purpose: yaml.v3 aborts
// the whole decode on a plain error from an Unmarshaler and drops the position,
// while a TypeError is collected and the decode carries on. So this is what
// makes "every reason at once, each with its line" true for the enums as well
// as for the keys.
func nodeReject(n *yaml.Node, code Code, kv ...string) error {
	params, err := json.Marshal(Reject(code, "", n.Line, kv...).Params)
	if err != nil {
		// A map of strings always marshals.
		panic(err)
	}
	return &yaml.TypeError{
		Errors: []string{fmt.Sprintf("line %d: %s%s %s", n.Line, structuredMark, code, params)},
	}
}

// rejectionOf reads one of the library's messages, with its line already
// split off, into a rejection at that line.
func rejectionOf(line int, msg string) Rejection {
	if rest, ok := strings.CutPrefix(msg, structuredMark); ok {
		code, raw, _ := strings.Cut(rest, " ")
		var params map[string]string
		if json.Unmarshal([]byte(raw), &params) == nil {
			return Rejection{Code: Code(code), Line: line, Params: params}
		}
	}
	// "field X not found in type T": a key the schema does not know. The
	// type is Go's name for the place, and the line already says where.
	if rest, ok := strings.CutPrefix(msg, "field "); ok {
		if key, _, ok := strings.Cut(rest, " not found in type "); ok {
			return Reject(CodeKeyUnknown, "", line, "key", key)
		}
	}
	// "mapping key \"x\" already defined at line N".
	if rest, ok := strings.CutPrefix(msg, "mapping key "); ok {
		if quoted, at, ok := strings.Cut(rest, " already defined at line "); ok {
			if key, err := strconv.Unquote(quoted); err == nil {
				if _, err := strconv.Atoi(at); err == nil {
					return Reject(CodeKeyDuplicate, "", line, "key", key, "other_line", at)
				}
			}
		}
	}
	// "cannot unmarshal !!str `abc` into []string".
	if rest, ok := strings.CutPrefix(msg, "cannot unmarshal "); ok {
		if i := strings.LastIndex(rest, " into "); i >= 0 {
			tag, _, _ := strings.Cut(rest[:i], " ")
			return Reject(CodeValueWrongType, "", line,
				"found", string(shapeOfTag(tag)), "expected", string(shapeOfGoType(rest[i+len(" into "):])))
		}
	}
	return Reject(CodeDocumentMalformed, "", line, "problem", string(yamlProblemOf(msg)))
}

// yamlProblems are the parser's and scanner's problems (yaml.v3 v3.0.1's
// scannerc.go, parserc.go and readerc.go), each with the kind an author can
// act on. A problem a later release words differently reads as
// YAMLUnreadable — still a code, never the library's English.
var yamlProblems = map[string]YAMLProblem{
	"found a tab character that violates indentation":              YAMLTabIndentation,
	"found a tab character where an indentation space is expected": YAMLTabIndentation,

	"did not find expected key":                              YAMLStructure,
	"did not find expected node content":                     YAMLStructure,
	"did not find expected '-' indicator":                    YAMLStructure,
	"mapping values are not allowed in this context":         YAMLStructure,
	"mapping keys are not allowed in this context":           YAMLStructure,
	"block sequence entries are not allowed in this context": YAMLStructure,
	"could not find expected ':'":                            YAMLStructure,
	"did not find expected <document start>":                 YAMLStructure,
	"did not find expected <stream-start>":                   YAMLStructure,
	"found unexpected document indicator":                    YAMLStructure,

	"found unexpected end of stream":   YAMLUnterminated,
	"did not find expected ',' or ']'": YAMLUnterminated,
	"did not find expected ',' or '}'": YAMLUnterminated,

	"found character that cannot start any token":           YAMLCharacter,
	"found unknown escape character":                        YAMLCharacter,
	"found invalid Unicode character escape code":           YAMLCharacter,
	"did not find expected hexdecimal number":               YAMLCharacter,
	"did not find expected alphabetic or numeric character": YAMLCharacter,
	"did not find expected comment or line break":           YAMLCharacter,
	"did not find expected whitespace or line break":        YAMLCharacter,
	"did not find expected whitespace":                      YAMLCharacter,
	"found an indentation indicator equal to 0":             YAMLCharacter,
	"found unexpected non-alphabetical character":           YAMLCharacter,

	"invalid leading UTF-8 octet":             YAMLEncoding,
	"invalid trailing UTF-8 octet":            YAMLEncoding,
	"incomplete UTF-8 octet sequence":         YAMLEncoding,
	"invalid length of a UTF-8 sequence":      YAMLEncoding,
	"found an incorrect leading UTF-8 octet":  YAMLEncoding,
	"found an incorrect trailing UTF-8 octet": YAMLEncoding,
	"incomplete UTF-16 character":             YAMLEncoding,
	"incomplete UTF-16 surrogate pair":        YAMLEncoding,
	"unexpected low surrogate area":           YAMLEncoding,
	"expected low surrogate area":             YAMLEncoding,
	"invalid Unicode character":               YAMLEncoding,
	"control characters are not allowed":      YAMLEncoding,

	"found undefined tag handle":                   YAMLTag,
	"did not find expected tag URI":                YAMLTag,
	"did not find URI escaped octet":               YAMLTag,
	"did not find expected '!'":                    YAMLTag,
	"did not find the expected '>'":                YAMLTag,
	"found duplicate %YAML directive":              YAMLTag,
	"found duplicate %TAG directive":               YAMLTag,
	"found incompatible YAML document":             YAMLTag,
	"found unknown directive name":                 YAMLTag,
	"could not find expected directive name":       YAMLTag,
	"did not find expected version number":         YAMLTag,
	"found extremely long version number":          YAMLTag,
	"did not find expected digit or '.' character": YAMLTag,
	"!!binary value contains invalid base64 data":  YAMLTag,

	"document contains excessive aliasing": YAMLAlias,
}

// yamlProblemOf classifies one of the library's messages, its line already
// split off.
func yamlProblemOf(msg string) YAMLProblem {
	if p, ok := yamlProblems[msg]; ok {
		return p
	}
	switch {
	case strings.HasPrefix(msg, "unknown anchor "),
		strings.HasPrefix(msg, "anchor ") && strings.HasSuffix(msg, " value contains itself"):
		return YAMLAlias
	case strings.HasPrefix(msg, "input error: "):
		return YAMLEncoding
	}
	return YAMLUnreadable
}

// shapeOfTag is the shape a YAML tag names: the value as written.
func shapeOfTag(tag string) Shape {
	switch tag {
	case "!!str":
		return ShapeString
	case "!!int":
		return ShapeInteger
	case "!!float":
		return ShapeNumber
	case "!!bool":
		return ShapeBoolean
	case "!!seq":
		return ShapeList
	case "!!map":
		return ShapeMapping
	case "!!null":
		return ShapeNull
	case "!!timestamp":
		return ShapeTimestamp
	}
	return ShapeOther
}

// shapeOfGoType is the shape a Go type decodes from: what the schema expects.
// yaml.v3 names the type it could not fill as Go spells it, so a named type
// is looked up among every type the bundle is built from.
func shapeOfGoType(name string) Shape {
	switch {
	case strings.HasPrefix(name, "*"):
		return shapeOfGoType(name[1:])
	case strings.HasPrefix(name, "[]"):
		return ShapeList
	case strings.HasPrefix(name, "map["):
		return ShapeMapping
	}
	if s, ok := schemaShapes[name]; ok {
		return s
	}
	switch name {
	case "string":
		return ShapeString
	case "bool":
		return ShapeBoolean
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return ShapeInteger
	case "float32", "float64":
		return ShapeNumber
	}
	return ShapeOther
}

// schemaShapes is the shape of every named type reachable from Bundle, by the
// name reflect gives it — which is the name yaml.v3 prints.
var schemaShapes = func() map[string]Shape {
	out := map[string]Shape{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		if t.Name() != "" {
			if _, seen := out[t.String()]; seen {
				return
			}
			out[t.String()] = shapeOfKind(t.Kind())
		}
		//exhaustive:ignore a scalar holds no type, so only what can contain one is walked.
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(t.Elem())
		case reflect.Map:
			walk(t.Key())
			walk(t.Elem())
		case reflect.Struct:
			for i := range t.NumField() {
				if t.Field(i).IsExported() {
					walk(t.Field(i).Type)
				}
			}
		}
	}
	walk(reflect.TypeFor[Bundle]())
	return out
}()

func shapeOfKind(k reflect.Kind) Shape {
	//exhaustive:ignore the remaining kinds (funcs, channels, interfaces) decode from no YAML shape the schema names.
	switch k {
	case reflect.String:
		return ShapeString
	case reflect.Bool:
		return ShapeBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return ShapeInteger
	case reflect.Float32, reflect.Float64:
		return ShapeNumber
	case reflect.Slice, reflect.Array:
		return ShapeList
	case reflect.Map, reflect.Struct:
		return ShapeMapping
	}
	return ShapeOther
}
