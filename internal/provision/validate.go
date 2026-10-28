// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// The bounds a call is held to before anything is read or written.
const (
	// maxIDLen bounds a tenant and an external id, a subject's or a group's.
	maxIDLen = 256
	// maxNameLen bounds a username, a display name and a group name.
	maxNameLen = 256
	// maxEmailLen is the longest address RFC 5321 permits.
	maxEmailLen = 320
	// maxCodeLen bounds a reason code and an attribute code.
	maxCodeLen = 128
	// maxValueLen bounds an attribute's value.
	maxValueLen = 1024
	// MaxAttributes and MaxGroups bound what one subject carries: a person in
	// more groups than this is a directory feeding Control something it was
	// not meant to.
	MaxAttributes = 256
	MaxGroups     = 1024
)

// refusal is a call Control will not apply, and why. It is returned to the
// sync as an *ext.Error and recorded as `identity_sync.refused`.
type refusal struct {
	kind ext.Kind
	// field is the field at fault, for KindInvalid and KindConflict.
	field string
	// conflict is the subject that already presents a username. It goes on
	// the record and never into the error, which leaves for somebody else's
	// software.
	conflict string
	msg      string
}

func invalidField(field, format string, args ...any) *refusal {
	return &refusal{kind: ext.KindInvalid, field: field, msg: fmt.Sprintf(format, args...)}
}

func (r *refusal) message() string {
	if r.field != "" {
		return "field " + r.field + ": " + r.msg
	}
	return r.msg
}

// Error lets a refusal travel out of the transaction that found it.
func (r *refusal) Error() string { return r.message() }

// checkID holds an identifier to present, bounded, one line and unpadded.
func checkID(field, v string) *refusal {
	switch {
	case v == "" || strings.TrimSpace(v) == "":
		return invalidField(field, "it is required")
	case len(v) > maxIDLen:
		return invalidField(field, "it is longer than %d bytes", maxIDLen)
	case !utf8.ValidString(v) || hasControl(v):
		return invalidField(field, "it is not one line of text")
	case v != strings.TrimSpace(v):
		return invalidField(field, "it has leading or trailing whitespace")
	}
	return nil
}

// checkLine holds an optional value to one bounded line of text.
func checkLine(field, v string, limit int) *refusal {
	switch {
	case len(v) > limit:
		return invalidField(field, "it is longer than %d bytes", limit)
	case !utf8.ValidString(v) || hasControl(v):
		return invalidField(field, "it is not one line of text")
	}
	return nil
}

func hasControl(v string) bool { return strings.ContainsFunc(v, unicode.IsControl) }

// clean returns v if it is fit to be written to a record, and "" if it is not.
func clean(v string) string {
	if len(v) > maxIDLen || !utf8.ValidString(v) || hasControl(v) {
		return ""
	}
	return v
}

// instant is a directory time as Control stores and compares it: UTC, to the
// microsecond the database keeps. Compared at a finer grain, a replay of the
// last applied upsert would look later than itself and apply twice.
func instant(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// subjectRequest is an upsert that passed validation.
type subjectRequest struct {
	tenant      store.Tenant
	externalID  string
	username    string
	displayName string
	email       string
	groupIDs    []string
	attributes  map[string]string
	active      bool
	updatedAt   time.Time
}

func validateSubject(in ext.SyncedSubject) (subjectRequest, *refusal) {
	req := subjectRequest{
		tenant: store.Tenant(in.Tenant), externalID: in.ExternalID, username: in.Username,
		displayName: in.DisplayName, email: in.Email, active: in.Active,
	}
	if bad := checkID("tenant", string(in.Tenant)); bad != nil {
		return req, bad
	}
	if bad := checkID("external_id", in.ExternalID); bad != nil {
		return req, bad
	}
	if in.Username != "" {
		// A username is a login name the SSH path matches: padded, it is a
		// name nobody types.
		if bad := checkID("username", in.Username); bad != nil {
			return req, bad
		}
	}
	if bad := checkLine("display_name", in.DisplayName, maxNameLen); bad != nil {
		return req, bad
	}
	if bad := checkLine("email", in.Email, maxEmailLen); bad != nil {
		return req, bad
	}
	if in.UpdatedAt.IsZero() {
		return req, invalidField("updated_at", "it is required: it is what orders one write against another")
	}
	req.updatedAt = instant(in.UpdatedAt)

	if len(in.Groups) > MaxGroups {
		return req, invalidField("groups", "it names more than %d groups", MaxGroups)
	}
	for _, g := range in.Groups {
		if bad := checkID("groups", g); bad != nil {
			return req, bad
		}
	}
	req.groupIDs = slices.Compact(slices.Sorted(slices.Values(in.Groups)))

	if len(in.Attributes) > MaxAttributes {
		return req, invalidField("attributes", "it has more than %d attributes", MaxAttributes)
	}
	req.attributes = map[string]string{}
	for _, code := range slices.Sorted(maps.Keys(in.Attributes)) {
		if bad := checkID("attributes", code); bad != nil || len(code) > maxCodeLen {
			return req, invalidField("attributes", "an attribute code is not one line of at most %d bytes", maxCodeLen)
		}
		if bad := checkLine("attributes", in.Attributes[code], maxValueLen); bad != nil {
			return req, invalidField("attributes", "the value of %q is not one line of at most %d bytes", code, maxValueLen)
		}
		if identity.ReservedName(code) {
			// A name this server sets for itself is never stored, not even
			// in the directory's own copy: the mapping never sees it.
			continue
		}
		req.attributes[code] = in.Attributes[code]
	}
	return req, nil
}

// groupRequest is a group upsert that passed validation.
type groupRequest struct {
	tenant     store.Tenant
	externalID string
	name       string
	updatedAt  time.Time
}

func validateGroup(in ext.SyncedGroup) (groupRequest, *refusal) {
	req := groupRequest{tenant: store.Tenant(in.Tenant), externalID: in.ExternalID, name: in.Name}
	if bad := checkID("tenant", string(in.Tenant)); bad != nil {
		return req, bad
	}
	if bad := checkID("external_id", in.ExternalID); bad != nil {
		return req, bad
	}
	if strings.TrimSpace(in.Name) == "" {
		return req, invalidField("name", "it is required")
	}
	if bad := checkLine("name", in.Name, maxNameLen); bad != nil {
		return req, bad
	}
	if in.UpdatedAt.IsZero() {
		return req, invalidField("updated_at", "it is required: it is what orders one write against another")
	}
	req.updatedAt = instant(in.UpdatedAt)
	return req, nil
}

// validateRef holds a call that names a tenant and an id, and an optional
// reason code, to what it must be.
func validateRef(tenant ext.Tenant, externalID, reasonCode string) *refusal {
	if bad := checkID("tenant", string(tenant)); bad != nil {
		return bad
	}
	if bad := checkID("external_id", externalID); bad != nil {
		return bad
	}
	return checkLine("reason_code", reasonCode, maxCodeLen)
}
