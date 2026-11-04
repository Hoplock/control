// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"encoding"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoplock/control/internal/httpapi/north"
)

// NORTH-BOUND ANSWERS NAME EVERY MEMBER (§8, 0045).
//
// Every body the package's writer renders, in every test of this package, is
// walked by value, and every exported struct field it reaches that has no
// `json` tag, or whose tag names a key that is not snake_case, is recorded.
// After the tests run, any record fails the package. So a store, fleet or
// service struct rendered directly — whose members are Go's field names, and
// whose rename inside Control would change the wire without failing anything —
// fails the build on the day it is rendered.
//
// Map keys are data (labels, attributes, claims) and are never checked. A
// type that marshals itself is its own answer and is not walked.

// snakeCase is a member name an answer may carry.
var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
)

// unnamed are the fields the probe found, keyed by type, field and the
// answer path it was found on.
var unnamed = struct {
	sync.Mutex
	found map[string]bool
}{found: map[string]bool{}}

func TestMain(m *testing.M) {
	north.SetAnswerProbe(probeAnswer)
	code := m.Run()
	unnamed.Lock()
	found := slices.Sorted(func(yield func(string) bool) {
		for k := range unnamed.found {
			if !yield(k) {
				return
			}
		}
	})
	unnamed.Unlock()
	if len(found) > 0 {
		fmt.Fprintf(os.Stderr, "north-bound answers render %d member(s) without a snake_case json name (§8):\n  %s\n\n"+
			"Render the answer through a view whose every field has a snake_case `json` tag; never render a "+
			"store, fleet or service struct directly.\n", len(found), strings.Join(found, "\n  "))
		os.Exit(1)
	}
	os.Exit(code)
}

// probeAnswer records every unnamed member of one body.
func probeAnswer(body any) {
	found := map[string]bool{}
	walkAnswer(reflect.ValueOf(body), "", map[reflect.Type]bool{}, func(rec string) { found[rec] = true })
	if len(found) == 0 {
		return
	}
	unnamed.Lock()
	defer unnamed.Unlock()
	for rec := range found {
		unnamed.found[rec] = true
	}
}

// marshalsItself is a type encoding/json does not walk.
func marshalsItself(t reflect.Type) bool {
	return t.Implements(jsonMarshaler) || t.Implements(textMarshaler) ||
		(t.Kind() != reflect.Pointer && (reflect.PointerTo(t).Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(textMarshaler)))
}

// walkAnswer walks v by value. zeros holds the types being walked as a zero
// value — the element of a nil pointer or an empty slice — so a type that
// contains itself is walked once and not forever.
func walkAnswer(v reflect.Value, path string, zeros map[reflect.Type]bool, record func(string)) {
	if !v.IsValid() {
		return
	}
	t := v.Type()
	if marshalsItself(t) {
		return
	}
	//exhaustive:ignore every other kind is a scalar, which names no member.
	switch t.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			walkAnswer(v.Elem(), path, zeros, record)
		}
	case reflect.Pointer:
		if !v.IsNil() {
			walkAnswer(v.Elem(), path, zeros, record)
			return
		}
		walkZero(t.Elem(), path, zeros, record)
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return // []byte renders as a string
		}
		if v.Len() > 0 {
			walkAnswer(v.Index(0), path+"[]", zeros, record)
			return
		}
		walkZero(t.Elem(), path+"[]", zeros, record)
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			walkAnswer(iter.Value(), joinPath(path, fmt.Sprint(iter.Key().Interface())), zeros, record)
		}
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, ok := memberName(f)
			if name == "-" {
				continue
			}
			at := path
			if !f.Anonymous {
				at = joinPath(path, name)
				if !ok {
					record(fmt.Sprintf("%s.%s at %s", t.String(), f.Name, at))
				}
			}
			walkAnswer(v.Field(i), at, zeros, record)
		}
	}
}

// walkZero walks a zero value of t, when t is (or leads to) a struct, once
// per type on the path.
func walkZero(t reflect.Type, path string, zeros map[reflect.Type]bool, record func(string)) {
	base := t
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct || zeros[base] {
		return
	}
	zeros[base] = true
	defer delete(zeros, base)
	walkAnswer(reflect.Zero(t), path, zeros, record)
}

// memberName is the key encoding/json gives f, and whether it is a name an
// answer may carry: a `json` tag naming a snake_case key, or `-`.
func memberName(f reflect.StructField) (string, bool) {
	tag, has := f.Tag.Lookup("json")
	name, _, _ := strings.Cut(tag, ",")
	if !has || name == "" {
		return f.Name, false
	}
	if name == "-" {
		return name, true
	}
	return name, snakeCase.MatchString(name)
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
