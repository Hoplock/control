// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"sort"
	"strings"
)

// The initial-auth password never reaches this server, and must never be
// written even if a malformed record contains one (PLAN §7).
//
// THIS IS A CONTROL ON THIS SIDE, NOT A RESTATEMENT OF THE PROXY'S PROMISE.
// The proxy's redaction is structural — no capture point is ever handed the
// password — and that is the right design and not a reason to trust the wire.
// A record reaching this function arrived over a network from a component this
// server does not build, and "they promised" is not something an auditor can
// be shown. So a password-shaped field is removed here, before anything is
// hashed or written, and what is stored is the redaction rather than the
// value.
//
// REDACTION RATHER THAN REJECTION, and the choice matters. A record carrying a
// password is a record of something that happened, and refusing it would
// delete the evidence of the bug that produced it — the one case where an
// operator most needs the record. So the record is kept, the value is replaced
// with [RedactedValue], and the key is listed in the record's own `redacted`
// field so the removal is itself an audit fact. A silent removal would be a
// record that lies by omission, which is the failure this whole package exists
// to prevent.

// RedactedValue replaces a redacted attribute's value. It is a fixed string
// rather than a hash or a length: both of those are information about a
// credential, and a store that keeps "the password was 11 characters" has kept
// part of the password.
const RedactedValue = "[redacted]"

// redactedKeys are the attribute keys whose values are removed.
//
// It is an exact-match set rather than a substring scan over every value,
// which is deliberate: a scan that decides what looks like a password would
// redact a command an operator needs to read, and a filter that mangles real
// evidence to protect against a value that should never arrive is a worse
// trade than a named list. The list is what the contract and the proxy's
// vocabulary can produce; anything outside it is a contract violation and is
// caught by [redactedPrefixes] below.
var redactedKeys = map[string]struct{}{
	"password":         {},
	"passwd":           {},
	"pass":             {},
	"secret":           {},
	"credential":       {},
	"credentials":      {},
	"token":            {},
	"api_key":          {},
	"apikey":           {},
	"private_key":      {},
	"passphrase":       {},
	"auth_password":    {},
	"initial_password": {},
	"target_password":  {},
}

// redactedPrefixes catch the namespaced forms — `device_field.password`, a
// driver's `credential.secret` — without turning the check into a substring
// scan over every key. A device field is opaque data and is stored as it
// arrives (proxy D13), with exactly this exception: NONE OF THEM IS EVER
// CREDENTIAL MATERIAL, and a driver that put one there has produced a record
// this server must not keep intact.
var redactedPrefixes = []string{
	"password",
	"secret",
	"credential_secret",
	"private_key",
	"passphrase",
}

// Redact removes password-shaped attribute values in place and returns the
// keys it removed, sorted. The returned list is stored on the record.
func Redact(r *Record) []string {
	var removed []string
	for key, value := range r.Attributes {
		if value == "" || !isSecretKey(key) {
			continue
		}
		r.Attributes[key] = RedactedValue
		removed = append(removed, key)
	}
	if len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)
	return removed
}

// isSecretKey reports whether an attribute key names a credential.
//
// The last segment of a namespaced key is what is matched, so
// `device_field.password` and `grant_additional_context.api_key` are caught by
// the same list that catches a bare `password`.
func isSecretKey(key string) bool {
	last := key
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		last = key[i+1:]
	}
	lowered := strings.ToLower(last)
	if _, ok := redactedKeys[lowered]; ok {
		return true
	}
	for _, p := range redactedPrefixes {
		if strings.HasPrefix(lowered, p) {
			return true
		}
	}
	return false
}
