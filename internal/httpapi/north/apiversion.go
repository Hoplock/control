// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"encoding"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// The north-bound API version (0046, PLAN M19).
//
// Until 0046 this surface shipped in lockstep with its only clients. A
// supervisor consuming many deployments meets version skew, so the surface
// carries an explicit version — distinct from the software version (M23) and
// from the contract's (M1) — negotiated on `policy_version`'s principle (§4):
// the client names the version it can read, and the server never answers
// outside it.
//
//   - A client declares `Hoplock-Api-Version: N`. A client that declares
//     nothing is served the current version: that is every client shipped
//     with the server (the console, policyctl, a host's own), and a client
//     shipped apart from it declares. Every answer names the version it was
//     served in, in the same header.
//   - A declared version this server does not answer in — older than
//     MinAPIVersion, newer than APIVersion, or not a number — is refused `400
//     api_version_unsupported`, naming the range. It is never served a guess.
//   - A member added after version N carries `since:"M"` (M > N) on its view.
//     Served to a client declaring N, it is left out when it holds its
//     documented absent value (Go's zero value: that is the definition of
//     "absent" on this surface), and when it holds anything else the server
//     cannot express the answer within N and says so — `500
//     api_version_unexpressible` with a correlation id, never a thinned body
//     and never a `401` (M11). That is the south-bound rule (§4): a
//     restriction an old reader cannot see is not silently dropped.
//
// WHAT MOVES THE VERSION. Any change a client of the previous version could
// observe: a new member (tagged `since`), a new route, a new code, a new value
// of a closed set. An ADDITIVE change is one a client of N is served exactly
// as before — a member with a documented absent value, left out for it while it
// holds that value. A CHANGED MEANING is not additive whatever it does to the
// JSON: it moves the version AND MinAPIVersion, after the deprecation window
// docs/API-VERSIONING.md states. The bounded-list shape (M26) — the limit,
// the cursor and how an answer says it stopped short — is part of what is
// promised, list by list. A key of an audit record's `attributes` is NOT a
// member: that map is open, its keys are its writer's (the proxy's under the
// contract, or this server's for its own records), a reader skips one it does
// not know, and a new key moves nothing.
//
// The history is docs/API-VERSIONING.md's: 1 is the surface as 0046 left it,
// and 2 adds the Hoplock-Acting-Operator request header (0048, acting.go). A
// new request header is additive, like a new route, and it still moves the
// version, so that a client can tell whether a deployment honours it.
const (
	// APIVersion is the version this server serves by default and the
	// newest it answers in.
	APIVersion = 2
	// MinAPIVersion is the oldest version it still answers in.
	MinAPIVersion = 1
	// APIVersionHeader carries the declared version in, and the served one
	// out.
	APIVersionHeader = "Hoplock-Api-Version"
)

// versionedWriter carries the version a request is served in to writeJSON,
// which the handlers call without the request.
type versionedWriter struct {
	http.ResponseWriter
	version       int
	current       int
	correlationID string
}

func (w *versionedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// withAPIVersion negotiates the version, refusing one outside the range.
func (s *Server) withAPIVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := s.apiVersion
		if raw := strings.TrimSpace(r.Header.Get(APIVersionHeader)); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < s.minAPIVersion || n > s.apiVersion {
				w.Header().Set(APIVersionHeader, strconv.Itoa(s.apiVersion))
				writeError(w, http.StatusBadRequest, Error{
					Code: CodeAPIVersionUnsupported, CorrelationID: CorrelationIDFrom(r.Context()),
					Message:    "this server does not answer in the API version this request declared",
					Parameters: map[string]any{"declared": raw, "min": s.minAPIVersion, "max": s.apiVersion},
				})
				return
			}
			version = n
		}
		w.Header().Set(APIVersionHeader, strconv.Itoa(version))
		next.ServeHTTP(&versionedWriter{ResponseWriter: w, version: version, current: s.apiVersion, correlationID: CorrelationIDFrom(r.Context())}, r)
	})
}

// servedVersion finds the version a writer serves in; ok is false outside
// the chain (a test writing directly), which serves the current version.
func servedVersion(w http.ResponseWriter) (*versionedWriter, bool) {
	for w != nil {
		if v, ok := w.(*versionedWriter); ok {
			return v, true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, false
		}
		w = u.Unwrap()
	}
	return nil, false
}

// unexpressibleError names the member that could not be left out.
type unexpressibleError struct {
	path  string
	since int
}

func (e *unexpressibleError) Error() string {
	return fmt.Sprintf("member %s (since version %d) holds a value with no absent form", e.path, e.since)
}

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
)

// project renders v as version `version` sees it: every member introduced
// after it left out while it holds its absent value, and an error when one
// does not. The result encodes to what encoding/json would have written for v,
// less those members.
func project(v reflect.Value, version int, path string) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	t := v.Type()
	if t.Implements(jsonMarshaler) || t.Implements(textMarshaler) ||
		reflect.PointerTo(t).Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(textMarshaler) {
		return v.Interface(), nil
	}
	//exhaustive:ignore every other kind is a scalar, which encodes as itself.
	switch t.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil, nil
		}
		return project(v.Elem(), version, path)
	case reflect.Slice:
		if v.IsNil() {
			return nil, nil
		}
		if t.Elem().Kind() == reflect.Uint8 {
			return v.Interface(), nil
		}
		fallthrough
	case reflect.Array:
		out := make([]any, v.Len())
		for i := range v.Len() {
			e, err := project(v.Index(i), version, path+"[]")
			if err != nil {
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	case reflect.Map:
		if v.IsNil() {
			return nil, nil
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			e, err := project(iter.Value(), version, path+"."+key)
			if err != nil {
				return nil, err
			}
			out[key] = e
		}
		return out, nil
	case reflect.Struct:
		out := map[string]any{}
		if err := projectStruct(v, version, path, out); err != nil {
			return nil, err
		}
		return out, nil
	}
	return v.Interface(), nil
}

func projectStruct(v reflect.Value, version int, path string, out map[string]any) error {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		fv := v.Field(i)
		// An untagged embedded struct is flattened, exported or not, as
		// encoding/json does.
		if f.Anonymous && name == "" && fv.Kind() == reflect.Struct {
			if err := projectStruct(fv, version, path, out); err != nil {
				return err
			}
			continue
		}
		if !f.IsExported() || (name == "-" && opts == "") {
			continue
		}
		if name == "" {
			name = f.Name
		}
		at := path + "." + name
		if since, ok := f.Tag.Lookup("since"); ok {
			n, err := strconv.Atoi(since)
			if err != nil {
				return fmt.Errorf("member %s: since %q is not a version", at, since)
			}
			if n > version {
				if fv.IsZero() {
					continue
				}
				return &unexpressibleError{path: at, since: n}
			}
		}
		if hasOpt(opts, "omitempty") && emptyValue(fv) {
			continue
		}
		if hasOpt(opts, "omitzero") && fv.IsZero() {
			continue
		}
		e, err := project(fv, version, at)
		if err != nil {
			return err
		}
		if hasOpt(opts, "string") {
			//exhaustive:ignore `,string` applies to scalars alone, as in encoding/json.
			switch fv.Kind() {
			case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
				b, err := json.Marshal(e)
				if err != nil {
					return err
				}
				e = string(b)
			}
		}
		out[name] = e
	}
	return nil
}

func hasOpt(opts, want string) bool {
	for o := range strings.SplitSeq(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

// emptyValue is encoding/json's omitempty test.
func emptyValue(v reflect.Value) bool {
	//exhaustive:ignore the remaining kinds are never empty, as encoding/json reads them.
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}
