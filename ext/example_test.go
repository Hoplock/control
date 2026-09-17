// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext_test

import (
	"context"
	"fmt"
	"slices"

	"github.com/hoplock/control/ext"
)

// queueSink is an out-of-tree AuditSink: the whole of what a third party has
// to write to export Hoplock Control's audit records somewhere Control has
// never heard of. It lives outside this module in real life; nothing about it
// depends on being inside it.
type queueSink struct {
	topic string
}

// Export delivers one batch. It is idempotent on RecordID, because Control
// retries a batch it could not confirm and a sink that doubles on retry
// produces an audit trail that double-counts.
func (s *queueSink) Export(ctx context.Context, batch []ext.AuditRecord) error {
	for _, rec := range batch {
		if err := ctx.Err(); err != nil {
			// Control's deadline for this attempt has passed. Report it
			// as unavailable rather than as a bare error: an
			// unclassified failure is an outage (M11), and this one has
			// a name.
			return ext.Errorf(ext.PointAuditSink, "example.com/queue-sink", "AuditSink.Export",
				ext.KindUnavailable, "deadline reached after %d records: %v", rec.Sequence, err)
		}
		_ = rec // publish to s.topic, keyed by rec.RecordID
	}
	return nil
}

// Example shows an out-of-tree implementation registering itself. A host
// binary — Hoplock Enterprise's, or anybody's — builds a registry, registers
// what it brings, and hands it to Control, which adds its own defaults and
// seals it before anything starts.
func Example() {
	registry := ext.NewRegistry()

	me := ext.Registration{Provider: "example.com/queue-sink", Version: "1.0.0"}
	if err := registry.RegisterAuditSink(me, &queueSink{topic: "hoplock.audit"}); err != nil {
		fmt.Println("registration failed:", err)
		return
	}

	// Registering the same provider twice at one point is an error naming
	// both sides, rather than a silent last-wins that would let two modules
	// of one product disagree invisibly.
	if err := registry.RegisterAuditSink(me, &queueSink{topic: "other"}); err != nil {
		fmt.Println("second registration refused")
	}

	// A host binary can log its own wiring before handing the registry over.
	// Implementations are not readable from a Registry at all: that is what
	// makes a half-registered extension unreachable rather than merely
	// discouraged.
	shown := []ext.Point{ext.PointAuditSink, ext.PointArchiveStore}
	for _, st := range registry.Status() {
		if slices.Contains(shown, st.Info.Point) {
			fmt.Println(st)
		}
	}

	// Output:
	// second registration refused
	// AuditSink: example.com/queue-sink@1.0.0
	// ArchiveStore: none registered (disabled) — retention deletes records when their window closes and there is no long-term copy
}
