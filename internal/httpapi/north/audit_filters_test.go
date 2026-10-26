// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"reflect"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
)

// An audit list's cursor binds the whole selection its reader applies (0036,
// M26.2), so a cursor minted under one selection is refused under another
// rather than continuing a list it was not a position in. Every field of the
// query but its Limit is a filter, so each one, set alone, moves the digest,
// and no two of them move it the same way. A field added to the query and not
// to auditFilters fails here.
func TestEverySelectionFieldBindsTheAuditCursor(t *testing.T) {
	digest := func(q store.AuditQuery) string { return filterDigest(auditFilters(q)) }
	base := digest(store.AuditQuery{})

	typ := reflect.TypeFor[store.AuditQuery]()
	seen := map[string]string{}
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Name == "Limit" {
			continue // Query's alone; a page's limit is not a filter
		}
		var q store.AuditQuery
		if field.Name == "DeviceFieldValue" {
			// The value is read only beside a name.
			q.DeviceFieldName = "vdom"
		}
		before := digest(q)
		v := reflect.ValueOf(&q).Elem().Field(i)
		switch v.Interface().(type) {
		case string:
			v.SetString("x")
		case bool:
			v.SetBool(true)
		case []string:
			v.Set(reflect.ValueOf([]string{"x"}))
		case *string:
			empty := "" // a value, not an absence: an administrator's object kind
			v.Set(reflect.ValueOf(&empty))
		case time.Time:
			v.Set(reflect.ValueOf(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)))
		default:
			t.Fatalf("AuditQuery.%s is a %s, which this test does not know how to set", field.Name, field.Type)
		}
		after := digest(q)
		if after == before {
			t.Errorf("AuditQuery.%s does not bind the cursor: auditFilters does not render it", field.Name)
			continue
		}
		if other, ok := seen[after]; ok {
			t.Errorf("AuditQuery.%s and .%s bind the same filter", field.Name, other)
		}
		seen[after] = field.Name
	}
	if _, ok := seen[base]; ok {
		t.Error("a field set alone binds what the empty query binds")
	}

	// One selection spelt two ways binds once: repeated values sorted and
	// counted once, an instant in any zone.
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	same := []struct{ a, b store.AuditQuery }{
		{store.AuditQuery{Kinds: []string{"command", "auth", "command"}}, store.AuditQuery{Kinds: []string{"auth", "command"}}},
		{store.AuditQuery{Severities: []string{"warn", "info"}}, store.AuditQuery{Severities: []string{"info", "warn"}}},
		{store.AuditQuery{From: at.In(time.FixedZone("x", 3600))}, store.AuditQuery{From: at}},
	}
	for _, c := range same {
		if digest(c.a) != digest(c.b) {
			t.Errorf("%+v and %+v select the same records and bind different cursors", c.a, c.b)
		}
	}
	if digest(store.AuditQuery{Kinds: []string{"a", "b"}}) == digest(store.AuditQuery{Kinds: []string{"a"}}) {
		t.Error("two kinds bind what one does")
	}
}

// The showcase join's cursor binds the whole of its query the same way
// (0037): each field, set alone, moves the digest, and no two move it alike.
// The label's value binds even when empty, because an empty value is one the
// join matches.
func TestEveryShowcaseFieldBindsItsCursor(t *testing.T) {
	digest := func(q store.BlockedCommandQuery) string { return filterDigest(blockedFilters(q)) }
	base := store.BlockedCommandQuery{LabelName: "env"}

	typ := reflect.TypeFor[store.BlockedCommandQuery]()
	seen := map[string]string{digest(base): "the base"}
	for i := range typ.NumField() {
		field := typ.Field(i)
		q := base
		v := reflect.ValueOf(&q).Elem().Field(i)
		switch v.Interface().(type) {
		case string:
			v.SetString("x")
		case bool:
			v.SetBool(true)
		case time.Time:
			v.Set(reflect.ValueOf(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)))
		default:
			t.Fatalf("BlockedCommandQuery.%s is a %s, which this test does not know how to set", field.Name, field.Type)
		}
		after := digest(q)
		if other, ok := seen[after]; ok {
			t.Errorf("BlockedCommandQuery.%s binds what %s binds: blockedFilters does not render it apart", field.Name, other)
		}
		seen[after] = "BlockedCommandQuery." + field.Name
	}

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if digest(store.BlockedCommandQuery{LabelName: "env", From: at.In(time.FixedZone("x", 3600))}) !=
		digest(store.BlockedCommandQuery{LabelName: "env", From: at}) {
		t.Error("one instant in two zones binds two cursors")
	}
	if digest(store.BlockedCommandQuery{LabelName: "env", From: at}) == digest(store.BlockedCommandQuery{LabelName: "env", To: at}) {
		t.Error("a window's start binds what its end does")
	}
}
