// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// Bounded lists (M26): every reader that pages, held to the same promises.
// A reader added by a later chunk joins pagedReaders and inherits them all.

// pagedReader is one list the store pages, reduced to what these tests need:
// put an item, delete it, and read a page of ids.
type pagedReader struct {
	put  func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error
	del  func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error
	page func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error)
}

var pagedReaders = map[string]pagedReader{
	"Targets.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.Targets().Upsert(ctx, tenant, storetest.Target(id, id+".example", map[string]string{"env": "prod"}))
		},
		del: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.Targets().Delete(ctx, tenant, id)
		},
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.Targets().Page(ctx, tenant, nil, p)
			ids := make([]string, 0, len(got.Rows))
			for _, t := range got.Rows {
				ids = append(ids, t.ID)
			}
			return ids, got.More, err
		},
	},
	"Subjects.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.Subjects().Upsert(ctx, tenant, storetest.Subject(id))
		},
		del: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.Subjects().Delete(ctx, tenant, id)
		},
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.Subjects().Page(ctx, tenant, p)
			ids := make([]string, 0, len(got.Rows))
			for _, s := range got.Rows {
				ids = append(ids, s.ID)
			}
			return ids, got.More, err
		},
	},
	"Proxies.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.Proxies().Upsert(ctx, tenant, storetest.Proxy(id, "zone-a"))
		},
		del: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.Proxies().Delete(ctx, tenant, id)
		},
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.Proxies().Page(ctx, tenant, p)
			ids := make([]string, 0, len(got.Rows))
			for _, px := range got.Rows {
				ids = append(ids, px.ID)
			}
			return ids, got.More, err
		},
	},
	// The rollout view, whole and drifted. Every state put here is drifted:
	// published, and never reported.
	"ProxyConfigs.PageStates": {
		put: putDriftedState,
		del: store.DeleteProxyConfigState,
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			return stateIDs(st.ProxyConfigs().PageStates(ctx, tenant, false, p))
		},
	},
	"ProxyConfigs.PageStates drifted": {
		put: putDriftedState,
		del: store.DeleteProxyConfigState,
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			return stateIDs(st.ProxyConfigs().PageStates(ctx, tenant, true, p))
		},
	},
}

func putDriftedState(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
	return st.ProxyConfigs().PutState(ctx, tenant, store.ProxyConfigState{
		ProxyID: id, DesiredVersion: 1, DesiredHash: "h1", DesiredDocument: json.RawMessage(`{}`),
	})
}

func stateIDs(got store.Paged[store.ProxyConfigState], err error) ([]string, bool, error) {
	ids := make([]string, 0, len(got.Rows))
	for _, s := range got.Rows {
		ids = append(ids, s.ProxyID)
	}
	return ids, got.More, err
}

func putAll(t *testing.T, st *store.Store, r pagedReader, tenant store.Tenant, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := r.put(t.Context(), st, tenant, id); err != nil {
			t.Fatalf("put %s in %s: %v", id, tenant, err)
		}
	}
}

// walk reads a list to its end, limit items at a time, calling between after
// each page with the position the next one starts from.
func walk(t *testing.T, st *store.Store, r pagedReader, tenant store.Tenant, limit int, between func(after string)) []string {
	t.Helper()
	var seen []string
	p := store.Page{Limit: limit}
	for range store.MaxPageLimit {
		ids, more, err := r.page(t.Context(), st, tenant, p)
		if err != nil {
			t.Fatalf("page after %v: %v", p.After, err)
		}
		if more && len(ids) != limit {
			t.Fatalf("a page that continues holds %d items, want exactly %d", len(ids), limit)
		}
		seen = append(seen, ids...)
		if !more {
			return seen
		}
		last := ids[len(ids)-1]
		if between != nil {
			between(last)
		}
		p.After = []any{last}
	}
	t.Fatal("the walk never ended")
	return nil
}

func TestAPageHoldsItsLimitAndSaysWhetherTheListContinues(t *testing.T) {
	t.Parallel()
	for name, r := range pagedReaders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := storetest.New(t)
			ctx := t.Context()
			putAll(t, st, r, tenantA, "i-3", "i-1", "i-5", "i-2", "i-4")

			cases := []struct {
				limit int
				after []any
				want  []string
				more  bool
			}{
				{limit: 2, want: []string{"i-1", "i-2"}, more: true},
				{limit: 4, want: []string{"i-1", "i-2", "i-3", "i-4"}, more: true},
				{limit: 5, want: []string{"i-1", "i-2", "i-3", "i-4", "i-5"}, more: false},
				{limit: 10, want: []string{"i-1", "i-2", "i-3", "i-4", "i-5"}, more: false},
				{limit: 2, after: []any{"i-2"}, want: []string{"i-3", "i-4"}, more: true},
				{limit: 2, after: []any{"i-3"}, want: []string{"i-4", "i-5"}, more: false},
				// A position need not be an item: a deleted one continues
				// from where it was.
				{limit: 10, after: []any{"i-25"}, want: []string{"i-3", "i-4", "i-5"}, more: false},
				{limit: 10, after: []any{"i-5"}, want: []string{}, more: false},
			}
			for _, c := range cases {
				ids, more, err := r.page(ctx, st, tenantA, store.Page{Limit: c.limit, After: c.after})
				if err != nil {
					t.Fatalf("limit %d after %v: %v", c.limit, c.after, err)
				}
				if !slices.Equal(ids, c.want) || more != c.more {
					t.Errorf("limit %d after %v: %v more=%t, want %v more=%t", c.limit, c.after, ids, more, c.want, c.more)
				}
			}

			// An empty tenant is an empty page that ends, not an error.
			ids, more, err := r.page(ctx, st, "tenant-empty", store.Page{Limit: store.DefaultPageLimit})
			if err != nil || len(ids) != 0 || more {
				t.Errorf("an empty tenant answered %v more=%t, %v; want an empty page that ends", ids, more, err)
			}
		})
	}
}

// A reader handed a page the handler should have refused answers KindInvalid
// and never clamps (M26.1).
func TestAPageOutsideItsBoundsIsInvalid(t *testing.T) {
	t.Parallel()
	for name, r := range pagedReaders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := storetest.New(t)
			for _, p := range []store.Page{
				{Limit: 0},
				{Limit: -1},
				{Limit: store.MaxPageLimit + 1},
				{Limit: 1, After: []any{"a", "b"}},
			} {
				if _, _, err := r.page(t.Context(), st, tenantA, p); !store.IsInvalid(err) {
					t.Errorf("page %+v answered %v, want KindInvalid", p, err)
				}
			}
			if _, _, err := r.page(t.Context(), st, tenantA, store.Page{Limit: store.MaxPageLimit}); err != nil {
				t.Errorf("the maximum limit was refused: %v", err)
			}
		})
	}
}

// A position is not a scope (M18): one taken from tenant A's list continues
// tenant B's from the same place and reads only B's rows.
func TestAPositionFromOneTenantReadsOnlyAnothersRows(t *testing.T) {
	t.Parallel()
	for name, r := range pagedReaders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := storetest.New(t)
			putAll(t, st, r, tenantA, "a-1", "a-2", "m-a", "z-a")
			putAll(t, st, r, tenantB, "b-1", "m-b", "z-b")

			ids, _, err := r.page(t.Context(), st, tenantA, store.Page{Limit: 2})
			if err != nil {
				t.Fatalf("tenant A: %v", err)
			}
			got, more, err := r.page(t.Context(), st, tenantB, store.Page{Limit: 10, After: []any{ids[len(ids)-1]}})
			if err != nil {
				t.Fatalf("tenant B after A's position: %v", err)
			}
			if want := []string{"b-1", "m-b", "z-b"}; !slices.Equal(got, want) || more {
				t.Errorf("tenant B after %q answered %v more=%t, want %v: only B's rows, from that position",
					ids[len(ids)-1], got, more, want)
			}
		})
	}
}

// A keyset is stable under writes (M26.2): an item that exists for the whole
// walk is seen exactly once, an item added behind the position is not seen, one
// added ahead of it is, and the item the position names may be gone.
func TestAWalkIsStableUnderInsertsAndDeletes(t *testing.T) {
	t.Parallel()
	for name, r := range pagedReaders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := storetest.New(t)
			ctx := t.Context()
			stable := []string{"k-01", "k-02", "k-03", "k-04", "k-05", "k-06", "k-07", "k-08"}
			putAll(t, st, r, tenantA, stable...)
			putAll(t, st, r, tenantA, "k-035") // deleted before the walk reaches it

			pages := 0
			seen := walk(t, st, r, tenantA, 2, func(after string) {
				pages++
				if pages != 1 {
					return
				}
				// After the first page (k-01, k-02): one behind, one ahead,
				// one deleted ahead, and the item the position names gone.
				putAll(t, st, r, tenantA, "k-00", "k-055")
				for _, id := range []string{"k-035", after} {
					if err := r.del(ctx, st, tenantA, id); err != nil {
						t.Fatalf("delete %s: %v", id, err)
					}
				}
			})
			want := []string{"k-01", "k-02", "k-03", "k-04", "k-05", "k-055", "k-06", "k-07", "k-08"}
			if !slices.Equal(seen, want) {
				t.Errorf("the walk saw %v, want %v", seen, want)
			}
		})
	}
}

// The same promise with the writes racing the walk rather than placed between
// its pages: every item that exists throughout is seen exactly once, and the
// walk never goes backwards.
func TestAWalkIsStableUnderConcurrentWrites(t *testing.T) {
	t.Parallel()
	for name, r := range pagedReaders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st := storetest.New(t)
			ctx := t.Context()
			var stable []string
			for i := range 60 {
				stable = append(stable, fmt.Sprintf("k-%03d-a", i))
			}
			putAll(t, st, r, tenantA, stable...)

			stop := make(chan struct{})
			var wg sync.WaitGroup
			var churnErr error
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					// Interleaved with the stable items, so every page's
					// neighbourhood is written while it is read.
					id := fmt.Sprintf("k-%03d-b", (i*7)%60)
					if err := r.put(ctx, st, tenantA, id); err != nil {
						churnErr = err
						return
					}
					if i%2 == 0 {
						if err := r.del(ctx, st, tenantA, id); err != nil {
							churnErr = err
							return
						}
					}
				}
			}()
			seen := walk(t, st, r, tenantA, 7, nil)
			close(stop)
			wg.Wait()
			if churnErr != nil {
				t.Fatalf("the concurrent writer failed: %v", churnErr)
			}

			if !slices.IsSorted(seen) || len(slices.Compact(slices.Clone(seen))) != len(seen) {
				t.Errorf("the walk repeated an item or went backwards: %v", seen)
			}
			for _, id := range stable {
				if n := countOf(seen, id); n != 1 {
					t.Errorf("%s existed for the whole walk and was seen %d times", id, n)
				}
			}
		})
	}
}

func countOf(ids []string, id string) int {
	n := 0
	for _, v := range ids {
		if v == id {
			n++
		}
	}
	return n
}

// A failure while a page is read is an error, never the rows read before it
// answered as though the list had ended (M26.6, M11): whether Postgres refuses
// the statement or a row fails to scan after the rows before it were read.
func TestAFailureDuringAPageIsNeverAShortPage(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	putAll(t, st, pagedReaders["Targets.Page"], tenantA, "f-1", "f-2", "f-3", "f-4")

	for name, inStatement := range map[string]bool{"in the statement": true, "in the scan": false} {
		got, err := store.TargetIDPageFailingAt(t.Context(), st, tenantA, store.Page{Limit: 10}, "f-3", inStatement)
		if err == nil {
			t.Errorf("%s: a page that failed at its third row answered %v with no error", name, got.Rows)
			continue
		}
		if got.Rows != nil || got.More {
			t.Errorf("%s: a failed page answered rows %v more=%t beside its error, want nothing", name, got.Rows, got.More)
		}
		if store.IsNotFound(err) || store.IsInvalid(err) {
			t.Errorf("%s: a failed page is %v; it is an outage, not absence or a bad argument", name, err)
		}
	}

	// The same statement with nothing to fail on is a whole page, so the
	// failures above are the ones the test placed.
	if got, err := store.TargetIDPageFailingAt(t.Context(), st, tenantA, store.Page{Limit: 10}, "none", true); err != nil || len(got.Rows) != 4 {
		t.Fatalf("the statement without its failure answered %v, %v; want four rows", got.Rows, err)
	}
}

// Targets.Page filters as ListByLabels does, and the position continues the
// filtered list.
func TestTargetPagesAreFilteredByLabels(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	for _, tg := range []store.Target{
		storetest.Target("t-1", "t-1.example", map[string]string{"env": "prod", "role": "db"}),
		storetest.Target("t-2", "t-2.example", map[string]string{"env": "dev"}),
		storetest.Target("t-3", "t-3.example", map[string]string{"env": "prod"}),
		storetest.Target("t-4", "t-4.example", map[string]string{"env": "prod", "role": "db"}),
	} {
		if err := st.Targets().Upsert(ctx, tenantA, tg); err != nil {
			t.Fatalf("upsert %s: %v", tg.ID, err)
		}
	}
	first, err := st.Targets().Page(ctx, tenantA, map[string]string{"env": "prod"}, store.Page{Limit: 2})
	if err != nil || len(first.Rows) != 2 || first.Rows[1].ID != "t-3" || !first.More {
		t.Fatalf("env=prod, first page: %+v, %v; want t-1, t-3 and more", first, err)
	}
	rest, err := st.Targets().Page(ctx, tenantA, map[string]string{"env": "prod"}, store.Page{Limit: 2, After: []any{"t-3"}})
	if err != nil || len(rest.Rows) != 1 || rest.Rows[0].ID != "t-4" || rest.More {
		t.Fatalf("env=prod after t-3: %+v, %v; want t-4 and the end", rest, err)
	}
	db, err := st.Targets().Page(ctx, tenantA, map[string]string{"env": "prod", "role": "db"}, store.Page{Limit: 10})
	if err != nil || len(db.Rows) != 2 || db.Rows[0].ID != "t-1" || db.Rows[1].ID != "t-4" {
		t.Fatalf("env=prod,role=db: %+v, %v; want t-1 and t-4", db, err)
	}
	if db.Rows[0].Hostname != "t-1.example" || db.Rows[0].Labels["role"] != "db" || db.Rows[0].CreatedAt.IsZero() {
		t.Errorf("a paged target lost its columns: %+v", db.Rows[0])
	}
}
