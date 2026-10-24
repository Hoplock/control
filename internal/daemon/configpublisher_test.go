// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/revoke"
	"github.com/hoplock/control/internal/store"
)

// The configuration publisher says the stream took a `config_changed` once it is
// logged (0027), whether or not a subscription on this node holds the proxy's
// stream: the node that does delivers it from the log, and a proxy that
// reconnects anywhere is replayed it. Before the log was shared only a delivery
// on this node could say that (0021). A refusal is still an error, so the
// announcer owes it.
func TestTheConfigPublisherReportsWhatTheStreamTook(t *testing.T) {
	bus, err := revoke.New(revoke.Options{HeartbeatInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	const tenant = store.Tenant("acme")
	pub := busConfigPublisher{bus}
	ev := contract.ConfigChangedEvent{Version: "v1", Hash: "sha256:" + strings.Repeat("a", 64)}

	if err := pub.PublishConfigChange(t.Context(), tenant, "p-a", ev); err != nil {
		t.Fatalf("with no subscriber here: %v, want the stream to take it", err)
	}
	if err := pub.PublishConfigChange(t.Context(), tenant, "p-a", contract.ConfigChangedEvent{}); err == nil {
		t.Error("a config_changed naming nothing was taken")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := bus.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pub.PublishConfigChange(t.Context(), tenant, "p-a", ev); !errors.Is(err, revoke.ErrClosed) {
		t.Errorf("on a closed stream: %v, want ErrClosed so the announcement stays owed", err)
	}
}
