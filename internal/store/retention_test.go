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
		_, _, err := st.Audit().RetainPrefix(ctx, tenantA, stream, 2, "x")
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
