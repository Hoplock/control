// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision_test

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/provision"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// Directory-scoped group entries and the recalculation a mapping write starts
// (0043), against Postgres and a real revocation bus.

// scopedMapping is the request's example (enterprise#51), with an unscoped
// `admins → all-admins` beside the scoped ones when unscopedAdmins is set and
// `require_directories` when required is.
func scopedMapping(unscopedAdmins, required bool) string {
	var b strings.Builder
	b.WriteString("schema_version: 1\ngroups:\n  claim: groups\n")
	if required {
		b.WriteString("  require_directories: true\n")
	}
	b.WriteString(`  map:
    - external: BigCorporationAdmins
      directories: [de, fr, jp]
      group: corp-admins
    - external: admins
      directories: [de]
      group: de-admins
    - external: admins
      directories: [fr]
      group: fr-admins
    - external: engineering
      group: engineering
`)
	if unscopedAdmins {
		b.WriteString("    - external: admins\n      group: all-admins\n")
	}
	return b.String()
}

// unscopedOnly maps `admins → all-admins` and `engineering` for every
// directory: what a tenant had before it scoped anything.
const unscopedOnly = `
schema_version: 1
groups:
  claim: groups
  map:
    - external: admins
      group: all-admins
    - external: engineering
      group: engineering
`

// directories are the four of the request's example.
var directories = []string{"de", "fr", "jp", "us"}

// personIn is a directory record of a person in a source.
func (h *harness) personIn(src, externalID, username string, groups ...string) ext.SyncedSubject {
	p := h.person(externalID, username, groups...)
	p.Source = src
	return p
}

// groupIn is a directory group of a source.
func (h *harness) groupIn(src, externalID, name string) ext.SyncedGroup {
	g := h.group(externalID, name)
	g.Source = src
	return g
}

// putMapping writes and activates a mapping as `identity mapping-put` does: no
// API call, so nothing starts a recalculation.
func (h *harness) putMapping(document string) *identity.Mapping {
	h.t.Helper()
	m, err := h.federation.PutMapping(h.t.Context(), tenant, []byte(document), "cli", nil)
	if err != nil {
		h.t.Fatalf("mapping: %v", err)
	}
	return m
}

// provisionEveryDirectory gives each of the four directories the request's
// three groups and one person in all of them: `p-<source>`.
func (h *harness) provisionEveryDirectory() {
	h.t.Helper()
	for _, src := range directories {
		h.mustGroup(h.groupIn(src, "g-bca", "BigCorporationAdmins"))
		h.mustGroup(h.groupIn(src, "g-adm", "admins"))
		h.mustGroup(h.groupIn(src, "g-eng", "engineering"))
		h.mustUpsert(h.personIn(src, "p-"+src, "person-"+src, "g-bca", "g-adm", "g-eng"))
	}
}

// groupsIn reads the groups of a source's person.
func (h *harness) groupsIn(src, externalID string) []string {
	h.t.Helper()
	s, err := h.store.Subjects().Get(h.t.Context(), tenant, h.linkIn(src, externalID).SubjectID)
	if err != nil {
		h.t.Fatalf("subject of %s/%s: %v", src, externalID, err)
	}
	return s.Groups
}

func (h *harness) recalculator(batch int, revoker provision.Revoker) *provision.Recalculator {
	h.t.Helper()
	if revoker == nil {
		revoker = h.revoker
	}
	r, err := provision.NewRecalculator(provision.RecalculatorOptions{
		Store: h.store, Revoker: revoker, Recorder: h.emitter, Logger: slog.New(slog.DiscardHandler), Batch: batch,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *harness) stale(version int) int64 {
	h.t.Helper()
	n, err := h.store.DirectoryLinks().StaleCount(h.t.Context(), tenant, version)
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// applied returns the tenant's `operator.claim_mapping.applied` records,
// oldest first.
func (h *harness) applied() []store.AuditRecord {
	h.t.Helper()
	rows := h.records(tenant, audit.EventClaimMappingApplied)
	slices.Reverse(rows)
	return rows
}

func appliedField(r store.AuditRecord, key string) string {
	return r.Attributes[audit.AttrOperatorPrefix+key]
}

func TestADirectoryGroupReachesOnlyEntriesThatListItsSource(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(scopedMapping(false, false))
	h.provisionEveryDirectory()

	for src, want := range map[string][]string{
		"de": {"corp-admins", "de-admins", "engineering"},
		"fr": {"corp-admins", "engineering", "fr-admins"},
		"jp": {"corp-admins", "engineering"},
		// A directory no entry lists: its admins maps nothing.
		"us": {"engineering"},
	} {
		if got := h.groupsIn(src, "p-"+src); !slices.Equal(got, want) {
			t.Errorf("a %s subject has %v, want %v", src, got, want)
		}
	}

	// Instead of, not as well as: with an unscoped admins entry too, a de
	// subject's admins is de-admins alone, and a us subject's all-admins.
	h.putMapping(scopedMapping(true, false))
	h.mustUpsert(h.personIn("de", "p-de", "person-de", "g-adm"))
	h.mustUpsert(h.personIn("us", "p-us", "person-us", "g-adm"))
	if got := h.groupsIn("de", "p-de"); !slices.Equal(got, []string{"de-admins"}) {
		t.Errorf("a de subject's admins mapped to %v, want de-admins alone", got)
	}
	if got := h.groupsIn("us", "p-us"); !slices.Equal(got, []string{"all-admins"}) {
		t.Errorf("a us subject's admins mapped to %v, want all-admins", got)
	}
}

func TestOneEntryMapsAGroupSeveralDirectoriesShare(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(scopedMapping(false, false))
	h.provisionEveryDirectory()
	for _, src := range directories {
		has := slices.Contains(h.groupsIn(src, "p-"+src), "corp-admins")
		if want := src != "us"; has != want {
			t.Errorf("a %s subject has corp-admins: %t, want %t", src, has, want)
		}
	}
}

func TestRequiringScopingLeavesUnscopedEntriesToLogins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(scopedMapping(true, true))
	h.provisionEveryDirectory()

	if got := h.groupsIn("us", "p-us"); len(got) != 0 {
		t.Errorf("under require_directories a us subject has %v; an unscoped entry reached a directory group", got)
	}
	if got := h.groupsIn("de", "p-de"); !slices.Equal(got, []string{"corp-admins", "de-admins"}) {
		t.Errorf("under require_directories a de subject has %v", got)
	}
	active, err := identity.ActiveMapping(t.Context(), h.store, tenant)
	if err != nil {
		t.Fatal(err)
	}
	login := active.Apply(identity.Assertion{MultiClaims: map[string][]string{"groups": {"admins", "engineering", "BigCorporationAdmins"}}})
	if want := []string{"all-admins", "engineering"}; !slices.Equal(login.Groups, want) {
		t.Errorf("a login got %v, want %v", login.Groups, want)
	}
}

// A de subject provisioned under an unscoped mapping loses what the scoped one
// takes away once that is written, with no call on the sink.
func TestAMappingWriteReachesEveryDirectorySubject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(unscopedOnly)
	h.mustGroup(h.groupIn("de", "g-adm", "admins"))
	h.mustUpsert(h.personIn("de", "p-de", "person-de", "g-adm"))
	if got := h.groupsIn("de", "p-de"); !slices.Equal(got, []string{"all-admins"}) {
		t.Fatalf("before: %v", got)
	}
	subjectID := h.linkIn("de", "p-de").SubjectID
	before := len(h.revoker.invalidated())

	m := h.putMapping(scopedMapping(true, false))
	if n := h.stale(m.Version); n != 1 {
		t.Fatalf("stale before the run: %d, want 1", n)
	}
	got, err := h.recalculator(0, nil).Recalculate(t.Context(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != m.Version || got.Recalculated != 1 || got.Changed != 1 || !got.Complete {
		t.Errorf("the run reports %+v", got)
	}
	if g := h.groupsIn("de", "p-de"); !slices.Equal(g, []string{"de-admins"}) {
		t.Errorf("after: %v, want de-admins alone", g)
	}
	row, err := h.store.Subjects().Get(t.Context(), tenant, subjectID)
	if err != nil || row.MappingVersion != m.Version {
		t.Errorf("the row names mapping %d (%v), want %d", row.MappingVersion, err, m.Version)
	}
	invs := h.revoker.invalidated()[before:]
	if len(invs) != 1 || invs[0].Subject != subjectID {
		t.Errorf("published %+v, want one cache_invalidate for %s", invs, subjectID)
	}
	if l := h.linkIn("de", "p-de"); l.InvalidateOwed {
		t.Error("the invalidation was published and is still owed")
	}
	if n := h.stale(m.Version); n != 0 {
		t.Errorf("stale after the run: %d", n)
	}
	records := h.applied()
	if len(records) != 1 {
		t.Fatalf("%d applied records, want 1", len(records))
	}
	r := records[0]
	if r.Attributes[audit.AttrAfter] != strconv.Itoa(m.Version) || appliedField(r, "recalculated") != "1" ||
		appliedField(r, "changed") != "1" || appliedField(r, "complete") != "true" ||
		r.Attributes[audit.AttrPrincipalID] != "cli" {
		t.Errorf("applied record: %v", r.Attributes)
	}
	published := h.records(tenant, audit.EventRevocationPublished)
	if len(published) == 0 || published[0].Attributes[audit.AttrPrincipalID] != "cli" {
		t.Errorf("the invalidation was not published as the mapping's author: %v", published)
	}

	// A second run finds nothing, writes nothing and records nothing.
	again, err := h.recalculator(0, nil).Recalculate(t.Context(), tenant)
	if err != nil || again.Recalculated != 0 || !again.Complete {
		t.Errorf("a second run: %+v, %v", again, err)
	}
	if n := len(h.applied()); n != 1 {
		t.Errorf("a run that recalculated nothing recorded: %d records", n)
	}
	h.verify()
}

// hookRevoker runs a hook before the first n invalidations it is asked for.
type hookRevoker struct {
	provision.Revoker
	calls atomic.Int32
	hook  func(call int)
}

func (r *hookRevoker) Invalidate(ctx context.Context, t store.Tenant, aud revoke.Audience, inv revoke.Invalidation) (revoke.Receipt, error) {
	r.hook(int(r.calls.Add(1)))
	return r.Revoker.Invalidate(ctx, t, aud, inv)
}

// provisionAdmins provisions n de subjects, each in admins.
func (h *harness) provisionAdmins(n int) {
	h.t.Helper()
	h.mustGroup(h.groupIn("de", "g-adm", "admins"))
	for i := range n {
		h.mustUpsert(h.personIn("de", fmt.Sprintf("p-%03d", i), fmt.Sprintf("person-%03d", i), "g-adm"))
	}
}

func TestARecalculationEndsOnTheNewestMapping(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(unscopedOnly)
	h.provisionAdmins(5)

	second := h.putMapping(scopedMapping(true, false))
	var third *identity.Mapping
	revoker := &hookRevoker{Revoker: h.revoker, hook: func(call int) {
		if call == 1 {
			// Written mid-run, after the first batch committed.
			third = h.putMapping(scopedMapping(false, true))
		}
	}}
	got, err := h.recalculator(1, revoker).Recalculate(t.Context(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	if third == nil || got.Version != third.Version || !got.Complete {
		t.Fatalf("the run ended on %+v", got)
	}
	if n := h.stale(third.Version); n != 0 {
		t.Errorf("%d subjects are not on the newest mapping", n)
	}
	for i := range 5 {
		if g := h.groupsIn("de", fmt.Sprintf("p-%03d", i)); !slices.Equal(g, []string{"de-admins"}) {
			t.Errorf("subject %d has %v", i, g)
		}
	}
	records := h.applied()
	if len(records) != 2 {
		t.Fatalf("%d applied records, want one per version", len(records))
	}
	if records[0].Attributes[audit.AttrAfter] != strconv.Itoa(second.Version) || appliedField(records[0], "complete") != "false" {
		t.Errorf("the superseded version's record: %v", records[0].Attributes)
	}
	if records[1].Attributes[audit.AttrAfter] != strconv.Itoa(third.Version) || appliedField(records[1], "complete") != "true" ||
		appliedField(records[1], "recalculated") != "5" {
		t.Errorf("the newest version's record: %v", records[1].Attributes)
	}
}

func TestARecalculationResumesAfterItStops(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(unscopedOnly)
	h.provisionAdmins(4)
	m := h.putMapping(scopedMapping(true, false))

	ctx, cancel := context.WithCancel(t.Context())
	revoker := &hookRevoker{Revoker: h.revoker, hook: func(call int) {
		if call == 1 {
			cancel() // the node stops after its first batch committed
		}
	}}
	r := h.recalculator(1, revoker)
	if _, err := r.Recalculate(ctx, tenant); err == nil {
		t.Fatal("a stopped run reported no error")
	}
	if n := h.stale(m.Version); n != 3 {
		t.Fatalf("after one batch %d are stale, want 3", n)
	}
	if records := h.applied(); len(records) != 1 || appliedField(records[0], "complete") != "false" {
		t.Fatalf("a stopped run's record: %v", records)
	}

	// The sweep on any node finishes it: the tenant was tracked by the run.
	h.recalculator(1, nil).Sweep(t.Context())
	if n := h.stale(m.Version); n != 3 {
		t.Fatalf("a recalculator never told of the tenant swept it: %d stale", n)
	}
	r.Sweep(t.Context())
	if n := h.stale(m.Version); n != 0 {
		t.Errorf("after the sweep %d are stale", n)
	}
}

// A mapping written by `identity mapping-put`, with no API call and no
// identity sync, is applied by the sweep.
func TestARecalculationNeedsNoSync(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(unscopedOnly)
	h.provisionAdmins(3)

	// Built from the store, the stream and the recorder alone: no sink, no
	// runner, nothing registered.
	r := h.recalculator(0, nil)
	r.Track(tenant)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx, 20*time.Millisecond)
	}()

	m := h.putMapping(scopedMapping(true, false))
	deadline := time.Now().Add(10 * time.Second)
	for h.stale(m.Version) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the sweep never applied the mapping")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if g := h.groupsIn("de", "p-000"); !slices.Equal(g, []string{"de-admins"}) {
		t.Errorf("after the sweep: %v", g)
	}
}

// Start returns at once and recalculates under Run's lifetime.
func TestStartRecalculatesInTheBackground(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(unscopedOnly)
	h.provisionAdmins(2)

	r := h.recalculator(0, nil)
	m := h.putMapping(scopedMapping(true, false))
	r.Start(tenant) // before Run: tracks, starts nothing
	if n := h.stale(m.Version); n != 2 {
		t.Fatalf("Start before Run recalculated: %d stale", n)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx, time.Hour)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for h.stale(m.Version) != 0 { // the first sweep, at Run's start
		if time.Now().After(deadline) {
			t.Fatal("the first sweep never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	next := h.putMapping(scopedMapping(false, true))
	r.Start(tenant)
	for h.stale(next.Version) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("Start never recalculated")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// Sink writes and a recalculation take the same locks in the same order: run
// them together under -race and neither deadlocks, and every subject ends on
// the active mapping.
func TestSinkWritesAndARecalculationShareTheirLocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(unscopedOnly)
	const people = 12
	h.provisionAdmins(people)
	m := h.putMapping(scopedMapping(true, false))

	// The directory's clock, advanced under a lock: two writers share it.
	var clock sync.Mutex
	next := func() time.Time {
		clock.Lock()
		defer clock.Unlock()
		return h.tick()
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for w := range 2 {
		wg.Go(func() {
			r := h.recalculator(2, nil)
			for range 3 {
				if _, err := r.Recalculate(ctx, tenant); err != nil {
					errs <- fmt.Errorf("recalculator %d: %w", w, err)
					return
				}
			}
		})
	}
	wg.Go(func() {
		for round := range 3 {
			for i := range people {
				p := ext.SyncedSubject{
					Tenant: ext.Tenant(tenant), Source: "de", ExternalID: fmt.Sprintf("p-%03d", i),
					Username: fmt.Sprintf("person-%03d", i), Groups: []string{"g-adm"},
					Attributes: map[string]string{"round": strconv.Itoa(round)}, Active: true, UpdatedAt: next(),
				}
				if err := h.sink.UpsertSubject(ctx, p); err != nil {
					errs <- fmt.Errorf("sink: %w", err)
					return
				}
			}
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if ctx.Err() != nil {
		t.Fatal("the sink and the recalculation did not finish: a deadlock")
	}
	if n := h.stale(m.Version); n != 0 {
		t.Errorf("%d subjects are not on the active mapping", n)
	}
}

func TestABatchPastTheBoundInvalidatesTheTenant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(unscopedOnly)
	h.provisionAdmins(70)
	before := len(h.revoker.invalidated())
	h.putMapping(scopedMapping(true, false))

	got, err := h.recalculator(0, nil).Recalculate(t.Context(), tenant)
	if err != nil || got.Changed != 70 {
		t.Fatalf("%+v, %v", got, err)
	}
	invs := h.revoker.invalidated()[before:]
	if len(invs) != 1 || !invs[0].All {
		t.Errorf("published %d invalidations (%+v), want one for the tenant", len(invs), invs)
	}
	for i := range 70 {
		if h.linkIn("de", fmt.Sprintf("p-%03d", i)).InvalidateOwed {
			t.Fatalf("subject %d still owes its invalidation", i)
		}
	}
}

// An invalidation a run committed and could not publish is delivered by the
// next run, though the subject is no longer stale.
func TestAnUndeliveredInvalidationIsDeliveredByTheNextRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.putMapping(unscopedOnly)
	h.provisionAdmins(1)
	m := h.putMapping(scopedMapping(true, false))

	h.revoker.fail.Store(true)
	if _, err := h.recalculator(0, nil).Recalculate(t.Context(), tenant); err == nil {
		t.Fatal("a run whose stream refused reported no error")
	}
	if n := h.stale(m.Version); n != 0 || !h.linkIn("de", "p-000").InvalidateOwed {
		t.Fatalf("stale %d, owed %t: the batch should stand and owe its invalidation", n, h.linkIn("de", "p-000").InvalidateOwed)
	}
	h.revoker.fail.Store(false)
	before := len(h.revoker.invalidated())
	if _, err := h.recalculator(0, nil).Recalculate(t.Context(), tenant); err != nil {
		t.Fatal(err)
	}
	if invs := h.revoker.invalidated()[before:]; len(invs) != 1 || h.linkIn("de", "p-000").InvalidateOwed {
		t.Errorf("the next run published %+v and left owed=%t", invs, h.linkIn("de", "p-000").InvalidateOwed)
	}
}
