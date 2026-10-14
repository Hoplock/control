// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Configuration on the wire (proxy D18, Hoplock/proxy#65).
//
// THE ONE PLACE this server's `int64` version and bare hex hash become the
// contract's strings, and back. `version` is opaque to the proxy and `hash` is
// an opaque content identifier it only compares for equality (the contract's
// example is `sha256:<hex>`); the event, the fetch, the `ETag` and the report
// all go through the four functions below, so the strings are a rendering and
// never a second numbering.

// configVersionPrefix and configHashPrefix shape the two wire strings.
const (
	configVersionPrefix = "v"
	configHashPrefix    = "sha256:"
)

// WireConfigVersion renders a materialised version for the wire.
func WireConfigVersion(v int64) string { return configVersionPrefix + strconv.FormatInt(v, 10) }

// ParseWireConfigVersion reads a wire version back. A string that is not one of
// this server's versions answers false: the proxy is saying truthfully what it
// runs, so that is drift to report, never a request to refuse.
func ParseWireConfigVersion(s string) (int64, bool) {
	rest, ok := strings.CutPrefix(s, configVersionPrefix)
	if !ok || rest == "" || strings.HasPrefix(rest, "0") || strings.HasPrefix(rest, "+") {
		return 0, false
	}
	v, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// WireConfigHash renders a document's hex digest as the contract's content
// identifier.
func WireConfigHash(hexDigest string) string { return configHashPrefix + hexDigest }

// ParseWireConfigHash reads a content identifier back to the bare digest.
func ParseWireConfigHash(s string) (string, bool) {
	rest, ok := strings.CutPrefix(s, configHashPrefix)
	if !ok || len(rest) != 64 {
		return "", false
	}
	for _, c := range rest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return rest, true
}

// ConfigETag is the fetch's entity tag: the wire hash, quoted.
func ConfigETag(hexDigest string) string { return ETagOfWireHash(WireConfigHash(hexDigest)) }

// ETagOfWireHash quotes a wire hash as an entity tag.
func ETagOfWireHash(wireHash string) string { return `"` + wireHash + `"` }

// IfNoneMatchHolds reports whether an If-None-Match header names the document
// still desired. It is compared AS AN ENTITY TAG (RFC 9110 §13.1.2), so quoting
// is not a mismatch: a quoted or bare tag, a weak one, a list, and `*` are all
// read.
func IfNoneMatchHolds(header, hexDigest string) bool {
	want := WireConfigHash(hexDigest)
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" {
			return true
		}
		tag = strings.TrimPrefix(tag, "W/")
		tag = strings.Trim(tag, `"`)
		if tag == want {
			return true
		}
	}
	return false
}

// THE FLEET-OWNED KEYS (proxy D18).
//
// A setting is fleet-owned only by being listed, and the list is
// `ProxyConfigDocument.settings` in the vendored contract. Everything else is
// bootstrap-only — what the proxy needs to reach or be recognised by Hoplock
// Control, any path to host material, any listener, and
// `control.cache.stale_after` — and the proxy rejects a document naming any
// other key WHOLE. So a publish naming one is refused here, at publish time,
// rather than staged as a rollout that can only ever report `rejected`.
//
// This is the one copy, and TestTheFleetOwnedKeysAreTheContracts ties it to the
// vendored document: the next `make contract-sync` that adds or removes a key
// fails that test rather than slipping past it. The kind beside each key is the
// contract's statement of its shape (durations as Go duration strings, counts as
// integers, addresses as strings); the proxy's own validation remains the
// authority, and a document it rejects anyway arrives as `rejected` on the
// report. Whether a key applies live or at restart is the proxy's business, and
// is not stored here.

// SettingKind is the shape a fleet-owned setting's value must take.
type SettingKind int

const (
	// SettingDuration is a Go duration string (`"30s"`, `"5m"`).
	SettingDuration SettingKind = iota
	// SettingCount is an integer.
	SettingCount
	// SettingAddress is a string.
	SettingAddress
)

// String names the kind for a refusal.
func (k SettingKind) String() string {
	switch k {
	case SettingDuration:
		return "a Go duration string such as \"30s\""
	case SettingCount:
		return "an integer"
	case SettingAddress:
		return "a string"
	}
	return "unknown"
}

// fleetOwned is every fleet-owned key and its shape.
var fleetOwned = map[string]SettingKind{
	"control.cache.max_ttl":           SettingDuration,
	"session.deadline_warning":        SettingDuration,
	"control.cache.max_entries":       SettingCount,
	"dial.dial_timeout":               SettingDuration,
	"dial.default_target_port":        SettingCount,
	"chain.max_hops":                  SettingCount,
	"chain.upstream.address":          SettingAddress,
	"auth.user.mfa.min_poll_interval": SettingDuration,
	"auth.user.mfa.progress_interval": SettingDuration,
	"auth.user.mfa.max_wait":          SettingDuration,
	"logging.batch_size":              SettingCount,
	"logging.flush_interval":          SettingDuration,
	"logging.queue_size":              SettingCount,
	"logging.send_timeout":            SettingDuration,
	"logging.retry_min":               SettingDuration,
	"logging.retry_max":               SettingDuration,
	"logging.max_payload_bytes":       SettingCount,
}

// FleetOwnedKeys returns the fleet-owned keys, sorted.
func FleetOwnedKeys() []string {
	out := make([]string, 0, len(fleetOwned))
	for k := range fleetOwned {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ConfigKeyError is a document naming a setting this server will not publish.
type ConfigKeyError struct {
	// Code is CodeConfigKeyNotFleetOwned or CodeConfigValueInvalid (M21).
	Code string
	Key  string
	// Want names the shape a value must take, for an invalid value.
	Want string
}

// The two refusals a publish can meet before anything is stored.
const (
	CodeConfigKeyNotFleetOwned = "config_key_not_fleet_owned"
	CodeConfigValueInvalid     = "config_value_invalid"
)

func (e *ConfigKeyError) Error() string {
	if e.Code == CodeConfigValueInvalid {
		return fmt.Sprintf("fleet: setting %q must be %s", e.Key, e.Want)
	}
	return fmt.Sprintf("fleet: %q is not a fleet-owned setting; the proxy keeps it in its bootstrap file and rejects a document naming it", e.Key)
}

// ValidateConfigDocument refuses a document the proxy would reject: a key that
// is not fleet-owned — a nested object included, because keys are flat and
// dotted — or a value not of its key's shape. The first refusal in key order is
// returned, so the answer is stable.
func ValidateConfigDocument(doc ConfigDocument) error {
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		kind, ok := fleetOwned[key]
		if !ok {
			return &ConfigKeyError{Code: CodeConfigKeyNotFleetOwned, Key: key}
		}
		if !valueHasShape(doc[key], kind) {
			return &ConfigKeyError{Code: CodeConfigValueInvalid, Key: key, Want: kind.String()}
		}
	}
	return nil
}

func valueHasShape(v any, kind SettingKind) bool {
	switch kind {
	case SettingDuration:
		s, ok := v.(string)
		if !ok {
			return false
		}
		_, err := time.ParseDuration(s)
		return err == nil
	case SettingCount:
		switch n := v.(type) {
		case int, int32, int64:
			return true
		case float64:
			return n == math.Trunc(n) && !math.IsInf(n, 0)
		}
		return false
	case SettingAddress:
		s, ok := v.(string)
		return ok && s != ""
	}
	return false
}
