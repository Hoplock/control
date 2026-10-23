// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package export

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/archive"
	"github.com/hoplock/control/internal/store"
)

// Options configures a Delivery.
type Options struct {
	// Store is where the records, the positions and the leases are.
	Store *store.Store
	// Sinks are the registered sinks, as ext.Extensions.AuditSinks returns
	// them. At least one is required: with none, there is no delivery.
	Sinks []ext.Bound[ext.AuditSink]
	// BatchRecords bounds one Export call, one stream's records.
	BatchRecords int
	// AttemptTimeout is the deadline on each Export.
	AttemptTimeout time.Duration
	// IdleInterval is how long delivery waits with nothing to deliver.
	IdleInterval time.Duration
	// BackoffCap is the longest wait between attempts on a failing stream.
	BackoffCap time.Duration
	// MaxInFlight bounds the batches in flight to each sink at once.
	MaxInFlight int
	// Node names this process as a lease holder, for every sink alike.
	// Empty takes a random name, unique per process.
	Node   string
	Logger *slog.Logger
}

// Delivery hands every committed record to every registered sink, through one
// feed per sink.
type Delivery struct {
	node  string
	feeds []*archive.Feed
	by    map[string]*archive.Feed
}

// New builds delivery to every sink. It delivers nothing until Start.
func New(o Options) (*Delivery, error) {
	if len(o.Sinks) == 0 {
		return nil, errors.New("export: delivery needs a registered sink")
	}
	node := o.Node
	if node == "" {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("export: name this node: %w", err)
		}
		node = "node-" + hex.EncodeToString(b[:])
	}
	d := &Delivery{node: node, by: make(map[string]*archive.Feed, len(o.Sinks))}
	for _, s := range o.Sinks {
		provider := s.Registration.Provider
		if _, dup := d.by[provider]; dup || provider == "" {
			// The registry refuses both; a position is named by the
			// provider, so two sinks under one name would share one.
			return nil, fmt.Errorf("export: every sink is named by a provider of its own, and %q is not", provider)
		}
		sink := s.Impl
		feed, err := archive.New(archive.Options{
			Store: o.Store,
			Consumer: &archive.Consumer{
				Key: store.SinkConsumer(provider), Point: ext.PointAuditSink, Provider: provider,
				Deliver: sink.Export,
			},
			BatchRecords:   o.BatchRecords,
			AttemptTimeout: o.AttemptTimeout,
			IdleInterval:   o.IdleInterval,
			BackoffCap:     o.BackoffCap,
			InFlight:       o.MaxInFlight,
			Node:           node,
			Logger:         o.Logger,
		})
		if err != nil {
			return nil, err
		}
		d.feeds = append(d.feeds, feed)
		d.by[provider] = feed
	}
	return d, nil
}

// Node is this process's name as a lease holder.
func (d *Delivery) Node() string { return d.node }

// Track adds a tenant to the set every sink is delivered. It is idempotent,
// never blocks, and is safe to call from the ingest path.
func (d *Delivery) Track(tenant store.Tenant) {
	for _, f := range d.feeds {
		f.Track(tenant)
	}
}

// Lag is how far behind the sink a provider names is in a tenant, and false
// when no sink is registered under that name.
func (d *Delivery) Lag(ctx context.Context, tenant store.Tenant, provider string) (store.FeedLag, bool, error) {
	f, ok := d.by[provider]
	if !ok {
		return store.FeedLag{}, false, nil
	}
	lag, err := f.Lag(ctx, tenant)
	return lag, true, err
}

// Start delivers to every sink until ctx is done or Stop is called.
func (d *Delivery) Start(ctx context.Context) {
	for _, f := range d.feeds {
		f.Start(ctx)
	}
}

// Stop ends delivery to every sink, until ctx is done. A batch in flight is not
// confirmed, so it is offered again at the next start; an Export that has not
// returned by then is abandoned, not waited on.
func (d *Delivery) Stop(ctx context.Context) error {
	// Every sink stops at once, so one whose workers are slow to finish
	// spends none of another's share of the deadline.
	errs := make([]error, len(d.feeds))
	var wg sync.WaitGroup
	for i, f := range d.feeds {
		wg.Go(func() { errs[i] = f.Stop(ctx) })
	}
	wg.Wait()
	return errors.Join(errs...)
}
