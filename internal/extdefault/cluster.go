// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package extdefault

import (
	"context"
	"sync"
	"time"

	"github.com/hoplock/control/ext"
)

// singleNodeBusDepth is how many events a subscriber may fall behind by before
// it is dropped. A subscriber that has stopped reading must never be allowed to
// block a publisher: the bus carries control-plane facts, and a stuck console
// stream is not a reason for policy activation to hang.
const singleNodeBusDepth = 64

// SingleNode is Control's own ClusterCoordinator: one member, every singleton
// held, and an in-process event bus. It is the implementation a deployment of
// this repository alone runs with, and it answers honestly rather than
// trivially — there genuinely is one node, it genuinely is the leader, and
// events genuinely reach every subscriber in the deployment, because the
// deployment is this process.
//
// That matters more than it looks. Because the default is real, the code above
// the seam has exactly one path: a job that must run on one node campaigns for
// a singleton and watches for losing it, here as in a clustered deployment, and
// the clustered case is not a second, less-tested branch (PLAN M9, M19).
//
// A SingleNode is safe for concurrent use.
type SingleNode struct {
	node ext.Node

	mu     sync.Mutex
	leases map[string]*singleNodeLeadership
	subs   map[string]map[int]*subscription
	nextID int
	closed bool
}

// subscription is one reader of one topic. Its channel is closed exactly once,
// through closeOnce, because both the subscriber's own cancel function and a
// coordinator-wide Close can end it and they can race.
type subscription struct {
	ch        chan ext.ClusterEvent
	closeOnce sync.Once
}

// close ends the subscription. It is safe to call from either side, and more
// than once.
func (s *subscription) close() {
	s.closeOnce.Do(func() { close(s.ch) })
}

// NewSingleNode returns the single-node coordinator for this process.
func NewSingleNode(node ext.Node) *SingleNode {
	return &SingleNode{
		node:   node,
		leases: make(map[string]*singleNodeLeadership),
		subs:   make(map[string]map[int]*subscription),
	}
}

// Node returns this process's membership record.
func (s *SingleNode) Node(context.Context) (ext.Node, error) { return s.node, nil }

// Members returns the deployment's membership: this node, and nothing else.
func (s *SingleNode) Members(context.Context) ([]ext.Node, error) {
	return []ext.Node{s.node}, nil
}

// Lead acquires a cluster-wide singleton, which on one node is immediate and
// uncontested. Asking twice for the same singleton without releasing it first
// is an ext.ErrConflict error rather than two holders: two callers believing
// they hold the same singleton is the exact bug the singleton exists to
// prevent, and it must not become possible just because there is one node.
func (s *SingleNode) Lead(ctx context.Context, singleton string) (ext.Leadership, error) {
	if singleton == "" {
		return nil, ext.Errorf(ext.PointClusterCoordinator, ext.ProviderControl, "ClusterCoordinator.Lead",
			ext.KindInvalid, "a singleton must be named")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ext.Errorf(ext.PointClusterCoordinator, ext.ProviderControl, "ClusterCoordinator.Lead",
			ext.KindUnavailable, "the coordinator is closed")
	}
	if _, held := s.leases[singleton]; held {
		return nil, ext.Errorf(ext.PointClusterCoordinator, ext.ProviderControl, "ClusterCoordinator.Lead",
			ext.KindConflict, "singleton %q is already held by this node", singleton)
	}

	l := &singleNodeLeadership{
		owner:     s,
		singleton: singleton,
		held:      true,
		changes:   make(chan bool, 1),
		released:  make(chan struct{}),
	}
	// The current state is delivered once before any transition, so a
	// consumer that only selects on Changes starts working without first
	// having to ask Held.
	l.changes <- true
	s.leases[singleton] = l

	// The context passed to Lead bounds the leadership, exactly as the
	// interface says. On one node nothing can take the singleton away, so
	// cancellation is the only way it is ever lost — and a caller that
	// watches Changes therefore sees the same shape it would see in a
	// clustered deployment.
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = l.Release(context.WithoutCancel(ctx))
			case <-l.released:
				// Released or closed by other means; nothing left to
				// watch, and the goroutine must not outlive it.
			}
		}()
	}
	return l, nil
}

// releaseLease drops a singleton. It is called by the leadership handle.
func (s *SingleNode) releaseLease(singleton string, l *singleNodeLeadership) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[singleton] == l {
		delete(s.leases, singleton)
	}
}

// Publish delivers an event to every subscriber of its topic in this process.
// A subscriber that has fallen behind by more than singleNodeBusDepth loses the
// event rather than blocking the publisher; losing events is what a subscriber
// asks for by not reading, and the control plane's durable record is the
// database rather than the bus.
func (s *SingleNode) Publish(ctx context.Context, ev ext.ClusterEvent) error {
	if ev.Topic == "" {
		return ext.Errorf(ext.PointClusterCoordinator, ext.ProviderControl, "ClusterCoordinator.Publish",
			ext.KindInvalid, "an event must name a topic")
	}
	if err := ctx.Err(); err != nil {
		return ext.Errorf(ext.PointClusterCoordinator, ext.ProviderControl, "ClusterCoordinator.Publish",
			ext.KindUnavailable, "publish cancelled: %v", err)
	}
	if ev.Origin == "" {
		ev.Origin = s.node.ID
	}
	if ev.PublishedAt.IsZero() {
		ev.PublishedAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ext.Errorf(ext.PointClusterCoordinator, ext.ProviderControl, "ClusterCoordinator.Publish",
			ext.KindUnavailable, "the coordinator is closed")
	}
	for _, sub := range s.subs[ev.Topic] {
		select {
		case sub.ch <- ev:
		default:
		}
	}
	return nil
}

// Subscribe returns a channel of events on a topic and the function that
// cancels it.
func (s *SingleNode) Subscribe(ctx context.Context, topic string) (<-chan ext.ClusterEvent, func(), error) {
	if topic == "" {
		return nil, nil, ext.Errorf(ext.PointClusterCoordinator, ext.ProviderControl, "ClusterCoordinator.Subscribe",
			ext.KindInvalid, "a subscription must name a topic")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, ext.Errorf(ext.PointClusterCoordinator, ext.ProviderControl, "ClusterCoordinator.Subscribe",
			ext.KindUnavailable, "the coordinator is closed")
	}
	id := s.nextID
	s.nextID++
	sub := &subscription{ch: make(chan ext.ClusterEvent, singleNodeBusDepth)}
	if s.subs[topic] == nil {
		s.subs[topic] = make(map[int]*subscription)
	}
	s.subs[topic][id] = sub
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		if subs, ok := s.subs[topic]; ok {
			delete(subs, id)
			if len(subs) == 0 {
				delete(s.subs, topic)
			}
		}
		s.mu.Unlock()
		sub.close()
	}
	if ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			cancel()
		}()
	}
	return sub.ch, cancel, nil
}

// Close releases every singleton and drops every subscription. It is called on
// shutdown.
func (s *SingleNode) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	leases := make([]*singleNodeLeadership, 0, len(s.leases))
	for _, l := range s.leases {
		leases = append(leases, l)
	}
	s.leases = make(map[string]*singleNodeLeadership)
	subs := s.subs
	s.subs = make(map[string]map[int]*subscription)
	s.mu.Unlock()

	for _, l := range leases {
		l.drop()
	}
	for _, byID := range subs {
		for _, sub := range byID {
			sub.close()
		}
	}
}

// singleNodeLeadership is a held singleton on the single-node coordinator.
type singleNodeLeadership struct {
	owner     *SingleNode
	singleton string

	mu       sync.Mutex
	held     bool
	changes  chan bool
	released chan struct{}
	done     bool
}

// Held reports whether the singleton is held.
func (l *singleNodeLeadership) Held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

// Changes delivers leadership transitions.
func (l *singleNodeLeadership) Changes() <-chan bool { return l.changes }

// Release gives up the singleton. It is safe to call more than once.
func (l *singleNodeLeadership) Release(context.Context) error {
	l.owner.releaseLease(l.singleton, l)
	l.drop()
	return nil
}

// drop marks the leadership lost and closes its channel exactly once.
func (l *singleNodeLeadership) drop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return
	}
	l.done = true
	l.held = false
	close(l.changes)
	close(l.released)
}
