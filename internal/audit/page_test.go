// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// allOfIt is a page that holds every list these tests read whole.
var allOfIt = store.Page{Limit: store.MaxPageLimit}

// rowsOf is a page's rows, for a test that reads a list in one page.
func rowsOf[T any](p store.Paged[T], err error) ([]T, error) { return p.Rows, err }

// auditPlace is an item's place in the audit order (M26.4), as the item
// carries it.
type auditPlace struct {
	id     string
	at     time.Time
	stream string
	seq    int64
}

func rowPlace(r audit.Row) auditPlace {
	return auditPlace{r.RecordID, r.RecordedAt, r.Stream, r.ChainSeq}
}

func driftPlace(c audit.DriftChange) auditPlace {
	return auditPlace{c.RecordID, c.RecordedAt, c.Stream, c.ChainSeq}
}

func unmetPlace(u audit.UnmetAlgorithmPolicy) auditPlace {
	return auditPlace{u.RecordID, u.RecordedAt, u.Stream, u.ChainSeq}
}

func gapPlace(g audit.Gap) auditPlace {
	return auditPlace{g.RecordID, g.ReportedAt, g.Stream, g.ChainSeq}
}

func placesOf[T any](rows []T, place func(T) auditPlace) []auditPlace {
	out := make([]auditPlace, 0, len(rows))
	for _, row := range rows {
		out = append(out, place(row))
	}
	return out
}

// The five lists the north-bound audit query pages (0036) each read one page
// of the audit query in its order, newest first or oldest first, and carry
// each item's place in it, so a walk continues from the last item it was
// handed. The records are ingested from two proxies within one second, so
// most share an instant in two streams and every page boundary is a tie.
func TestTheFiveAuditListsPageInTheAuditOrder(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	stamp := func(rec contract.LogRecord, when time.Time) contract.LogRecord {
		rec.Timestamp = when.Format(time.RFC3339)
		return rec
	}
	const serial = "9"
	seed := func(proxy, prefix string, when time.Time) {
		var recs []contract.LogRecord
		for i := range 2 {
			n := prefix + strconv.Itoa(i)
			drift := withAttrs(record("d-"+n, "sess-"+prefix), "provisioning", map[string]string{
				audit.AttrEvent: audit.EventDeviceConfigChange, audit.AttrDeviceChangeOp: "create",
				audit.AttrTargetAccount: "hl-" + n,
			})
			unmet := withAttrs(record("u-"+n, "sess-"+prefix), "error", map[string]string{
				audit.AttrEvent: audit.EventAlgorithmPolicyUnmet, audit.AttrAlgorithmAxis: "host_key",
				audit.AttrAlgorithmPolicyCause: "profile", audit.AttrAlgorithmProfile: "default",
			})
			cert := withAttrs(record("c-"+n, "sess-"+prefix), "provisioning", map[string]string{
				audit.AttrCredentialMethod: "brokered-certificate", audit.AttrCredentialRung: "1",
				audit.AttrCertificateSerial: serial,
			})
			recs = append(recs,
				stamp(drift, when), stamp(unmet, when), stamp(cert, when),
				stamp(gap("g-"+n, "sess-"+prefix, "evicted", 1, time.Time{}, time.Time{}, nil), when),
				stamp(record("r-"+n, "sess-"+prefix), when),
			)
		}
		mustIngest(ctx, t, in, tenantA, proxy, recs...)
	}
	seed(proxyA, "a", at)
	seed("proxy-2", "b", at)
	seed(proxyA, "old", at.Add(-time.Hour))

	for _, tc := range []struct {
		name string
		want int // how many items the list holds
		read func(ctx context.Context, ascending bool, p store.Page) ([]auditPlace, bool, error)
	}{
		{"Find", 30, func(ctx context.Context, asc bool, p store.Page) ([]auditPlace, bool, error) {
			got, err := reader.Find(ctx, tenantA, audit.Query{Ascending: asc}, p)
			return placesOf(got.Rows, rowPlace), got.More, err
		}},
		{"DeviceConfigChanges", 6, func(ctx context.Context, asc bool, p store.Page) ([]auditPlace, bool, error) {
			got, err := reader.DeviceConfigChanges(ctx, tenantA, audit.DriftQuery{Ascending: asc}, p)
			return placesOf(got.Rows, driftPlace), got.More, err
		}},
		{"AlgorithmPolicyUnmet", 6, func(ctx context.Context, asc bool, p store.Page) ([]auditPlace, bool, error) {
			got, err := reader.AlgorithmPolicyUnmet(ctx, tenantA, audit.UnmetQuery{Ascending: asc}, p)
			return placesOf(got.Rows, unmetPlace), got.More, err
		}},
		{"Gaps", 6, func(ctx context.Context, asc bool, p store.Page) ([]auditPlace, bool, error) {
			got, err := reader.Gaps(ctx, tenantA, audit.GapQuery{Ascending: asc}, p)
			return placesOf(got.Rows, gapPlace), got.More, err
		}},
		{"CertificateSessions", 6, func(ctx context.Context, asc bool, p store.Page) ([]auditPlace, bool, error) {
			got, err := reader.CertificateSessions(ctx, tenantA, audit.CertificateQuery{Serial: 9, Ascending: asc}, p)
			return placesOf(got.Rows, rowPlace), got.More, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			whole, more, err := tc.read(ctx, false, allOfIt)
			if err != nil || more || len(whole) != tc.want {
				t.Fatalf("the whole list: %d items more=%t (%v), want %d that end", len(whole), more, err, tc.want)
			}
			// Every item's place is its record's, and the list is in the
			// audit order, newest first.
			for _, item := range whole {
				rec, err := reader.Get(ctx, tenantA, item.id)
				if err != nil {
					t.Fatal(err)
				}
				if !rec.RecordedAt.Equal(item.at) || rec.Stream != item.stream || rec.ChainSeq != item.seq {
					t.Errorf("%s carries %v %s %d, its record is at %v %s %d",
						item.id, item.at, item.stream, item.seq, rec.RecordedAt, rec.Stream, rec.ChainSeq)
				}
			}
			newestFirst := slices.IsSortedFunc(whole, func(a, b auditPlace) int {
				return -cmp.Or(a.at.Compare(b.at), cmp.Compare(a.stream, b.stream), cmp.Compare(a.seq, b.seq))
			})
			if !newestFirst {
				t.Errorf("the list is not newest first by recorded-at, stream and chain sequence: %+v", whole)
			}
			if !whole[0].at.Equal(whole[1].at) || whole[0].stream == whole[len(whole)/2].stream {
				t.Fatalf("the seed made no tie across streams: %+v", whole)
			}

			oldestFirst := slices.Clone(whole)
			slices.Reverse(oldestFirst)
			for _, asc := range []bool{false, true} {
				want := whole
				if asc {
					want = oldestFirst
				}
				for _, limit := range []int{1, 4} {
					var seen []auditPlace
					p := store.Page{Limit: limit}
					for range len(want) + 1 {
						got, more, err := tc.read(ctx, asc, p)
						if err != nil {
							t.Fatal(err)
						}
						if more && len(got) != limit {
							t.Fatalf("a page that continues holds %d items, want %d", len(got), limit)
						}
						seen = append(seen, got...)
						if !more {
							break
						}
						last := got[len(got)-1]
						p.After = []any{last.at, last.stream, last.seq}
					}
					if !slices.Equal(seen, want) {
						t.Errorf("ascending=%t, %d a page: the walk saw %v, want %v", asc, limit, ids(seen), ids(want))
					}
				}
			}
		})
	}
}

func ids(places []auditPlace) []string {
	out := make([]string, 0, len(places))
	for _, p := range places {
		out = append(out, p.id)
	}
	return out
}
