// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// The configuration publisher says a `config_changed` was delivered only when a
// subscription on this node took it (0021): an event the bus merely retained,
// with no subscriber here, is owed until the node holding the proxy's stream
// announces it.
func TestTheConfigPublisherReportsDeliveryNotAcceptance(t *testing.T) {
	bus, err := revoke.New(revoke.Options{HeartbeatInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = bus.Close(ctx)
	})
	const tenant = store.Tenant("acme")
	pub := busConfigPublisher{bus}
	ev := contract.ConfigChangedEvent{Version: "v1", Hash: "sha256:" + strings.Repeat("a", 64)}

	if delivered, err := pub.PublishConfigChange(t.Context(), tenant, "p-a", ev); err != nil || delivered {
		t.Fatalf("with no subscriber here: delivered=%v err=%v, want false and no error", delivered, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		_ = bus.Subscribe(ctx, tenant, "p-a", "", func(*contract.RevocationEvent) error { return nil })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		live, _ := bus.LiveSubscriptions(t.Context(), tenant)
		if _, ok := live["p-a"]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the subscription never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if delivered, err := pub.PublishConfigChange(t.Context(), tenant, "p-a", ev); err != nil || !delivered {
		t.Errorf("with p-a subscribed here: delivered=%v err=%v, want true", delivered, err)
	}
}
