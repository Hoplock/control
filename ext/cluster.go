// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"context"
	"time"
)

// NodeID identifies one running Control process within a deployment.
type NodeID string

// Node describes one member of the deployment.
type Node struct {
	// ID is the node's identifier, stable for the life of the process.
	ID NodeID
	// Address is how other nodes reach it, when that means anything. It is
	// empty for a single-node deployment.
	Address string
	// Version is the node's build version, so that a rolling upgrade is
	// visible as a rolling upgrade rather than as a fault (M19).
	Version string
	// StartedAt is when the process came up.
	StartedAt time.Time
}

// Leadership is a handle on a cluster-wide singleton. Holding it means this
// node, and no other, may run the named job.
//
// Leadership is not permanent and an implementation must not pretend it is.
// Changes delivers every transition, and a job that holds a leadership watches
// it: losing the singleton has to stop the work, because the whole reason the
// singleton exists is that two nodes doing it is wrong.
type Leadership interface {
	// Held reports whether the singleton is held right now.
	Held() bool
	// Changes delivers true when the singleton is gained and false when it
	// is lost. An implementation sends the current state once, before any
	// transition, so a consumer that only reads this channel does not have
	// to poll Held first. The channel is closed when the leadership ends —
	// by Release, or by the context passed to Lead being cancelled — so a
	// consumer ranging over it may treat the close as a final loss.
	Changes() <-chan bool
	// Release gives up the singleton and closes Changes. It is safe to call
	// more than once.
	Release(ctx context.Context) error
}

// ClusterEvent is one message on the deployment-wide bus. The bus carries
// control-plane facts between nodes — a policy bundle was activated, a
// revocation was published, the fleet's health changed — so that a node which
// did not handle the request still learns what happened.
//
// It is a notification, never the record. Control keeps what happened in its
// own store and uses the bus to tell the other nodes to read it, so an event
// the bus delivers twice, late or out of order costs Control nothing, and one
// it loses costs a node at most the interval on which it reads the store
// anyway. The event stream (`GET /v1/proxies/{proxy_id}/events`) is the first
// user (0027): its topic is `hoplock.events.logged`, its Kind `logged`, its
// Tenant the tenant whose log moved, and its Payload a small JSON object naming
// the position logged.
type ClusterEvent struct {
	// Tenant owns the event, empty for a deployment-wide one.
	Tenant Tenant
	// Topic groups events; a subscriber names one.
	Topic string
	// Kind is the event's stable code within the topic.
	Kind string
	// Origin is the node that published it, so a subscriber can ignore its
	// own.
	Origin NodeID
	// Sequence orders events within a topic where the implementation can
	// provide an ordering. Zero means unordered.
	Sequence uint64
	// PublishedAt is when the origin published it.
	PublishedAt time.Time
	// Payload is the event body, opaque to the bus. It is already encoded,
	// because the bus must not need to know the types the control plane
	// sends over it.
	Payload []byte
}

// ClusterCoordinator supplies node membership, cluster-wide singletons, and the
// event bus between nodes. What varies is whether there is more than one node:
// a single process needs no election and no bus, and a highly-available
// deployment needs a real one — but the code above this seam must not be able
// to tell, or there are two code paths and only one of them is tested.
//
// When no implementation is registered, Control's own wiring registers the
// single-node coordinator, so this point is never empty in a running server.
// That default answers honestly rather than trivially: there is one member,
// this node, and it holds every singleton, and the bus delivers in process.
// A single-node deployment therefore needs no clustering at all, and a
// clustered one gets a real election with no second code path (M9, M19).
//
// Everything here is control plane. The decision path does not take a lock, and
// a coordinator that is slow or unreachable must never turn into a denied
// login (M5, M11).
//
// # What the bus must deliver
//
// Control's event stream wakes the other nodes through Publish and Subscribe
// every time it logs a revocation or a configuration change, so a clustered
// implementation is written against these rules, and Control relies on nothing
// stronger:
//
//   - AT LEAST ONCE, TO EVERY NODE. An event Publish accepted (returned nil
//     for) reaches every subscription of its topic that was open when Publish
//     was called, on every node including the publisher's — or that
//     subscription is dropped, below. Duplicates are allowed.
//   - NO ORDER IS REQUIRED. Events may arrive in any order, within a topic and
//     across topics. Sequence may be left zero.
//   - DROPPED MEANS CLOSED. A subscription the bus cannot keep up with — its
//     reader is behind, or its node lost contact with the other nodes — is
//     ended by closing its channel, after the events already in it. It is
//     never kept open while events are skipped: a closed channel is how
//     Control knows to subscribe again and read its store for what it missed,
//     and a channel that silently skipped is a revocation a proxy never
//     receives until Control's next scheduled read.
//   - ENDS WITH ITS CONTEXT. The channel is closed when the subscriber's
//     cancel function is called or the context passed to Subscribe ends.
//   - BYTES ARE OPAQUE. Tenant, Topic, Kind, Origin and Payload arrive as
//     published. Origin, when the publisher sets it, is the node Node
//     returned to it, and is how Control skips its own events.
//   - A FAILED PUBLISH IS AN ERROR, NOT A HANG. Publish returns promptly; an
//     error tells Control the other nodes may not have been told, and it
//     costs them at most one scheduled read of the store.
//
// Node must return an ID that is unique among the deployment's live members
// and stable for the life of the process: Control keys the event streams this
// node holds by it in the store.
type ClusterCoordinator interface {
	// Node returns this process's own membership record.
	Node(ctx context.Context) (Node, error)
	// Members returns every node currently in the deployment, including
	// this one.
	Members(ctx context.Context) ([]Node, error)
	// Lead acquires the named cluster-wide singleton. It returns as soon as
	// the campaign is under way, whether or not the singleton was won — the
	// caller learns that from the returned Leadership, which is what makes
	// gaining it later indistinguishable from having it at once. The
	// singleton is held until Release, or until the context passed here is
	// cancelled.
	Lead(ctx context.Context, singleton string) (Leadership, error)
	// Publish sends an event to every subscriber of its topic, on every
	// node, including this one, at least once (see "What the bus must
	// deliver").
	Publish(ctx context.Context, ev ClusterEvent) error
	// Subscribe returns a channel of events on a topic and a function that
	// cancels the subscription and closes the channel. A subscriber that
	// stops reading must be dropped rather than allowed to block the bus,
	// and dropping it closes its channel: it is never left open with events
	// skipped.
	Subscribe(ctx context.Context, topic string) (<-chan ClusterEvent, func(), error)
}
