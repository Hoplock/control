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
	"time"

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
	// key is the position of the item put as id, for a list whose order key
	// has more than one component; the id leads it, so the ids sort as the
	// list does. Nil is a list keyed by the id alone.
	key func(id string) []any
}

// at is the position of the item put as id.
func (r pagedReader) at(id string) []any {
	if r.key == nil {
		return []any{id}
	}
	return r.key(id)
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
	// What each build declares and each target was seen to take (0032).
	// Each observation is put at port 22, and a rung at platform linux;
	// TestObservationPagesFollowTheirWholeKey walks the other components.
	"ProxyAlgorithmDeclarations.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			_, err := st.ProxyAlgorithmDeclarations().Put(ctx, tenant, store.ProxyAlgorithmDeclaration{
				ProxyID: id, Declaration: []byte(`{}`), Digest: "d-" + id, DeclaredAt: time.Now(),
			})
			return err
		},
		del: store.DeleteAlgorithmDeclaration,
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.ProxyAlgorithmDeclarations().Page(ctx, tenant, p)
			ids := make([]string, 0, len(got.Rows))
			for _, d := range got.Rows {
				ids = append(ids, d.ProxyID)
			}
			return ids, got.More, err
		},
	},
	"TargetCapabilities.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.TargetCapabilities().Put(ctx, tenant, store.TargetCapabilityRecord{
				Hostname: id, Port: 22, Platform: "linux", Execution: []string{"account-confined"}, ObservedAt: time.Now(),
			})
		},
		del: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return store.DeleteTargetCapability(ctx, st, tenant, id, 22, "linux")
		},
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.TargetCapabilities().Page(ctx, tenant, p)
			ids := make([]string, 0, len(got.Rows))
			for _, c := range got.Rows {
				ids = append(ids, c.Hostname)
			}
			return ids, got.More, err
		},
		key: func(id string) []any { return []any{id, int64(22), "linux"} },
	},
	"TargetKexObservations.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.TargetKexObservations().Put(ctx, tenant, store.TargetKexObservation{
				Hostname: id, Port: 22, FloorMet: "modern-kex", ObservedAt: time.Now(),
			})
		},
		del: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return store.DeleteTargetKex(ctx, st, tenant, id, 22)
		},
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.TargetKexObservations().Page(ctx, tenant, p)
			ids := make([]string, 0, len(got.Rows))
			for _, o := range got.Rows {
				ids = append(ids, o.Hostname)
			}
			return ids, got.More, err
		},
		key: func(id string) []any { return []any{id, int64(22)} },
	},
	// The grant lists (0033) are read newest first, so each item is put at
	// the instant newestFirst gives its id: later for a smaller id, so the
	// ids sort as the list does. The holder's view reads the other index.
	"Grants.Page": {
		put:  putGrant,
		del:  store.DeleteGrant,
		page: grantIDs(store.GrantQuery{}),
		key:  func(id string) []any { return []any{newestFirst(id), id} },
	},
	"Grants.Page subject": {
		put:  putGrant,
		del:  store.DeleteGrant,
		page: grantIDs(store.GrantQuery{SubjectID: "alice"}),
		key:  func(id string) []any { return []any{newestFirst(id), id} },
	},
	"GrantRequests.PageForSubject": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			req := storetest.GrantRequest(id, "alice", time.Hour)
			req.RequestedAt = newestFirst(id)
			if err := st.GrantRequests().Insert(ctx, tenant, req); err != nil && !store.IsConflict(err) {
				return err
			}
			return nil
		},
		del: store.DeleteGrantRequest,
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.GrantRequests().PageForSubject(ctx, tenant, "alice", store.GrantRequestQuery{}, p)
			ids := make([]string, 0, len(got.Rows))
			for _, req := range got.Rows {
				ids = append(ids, req.ID)
			}
			return ids, got.More, err
		},
		key: func(id string) []any { return []any{newestFirst(id), id} },
	},
	// The role bindings and outstanding certificates (0034). Each binding is
	// a subject's, to one role, so the holder's id leads its position;
	// TestRoleBindingPagesFollowTheirWholeKey walks the role and the holder's
	// kind. A certificate is read newest first, like a grant, and keyed by its
	// serial after its issuance.
	"RoleBindings.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.RoleBindings().Bind(ctx, tenant, store.RoleBinding{SubjectID: id, Role: "auditor"})
		},
		del: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return st.RoleBindings().Unbind(ctx, tenant, store.RoleBinding{SubjectID: id, Role: "auditor"})
		},
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.RoleBindings().Page(ctx, tenant, p)
			ids := make([]string, 0, len(got.Rows))
			for _, b := range got.Rows {
				ids = append(ids, b.SubjectID)
			}
			return ids, got.More, err
		},
		key: func(id string) []any { return []any{"auditor", false, id} },
	},
	"SSHCertificates.OutstandingPage": {
		put: putCertificate,
		del: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return store.DeleteSSHCertificate(ctx, st, tenant, certificateSerial(id))
		},
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.SSHCertificates().OutstandingPage(ctx, tenant, time.Now(), p)
			ids := make([]string, 0, len(got.Rows))
			for _, c := range got.Rows {
				ids = append(ids, c.KeyID)
			}
			return ids, got.More, err
		},
		key: func(id string) []any { return []any{newestFirst(id), certificateSerial(id)} },
	},
	// The policy version history (0035), newest first by version alone. A
	// version is numbered from its id so that a later id is an older
	// version, and its digest is the id, which is how a page names it.
	"PolicyBundles.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			err := st.PolicyBundles().Insert(ctx, tenant, store.PolicyBundle{
				Version: bundleVersion(id), Source: []byte("# " + id), Hash: id, UploadedBy: "test",
			})
			if store.IsConflict(err) {
				return nil
			}
			return err
		},
		del: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return store.DeletePolicyBundle(ctx, st, tenant, bundleVersion(id))
		},
		page: func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
			got, err := st.PolicyBundles().Page(ctx, tenant, p)
			ids := make([]string, 0, len(got.Rows))
			for _, b := range got.Rows {
				ids = append(ids, b.Hash)
			}
			return ids, got.More, err
		},
		key: func(id string) []any { return []any{bundleVersion(id)} },
	},
	// The audit query (0036), keyed by recorded-at, stream and chain
	// sequence. Each record is the first of a stream named for its id, so
	// its position follows from the id. Newest first, a record is put at
	// the instant newestFirst gives its id. Oldest first, every record is
	// put at ONE instant, so the whole order — and every walk the tests
	// below make, the racing one included — is the tiebreak's.
	"Audit.Page": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return putAuditRecord(ctx, st, tenant, id, newestFirst(id))
		},
		del:  store.DeleteAuditRecord,
		page: auditIDs(store.AuditQuery{}),
		key:  func(id string) []any { return []any{newestFirst(id), auditStream(id), int64(1)} },
	},
	"Audit.Page ascending at one instant": {
		put: func(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
			return putAuditRecord(ctx, st, tenant, id, newestFirstEpoch)
		},
		del:  store.DeleteAuditRecord,
		page: auditIDs(store.AuditQuery{Ascending: true}),
		key:  func(id string) []any { return []any{newestFirstEpoch, auditStream(id), int64(1)} },
	},
}

// auditStream is the stream the record put as id is the first of.
func auditStream(id string) string { return "proxy:" + id }

// putAuditRecord stores the record id, recorded at at, as the first of its
// own stream. A record already stored is left as it is, as the other lists'
// upserts do.
func putAuditRecord(ctx context.Context, st *store.Store, tenant store.Tenant, id string, at time.Time) error {
	rec := storetest.AuditRecord(id, auditStream(id), 1)
	rec.RecordedAt = at
	if err := st.Audit().Append(ctx, tenant, rec); err != nil && !store.IsConflict(err) {
		return err
	}
	return nil
}

func auditIDs(q store.AuditQuery) func(context.Context, *store.Store, store.Tenant, store.Page) ([]string, bool, error) {
	return func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
		got, err := st.Audit().Page(ctx, tenant, q, p)
		ids := make([]string, 0, len(got.Rows))
		for _, rec := range got.Rows {
			ids = append(ids, rec.RecordID)
		}
		return ids, got.More, err
	}
}

// newestFirstEpoch is the latest instant newestFirst gives.
var newestFirstEpoch = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

// newestFirst is when the item put as id was created in a list read newest
// first: strictly earlier for a later id, comparing ids as Go does, so a walk
// of such a list reads the ids in ascending order as every other list here
// does. It reads the first eight bytes of the id in base 64 over the alphabet
// these tests' ids use (`-`, digits and lower-case letters, in their byte
// order), a missing byte lowest, as microseconds before the epoch.
func newestFirst(id string) time.Time {
	if len(id) > 8 {
		panic(fmt.Sprintf("newestFirst: %q is longer than eight bytes", id))
	}
	var n int64
	for i := range 8 {
		n *= 64
		if i >= len(id) {
			continue
		}
		switch c := id[i]; {
		case c == '-':
			n++
		case c >= '0' && c <= '9':
			n += 2 + int64(c-'0')
		case c >= 'a' && c <= 'z':
			n += 12 + int64(c-'a')
		default:
			panic(fmt.Sprintf("newestFirst: %q is outside the ids' alphabet", id))
		}
	}
	return newestFirstEpoch.Add(-time.Duration(n) * time.Microsecond)
}

// putGrant stores a live grant held by alice, created at newestFirst(id), in
// one transaction: a concurrent walk never sees it at the database's clock. A
// grant already stored is left as it is, as the other lists' upserts do.
func putGrant(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
	err := st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		if err := tx.Grants().Insert(ctx, tenant, storetest.Grant(id, "alice", time.Hour)); err != nil {
			return err
		}
		return store.SetGrantCreatedAt(ctx, tx, tenant, id, newestFirst(id))
	})
	if store.IsConflict(err) {
		return nil
	}
	return err
}

// testCAKeyID names the CA key every certificate these tests put is signed
// by: a row the certificate's foreign key needs, and nothing more.
const testCAKeyID = "ca-test"

// certificateSerial is the serial of the certificate put as id: distinct for
// each id, and larger for a later one.
func certificateSerial(id string) int64 {
	return newestFirstEpoch.Sub(newestFirst(id)).Microseconds()
}

// putCertificate stores an outstanding certificate whose key id is id, issued
// at newestFirst(id), with its tenant's CA key beside it. A certificate or key
// already stored is left as it is.
func putCertificate(ctx context.Context, st *store.Store, tenant store.Tenant, id string) error {
	if err := st.CAKeys().Insert(ctx, tenant, store.CAKey{
		KeyID: testCAKeyID, Algorithm: "ed25519", PublicKey: []byte("public"), PrivateRef: "software", Active: true,
	}); err != nil && !store.IsConflict(err) {
		return err
	}
	return insertCertificate(ctx, st, tenant, store.SSHCertificate{
		Serial: certificateSerial(id), KeyID: id, IssuedAt: newestFirst(id),
		ValidBefore: newestFirstEpoch.Add(24 * time.Hour),
	})
}

// insertCertificate stores c, filling what a test leaves out: the test CA key,
// a subject, a window opening a minute before issuance, and certificate text.
// A serial already stored is left as it is.
func insertCertificate(ctx context.Context, st *store.Store, tenant store.Tenant, c store.SSHCertificate) error {
	if c.CAKeyID == "" {
		c.CAKeyID = testCAKeyID
	}
	if c.SubjectID == "" {
		c.SubjectID = "alice"
	}
	if c.ValidAfter.IsZero() {
		c.ValidAfter = c.IssuedAt.Add(-time.Minute)
	}
	if c.Certificate == "" {
		c.Certificate = "ssh-ed25519-cert-v01@openssh.com " + c.KeyID
	}
	if err := st.SSHCertificates().Insert(ctx, tenant, c); err != nil && !store.IsConflict(err) {
		return err
	}
	return nil
}

func grantIDs(q store.GrantQuery) func(context.Context, *store.Store, store.Tenant, store.Page) ([]string, bool, error) {
	return func(ctx context.Context, st *store.Store, tenant store.Tenant, p store.Page) ([]string, bool, error) {
		got, err := st.Grants().Page(ctx, tenant, q, p)
		ids := make([]string, 0, len(got.Rows))
		for _, g := range got.Rows {
			ids = append(ids, g.ID)
		}
		return ids, got.More, err
	}
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
		p.After = r.at(last)
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
				after string // the id of the item the page follows; "" starts the list
				want  []string
				more  bool
			}{
				{limit: 2, want: []string{"i-1", "i-2"}, more: true},
				{limit: 4, want: []string{"i-1", "i-2", "i-3", "i-4"}, more: true},
				{limit: 5, want: []string{"i-1", "i-2", "i-3", "i-4", "i-5"}, more: false},
				{limit: 10, want: []string{"i-1", "i-2", "i-3", "i-4", "i-5"}, more: false},
				{limit: 2, after: "i-2", want: []string{"i-3", "i-4"}, more: true},
				{limit: 2, after: "i-3", want: []string{"i-4", "i-5"}, more: false},
				// A position need not be an item: a deleted one continues
				// from where it was.
				{limit: 10, after: "i-25", want: []string{"i-3", "i-4", "i-5"}, more: false},
				{limit: 10, after: "i-5", want: []string{}, more: false},
			}
			for _, c := range cases {
				var after []any
				if c.after != "" {
					after = r.at(c.after)
				}
				ids, more, err := r.page(ctx, st, tenantA, store.Page{Limit: c.limit, After: after})
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
			bad := []store.Page{
				{Limit: 0},
				{Limit: -1},
				{Limit: store.MaxPageLimit + 1},
				{Limit: 1, After: append(r.at("a"), "b")},
			}
			if key := r.at("a"); len(key) > 1 {
				bad = append(bad, store.Page{Limit: 1, After: key[:len(key)-1]})
			}
			for _, p := range bad {
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
			got, more, err := r.page(t.Context(), st, tenantB, store.Page{Limit: 10, After: r.at(ids[len(ids)-1])})
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

// The observation lists are keyed by more than the hostname (M26.4): a rung
// by hostname, port and platform, a key exchange by hostname and port. A walk
// one row at a time follows every component, a port in numeric order and not
// as text, and a position from tenant A continues tenant B's list with B's
// rows alone (M18). Every column survives the page.
func TestObservationPagesFollowTheirWholeKey(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

	type rung struct {
		host     string
		port     int32
		platform string
	}
	rungs := []rung{{"h-2", 0, ""}, {"h-1", 2222, ""}, {"h-1", 22, "linux"}, {"h-1", 80, "linux"}, {"h-1", 22, ""}}
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		for _, r := range rungs {
			if err := st.TargetCapabilities().Put(ctx, tenant, store.TargetCapabilityRecord{
				Hostname: r.host, Port: r.port, Platform: r.platform, Execution: []string{string(tenant)},
				Reach: []string{"network-policy"}, ObservedAt: at, ReportedBy: "p-1",
			}); err != nil {
				t.Fatalf("put rung %+v in %s: %v", r, tenant, err)
			}
			if r.platform != "" {
				continue
			}
			if err := st.TargetKexObservations().Put(ctx, tenant, store.TargetKexObservation{
				Hostname: r.host, Port: r.port, FloorMet: "modern-kex", Negotiated: string(tenant),
				Offered: []string{"curve25519-sha256"}, ObservedAt: at, ReportedBy: "p-1",
			}); err != nil {
				t.Fatalf("put key exchange %+v in %s: %v", r, tenant, err)
			}
			if err := st.TargetKexObservations().Put(ctx, tenant, store.TargetKexObservation{
				Hostname: r.host, Port: r.port + 58, FloorMet: "none", ObservedAt: at,
			}); err != nil {
				t.Fatalf("put key exchange %+v in %s: %v", r, tenant, err)
			}
		}
	}

	var gotRungs []rung
	p := store.Page{Limit: 1}
	for range len(rungs) + 1 {
		page, err := st.TargetCapabilities().Page(ctx, tenantA, p)
		if err != nil {
			t.Fatalf("rungs after %v: %v", p.After, err)
		}
		if len(page.Rows) != 1 {
			t.Fatalf("rungs after %v: %d rows, want 1", p.After, len(page.Rows))
		}
		c := page.Rows[0]
		if len(c.Execution) != 1 || c.Execution[0] != string(tenantA) || c.Reach[0] != "network-policy" ||
			!c.ObservedAt.Equal(at) || c.ReportedBy != "p-1" {
			t.Fatalf("a paged rung lost its columns or is not tenant A's: %+v", c)
		}
		gotRungs = append(gotRungs, rung{c.Hostname, c.Port, c.Platform})
		if !page.More {
			break
		}
		p.After = []any{c.Hostname, int64(c.Port), c.Platform}
	}
	wantRungs := []rung{{"h-1", 22, ""}, {"h-1", 22, "linux"}, {"h-1", 80, "linux"}, {"h-1", 2222, ""}, {"h-2", 0, ""}}
	if !slices.Equal(gotRungs, wantRungs) {
		t.Errorf("the rung walk saw %v, want %v", gotRungs, wantRungs)
	}

	type kex struct {
		host string
		port int32
	}
	var gotKex []kex
	p = store.Page{Limit: 1}
	for range 10 {
		page, err := st.TargetKexObservations().Page(ctx, tenantA, p)
		if err != nil {
			t.Fatalf("key exchanges after %v: %v", p.After, err)
		}
		if len(page.Rows) != 1 {
			t.Fatalf("key exchanges after %v: %d rows, want 1", p.After, len(page.Rows))
		}
		o := page.Rows[0]
		if o.FloorMet == "modern-kex" && (o.Negotiated != string(tenantA) || len(o.Offered) != 1 || !o.ObservedAt.Equal(at)) {
			t.Fatalf("a paged key exchange lost its columns or is not tenant A's: %+v", o)
		}
		gotKex = append(gotKex, kex{o.Hostname, o.Port})
		if !page.More {
			break
		}
		p.After = []any{o.Hostname, int64(o.Port)}
	}
	wantKex := []kex{{"h-1", 22}, {"h-1", 80}, {"h-1", 2222}, {"h-1", 2280}, {"h-2", 0}, {"h-2", 58}}
	if !slices.Equal(gotKex, wantKex) {
		t.Errorf("the key-exchange walk saw %v, want %v", gotKex, wantKex)
	}

	// Tenant A's position in tenant B: B's rows, from that place.
	rest, err := st.TargetCapabilities().Page(ctx, tenantB, store.Page{Limit: 10, After: []any{"h-1", int64(22), "linux"}})
	if err != nil || len(rest.Rows) != 3 || rest.More {
		t.Fatalf("tenant B's rungs after (h-1, 22, linux): %+v, %v; want three rows and the end", rest.Rows, err)
	}
	for _, c := range rest.Rows {
		if c.Execution[0] != string(tenantB) {
			t.Errorf("tenant B's page holds %s's row %+v", c.Execution[0], c)
		}
	}
	kexRest, err := st.TargetKexObservations().Page(ctx, tenantB, store.Page{Limit: 10, After: []any{"h-1", int64(2222)}})
	if err != nil || len(kexRest.Rows) != 3 || kexRest.More {
		t.Fatalf("tenant B's key exchanges after (h-1, 2222): %+v, %v; want three rows and the end", kexRest.Rows, err)
	}
	if kexRest.Rows[1].Hostname != "h-2" || kexRest.Rows[1].Negotiated != string(tenantB) {
		t.Errorf("tenant B's key exchange (h-2, 0) is %+v, want B's own", kexRest.Rows[1])
	}
}

// A declaration's stored bytes and digest survive the page: the fleet decodes
// them, and an undecodable one is the fleet's to fail on, not the store's.
func TestADeclarationPageKeepsItsDocument(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		if _, err := st.ProxyAlgorithmDeclarations().Put(ctx, tenant, store.ProxyAlgorithmDeclaration{
			ProxyID: "p-1", Declaration: []byte(`{"algorithms":{"t":"` + string(tenant) + `"}}`), Digest: "d-" + string(tenant), DeclaredAt: at,
		}); err != nil {
			t.Fatalf("put in %s: %v", tenant, err)
		}
	}
	page, err := st.ProxyAlgorithmDeclarations().Page(ctx, tenantA, store.Page{Limit: 10})
	if err != nil || len(page.Rows) != 1 || page.More {
		t.Fatalf("tenant A's declarations: %+v, %v; want its one", page.Rows, err)
	}
	if d := page.Rows[0]; string(d.Declaration) != `{"algorithms":{"t":"tenant-a"}}` || d.Digest != "d-tenant-a" || !d.DeclaredAt.Equal(at) {
		t.Errorf("tenant A's declaration came back as %+v (%s)", d, d.Declaration)
	}
}
