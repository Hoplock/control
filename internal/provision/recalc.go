// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package provision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
)

// A NEW CLAIM MAPPING REACHES EVERY DIRECTORY SUBJECT BY ITSELF (0043).
//
// A provisioned subject's groups and attributes are what the tenant's claim
// mapping made of the directory's data when it was last derived, and its row
// names that mapping's version. A sink upsert whose UpdatedAt is not later than
// the last applied one is a replay that writes nothing, so a directory that
// re-pushes unchanged data changes nothing, and a person who never changes in
// the directory would keep what an older mapping gave them indefinitely. A
// mapping that scopes or removes an entry exists to take access away, so that
// cannot stand.
//
// So a directory subject whose row names a version other than the tenant's
// active one is STALE, and the Recalculator recalculates a tenant's stale
// subjects exactly as a sink write recalculates one: the tenant's
// directory-groups lock shared, then each identity's own lock (0040's order),
// `derive` through the mapping active when the batch runs, the row written, the
// link marked as owing a cache invalidation when what a decision reads
// changed, and after each batch commits those invalidations published as a
// group change publishes them. It needs no registered sync: each subject's
// provider comes from its link row, so a lapsed sync cannot freeze an older
// mapping's groups in place.
//
// Progress is never stored. Staleness is read from the rows, so a run resumes
// by asking again, and two runs that overlap — on two nodes, or after a second
// mapping write — cost only reads: each subject is skipped once its lock is
// held if its row already names the active version, and a run ends only when
// nothing is stale, so a tenant always ends on its newest mapping.

// DefaultRecalculateInterval is how often the sweep looks for stale directory
// subjects in the tenants this process has seen.
const DefaultRecalculateInterval = time.Minute

// RecalculateBatch bounds the subjects one transaction recalculates.
const RecalculateBatch = 200

// recordTimeout bounds the record a run writes as it ends, which is written
// even when the run was stopped by its context.
const recordTimeout = 10 * time.Second

// RecalculationPrincipal is the actor of a recalculation's records when the
// mapping version names no author.
const RecalculationPrincipal = "claim-mapping"

// RecalculatorOptions configures a Recalculator.
type RecalculatorOptions struct {
	Store    *store.Store
	Revoker  Revoker
	Recorder Recorder
	Logger   *slog.Logger
	// Batch bounds one transaction. Zero takes RecalculateBatch.
	Batch int
}

// Recalculator brings a tenant's directory subjects onto its active claim
// mapping (0043). It is built from the store and the revocation publisher
// alone, never from a registered sync, and is safe for concurrent use.
//
// It walks the tenants this process has seen, because the store cannot list
// tenants (M18): the configured one, every tenant a proxy subscribes in, every
// tenant whose claim mapping is written or read here, and every tenant the
// identity sync writes in. Each is reported with Track.
type Recalculator struct {
	store    *store.Store
	revoker  Revoker
	recorder Recorder
	log      *slog.Logger
	batch    int

	mu      sync.Mutex
	tenants map[store.Tenant]struct{}
	// running marks a tenant this node is recalculating; again asks that
	// run to look once more when it ends, because a mapping was written
	// while it ran.
	running  map[store.Tenant]bool
	again    map[store.Tenant]bool
	lifetime context.Context
	runs     sync.WaitGroup
}

// NewRecalculator builds a Recalculator. It refuses to build without what it
// needs.
func NewRecalculator(o RecalculatorOptions) (*Recalculator, error) {
	if o.Store == nil || o.Revoker == nil || o.Recorder == nil {
		return nil, errors.New("provision: a recalculation needs a store, the revocation publisher and the audit recorder")
	}
	r := &Recalculator{
		store: o.Store, revoker: o.Revoker, recorder: o.Recorder, log: o.Logger, batch: o.Batch,
		tenants: map[store.Tenant]struct{}{}, running: map[store.Tenant]bool{}, again: map[store.Tenant]bool{},
	}
	if r.batch <= 0 {
		r.batch = RecalculateBatch
	}
	if r.batch > store.MaxStaleBatch {
		r.batch = store.MaxStaleBatch
	}
	if r.log == nil {
		r.log = slog.Default()
	}
	return r, nil
}

// Track reports a tenant this process has seen, so the sweep looks in it. It
// is idempotent, cheap and only ever adds.
func (r *Recalculator) Track(tenant store.Tenant) {
	if tenant == "" {
		return
	}
	r.mu.Lock()
	r.tenants[tenant] = struct{}{}
	r.mu.Unlock()
}

// Start recalculates the tenant in the background, under the lifetime Run was
// given rather than the caller's, and returns at once: a mapping write never
// waits for it. Before Run, or once Run's context has ended, it only tracks the
// tenant, and the sweep picks it up. A tenant this node is already
// recalculating is looked at once more when that run ends.
func (r *Recalculator) Start(tenant store.Tenant) {
	r.Track(tenant)
	r.mu.Lock()
	lifetime := r.lifetime
	r.mu.Unlock()
	if lifetime == nil || lifetime.Err() != nil || !r.claim(tenant) {
		return
	}
	r.runs.Add(1)
	go func() {
		defer r.runs.Done()
		r.runClaimed(lifetime, tenant)
	}()
}

// Run sweeps every tenant this process has seen at once and then every
// interval (zero takes DefaultRecalculateInterval), recalculating each that has
// a stale directory subject, until ctx ends. It returns when ctx has ended and
// every run Start began has returned.
func (r *Recalculator) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultRecalculateInterval
	}
	r.mu.Lock()
	r.lifetime = ctx
	r.mu.Unlock()
	defer r.runs.Wait()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		r.Sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep recalculates, one after another, every tenant this process has seen
// that this node is not already recalculating.
func (r *Recalculator) Sweep(ctx context.Context) {
	r.mu.Lock()
	tenants := make([]store.Tenant, 0, len(r.tenants))
	for t := range r.tenants {
		tenants = append(tenants, t)
	}
	r.mu.Unlock()
	slices.Sort(tenants)
	for _, t := range tenants {
		if ctx.Err() != nil {
			return
		}
		if r.claim(t) {
			r.runClaimed(ctx, t)
		}
	}
}

// claim marks the tenant as being recalculated on this node. False means a run
// is already in progress, which is then asked to look once more.
func (r *Recalculator) claim(tenant store.Tenant) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[tenant] {
		r.again[tenant] = true
		return false
	}
	r.running[tenant] = true
	return true
}

// runClaimed recalculates a claimed tenant until no further look is asked for,
// then releases it.
func (r *Recalculator) runClaimed(ctx context.Context, tenant store.Tenant) {
	for {
		if _, err := r.Recalculate(ctx, tenant); err != nil && ctx.Err() == nil {
			r.log.WarnContext(ctx, "recalculating the tenant's directory subjects failed; the sweep tries again",
				"event", "claim_mapping_recalculation_failed", "tenant", tenant.String(), "error", err.Error())
		}
		r.mu.Lock()
		if r.again[tenant] && ctx.Err() == nil {
			delete(r.again, tenant)
			r.mu.Unlock()
			continue
		}
		delete(r.again, tenant)
		delete(r.running, tenant)
		r.mu.Unlock()
		return
	}
}

// Recalculation is what one Recalculate call did.
type Recalculation struct {
	// Version is the claim mapping version it last applied.
	Version int
	// Recalculated counts the subjects it re-derived, and Changed those
	// among them whose groups, claims or login names moved.
	Recalculated, Changed int
	// Complete reports that it left the tenant with nothing stale.
	Complete bool
}

// applied is one version's share of a call, recorded as one
// `operator.claim_mapping.applied` when the version changes or the call ends.
type applied struct {
	version              int
	author               string
	recalculated, change int
}

// Recalculate recalculates the tenant's stale directory subjects, batch by
// batch in subject order, each batch its own transaction whose owed cache
// invalidations are published after it commits, until nothing is stale: a
// pass that reaches the end starts again from the first subject, and the call
// returns only when a pass from the start finds nothing. It returns early, with
// the error, when ctx ends or a batch fails, and what committed stands.
//
// A call that recalculated anything records `operator.claim_mapping.applied`
// as it ends or stops: the version it applied, how many subjects it
// recalculated and how many changed, and whether it left nothing stale. A call
// that applied two versions, because a mapping was written while it ran,
// records each.
func (r *Recalculator) Recalculate(ctx context.Context, tenant store.Tenant) (Recalculation, error) {
	r.Track(tenant)
	var (
		total Recalculation
		run   applied
		after string
	)
	for {
		if err := ctx.Err(); err != nil {
			r.record(ctx, tenant, run, false)
			return total, err
		}
		b, err := r.recalculateBatch(ctx, tenant, after)
		if err != nil {
			r.record(ctx, tenant, run, false)
			return total, err
		}
		if b.version != run.version {
			r.record(ctx, tenant, run, false)
			run = applied{}
		}
		run.version, run.author = b.version, b.author
		run.recalculated += b.recalculated
		run.change += b.changed
		total.Version = b.version
		total.Recalculated += b.recalculated
		total.Changed += b.changed

		if err := r.publisher(b).invalidate(ctx, tenant, b.owed); err != nil {
			r.record(ctx, tenant, run, false)
			return total, fmt.Errorf("publishing the invalidations a recalculation owes: %w", err)
		}
		if b.read == 0 {
			if after == "" {
				r.record(ctx, tenant, run, true)
				total.Complete = true
				return total, nil
			}
			// The end of a pass: a subject made stale behind the cursor
			// meanwhile is found by the next one.
			after = ""
			continue
		}
		after = b.last
	}
}

// batch is what one transaction did.
type batch struct {
	// read counts the links the batch read; zero means nothing was stale
	// after its cursor. last is the last subject id read.
	read int
	last string
	// version and author are the mapping the batch applied.
	version int
	author  string
	// recalculated and changed are as in Recalculation.
	recalculated, changed int
	// owed are the links that owe a cache invalidation once it commits.
	owed []store.DirectoryLink
}

// recalculateBatch recalculates up to one batch of the tenant's stale subjects
// after the cursor, in one transaction. A tenant with nothing stale is found so
// by two reads and no transaction, which is what the sweep costs most of the
// time.
func (r *Recalculator) recalculateBatch(ctx context.Context, tenant store.Tenant, after string) (batch, error) {
	version, author := 0, ""
	switch row, err := r.store.ClaimMappings().Active(ctx, tenant); {
	case err == nil:
		version, author = row.Version, row.CreatedBy
	case !store.IsNotFound(err):
		return batch{}, err
	}
	peek, err := r.store.DirectoryLinks().Stale(ctx, tenant, version, after, 1)
	if err != nil || len(peek) == 0 {
		return batch{version: version, author: author}, err
	}

	var out batch
	err = r.store.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		out = batch{}
		// 0040's order: shared with every subject write and exclusive of a
		// group change, then each identity's own lock. The links come in
		// subject order, so two runs take their locks in the same order, and
		// a sink write holds one identity lock at a time: none can wait on
		// the other in a cycle.
		if err := tx.DirectoryGroups().Share(ctx, tenant); err != nil {
			return err
		}
		// The mapping active as the batch runs. An unreadable one is an
		// outage, never an empty mapping (M11): the batch fails and the
		// subjects keep what they have.
		mapping, err := identity.ActiveMapping(ctx, tx, tenant)
		if err != nil {
			return err
		}
		out.version, out.author = mapping.Version, mapping.Author
		links, err := tx.DirectoryLinks().Stale(ctx, tenant, mapping.Version, after, r.batch)
		if err != nil {
			return err
		}
		out.read = len(links)
		for _, l := range links {
			out.last = l.SubjectID
			if err := tx.DirectoryLinks().Lock(ctx, tenant, l.Source, l.ExternalID); err != nil {
				return err
			}
			// Read again under the lock: a sink write may have moved it.
			link, found, err := getLink(ctx, tx, tenant, l.Source, l.ExternalID)
			if err != nil {
				return err
			}
			if !found || !link.Provisioned {
				continue
			}
			link, did, err := recalculate(ctx, tx, tenant, link, mapping, true)
			if err != nil {
				return err
			}
			switch did {
			case recalculatedChanged:
				out.changed++
				out.recalculated++
			case recalculatedUnchanged:
				out.recalculated++
			case recalculatedSkipped:
				// Already on this version: a sink write or another run
				// got there first.
			}
			// What a deactivation owes is the sink's to deliver, with its
			// kill; an invalidation owed and not delivered — by this run or
			// an earlier one — is delivered here.
			if link.InvalidateOwed && !link.DeactivationOwed {
				out.owed = append(out.owed, link)
			}
		}
		return nil
	})
	return out, err
}

// publisher publishes a batch's invalidations as the mapping's author.
func (r *Recalculator) publisher(b batch) publisher {
	return publisher{
		store: r.store, revoker: r.revoker, recorder: r.recorder,
		actor: audit.OperatorActor{Principal: authorOf(b.author)}, cause: "a claim mapping changed",
	}
}

func authorOf(author string) string {
	if author == "" {
		return RecalculationPrincipal
	}
	return author
}

// record writes one version's `operator.claim_mapping.applied`, when it
// recalculated anything. It is written even when ctx has ended, because a
// stopped run's count is the record that it stopped.
func (r *Recalculator) record(ctx context.Context, tenant store.Tenant, run applied, complete bool) {
	if run.recalculated == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if err := r.recorder.OperatorEvent(ctx, nil, tenant, audit.OperatorAct{
		Event: audit.EventClaimMappingApplied, Actor: audit.OperatorActor{Principal: authorOf(run.author)},
		After: strconv.Itoa(run.version), Severity: contract.SeverityInfo,
		Fields: map[string]string{
			"recalculated": strconv.Itoa(run.recalculated),
			"changed":      strconv.Itoa(run.change),
			"complete":     strconv.FormatBool(complete),
		},
		Message: fmt.Sprintf("claim mapping %d applied to %d directory subjects, %d of them changed",
			run.version, run.recalculated, run.change),
	}); err != nil {
		r.log.ErrorContext(ctx, "a claim mapping recalculation could not be recorded",
			"event", "claim_mapping_applied_unrecorded", "tenant", tenant.String(),
			"version", run.version, "error", err.Error())
	}
}
