// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package extdefault_test

import (
	"context"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/extdefault"
)

// nopSink satisfies ext.AuditSink for tests that only need a value.
type nopSink struct{}

func (nopSink) Export(context.Context, []ext.AuditRecord) error { return nil }

func TestSingleNodeIsItsOwnDeploymentAndHoldsEverySingleton(t *testing.T) {
	ctx := t.Context()
	c := extdefault.NewSingleNode(ext.Node{ID: "node-a", Version: "v0", StartedAt: time.Now().UTC()})
	defer c.Close()

	members, err := c.Members(ctx)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].ID != "node-a" {
		t.Fatalf("Members = %v, want exactly this node", members)
	}

	lead, err := c.Lead(ctx, "supervisory-registration")
	if err != nil {
		t.Fatalf("Lead: %v", err)
	}
	if !lead.Held() {
		t.Error("the single node did not hold the singleton it campaigned for")
	}
	// The current state arrives on Changes before any transition, so a
	// consumer that only selects on the channel starts working without
	// first having to ask Held.
	select {
	case held := <-lead.Changes():
		if !held {
			t.Error("the first value on Changes was false")
		}
	default:
		t.Error("Changes delivered nothing; a consumer watching only the channel would never start")
	}

	// Two holders of one singleton is the exact bug a singleton exists to
	// prevent, and one node is not an excuse to allow it.
	if _, err := c.Lead(ctx, "supervisory-registration"); !ext.IsConflict(err) {
		t.Errorf("a second Lead on a held singleton returned %v, want a conflict", err)
	}

	if err := lead.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if lead.Held() {
		t.Error("the leadership still reports itself held after Release")
	}
	if _, open := <-lead.Changes(); open {
		t.Error("Changes was not closed by Release")
	}
	if err := lead.Release(ctx); err != nil {
		t.Errorf("a second Release returned %v, want nil", err)
	}

	// Releasing frees the name for the next campaign, which is what makes
	// "losing leadership drops the registration, gaining it re-registers"
	// testable without a cluster.
	again, err := c.Lead(ctx, "supervisory-registration")
	if err != nil {
		t.Fatalf("Lead after Release: %v", err)
	}
	if !again.Held() {
		t.Error("the re-acquired singleton is not held")
	}
}

func TestLeadershipIsLostWhenTheContextPassedToLeadIsCancelled(t *testing.T) {
	c := extdefault.NewSingleNode(ext.Node{ID: "node-a"})
	defer c.Close()

	ctx, cancel := context.WithCancel(t.Context())
	lead, err := c.Lead(ctx, "job")
	if err != nil {
		t.Fatalf("Lead: %v", err)
	}
	<-lead.Changes() // the initial true
	cancel()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-lead.Changes():
			if open {
				continue
			}
			if lead.Held() {
				t.Error("the leadership reports itself held after its context was cancelled")
			}
			return
		case <-deadline:
			t.Fatal("cancelling the context passed to Lead never ended the leadership")
		}
	}
}

func TestBusDeliversToEverySubscriberOfATopicAndNoOthers(t *testing.T) {
	ctx := t.Context()
	c := extdefault.NewSingleNode(ext.Node{ID: "node-a"})
	defer c.Close()

	first, cancelFirst, err := c.Subscribe(ctx, "policy")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	second, cancelSecond, err := c.Subscribe(ctx, "policy")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	other, cancelOther, err := c.Subscribe(ctx, "fleet")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancelSecond()
	defer cancelOther()

	if err := c.Publish(ctx, ext.ClusterEvent{Topic: "policy", Kind: "bundle.activated"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	for name, ch := range map[string]<-chan ext.ClusterEvent{"first": first, "second": second} {
		select {
		case ev := <-ch:
			if ev.Kind != "bundle.activated" {
				t.Errorf("%s received %q", name, ev.Kind)
			}
			if ev.Origin != "node-a" {
				t.Errorf("%s received origin %q, want the publishing node", name, ev.Origin)
			}
			if ev.PublishedAt.IsZero() {
				t.Errorf("%s received an event with no publication time", name)
			}
		default:
			t.Errorf("%s received nothing", name)
		}
	}
	select {
	case ev := <-other:
		t.Errorf("a subscriber to another topic received %v", ev)
	default:
	}

	cancelFirst()
	if _, open := <-first; open {
		t.Error("cancelling a subscription did not close its channel")
	}
	cancelFirst() // idempotent

	if err := c.Publish(ctx, ext.ClusterEvent{Topic: "policy", Kind: "bundle.activated"}); err != nil {
		t.Fatalf("Publish after a cancelled subscription: %v", err)
	}
}

// TestASlowSubscriberIsDroppedRatherThanBlockingThePublisher is why the bus
// has a depth at all: the control plane's durable record is the database, and
// a console stream that stopped reading is not a reason for policy activation
// to hang.
func TestASlowSubscriberIsDroppedRatherThanBlockingThePublisher(t *testing.T) {
	ctx := t.Context()
	c := extdefault.NewSingleNode(ext.Node{ID: "node-a"})
	defer c.Close()

	_, cancel, err := c.Subscribe(ctx, "noisy")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			if err := c.Publish(ctx, ext.ClusterEvent{Topic: "noisy", Kind: "tick"}); err != nil {
				t.Errorf("Publish: %v", err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a subscriber that stopped reading blocked the publisher")
	}
}

func TestCoordinatorRejectsUnnamedTopicsAndSingletons(t *testing.T) {
	ctx := t.Context()
	c := extdefault.NewSingleNode(ext.Node{ID: "node-a"})
	defer c.Close()

	if _, err := c.Lead(ctx, ""); !ext.IsInvalid(err) {
		t.Errorf("Lead(\"\") returned %v, want invalid", err)
	}
	if _, _, err := c.Subscribe(ctx, ""); !ext.IsInvalid(err) {
		t.Errorf("Subscribe(\"\") returned %v, want invalid", err)
	}
	if err := c.Publish(ctx, ext.ClusterEvent{}); !ext.IsInvalid(err) {
		t.Errorf("Publish with no topic returned %v, want invalid", err)
	}
}

func TestCloseEndsLeadershipsAndSubscriptions(t *testing.T) {
	ctx := t.Context()
	c := extdefault.NewSingleNode(ext.Node{ID: "node-a"})

	lead, err := c.Lead(ctx, "job")
	if err != nil {
		t.Fatalf("Lead: %v", err)
	}
	ch, _, err := c.Subscribe(ctx, "topic")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	c.Close()
	c.Close() // idempotent

	if lead.Held() {
		t.Error("a leadership survived Close")
	}
	if _, open := <-ch; open {
		t.Error("a subscription channel survived Close")
	}
	if _, err := c.Lead(ctx, "job"); !ext.IsUnavailable(err) {
		t.Errorf("Lead after Close returned %v, want unavailable", err)
	}
	if _, _, err := c.Subscribe(ctx, "topic"); !ext.IsUnavailable(err) {
		t.Errorf("Subscribe after Close returned %v, want unavailable", err)
	}
	if err := c.Publish(ctx, ext.ClusterEvent{Topic: "topic"}); !ext.IsUnavailable(err) {
		t.Errorf("Publish after Close returned %v, want unavailable", err)
	}
}

// TestSingleNodeSatisfiesTheInterface fails to compile rather than fails to
// run if the seam changes underneath the default.
func TestSingleNodeSatisfiesTheInterface(t *testing.T) {
	var _ ext.ClusterCoordinator = extdefault.NewSingleNode(ext.Node{ID: "n"})
}
