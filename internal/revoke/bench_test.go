// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package revoke_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// BenchmarkTheHintGateRead measures the one read the event plane puts on the
// decision path: fleet.Registry.EventStreamHealthy, which the cache-hint gate
// asks on every hinted answer (PLAN §5.4, M5). It reads this node's own
// subscriptions and the copy of every other node's, from memory.
//
// Before 0027 the gate read the whole tenant's subscription map and built a
// copy of it per call, so its cost grew with the number of streams this node
// held; a deployment-wide copy would have multiplied that by the node count.
// It now asks for one proxy, so the shapes below should all cost the same:
// "local" is a single node holding every stream, "spread" the same fleet with
// four streams in five held by other nodes. The learnings file records the
// numbers, and the whole-tenant read beside them for comparison.
func BenchmarkTheHintGateRead(b *testing.B) {
	for _, shape := range []struct {
		name          string
		local, remote int
	}{
		{"local-1000", 1000, 0},
		{"spread-1000-4000", 1000, 4000},
	} {
		b.Run(shape.name, func(b *testing.B) {
			bus, registry := gateFixture(b, shape.local, shape.remote)
			ctx := b.Context()
			b.Run("EventStreamHealthy", func(b *testing.B) {
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					if _, err := registry.EventStreamHealthy(ctx, tenantX, fmt.Sprintf("proxy-%d", i%shape.local)); err != nil {
						b.Fatal(err)
					}
					i++
				}
			})
			b.Run("whole-tenant-read", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := bus.LiveSubscriptions(ctx, tenantX); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// gateFixture is a node holding local streams, whose copy holds remote more
// that another node wrote.
func gateFixture(b *testing.B, local, remote int) (*revoke.Bus, *fleet.Registry) {
	b.Helper()
	c := storetest.New(b)
	bus, err := revoke.New(revoke.Options{
		Store: c, Node: "node-a", HeartbeatInterval: 10 * time.Second, SyncInterval: 20 * time.Millisecond,
	})
	if err != nil {
		b.Fatal(err)
	}
	bus.Start(b.Context())
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(func() {
		cancel()
		closeCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = bus.Close(closeCtx)
	})
	for i := range local {
		go func() {
			_ = bus.Subscribe(ctx, tenantX, fmt.Sprintf("proxy-%d", i), "", func(*contract.RevocationEvent) error { return nil })
		}()
	}
	if remote > 0 {
		rows := make([]store.EventSubscription, remote)
		for i := range rows {
			rows[i] = store.EventSubscription{ProxyID: fmt.Sprintf("remote-%d", i), SeenAt: time.Now()}
		}
		if err := c.EventSubscriptions().Put(b.Context(), tenantX, "node-b", rows); err != nil {
			b.Fatal(err)
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		live, err := bus.LiveSubscriptions(b.Context(), tenantX)
		if err != nil {
			b.Fatal(err)
		}
		if len(live) == local+remote {
			break
		}
		if time.Now().After(deadline) {
			b.Fatalf("the node sees %d streams, want %d", len(live), local+remote)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return bus, fleet.New(c, fleet.WithSubscriptionState(bus))
}
