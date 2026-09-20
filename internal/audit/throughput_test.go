// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store/storetest"
)

// Ingest throughput, measured against a REAL database and reported with the
// batch size it was measured at.
//
// IT IS A MEASUREMENT AND NOT A BUDGET, which is why it asserts nothing about
// the number. `/v1/authorize` has a latency budget because a proxy holds a
// user's handshake open while it waits (M5); log ingest has no such caller —
// the proxy batches to disk and ships asynchronously, and the priority path
// carries one record. What throughput decides is how far behind a fleet's
// buffers may fall, so the useful thing to publish is the figure and the
// conditions it was taken under, not a threshold that would fail on a laptop
// and pass on a build runner.
//
// The number this run produced is in
// docs/learnings/0010-audit-ingest-and-store-learnings.md, with the hardware.
func TestIngestThroughputIsMeasured(t *testing.T) {
	st := storetest.New(t)
	in := newIngester(t, st)
	ctx := t.Context()

	for _, batchSize := range []int{1, 50, 500} {
		const batches = 20

		// Built up front so the measurement is of ingest rather than of
		// building fixtures.
		prepared := make([][]contract.LogRecord, batches)
		for b := range batches {
			batch := make([]contract.LogRecord, batchSize)
			for i := range batch {
				batch[i] = record(fmt.Sprintf("tp-%d-%d-%d", batchSize, b, i),
					fmt.Sprintf("tp-session-%d-%d", batchSize, b))
			}
			prepared[b] = batch
		}

		start := time.Now()
		var stored int
		for _, batch := range prepared {
			rcpt, err := in.Ingest(ctx, audit.Submission{
				Tenant: tenantA, ProxyID: proxyA, Records: batch,
			})
			if err != nil {
				t.Fatalf("batch size %d: %v", batchSize, err)
			}
			stored += rcpt.Stored
		}
		elapsed := time.Since(start)

		if stored != batches*batchSize {
			t.Fatalf("batch size %d stored %d records, want %d", batchSize, stored, batches*batchSize)
		}
		t.Logf("batch size %4d: %6d records in %8s = %9.0f records/s (%s per batch)",
			batchSize, stored, elapsed.Round(time.Millisecond),
			float64(stored)/elapsed.Seconds(),
			(elapsed / batches).Round(time.Microsecond))
	}
}

// BenchmarkIngest is the same measurement in a form `go test -bench` can track
// over time.
func BenchmarkIngest(b *testing.B) {
	for _, batchSize := range []int{1, 50, 500} {
		b.Run(fmt.Sprintf("batch=%d", batchSize), func(b *testing.B) {
			st := storetest.New(b)
			in, err := audit.New(audit.Options{Store: st})
			if err != nil {
				b.Fatalf("audit.New: %v", err)
			}
			ctx := b.Context()

			b.ResetTimer()
			for n := 0; b.Loop(); n++ {
				batch := make([]contract.LogRecord, batchSize)
				for i := range batch {
					batch[i] = record(fmt.Sprintf("bench-%d-%d-%d", batchSize, n, i), "bench-session")
				}
				b.StartTimer()
				if _, err := in.Ingest(ctx, audit.Submission{
					Tenant: tenantA, ProxyID: proxyA, Records: batch,
				}); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
			}
			b.ReportMetric(float64(batchSize)*float64(b.N)/b.Elapsed().Seconds(), "records/s")
		})
	}
}
