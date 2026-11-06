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
	"strconv"
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
//
// EVERY ANSWER IS ALSO SERVED TO AN OLDER CLIENT AS IT WAS (0048). From
// version 2, a client declaring 1 is answered through the reflective
// projection (apiversion.go) rather than encoding/json, so the same walk
// projects every body to every version this server serves and holds the
// projection to what encoding/json writes for it. No view carries a `since`
// member yet, so no answer may differ at any served version, nor fail to be
// expressed in one. The phase that adds the first `since` member revisits
// this check: from then on an older version's projection differs from
// encoding/json by exactly the members it leaves out.

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

// misprojected are the answers whose projection to a served version did not
// encode as encoding/json does, keyed by type and version.
var misprojected = struct {
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
	misprojected.Lock()
	wrong := slices.Sorted(func(yield func(string) bool) {
		for k := range misprojected.found {
			if !yield(k) {
				return
			}
		}
	})
	misprojected.Unlock()
	if len(wrong) > 0 {
		fmt.Fprintf(os.Stderr, "north-bound answers are served differently to an older client (0048):\n  %s\n\n"+
			"A client declaring an older version is answered through project (apiversion.go); make it "+
			"agree with encoding/json for this answer.\n", strings.Join(wrong, "\n  "))
		os.Exit(1)
	}
	os.Exit(code)
}

// probeAnswer records every unnamed member of one body, and every served
// version it is not expressed in exactly as encoding/json writes it.
func probeAnswer(body any) {
	probeProjection(body)
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

// probeProjection projects body to every version this server serves and
// records each one that fails, or does not encode as encoding/json does.
func probeProjection(body any) {
	want, err := canonicalJSON(body)
	if err != nil {
		return // not an answer encoding/json can write; nothing to agree with
	}
	for v := north.MinAPIVersion; v <= north.APIVersion; v++ {
		var problem string
		if p, err := north.Project(body, v); err != nil {
			problem = err.Error()
		} else if got, err := canonicalJSON(p); err != nil || got != want {
			problem = fmt.Sprintf("projected %s, encoding/json %s", got, want)
		}
		if problem != "" {
			misprojected.Lock()
			misprojected.found[fmt.Sprintf("%T at version %d: %s", body, v, problem)] = true
			misprojected.Unlock()
		}
	}
}

// canonicalJSON is v encoded, decoded and encoded again, so two encodings of
// the same value compare equal whatever order their members were written in.
func canonicalJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	out, err := json.Marshal(decoded)
	return string(out), err
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
			// A member introduced after a version names it, and the
			// version it names is one this server serves (0046, M19).
			if since, has := f.Tag.Lookup("since"); has {
				if n, err := strconv.Atoi(since); err != nil || n < 2 || n > north.APIVersion {
					record(fmt.Sprintf("%s.%s at %s: since %q is not a version after the first and at most %d",
						t.String(), f.Name, at, since, north.APIVersion))
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
