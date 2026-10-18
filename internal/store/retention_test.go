// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// A retention pass takes the stream's APPEND LOCK (M8, 0021): while an append
// to the stream is open the pass waits, so the prefix never moves under a
// record being chained onto the stream. The append here is held open inside a
// transaction, which is exactly what a slow ingest is.
func TestARetentionPassWaitsForTheStreamsAppendLock(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	const stream = "proxy:p-lock"
	for seq := int64(1); seq <= 3; seq++ {
		if err := st.Audit().Append(ctx, tenantA, storetest.AuditRecord(fmt.Sprintf("r%d", seq), stream, seq)); err != nil {
			t.Fatal(err)
		}
	}

	held, release, appended := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		appended <- st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
			if _, err := tx.Audit().AppendChain(ctx, tenantA, stream, []string{"r4"},
				func(head store.AuditRecord, _ []string) ([]store.AuditRecord, error) {
					return []store.AuditRecord{storetest.AuditRecord("r4", stream, head.ChainSeq+1)}, nil
				}); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-appended:
		t.Fatalf("the append did not hold its lock: %v", err)
	}

	retained := make(chan error, 1)
	go func() {
		_, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, testSeal)
		retained <- err
	}()
	select {
	case err := <-retained:
		close(release)
		t.Fatalf("a retention pass ran while an append held the stream's lock (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-appended; err != nil {
		t.Fatal(err)
	}
	if err := <-retained; err != nil {
		t.Fatalf("the pass, once the append committed: %v", err)
	}
	head, err := st.Audit().ChainHead(ctx, tenantA, stream)
	if err != nil || head.ChainSeq != 4 {
		t.Errorf("head after both = %d, %v; want the appended record at 4", head.ChainSeq, err)
	}
}

// testSeal stands in for internal/audit's signature, which the store only
// carries: what a seal is worth is that the database cannot make one, and that
// is audit's to test.
var testSeal = store.RetainPrefixOptions{
	By: "x",
	Seal: func(context.Context, int64, string) (store.RetentionSeal, error) {
		return store.RetentionSeal{KeyName: "audit-retention:test", Signature: "c2ln"}, nil
	},
}

// The store writes no marker and no watermark without a seal, and a pass that
// carries one writes it beside the marker.
func TestTheStoreWritesNoRetentionWithoutASeal(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	const stream = "proxy:p-seal"
	for seq := int64(1); seq <= 4; seq++ {
		rec := storetest.AuditRecord(fmt.Sprintf("r%d", seq), stream, seq)
		rec.Hash = fmt.Sprintf("sha256:%02d", seq)
		if err := st.Audit().Append(ctx, tenantA, rec); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, store.RetainPrefixOptions{By: "x"}); !store.IsInvalid(err) {
		t.Errorf("a pass with no seal answered %v", err)
	}
	unsigned := store.RetainPrefixOptions{By: "x", Seal: func(context.Context, int64, string) (store.RetentionSeal, error) {
		return store.RetentionSeal{}, nil
	}}
	if _, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, unsigned); !store.IsInvalid(err) {
		t.Errorf("a pass whose seal is empty answered %v", err)
	}
	if _, err := st.Audit().RetainCaptures(ctx, tenantA, time.Now(), store.RetainCapturesOptions{By: "x"}); !store.IsInvalid(err) {
		t.Errorf("a capture pass with no seal answered %v", err)
	}
	if chain, _ := st.Audit().Chain(ctx, tenantA, stream, 0, 10); len(chain) != 4 {
		t.Fatalf("%d of 4 records left after refused passes", len(chain))
	}

	marker, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, testSeal)
	if err != nil {
		t.Fatal(err)
	}
	if marker.KeyName != "audit-retention:test" || marker.Signature != "c2ln" {
		t.Errorf("the marker carries %q/%q, not the pass's seal", marker.KeyName, marker.Signature)
	}
}
